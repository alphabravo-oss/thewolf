package rust

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

func TestSCADependencyIdentityCargoAudit(t *testing.T) {
	findings, err := parseCargoAuditOutput([]byte(`{"vulnerabilities":{"list":[{"advisory":{"id":"RUSTSEC-CVE-2026-1001","aliases":["CVE-2026-1001"],"title":"dependency advisory"},"package":{"name":"alpha","version":"1"}},{"advisory":{"id":"RUSTSEC-CVE-2026-1002","aliases":["CVE-2026-1002"],"title":"dependency advisory"},"package":{"name":"alpha","version":"1"}},{"advisory":{"id":"RUSTSEC-CVE-2026-1001","aliases":["CVE-2026-1001"],"title":"dependency advisory"},"package":{"name":"beta","version":"1"}},{"advisory":{"id":"RUSTSEC-CVE-2026-1001","aliases":["CVE-2026-1001"],"title":"dependency advisory"},"package":{"name":"alpha","version":"2"}}]}}`))
	if err != nil || len(findings) != 4 {
		t.Fatalf("fixture: count=%d err=%v", len(findings), err)
	}
	seen := map[string]bool{}
	for _, f := range findings {
		if f.PackageName == "" || f.PackageVersion == "" || f.PackageEcosystem == "" || f.AdvisoryID == "" {
			t.Fatalf("dependency identity missing: %+v", f)
		}
		fingerprint := identity.Build(f)
		if fingerprint.Version != 2 || seen[fingerprint.Stable] {
			t.Fatalf("distinct dependency occurrence lost: %+v", f)
		}
		seen[fingerprint.Stable] = true
	}
	if got := runner.Deduplicate(findings); len(got) != len(findings) {
		t.Fatalf("package/advisory/location collapse: %d -> %d", len(findings), len(got))
	}
	if got, err := parseCargoAuditOutput([]byte(`{}`)); err != nil || len(got) != 0 {
		t.Fatalf("empty output invented findings: %v", err)
	}
	if _, err := parseCargoAuditOutput([]byte(`{`)); err == nil {
		t.Fatal("malformed output accepted")
	}
}
