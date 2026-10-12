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
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datarobot/cli/internal/drapi"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmd_Args(t *testing.T) {
	cmd := Cmd()

	require.NoError(t, cmd.Args(cmd, []string{"/health"}))
	require.NoError(t, cmd.Args(cmd, []string{"68b0c1d2e3f4a5b6c7d8e9f0", "/health"}))
	require.Error(t, cmd.Args(cmd, nil))
	require.Error(t, cmd.Args(cmd, []string{"a", "b", "c"}))
}

func TestOptions_Method(t *testing.T) {
	assert.Equal(t, http.MethodGet, options{}.methodOrDefault())
	assert.Equal(t, http.MethodPost, options{data: "x"}.methodOrDefault())
	assert.Equal(t, http.MethodPost, options{json: "{}"}.methodOrDefault())
	assert.Equal(t, http.MethodPut, options{method: "put", data: "x"}.methodOrDefault())
}

func TestOptions_Request(t *testing.T) {
	file := filepath.Join(t.TempDir(), "body.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"a":1}`), 0o600))

	tests := []struct {
		name  string
		opts  options
		stdin string
		body  string
	}{
		{"literal", options{data: "hello"}, "", "hello"},
		{"file", options{json: "@" + file}, "", `{"a":1}`},
		{"stdin", options{data: "-"}, "piped", "piped"},
		{"stdin with @", options{json: "@-"}, "piped", "piped"},
	}

	for _, tt := range tests {
		req, err := tt.opts.request(strings.NewReader(tt.stdin))
		require.NoError(t, err, tt.name)

		body, err := io.ReadAll(req.Body)
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.body, string(body), tt.name)
	}
}

func TestOptions_RequestHeaders(t *testing.T) {
	req, err := options{
		json:    "{}",
		headers: []string{"accept: text/plain", "X-Trace: a", "x-trace:b"},
	}.request(nil)
	require.NoError(t, err)

	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.Equal(t, []string{"text/plain"}, req.Header.Values("Accept"), "a named header replaces the default")
	assert.Equal(t, []string{"a", "b"}, req.Header.Values("X-Trace"), "repeats add")

	_, err = options{headers: []string{"no colon"}}.request(nil)
	require.ErrorContains(t, err, "must be 'Name: value'")
}

func runAgainst(t *testing.T, handler http.HandlerFunc, opts options) (string, error) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, err := opts.request(nil)
	require.NoError(t, err)

	// Not a gateway path, so the call stays anonymous and needs no auth setup.
	req.URL = srv.URL + "/direct/path"

	var out bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	err = run(cmd, req, opts)

	return out.String(), err
}

func TestRun_IncludeWritesStatusAndHeadersBeforeBody(t *testing.T) {
	out, err := runAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-App", "1")
		_, _ = io.WriteString(w, "hello")
	}, options{include: true, timeout: 5 * time.Second})

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, "HTTP/1.1 200 OK\r\n"), out)
	assert.Contains(t, out, "X-App: 1\r\n")
	assert.True(t, strings.HasSuffix(out, "\r\n\r\nhello"), out)
}

func TestRun_ErrorStatusWritesBodyThenFails(t *testing.T) {
	out, err := runAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"detail":"warming up"}`)
	}, options{timeout: 5 * time.Second})

	assert.JSONEq(t, `{"detail":"warming up"}`, out, "the body is written before the failure")

	var httpErr *drapi.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusServiceUnavailable, httpErr.StatusCode)
}

func TestRun_OutputWritesBodyToFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "out.bin")

	out, err := runAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "payload")
	}, options{output: file, timeout: 5 * time.Second})

	require.NoError(t, err)
	assert.Empty(t, out)

	content, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(content))
}

func TestRun_SendsMethodAndBody(t *testing.T) {
	var gotMethod, gotBody string

	_, err := runAgainst(t, func(_ http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method

		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
	}, options{json: `{"q":1}`, timeout: 5 * time.Second})

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.JSONEq(t, `{"q":1}`, gotBody)
}
