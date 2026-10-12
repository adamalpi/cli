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
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gatewayPath = "/api/v2/endpoints/workloads/wl-1/"

// recordAuth serves 200 and records the Authorization header of each request.
func recordAuth(t *testing.T, got *[]string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = append(*got, r.Header.Get("Authorization"))

		_, _ = io.WriteString(w, "ok")
	}))

	t.Cleanup(srv.Close)

	return srv
}

// invoke sends a GET and returns the status code.
func invoke(t *testing.T, rawURL string, header http.Header) int {
	t.Helper()

	resp, err := Invoke(context.Background(), InvokeRequest{
		URL:     rawURL,
		Method:  http.MethodGet,
		Header:  header,
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)

	_ = resp.Body.Close()

	return resp.StatusCode
}

func TestInvoke_SendsTokenToConfiguredGateway(t *testing.T) {
	var auth []string

	srv := recordAuth(t, &auth)

	installSkipAuth(t)
	installEndpoint(t, srv.URL)

	invoke(t, srv.URL+gatewayPath+"health", nil)

	require.Len(t, auth, 1)
	assert.True(t, strings.HasPrefix(auth[0], "Bearer "), "the gateway call carries the token")
}

func TestInvoke_NeverSendsTokenOffTheGateway(t *testing.T) {
	var auth []string

	configured := recordAuth(t, &auth)
	other := recordAuth(t, &auth)

	installSkipAuth(t)
	installEndpoint(t, configured.URL)

	// A gateway path on another origin, and a direct URL on the configured one.
	invoke(t, other.URL+gatewayPath+"health", nil)
	invoke(t, configured.URL+"/direct/health", nil)

	assert.Equal(t, []string{"", ""}, auth)
}

func TestInvoke_DoesNotFollowRedirects(t *testing.T) {
	var auth []string

	elsewhere := recordAuth(t, &auth)

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/login", http.StatusFound)
	}))

	t.Cleanup(gateway.Close)

	installSkipAuth(t)
	installEndpoint(t, gateway.URL)

	assert.Equal(t, http.StatusFound, invoke(t, gateway.URL+gatewayPath, nil))
	assert.Empty(t, auth, "the redirect target is never called")
}

func TestInvoke_UserHeadersWin(t *testing.T) {
	var auth []string

	srv := recordAuth(t, &auth)

	installSkipAuth(t)
	installEndpoint(t, srv.URL)

	invoke(t, srv.URL+gatewayPath, http.Header{"Authorization": {"Bearer mine"}})

	assert.Equal(t, []string{"Bearer mine"}, auth)
}

func TestInvokeURL(t *testing.T) {
	const endpoint = "https://app.example.test/api/v2/endpoints/workloads/wl-1/"

	tests := []struct {
		path string
		want string
	}{
		{"/version", endpoint + "version"},
		{"version", endpoint + "version"},
		{"", endpoint},
		{"/", endpoint},
		{"/v1/models:predict?x=1&y=a%20b", endpoint + "v1/models:predict?x=1&y=a%20b"},
		{"//evil.example/x", endpoint + "evil.example/x"},
		{"https://evil.example/x", endpoint + "https://evil.example/x"},
	}

	for _, tt := range tests {
		got, err := InvokeURL(endpoint, tt.path)
		require.NoError(t, err, tt.path)
		assert.Equal(t, tt.want, got, tt.path)
	}

	got, err := InvokeURL(strings.TrimSuffix(endpoint, "/"), "health")
	require.NoError(t, err)
	assert.Equal(t, endpoint+"health", got, "an endpoint without a trailing slash still joins")
}

func TestInvokeURL_RefusesPathsThatLeaveTheEndpoint(t *testing.T) {
	const endpoint = "https://app.example.test/api/v2/endpoints/workloads/wl-1/"

	for _, path := range []string{"../wl-2/", "/a/../../x", "%2e%2e/x", "./x"} {
		_, err := InvokeURL(endpoint, path)
		require.Error(t, err, path)
		assert.Contains(t, err.Error(), "must stay under the workload endpoint", path)
	}
}
