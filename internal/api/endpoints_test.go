package api_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api"
	"github.com/alphabravocompany/thewolf/internal/api/openapi"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
)

var braceRe = regexp.MustCompile(`\{[^}]+\}`)

// heavyEndpoints may trigger Docker / long-running work; for these we still
// verify auth + scope but skip the JWT happy-path exercise.
var heavyEndpoints = map[string]bool{
	"POST /scanners/doctor":               true,
	"POST /scanners/pull":                 true,
	"POST /scanners/images/pull":          true,
	"POST /scanners/tools/check-updates":  true,
	"POST /config/plugins/{name}/install": true,
}

// reqFrom issues a request from a specified client IP. Each test case uses a
// distinct IP so the per-IP rate limiter (60-burst) never trips during the
// rapid-fire endpoint sweep — keeping the test about auth, not throttling.
func reqFrom(srv *api.Server, method, path, bearer, ip string, body []byte) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	r.Header.Set("X-Forwarded-For", ip)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, r)
	return w
}

// TestEveryEndpointAuthScopeAndReachability walks the entire OpenAPI catalog
// and, for every endpoint, asserts:
//   - public endpoints do not demand a credential;
//   - protected endpoints reject a missing credential with 401;
//   - protected endpoints reject an insufficiently-scoped token with 403;
//   - with a full JWT the handler runs without a 5xx and without a routing
//     404 (a handler 404 carrying the error envelope is fine).
func TestEveryEndpointAuthScopeAndReachability(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	// A token holding only read:findings — it satisfies the /findings GET
	// endpoints and nothing else, so every other protected endpoint must
	// reject it with 403.
	weak := createToken(t, srv, jwt, []string{apikey.ScopeReadFindings})

	const placeholder = "00000000-0000-0000-0000-000000000000"

	for i, ep := range openapi.Endpoints() {
		ep := ep
		ip := fmt.Sprintf("10.1.%d.%d", i/256, i%256)
		key := ep.Method + " " + ep.Path
		t.Run(key, func(t *testing.T) {
			path := "/api/v1" + braceRe.ReplaceAllString(ep.Path, placeholder)
			var body []byte
			if ep.Body != "" {
				body = []byte("{}")
			}

			// Public endpoints: a missing credential must not yield 401.
			if ep.Scope == "" {
				w := reqFrom(srv, ep.Method, path, "", ip, body)
				if w.Code == http.StatusUnauthorized {
					t.Errorf("public endpoint returned 401")
				}
				return
			}

			// Protected: no credential -> 401.
			if w := reqFrom(srv, ep.Method, path, "", ip, body); w.Code != http.StatusUnauthorized {
				t.Errorf("no credential: expected 401, got %d", w.Code)
			}

			// Protected with a scope requirement: a weak token -> 403.
			// Skipped for `self` endpoints and the read:findings endpoints
			// the weak token legitimately satisfies.
			if ep.Scope != "self" && ep.Scope != apikey.ScopeReadFindings {
				w := reqFrom(srv, ep.Method, path, weak, ip, body)
				if w.Code != http.StatusForbidden {
					t.Errorf("weak token: expected 403, got %d: %s", w.Code, w.Body.String())
				}
			}

			// JWT reachability: handler runs, no 5xx, no routing 404.
			if heavyEndpoints[key] || strings.HasSuffix(ep.Path, "/stream") {
				return
			}
			w := reqFrom(srv, ep.Method, path, jwt, ip, body)
			// 500 signals a crash/bug. 503 is acceptable: it's the graceful
			// "optional subsystem not configured" response (the container
			// scanner backend is not initialized in tests).
			if w.Code == http.StatusInternalServerError {
				t.Errorf("JWT request: internal server error: %s", w.Body.String())
			}
			if w.Code >= 500 && w.Code != http.StatusServiceUnavailable {
				t.Errorf("JWT request: unexpected server error %d: %s", w.Code, w.Body.String())
			}
			if w.Code >= 500 && !strings.Contains(w.Body.String(), `"error"`) {
				t.Errorf("JWT request: %d without an error envelope (likely a panic): %q", w.Code, w.Body.String())
			}
			if w.Code == http.StatusNotFound && !strings.Contains(w.Body.String(), `"error"`) {
				t.Errorf("JWT request: routing 404 (handler not reached): %q", w.Body.String())
			}
		})
	}
}
