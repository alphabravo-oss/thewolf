package middleware

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		method, path              string
		event, category, severity string
	}{
		{"POST", "/api/v1/auth/mfa/disable", "auth.mfa.disabled", "authentication", "critical"},
		{"POST", "/api/v1/auth/mfa/activate", "auth.mfa.enabled", "authentication", "warning"},
		{"PUT", "/api/v1/users/123/role", "user.role.changed", "authorization", "critical"},
		{"POST", "/api/v1/users/123/mfa/reset", "auth.mfa.reset", "authentication", "critical"},
		{"DELETE", "/api/v1/users/123", "user.deleted", "authorization", "critical"},
		{"POST", "/api/v1/config/secrets", "secret.created", "secrets", "warning"},
		{"DELETE", "/api/v1/config/secrets/9", "secret.deleted", "secrets", "warning"},
		{"POST", "/api/v1/auth/tokens", "apikey.created", "secrets", "warning"},
		{"PUT", "/api/v1/settings", "settings.updated", "configuration", "warning"},
		// Defaults: unmatched data routes.
		{"POST", "/api/v1/scans", "scans.created", "data", "info"},
		{"DELETE", "/api/v1/repos/9", "repos.deleted", "data", "warning"},
		{"PUT", "/api/v1/nodes/9", "nodes.updated", "configuration", "info"},
	}
	for _, c := range cases {
		e, cat, sev := Classify(c.method, c.path)
		if e != c.event || cat != c.category || sev != c.severity {
			t.Errorf("Classify(%s %s) = (%s, %s, %s), want (%s, %s, %s)",
				c.method, c.path, e, cat, sev, c.event, c.category, c.severity)
		}
	}
}
