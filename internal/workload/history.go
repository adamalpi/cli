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
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/datarobot/cli/internal/drapi"
)

// HistoryEntry is one rollout the platform recorded for a workload: the
// artifact it moved to, how, and how it ended. The record is kept as it came
// and re-emitted whole under JSON, runtime and config included.
type HistoryEntry struct {
	ID                  string    `json:"id"`
	CreatedAt           time.Time `json:"createdAt"`
	UserID              string    `json:"userId"`
	CandidateArtifactID string    `json:"candidateArtifactId"`
	Status              string    `json:"status"`
	Strategy            string    `json:"strategy"`
	Message             string    `json:"message"`

	raw json.RawMessage
}

type historyEntryFields HistoryEntry

func (h *HistoryEntry) UnmarshalJSON(data []byte) error {
	var fields historyEntryFields

	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	*h = HistoryEntry(fields)

	h.raw = append(json.RawMessage(nil), data...)

	return nil
}

func (h HistoryEntry) MarshalJSON() ([]byte, error) {
	if len(h.raw) > 0 {
		return h.raw, nil
	}

	return json.Marshal(historyEntryFields(h))
}

type workloadHistoryList struct {
	Data       []HistoryEntry `json:"data"`
	TotalCount int            `json:"totalCount"`
}

// maxHistoryPages bounds the walk, as maxEventPages does for the event trail.
const maxHistoryPages = 50

// ListWorkloadHistory returns the most recent limit entries, newest first.
//
// Pages are read by offset rather than through next links: the route's next
// link points under /api/v2/console/, which answers 404.
func ListWorkloadHistory(workloadID string, limit int) ([]HistoryEntry, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("invalid limit %d: must be positive", limit)
	}

	var all []HistoryEntry

	for page := 0; ; page++ {
		if page >= maxHistoryPages {
			return nil, fmt.Errorf("the history of workload %s did not end after %d pages; refusing to read more",
				workloadID, maxHistoryPages)
		}

		query := url.Values{}
		query.Set("limit", strconv.Itoa(maxPageSize))
		query.Set("offset", strconv.Itoa(len(all)))

		pageURL, err := drapi.EndpointURL("/workloads/"+escapeID(workloadID)+"/history/", query)
		if err != nil {
			return nil, err
		}

		var list workloadHistoryList

		if err := drapi.GetJSON(pageURL, "workload history", &list); err != nil {
			return nil, err
		}

		all = append(all, list.Data...)

		if len(list.Data) < maxPageSize || (list.TotalCount > 0 && len(all) >= list.TotalCount) {
			break
		}
	}

	slices.SortStableFunc(all, func(a, b HistoryEntry) int { return b.CreatedAt.Compare(a.CreatedAt) })

	if len(all) > limit {
		all = all[:limit]
	}

	return all, nil
}
