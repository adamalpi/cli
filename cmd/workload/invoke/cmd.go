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

package invoke

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/datarobot/cli/cmd/workload/internal/idargs"
	"github.com/datarobot/cli/internal/auth"
	"github.com/datarobot/cli/internal/drapi"
	"github.com/datarobot/cli/internal/features"
	"github.com/datarobot/cli/internal/telemetry"
	"github.com/datarobot/cli/internal/workload"
	"github.com/spf13/cobra"
)

type options struct {
	method  string
	headers []string
	data    string
	json    string
	include bool
	output  string
	timeout time.Duration
}

func Cmd() *cobra.Command {
	var (
		opts options
		ref  idargs.Ref
	)

	cmd := &cobra.Command{
		Use:   "invoke [<workload-id>] <path>",
		Short: "Send an HTTP request to a workload's endpoint.",
		Long: `Send an HTTP request to a workload's endpoint and write the response
body to stdout as it arrives.

The path is appended to the endpoint URL ('dr workload endpoint'). Your
DataRobot token is attached only when the endpoint is the DataRobot
gateway on your configured host; any other URL is called without it.
Redirects are not followed.

The method defaults to GET, or POST when a body is given. --data sends
its body as is; --json also sets Content-Type and Accept to
application/json. Both take a literal, @file, or - for stdin.

The command exits non-zero on an HTTP 4xx or 5xx, after writing the
body. With --output-format json the error is reported as a JSON
envelope on stderr; the body is never reshaped.

` + idargs.HelpText + `

Example:
  dr workload invoke /health
  dr workload invoke 68b0c1d2e3f4a5b6c7d8e9f0 /version -i
  dr workload invoke /predict --json '{"text": "hello"}'
  dr workload invoke /predict --json @request.json -o response.json
  dr workload invoke /upload -X PUT -H 'Content-Type: text/csv' --data @rows.csv`,
		Args:         cobra.RangeArgs(1, 2),
		PreRunE:      auth.EnsureAuthenticatedE,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[len(args)-1]

			req, err := opts.request(cmd.InOrStdin())
			if err != nil {
				return err
			}

			ref, err = idargs.Resolve(cmd, args[:len(args)-1])
			if err != nil {
				return err
			}

			wl, err := workload.GetWorkload(ref.ID)
			if err != nil {
				return ref.Wrap(err)
			}

			if wl.Endpoint == "" {
				return fmt.Errorf("workload %s has no endpoint URL", ref.ID)
			}

			req.URL, err = workload.InvokeURL(wl.Endpoint, path)
			if err != nil {
				return err
			}

			return run(cmd, req, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.method, "request", "X", "", "HTTP method (default GET, or POST with a body)")
	cmd.Flags().StringArrayVarP(&opts.headers, "header", "H", nil, "request header as 'Name: value' (repeatable)")
	cmd.Flags().StringVarP(&opts.data, "data", "d", "", "request body: a literal, @file, or - for stdin")
	cmd.Flags().StringVar(&opts.json, "json", "", "JSON request body: a literal, @file, or - for stdin")
	cmd.Flags().BoolVarP(&opts.include, "include", "i", false, "write the response status line and headers before the body")
	cmd.Flags().StringVarP(&opts.output, "output", "o", "", "write the response body to this file instead of stdout")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", drapi.DefaultClientTimeout,
		"time allowed for the whole request, including reading the body")
	cmd.MarkFlagsMutuallyExclusive("data", "json")

	idargs.AddDirFlag(cmd)
	features.SetGate(cmd, "workload-alpha")

	telemetry.TrackWith(cmd, func(_ *cobra.Command, args []string) map[string]any {
		idArgs := args
		if len(args) > 0 {
			idArgs = args[:len(args)-1]
		}

		return map[string]any{
			"workload_id":        idargs.TelemetryID(ref, idArgs),
			"workload_id_source": ref.Source,
			"method":             opts.methodOrDefault(),
		}
	})

	return cmd
}

func (o options) methodOrDefault() string {
	switch {
	case o.method != "":
		return strings.ToUpper(o.method)
	case o.data != "" || o.json != "":
		return http.MethodPost
	default:
		return http.MethodGet
	}
}

// request builds everything but the URL, so a bad flag fails before any
// API call.
func (o options) request(stdin io.Reader) (workload.InvokeRequest, error) {
	req := workload.InvokeRequest{
		Method:  o.methodOrDefault(),
		Header:  http.Header{},
		Timeout: o.timeout,
	}

	source := o.data

	if o.json != "" {
		source = o.json

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	}

	if source != "" {
		body, err := readBody(source, stdin)
		if err != nil {
			return req, err
		}

		req.Body = bytes.NewReader(body)
	}

	named := map[string]bool{}

	for _, raw := range o.headers {
		name, value, ok := strings.Cut(raw, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return req, fmt.Errorf("header %q must be 'Name: value'", raw)
		}

		name = http.CanonicalHeaderKey(strings.TrimSpace(name))

		// A header the user names replaces the default for it; repeats add.
		if !named[name] {
			req.Header.Del(name)

			named[name] = true
		}

		req.Header.Add(name, strings.TrimSpace(value))
	}

	return req, nil
}

func readBody(source string, stdin io.Reader) ([]byte, error) {
	switch {
	case source == "-" || source == "@-":
		return io.ReadAll(stdin)
	case strings.HasPrefix(source, "@"):
		body, err := os.ReadFile(source[1:])
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}

		return body, nil
	default:
		return []byte(source), nil
	}
}

func run(cmd *cobra.Command, req workload.InvokeRequest, opts options) error {
	resp, err := workload.Invoke(cmd.Context(), req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	stdout := cmd.OutOrStdout()

	if opts.include {
		fmt.Fprintf(stdout, "%s %s\r\n", resp.Proto, resp.Status)

		if err := resp.Header.Write(stdout); err != nil {
			return err
		}

		fmt.Fprint(stdout, "\r\n")
	}

	if err := writeBody(stdout, resp.Body, opts.output); err != nil {
		return err
	}

	location := resp.Header.Get("Location")
	if isRedirect(resp.StatusCode) && location != "" && !opts.include {
		fmt.Fprintf(cmd.ErrOrStderr(), "Redirect to %s not followed (HTTP %d).\n", location, resp.StatusCode)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return &drapi.HTTPError{StatusCode: resp.StatusCode, URL: req.URL}
	}

	return nil
}

func isRedirect(status int) bool {
	return status >= http.StatusMultipleChoices && status < http.StatusBadRequest
}

func writeBody(stdout io.Writer, body io.Reader, output string) error {
	if output == "" {
		_, err := io.Copy(stdout, body)

		return err
	}

	file, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}

	_, err = io.Copy(file, body)

	return errors.Join(err, file.Close())
}
