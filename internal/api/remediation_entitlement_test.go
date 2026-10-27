package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

type grantsCapability string

func (g grantsCapability) Allows(capability string) bool {
	if strings.HasPrefix(capability, "enterprise.") {
		return capability == string(g)
	}
	return true
}

// grantRemediation runs the test as an Enterprise build licensed for AI fixing.
func grantRemediation(t *testing.T) {
	t.Helper()
	entitlement.SetActive(grantsCapability(entitlement.Remediation))
	t.Cleanup(func() { entitlement.SetActive(nil) })
}

// Community scans and reports; AI remediation is Enterprise-only, so every
// fix and remediation endpoint refuses before any handler or setting is read.
func TestCommunityCannotUseRemediation(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	read := createToken(t, srv, jwt, []string{apikey.ScopeReadFixes})
	write := createToken(t, srv, jwt, []string{apikey.ScopeWriteFixes})
	for _, c := range []struct{ method, path, token string }{
		{http.MethodGet, "/api/v1/fixes", read},
		{http.MethodGet, "/api/v1/fixes/engines", read},
		{http.MethodPost, "/api/v1/fixes", write},
		{http.MethodPost, "/api/v1/fixes/consoles", write},
		{http.MethodPost, "/api/v1/remediations/x/accept", write},
	} {
		w := request(srv, c.method, c.path, c.token, []byte(`{}`))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "enterprise_required") {
			t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	// Granting the capability reaches the normal handlers (disabled by default).
	grantRemediation(t)
	if w := request(srv, http.MethodPost, "/api/v1/fixes", write, []byte(`{}`)); !strings.Contains(w.Body.String(), "autofix_disabled") {
		t.Fatalf("entitled but disabled should reach the setting gate: %d %s", w.Code, w.Body.String())
	}
}
