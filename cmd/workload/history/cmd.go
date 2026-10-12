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
	"github.com/datarobot/cli/cmd/workload/internal/idargs"
	"github.com/datarobot/cli/internal/auth"
	"github.com/datarobot/cli/internal/countflags"
	"github.com/datarobot/cli/internal/outputformat"
	"github.com/datarobot/cli/internal/telemetry"
	"github.com/datarobot/cli/internal/workload"
	"github.com/spf13/cobra"
)

// listHistoryFn is the client call, a variable so tests can stand in for it.
var listHistoryFn = workload.ListWorkloadHistory

func Cmd() *cobra.Command {
	var outputFormat outputformat.OutputFormat

	var ref idargs.Ref

	var limit int

	cmd := &cobra.Command{
		Use:   "history [<workload-id>]",
		Short: "Show a workload's rollout history.",
		Long: `Show the rollouts the platform recorded for a workload: each time it
moved to an artifact or changed its runtime, which artifact, the strategy,
and how it ended.

The most recent --limit entries are printed newest first. By default,
output is a table with the platform's own message for each rollout. Use
--output-format json for the complete records, runtime included, as one
{"history": [...]} document.

` + idargs.HelpText + `

Example:
  dr workload history
  dr workload history 68b0c1d2e3f4a5b6c7d8e9f0 --limit 5
  dr workload history 68b0c1d2e3f4a5b6c7d8e9f0 --output-format json`,
		Args:         cobra.MaximumNArgs(1),
		PreRunE:      auth.EnsureAuthenticatedE,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFormat = outputformat.GetFormat(cmd)

			var err error

			ref, err = idargs.Resolve(cmd, args)
			if err != nil {
				return err
			}

			entries, err := listHistoryFn(ref.ID, limit)
			if err != nil {
				return ref.Wrap(err)
			}

			return workload.RenderWorkloadHistoryTo(cmd.OutOrStdout(), outputFormat, entries)
		},
	}

	outputformat.AddFlag(cmd, &outputFormat)
	idargs.AddDirFlag(cmd)

	cmd.Flags().Var(countflags.PositiveInt(&limit, 20), "limit", "Maximum number of recent rollouts to show")

	telemetry.TrackWith(cmd, func(c *cobra.Command, args []string) map[string]any {
		return map[string]any{
			"workload_id":        idargs.TelemetryID(ref, args),
			"workload_id_source": ref.Source,
			"limit":              limit,
			"output_format":      string(outputFormat),
		}
	})

	return cmd
}
