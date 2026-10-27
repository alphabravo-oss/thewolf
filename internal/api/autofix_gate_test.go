package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
)

func TestAutofixPublicExecutionGate(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	grantRemediation(t)
	read := createToken(t, srv, jwt, []string{apikey.ScopeReadFixes})
	write := createToken(t, srv, jwt, []string{apikey.ScopeWriteFixes})
	if value, err := store.GetSetting(t.Context(), "autofix_enabled"); err != nil || value != "false" {
		t.Fatalf("fresh server must default off: %q %v", value, err)
	}
	w := request(srv, http.MethodGet, "/api/v1/fixes/engines", read, nil)
	var engines struct {
		Data map[string]any `json:"data"`
	}
	mustJSON(t, w.Body.Bytes(), &engines)
	if w.Code != http.StatusOK || len(engines.Data) != 1 || engines.Data["configured"] == nil {
		t.Fatalf("disabled engine details leaked: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v1/fixes", "/api/v1/fixes/paused-job/resume"} {
		w := request(srv, http.MethodPost, path, write, []byte(`{}`))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "autofix_disabled") {
			t.Fatalf("disabled execution %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w = request(srv, http.MethodPut, "/api/v1/settings", jwt, []byte(`{"autofix_enabled":"true"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("admin enable: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v1/fixes", "/api/v1/fixes/paused-job/resume"} {
		w := request(srv, http.MethodPost, path, read, []byte(`{}`))
		if w.Code != http.StatusForbidden {
			t.Fatalf("read-only execution %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// Changing the role in the database must revoke settings access even with
	// the still-valid JWT that was minted while this user was an administrator.
	user, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	user.Role = "user"
	if err := store.UpdateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	w = request(srv, http.MethodPut, "/api/v1/settings", jwt, []byte(`{"autofix_enabled":"false"}`))
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin changed flag: %d %s", w.Code, w.Body.String())
	}
}
