package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL + "/api/v4", Token: "glpat-secret", HTTP: srv.Client()}
}

func TestProjectAccessLevels(t *testing.T) {
	tests := []struct {
		name, body string
		canPush    bool
		mrs        bool
	}{
		{"developer via project", `{"id":7,"default_branch":"main","permissions":{"project_access":{"access_level":30},"group_access":null}}`, true, true},
		{"maintainer via group", `{"id":7,"permissions":{"project_access":null,"group_access":{"access_level":40}}}`, true, true},
		{"reporter", `{"id":7,"permissions":{"project_access":{"access_level":20}}}`, false, true},
		{"no permissions block", `{"id":7}`, false, true},
		{"mrs disabled", `{"id":7,"merge_requests_access_level":"disabled","permissions":{"project_access":{"access_level":50}}}`, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != "/api/v4/projects/team%2Fsub%2Fapp" || r.Header.Get("PRIVATE-TOKEN") != "glpat-secret" {
					t.Errorf("request = %s token=%q", r.URL.EscapedPath(), r.Header.Get("PRIVATE-TOKEN"))
				}
				_, _ = w.Write([]byte(tt.body))
			})
			p, err := c.Project(t.Context(), "team/sub/app")
			if err != nil {
				t.Fatal(err)
			}
			if p.CanPush != tt.canPush || p.MergeRequestsEnabled != tt.mrs {
				t.Fatalf("got %+v", p)
			}
		})
	}
}

func TestErrorsRedactTokenAndNeverFollowRedirects(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			t.Error("client followed a redirect with the token")
		}
		if r.URL.Path == "/api/v4/redirect" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"403 Forbidden - glpat-secret lacks scope"}`))
	})
	err := c.Do(t.Context(), http.MethodGet, "/projects/x", nil, nil)
	var apiErr *APIError
	if !errorsAs(err, &apiErr) || apiErr.Status != 403 || strings.Contains(err.Error(), "glpat-secret") {
		t.Fatalf("err = %v", err)
	}
	if err := c.Do(t.Context(), http.MethodGet, "/redirect", nil, nil); err == nil {
		t.Fatal("redirect response must surface as an error")
	}
}

func TestDestinationCheckRunsBeforeRequest(t *testing.T) {
	c := &Client{BaseURL: "https://127.0.0.1/api/v4", HTTP: http.DefaultClient, CheckHost: func(context.Context, string) error { return http.ErrNotSupported }}
	if err := c.Do(t.Context(), http.MethodGet, "/projects/x", nil, nil); err != http.ErrNotSupported {
		t.Fatalf("err = %v", err)
	}
}

func errorsAs(err error, target **APIError) bool {
	e, ok := err.(*APIError)
	if ok {
		*target = e
	}
	return ok
}
