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
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/datarobot/cli/internal/outputformat"
	"github.com/datarobot/cli/tui"
)

// RenderWorkloadHistoryTo prints the history to w. Under JSON the writer
// receives one {"history": [...]} document, [] when there is none.
func RenderWorkloadHistoryTo(w io.Writer, format outputformat.OutputFormat, entries []HistoryEntry) error {
	if format == outputformat.OutputFormatJSON {
		if entries == nil {
			entries = []HistoryEntry{}
		}

		return outputformat.PrintJSONEnvelope(w, "history", entries)
	}

	if len(entries) == 0 {
		fmt.Fprintln(w, "No history found.")

		return nil
	}

	fmt.Fprintln(w, historyTable(entries, terminalWidth(w)))

	return nil
}

func historyTable(entries []HistoryEntry, width int) string {
	cellStyle := tui.BaseTextStyle.Padding(0, 1)

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(tui.TableBorderStyle).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return cellStyle.Bold(true)
			}

			return cellStyle
		}).
		Headers("CREATED", "STATUS", "STRATEGY", "ARTIFACT", "MESSAGE")

	if width > 0 {
		t = t.Width(width)
	}

	for _, h := range entries {
		t.Row(h.CreatedAt.UTC().Format(timestampFormat), orPlaceholder(h.Status), orPlaceholder(h.Strategy),
			orPlaceholder(h.CandidateArtifactID), orPlaceholder(h.Message))
	}

	return t.String()
}
