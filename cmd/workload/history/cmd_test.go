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

package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/datarobot/cli/internal/drapi"
	"github.com/datarobot/cli/internal/workload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stubList(t *testing.T, fn func(id string, limit int) ([]workload.HistoryEntry, error)) {
	t.Helper()

	prev := listHistoryFn
	listHistoryFn = fn

	t.Cleanup(func() { listHistoryFn = prev })
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := Cmd()
	cmd.PreRunE = nil
	cmd.SetArgs(args)

	var out bytes.Buffer

	cmd.SetOut(&out)

	err := cmd.Execute()

	return out.String(), err
}

func TestCmd_ArgIsOptional(t *testing.T) {
	cmd := Cmd()

	require.NoError(t, cmd.Args(cmd, []string{"68b0c1d2e3f4a5b6c7d8e9f0"}))
	require.NoError(t, cmd.Args(cmd, nil))
	require.Error(t, cmd.Args(cmd, []string{"a", "b"}))
}

func TestCmd_PassesTheLimitAndRendersATable(t *testing.T) {
	var (
		gotID    string
		gotLimit int
	)

	stubList(t, func(id string, limit int) ([]workload.HistoryEntry, error) {
		gotID, gotLimit = id, limit

		return []workload.HistoryEntry{{Status: "errored", Strategy: "rolling", CandidateArtifactID: "art-1", Message: "no bundle fits"}}, nil
	})

	out, err := run(t, "68b0c1d2e3f4a5b6c7d8e9f0", "--limit", "5")
	require.NoError(t, err)
	assert.Equal(t, "68b0c1d2e3f4a5b6c7d8e9f0", gotID)
	assert.Equal(t, 5, gotLimit)
	assert.Contains(t, out, "errored")
	assert.Contains(t, out, "no bundle fits")
}

func TestCmd_DefaultLimit(t *testing.T) {
	var gotLimit int

	stubList(t, func(_ string, limit int) ([]workload.HistoryEntry, error) {
		gotLimit = limit

		return nil, nil
	})

	out, err := run(t, "68b0c1d2e3f4a5b6c7d8e9f0")
	require.NoError(t, err)
	assert.Equal(t, 20, gotLimit)
	assert.Equal(t, "No history found.\n", out)
}

func TestCmd_JSONIsOneEnvelope(t *testing.T) {
	stubList(t, func(string, int) ([]workload.HistoryEntry, error) {
		return nil, nil
	})

	out, err := run(t, "68b0c1d2e3f4a5b6c7d8e9f0", "--output-format", "json")
	require.NoError(t, err)

	var envelope map[string]any

	require.NoError(t, json.Unmarshal([]byte(out), &envelope))
	assert.Equal(t, []any{}, envelope["history"])
}

func TestCmd_RefusesABadLimit(t *testing.T) {
	stubList(t, func(string, int) ([]workload.HistoryEntry, error) {
		t.Fatal("the client must not be called")

		return nil, nil
	})

	_, err := run(t, "68b0c1d2e3f4a5b6c7d8e9f0", "--limit", "0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
}

func TestCmd_ClientErrorsSurface(t *testing.T) {
	stubList(t, func(string, int) ([]workload.HistoryEntry, error) {
		return nil, errors.New("boom")
	})

	_, err := run(t, "68b0c1d2e3f4a5b6c7d8e9f0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestCmd_WrapsANotFoundForAManifestWorkload(t *testing.T) {
	stubList(t, func(string, int) ([]workload.HistoryEntry, error) {
		return nil, &drapi.HTTPError{StatusCode: http.StatusNotFound, URL: "https://example.test/api/v2/workloads/68b0c1d2e3f4a5b6c7d8e9f0/history/"}
	})

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".datarobot.yaml"), []byte("workloadId: 68b0c1d2e3f4a5b6c7d8e9f0\n"), 0o600))

	_, err := run(t, "--dir", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not on this instance")
}
