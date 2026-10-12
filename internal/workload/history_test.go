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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/datarobot/cli/internal/outputformat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyRecord is one entry in the shape staging answers with.
func historyRecord(id, createdAt, status string) string {
	return fmt.Sprintf(`{"id":%q,"createdAt":%q,"userId":"u1","workloadId":"wl-1",
		"candidateArtifactId":"art-%s","status":%q,"strategy":"rolling",
		"runtime":{"containerGroups":[{"name":"default","replicaCount":1}]},
		"message":"Maintenance of the inference server replacement completed."}`, id, createdAt, id, status)
}

// historyPage is one page as staging writes it, next link included: the link
// points under /console/ and must never be followed.
func historyPage(total int, records ...string) string {
	return fmt.Sprintf(`{"totalCount":%d,"count":%d,"next":"http://127.0.0.1/api/v2/console/workloads/wl-1/history/?offset=99","data":[%s]}`,
		total, len(records), strings.Join(records, ","))
}

func TestListWorkloadHistory_NewestFirst(t *testing.T) {
	serveAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/workloads/wl-1/history/", r.URL.Path)
		assert.Equal(t, "0", r.URL.Query().Get("offset"))

		fmt.Fprint(w, historyPage(3,
			historyRecord("a", "2026-10-02T12:00:00.000000+00:00", "completed"),
			historyRecord("c", "2026-10-02T14:00:00.000000+00:00", "errored"),
			historyRecord("b", "2026-10-02T13:00:00.000000+00:00", "completed"),
		))
	}))

	entries, err := ListWorkloadHistory("wl-1", 2)
	require.NoError(t, err)
	require.Len(t, entries, 2, "limit keeps the most recent")
	assert.Equal(t, "c", entries[0].ID)
	assert.Equal(t, "b", entries[1].ID)
	assert.Equal(t, "errored", entries[0].Status)
	assert.Equal(t, "art-c", entries[0].CandidateArtifactID)
	assert.Equal(t, time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC), entries[0].CreatedAt.UTC())
}

// The route's next link is broken, so pages are read by offset until the
// total is reached.
func TestListWorkloadHistory_PagesByOffset(t *testing.T) {
	var offsets []string

	serveAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/workloads/wl-1/history/", r.URL.Path, "the /console/ next link is not followed")

		offset := r.URL.Query().Get("offset")
		offsets = append(offsets, offset)

		start, _ := strconv.Atoi(offset)

		var records []string

		for i := start; i < min(start+maxPageSize, maxPageSize+1); i++ {
			records = append(records, historyRecord(strconv.Itoa(i), time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339), "completed"))
		}

		fmt.Fprint(w, historyPage(maxPageSize+1, records...))
	}))

	entries, err := ListWorkloadHistory("wl-1", 1000)
	require.NoError(t, err)
	assert.Equal(t, []string{"0", strconv.Itoa(maxPageSize)}, offsets)
	require.Len(t, entries, maxPageSize+1)
	assert.Equal(t, strconv.Itoa(maxPageSize), entries[0].ID)
}

// A route that keeps sending full pages is abandoned at the cap.
func TestListWorkloadHistory_StopsAtThePageCap(t *testing.T) {
	calls := 0

	serveAPI(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++

		records := make([]string, maxPageSize)
		for i := range records {
			records[i] = historyRecord("x", "2026-10-02T12:00:00Z", "completed")
		}

		fmt.Fprint(w, historyPage(0, records...))
	}))

	_, err := ListWorkloadHistory("wl-1", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not end after 50 pages")
	assert.Equal(t, maxHistoryPages, calls)
}

func TestListWorkloadHistory_RejectsANonPositiveLimit(t *testing.T) {
	_, err := ListWorkloadHistory("wl-1", 0)
	require.Error(t, err)
}

func TestRenderWorkloadHistory(t *testing.T) {
	var entries []HistoryEntry

	require.NoError(t, json.Unmarshal([]byte("["+historyRecord("h1", "2026-10-02T15:00:47.602000+00:00", "completed")+"]"), &entries))

	var text bytes.Buffer

	require.NoError(t, RenderWorkloadHistoryTo(&text, outputformat.OutputFormatText, entries))
	assert.Contains(t, text.String(), "2026-10-02 15:00 UTC")
	assert.Contains(t, text.String(), "rolling")
	assert.Contains(t, text.String(), "art-h1")
	assert.Contains(t, text.String(), "replacement completed")

	var out bytes.Buffer

	require.NoError(t, RenderWorkloadHistoryTo(&out, outputformat.OutputFormatJSON, entries))

	var envelope struct {
		History []map[string]any `json:"history"`
	}

	require.NoError(t, json.Unmarshal(out.Bytes(), &envelope))
	require.Len(t, envelope.History, 1)
	assert.Contains(t, envelope.History[0], "runtime", "the record is re-emitted whole")

	var empty bytes.Buffer

	require.NoError(t, RenderWorkloadHistoryTo(&empty, outputformat.OutputFormatJSON, nil))
	assert.JSONEq(t, `{"history":[]}`, empty.String())

	var none bytes.Buffer

	require.NoError(t, RenderWorkloadHistoryTo(&none, outputformat.OutputFormatText, nil))
	assert.Equal(t, "No history found.\n", none.String())
}
