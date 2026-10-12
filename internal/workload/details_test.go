// Copyright 2026 DataRobot, Inc. and its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workload

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/datarobot/cli/internal/config"
	"github.com/datarobot/cli/internal/config/viperx"
	"github.com/datarobot/cli/internal/outputformat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const detailedWorkloadDoc = `{
	"id": "wl-1",
	"name": "my-app",
	"status": "running",
	"type": "service",
	"importance": "low",
	"artifactId": "art-1",
	"endpoint": "https://app.example.com/api/v2/endpoints/workloads/wl-1/",
	"createdAt": "2026-06-10T08:00:00Z",
	"updatedAt": "2026-06-10T08:05:00Z",
	"owners": [{"id": "u-1", "fullName": "Ada Lovelace", "email": "ada@example.com", "userhash": "x"}],
	"permissions": ["CAN_VIEW", "CAN_UPDATE"],
	"tags": [{"id": "t-1", "name": "env", "value": "prod"}],
	"requestStats": {"totalRequests": 0, "errorRate": 0.0, "responseTime": 0},
	"runtime": {"containerGroups": [{
		"name": "default",
		"resourceBundles": ["cpu.small"],
		"replicaCount": 1,
		"resolvedBundle": {"id": "cpu.small", "cpuCount": 1.0, "memoryBytes": 536870912, "gpuCount": 0}
	}]}
}`

const workloadStatsDoc = `{"period": {"start": null, "end": null}, "metrics": {
	"totalRequests": 12, "totalErrorRate": 0.25, "responseTime": 175, "serverErrors": 1}}`

func serveDetails(t *testing.T, statsHandler http.HandlerFunc) {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v2/workloads/wl-1/{$}", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, detailedWorkloadDoc)
	})
	mux.HandleFunc("GET /api/v2/workloads/wl-1/stats/{$}", statsHandler)

	serveAPI(t, mux)
}

func TestGetWorkloadDetails_ReadsExtrasAndStats(t *testing.T) {
	var startTime, endTime string

	serveDetails(t, func(w http.ResponseWriter, r *http.Request) {
		startTime = r.URL.Query().Get("startTime")
		endTime = r.URL.Query().Get("endTime")

		fmt.Fprint(w, workloadStatsDoc)
	})

	d, err := GetWorkloadDetails("wl-1")
	require.NoError(t, err)

	assertProjection(t, &d.Workload, "wl-1", "my-app", "running")
	assert.Equal(t, viperx.GetString(config.DataRobotURL)+"/console-nextgen/workloads/wl-1/", d.UIURL)
	assert.Equal(t, []WorkloadUser{{ID: "u-1", FullName: "Ada Lovelace", Email: "ada@example.com"}}, d.Owners)
	assert.Equal(t, []string{"CAN_VIEW", "CAN_UPDATE"}, d.Permissions)
	assert.Equal(t, []WorkloadTag{{Name: "env", Value: "prod"}}, d.Tags)

	require.Len(t, d.Runtime.ContainerGroups, 1)
	require.NotNil(t, d.Runtime.ContainerGroups[0].ResolvedBundle)
	assert.Equal(t, int64(536870912), d.Runtime.ContainerGroups[0].ResolvedBundle.MemoryBytes)

	// The totals come from the stats route, not the document's zeros.
	require.NotNil(t, d.RequestStats)
	assert.Equal(t, RequestStats{TotalRequests: 12, TotalErrorRate: 0.25, ResponseTime: 175}, *d.RequestStats)

	start, err := time.Parse(time.RFC3339, startTime)
	require.NoError(t, err, "stats must be asked for a window")

	end, err := time.Parse(time.RFC3339, endTime)
	require.NoError(t, err, "the platform needs both ends of the window")

	assert.Equal(t, requestStatsWindow, end.Sub(start))
	assert.Equal(t, end, end.Truncate(24*time.Hour), "the window ends on a UTC midnight")
	assert.True(t, end.After(time.Now()) && end.Before(time.Now().Add(24*time.Hour)), "the window ends tonight")
}

func TestGetWorkloadDetails_StatsFailureStillReturnsWorkload(t *testing.T) {
	serveDetails(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	d, err := GetWorkloadDetails("wl-1")
	require.NoError(t, err)

	assert.Equal(t, "wl-1", d.ID)
	assert.Nil(t, d.RequestStats)
	assert.NotEmpty(t, d.Owners)
}

func fullDetails() WorkloadDetails {
	gpus := "A10G"

	return WorkloadDetails{
		Workload: makeTestWorkload("wl-1", "my-app", "running"),
		UIURL:    "https://app.example.com/console-nextgen/workloads/wl-1/",
		Runtime: RuntimeSettings{ContainerGroups: []ContainerGroupSettings{{
			Name:            "default",
			ResourceBundles: []string{"gpu.medium"},
			ResolvedBundle: &ResolvedBundle{
				ID: "gpu.medium", CPUCount: 4, MemoryBytes: 16 << 30, GPUCount: 1, GPUTypeLabel: &gpus,
			},
		}}},
		RequestStats: &RequestStats{TotalRequests: 12, TotalErrorRate: 0.25, ResponseTime: 175},
		Owners:       []WorkloadUser{{ID: "u-1", FullName: "Ada Lovelace", Email: "ada@example.com"}},
		Permissions:  []string{"CAN_VIEW", "CAN_UPDATE"},
		Tags:         []WorkloadTag{{Name: "env", Value: "prod"}},
	}
}

func TestRenderWorkloadDetails_TextShowsEveryDetail(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, RenderWorkloadDetails(outputformat.OutputFormatText, fullDetails()))
	})

	assert.Contains(t, out, "Status:")
	assert.Regexp(t, `UI:\s+https://app.example.com/console-nextgen/workloads/wl-1/`, out)
	assert.Regexp(t, `Bundle:\s+gpu.medium \(4 CPU, 16 GiB, 1 GPU A10G\)`, out)
	assert.Regexp(t, `Requests \(7d\):\s+12, 25.0% errors, median 175 ms`, out)
	assert.Regexp(t, `Owner:\s+Ada Lovelace <ada@example.com>`, out)
	assert.Regexp(t, `Permissions:\s+CAN_VIEW, CAN_UPDATE`, out)
	assert.Regexp(t, `Tags:\s+env=prod`, out)
}

func TestRenderWorkloadDetails_TextOmitsWhatIsMissing(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, RenderWorkloadDetails(outputformat.OutputFormatText,
			WorkloadDetails{Workload: makeTestWorkload("wl-1", "my-app", "submitted")}))
	})

	assert.Contains(t, out, "Status:")

	for _, label := range []string{"UI:", "Bundle:", "Requests", "Owner:", "Permissions:", "Tags:"} {
		assert.NotContains(t, out, label)
	}
}

func TestRenderWorkloadDetails_TextBundlePerGroup(t *testing.T) {
	d := WorkloadDetails{
		Workload: makeTestWorkload("wl-1", "my-app", "provisioning"),
		Runtime: RuntimeSettings{ContainerGroups: []ContainerGroupSettings{
			{Name: "web", ResourceBundles: []string{"cpu.small"}},
			{Name: "worker", ResolvedBundle: &ResolvedBundle{ID: "cpu.large", CPUCount: 2, MemoryBytes: 512 << 20}},
		}},
	}

	out := captureStdout(t, func() {
		require.NoError(t, RenderWorkloadDetails(outputformat.OutputFormatText, d))
	})

	assert.Regexp(t, `Bundle:\s+web: cpu.small \(requested\)`, out)
	assert.Regexp(t, `Bundle:\s+worker: cpu.large \(2 CPU, 512 MiB\)`, out)
}

func TestRenderWorkloadDetails_JSONCarriesEveryDetail(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, RenderWorkloadDetails(outputformat.OutputFormatJSON, fullDetails()))
	})

	var parsed map[string]any

	require.NoError(t, json.Unmarshal([]byte(out), &parsed))

	assert.Equal(t, "wl-1", parsed["id"])
	assert.Equal(t, "https://app.example.com/console-nextgen/workloads/wl-1/", parsed["uiUrl"])
	assert.Equal(t, map[string]any{
		"window": "7d", "totalRequests": 12.0, "errorRate": 0.25, "medianLatencyMs": 175.0,
	}, parsed["requestStats"])
	assert.Equal(t, []any{"CAN_VIEW", "CAN_UPDATE"}, parsed["permissions"])
	assert.Equal(t, []any{map[string]any{"name": "env", "value": "prod"}}, parsed["tags"])
	assert.Equal(t, []any{map[string]any{"id": "u-1", "fullName": "Ada Lovelace", "email": "ada@example.com"}},
		parsed["owners"])

	bundles, ok := parsed["bundles"].([]any)
	require.True(t, ok)
	require.Len(t, bundles, 1)

	bundle := bundles[0].(map[string]any)
	assert.Equal(t, "default", bundle["containerGroup"])
	assert.Equal(t, []any{"gpu.medium"}, bundle["requested"])
	assert.Equal(t, "gpu.medium", bundle["resolved"].(map[string]any)["id"])
}

func TestRenderWorkloadDetails_JSONOmitsWhatIsMissing(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, RenderWorkloadDetails(outputformat.OutputFormatJSON,
			WorkloadDetails{Workload: makeTestWorkload("wl-1", "my-app", "submitted")}))
	})

	var parsed map[string]any

	require.NoError(t, json.Unmarshal([]byte(out), &parsed))

	assert.Equal(t, "wl-1", parsed["id"])

	for _, key := range []string{"uiUrl", "bundles", "requestStats", "owners", "permissions", "tags"} {
		assert.NotContains(t, parsed, key)
	}
}
