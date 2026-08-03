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
		{"PUT", "/api/v1/users/123/scanner-supply-chain-access", "user.scanner_supply_chain_access.changed", "authorization", "critical"},
		{"POST", "/api/v1/users/123/mfa/reset", "auth.mfa.reset", "authentication", "critical"},
		{"DELETE", "/api/v1/users/123", "user.deleted", "authorization", "critical"},
		{"POST", "/api/v1/config/secrets", "secret.created", "secrets", "warning"},
		{"DELETE", "/api/v1/config/secrets/9", "secret.deleted", "secrets", "warning"},
		{"POST", "/api/v1/auth/tokens", "apikey.created", "secrets", "warning"},
		{"PUT", "/api/v1/settings", "settings.updated", "configuration", "warning"},
		{"PUT", "/api/v1/scanner-supply-chain/policy", "scanner.policy.updated", "configuration", "critical"},
		{"PATCH", "/api/v1/scanner-supply-chain/registries/registry-1", "scanner.registry.updated", "configuration", "critical"},
		{"POST", "/api/v1/scanner-supply-chain/candidates/candidate-1/approve", "scanner.candidate.approved", "system", "critical"},
		{"POST", "/api/v1/scanner-supply-chain/candidates/candidate-1/reject", "scanner.candidate.rejected", "system", "warning"},
		{"POST", "/api/v1/scanner-supply-chain/releases/release-1/revoke", "scanner.release.revoked", "system", "critical"},
		{"POST", "/api/v1/scanner-supply-chain/rollouts/rollout-1/rollback", "scanner.rollout.rollback", "system", "critical"},
		{"POST", "/api/v1/scanner-supply-chain/release-imports", "scanner.release.imported", "system", "critical"},
		{"GET", "/api/v1/scanner-supply-chain/releases/rel-1/export", "scanner.release.bundle_exported", "system", "info"},
		{"POST", "/api/v1/scanners/custom-builds", "scanner.custom_build.queued", "system", "warning"},
		{"POST", "/api/v1/scanners/custom-builds/build-1/cancel", "scanner.custom_build.cancellation_requested", "system", "warning"},
		{"POST", "/api/v1/scanners/custom-builds/build-1/retry", "scanner.custom_build.retried", "system", "warning"},
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

func TestOnlyReleaseBundleExportIsAnAuditedRead(t *testing.T) {
	if !isAuditedRead("/api/v1/scanner-supply-chain/releases/rel-1/export") {
		t.Fatal("release bundle export should be audited")
	}
	for _, path := range []string{
		"/api/v1/scanner-supply-chain/releases/rel-1",
		"/api/v1/scanner-supply-chain/audit/export",
		"/api/v1/scanner-supply-chain/releases/export",
	} {
		if isAuditedRead(path) {
			t.Errorf("ordinary read %q should not be audited", path)
		}
	}
}
