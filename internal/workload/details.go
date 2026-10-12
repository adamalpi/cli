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
	"net/url"
	"time"

	"github.com/datarobot/cli/internal/config"
	"github.com/datarobot/cli/internal/drapi"
	"github.com/datarobot/cli/internal/log"
)

const (
	requestStatsWindow      = 7 * 24 * time.Hour
	requestStatsWindowLabel = "7d"
	requestStatsTimeout     = 10 * time.Second
)

// WorkloadDetails is the workload as `dr workload get` shows it: the shared
// projection plus where to see it in the UI, what it runs on, its recent
// traffic and who owns it.
type WorkloadDetails struct {
	Workload

	Runtime     RuntimeSettings `json:"runtime"`
	Owners      []WorkloadUser  `json:"owners"`
	Permissions []string        `json:"permissions"`
	Tags        []WorkloadTag   `json:"tags"`

	UIURL string `json:"-"`

	// RequestStats is nil when the stats route did not answer.
	RequestStats *RequestStats `json:"-"`
}

type WorkloadUser struct {
	ID       string `json:"id"`
	FullName string `json:"fullName"`
	Email    string `json:"email"`
}

type WorkloadTag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RequestStats is the workload's traffic over requestStatsWindow.
// ResponseTime is the median, in milliseconds.
type RequestStats struct {
	TotalRequests  int     `json:"totalRequests"`
	TotalErrorRate float64 `json:"totalErrorRate"`
	ResponseTime   int     `json:"responseTime"`
}

// GetWorkloadDetails reads a workload with everything `get` shows. Request
// stats come from a second call, which is allowed to fail: the stats route
// queries the metrics backend, and a workload must still be readable when
// that is slow or down.
func GetWorkloadDetails(workloadID string) (*WorkloadDetails, error) {
	var details WorkloadDetails

	if err := getWorkloadDoc(workloadID, &details); err != nil {
		return nil, err
	}

	details.UIURL = workloadUIURL(details.ID)
	details.RequestStats = getRequestStats(details.ID, time.Now())

	return &details, nil
}

// The request stats embedded in the workload document only carry totals on
// the list route, so the single-workload numbers come from the stats route.
// The window is the list route's: seven whole UTC days ending tonight. Staging
// answers some other windows, a startTime alone among them, with a 500.
func getRequestStats(workloadID string, now time.Time) *RequestStats {
	end := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	query := url.Values{
		"startTime": {end.Add(-requestStatsWindow).Format(time.RFC3339)},
		"endTime":   {end.Format(time.RFC3339)},
	}

	statsURL, err := drapi.EndpointURL("/workloads/"+escapeID(workloadID)+"/stats/", query)
	if err != nil {
		return nil
	}

	var resp struct {
		Metrics RequestStats `json:"metrics"`
	}

	if err := drapi.GetJSON(statsURL, "workload stats", &resp, requestStatsTimeout); err != nil {
		log.Debugf("Request stats unavailable for workload %s: %v", workloadID, err)

		return nil
	}

	return &resp.Metrics
}

// WorkloadDetailsOutput is the JSON shape of `dr workload get`: the stable
// workload shape, plus each detail only when the platform had it.
type WorkloadDetailsOutput struct {
	WorkloadOutput

	UIURL        string              `json:"uiUrl,omitempty"`
	Bundles      []BundleOutput      `json:"bundles,omitempty"`
	RequestStats *RequestStatsOutput `json:"requestStats,omitempty"`
	Owners       []WorkloadUser      `json:"owners,omitempty"`
	Permissions  []string            `json:"permissions,omitempty"`
	Tags         []WorkloadTag       `json:"tags,omitempty"`
}

type BundleOutput struct {
	ContainerGroup string          `json:"containerGroup"`
	Requested      []string        `json:"requested,omitempty"`
	Resolved       *ResolvedBundle `json:"resolved,omitempty"`
}

type RequestStatsOutput struct {
	Window          string  `json:"window"`
	TotalRequests   int     `json:"totalRequests"`
	ErrorRate       float64 `json:"errorRate"`
	MedianLatencyMs int     `json:"medianLatencyMs"`
}

func NewWorkloadDetailsOutput(d WorkloadDetails) WorkloadDetailsOutput {
	out := WorkloadDetailsOutput{
		WorkloadOutput: NewWorkloadOutput(d.Workload),
		UIURL:          d.UIURL,
		Owners:         d.Owners,
		Permissions:    d.Permissions,
		Tags:           d.Tags,
	}

	for _, g := range d.Runtime.ContainerGroups {
		if len(g.ResourceBundles) == 0 && g.ResolvedBundle == nil {
			continue
		}

		out.Bundles = append(out.Bundles, BundleOutput{
			ContainerGroup: g.Name,
			Requested:      g.ResourceBundles,
			Resolved:       g.ResolvedBundle,
		})
	}

	if s := d.RequestStats; s != nil {
		out.RequestStats = &RequestStatsOutput{
			Window:          requestStatsWindowLabel,
			TotalRequests:   s.TotalRequests,
			ErrorRate:       s.TotalErrorRate,
			MedianLatencyMs: s.ResponseTime,
		}
	}

	return out
}

func workloadUIURL(workloadID string) string {
	base := config.GetBaseURL()
	if base == "" || workloadID == "" {
		return ""
	}

	return base + "/console-nextgen/workloads/" + escapeID(workloadID) + "/"
}
