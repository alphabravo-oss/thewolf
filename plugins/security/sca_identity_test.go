package security

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

func TestSCADependencyIdentityOSVScanner(t *testing.T) {
	findings, err := parseOSVScannerOutput([]byte(`{"results":[{"source":{"path":"package-lock.json"},"packages":[{"package":{"name":"alpha","version":"1","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-fixture-CVE-2026-1001","aliases":["CVE-2026-1001"],"summary":"dependency advisory"}]}]},{"source":{"path":"package-lock.json"},"packages":[{"package":{"name":"alpha","version":"1","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-fixture-CVE-2026-1002","aliases":["CVE-2026-1002"],"summary":"dependency advisory"}]}]},{"source":{"path":"package-lock.json"},"packages":[{"package":{"name":"beta","version":"1","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-fixture-CVE-2026-1001","aliases":["CVE-2026-1001"],"summary":"dependency advisory"}]}]},{"source":{"path":"package-lock.json"},"packages":[{"package":{"name":"alpha","version":"2","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-fixture-CVE-2026-1001","aliases":["CVE-2026-1001"],"summary":"dependency advisory"}]}]},{"source":{"path":"nested/package-lock.json"},"packages":[{"package":{"name":"alpha","version":"1","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-fixture-CVE-2026-1001","aliases":["CVE-2026-1001"],"summary":"dependency advisory"}]}]}]}`))
	if err != nil || len(findings) != 5 {
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
	if got, err := parseOSVScannerOutput([]byte(`{}`)); err != nil || len(got) != 0 {
		t.Fatalf("empty output invented findings: %v", err)
	}
	if _, err := parseOSVScannerOutput([]byte(`{`)); err == nil {
		t.Fatal("malformed output accepted")
	}
}
