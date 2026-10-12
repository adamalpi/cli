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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/datarobot/cli/internal/config"
	"github.com/datarobot/cli/internal/drapi"
	"github.com/datarobot/cli/internal/log"
)

const gatewayPathPrefix = config.DRAPIURLSuffix + "/endpoints/workloads/"

// EndpointTakesToken reports whether a workload endpoint URL may carry the
// CLI token. Only the DataRobot gateway on the configured origin does: it
// consumes the token before proxying, while a direct URL would carry it into
// the user's container access logs.
func EndpointTakesToken(rawURL string) (bool, error) {
	endpointURL, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Errorf("parse endpoint URL %q: %w", rawURL, err)
	}

	if endpointURL.Scheme == "" || endpointURL.Host == "" {
		return false, fmt.Errorf("endpoint URL %q is not absolute", rawURL)
	}

	if !strings.HasPrefix(endpointURL.EscapedPath(), gatewayPathPrefix) {
		return false, nil
	}

	matches, err := drapi.URLMatchesConfiguredBase(rawURL)
	if err != nil || !matches {
		log.Debug("withholding token because endpoint URL does not match configured base",
			"endpoint", rawURL,
			"configured_base", config.GetBaseURL(),
			"error", err)

		return false, nil
	}

	return true, nil
}

// InvokeURL joins path under the workload endpoint. A path that would climb
// out of it is refused, so the token meant for one workload stays on it.
func InvokeURL(endpoint, path string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint URL %q: %w", endpoint, err)
	}

	rel, query, hasQuery := strings.Cut(strings.TrimLeft(path, "/"), "?")

	for _, segment := range strings.Split(rel, "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return "", fmt.Errorf("path %q: %w", path, err)
		}

		if decoded == ".." || decoded == "." {
			return "", fmt.Errorf("path %q must stay under the workload endpoint", path)
		}
	}

	joined := strings.TrimSuffix(endpoint, "/") + "/" + rel
	if hasQuery {
		joined += "?" + query
	}

	target, err := url.Parse(joined)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", path, err)
	}

	if target.Scheme != base.Scheme || target.Host != base.Host {
		return "", fmt.Errorf("path %q must stay under the workload endpoint", path)
	}

	return target.String(), nil
}

// InvokeRequest is one call to a workload endpoint.
type InvokeRequest struct {
	URL     string
	Method  string
	Header  http.Header
	Body    io.Reader
	Timeout time.Duration
}

// Invoke sends r and returns the response with its body unread. The caller
// closes it. Redirects come back as the 3xx they are: following one would
// leave the gateway.
func Invoke(ctx context.Context, r InvokeRequest) (*http.Response, error) {
	takesToken, err := EndpointTakesToken(r.URL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, r.Body)
	if err != nil {
		return nil, err
	}

	if takesToken {
		if err := drapi.AuthorizeRequest(req); err != nil { //nolint:contextcheck // drapi takes no context
			return nil, err
		}
	} else {
		req.Header.Set("User-Agent", config.GetUserAgentHeader())
	}

	for name, values := range r.Header {
		req.Header[name] = values
	}

	client := drapi.NewHTTPClient(r.Timeout)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Timeout() {
			return nil, fmt.Errorf("no response from %s within %s", r.URL, client.Timeout)
		}

		return nil, err
	}

	return resp, nil
}
