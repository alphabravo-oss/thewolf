package general

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

func TestSCADependencyIdentityTrivy(t *testing.T) {
	findings, err := parseTrivyOutput([]byte(`{"Results":[{"Type":"npm","Target":"package-lock.json","Vulnerabilities":[{"VulnerabilityID":"CVE-2026-1001","PkgName":"alpha","InstalledVersion":"1","Severity":"HIGH","PkgIdentifier":{"PURL":"pkg:npm/alpha@1"}}]},{"Type":"npm","Target":"package-lock.json","Vulnerabilities":[{"VulnerabilityID":"CVE-2026-1002","PkgName":"alpha","InstalledVersion":"1","Severity":"HIGH","PkgIdentifier":{"PURL":"pkg:npm/alpha@1"}}]},{"Type":"npm","Target":"package-lock.json","Vulnerabilities":[{"VulnerabilityID":"CVE-2026-1001","PkgName":"beta","InstalledVersion":"1","Severity":"HIGH","PkgIdentifier":{"PURL":"pkg:npm/beta@1"}}]},{"Type":"npm","Target":"package-lock.json","Vulnerabilities":[{"VulnerabilityID":"CVE-2026-1001","PkgName":"alpha","InstalledVersion":"2","Severity":"HIGH","PkgIdentifier":{"PURL":"pkg:npm/alpha@2"}}]},{"Type":"npm","Target":"nested/package-lock.json","Vulnerabilities":[{"VulnerabilityID":"CVE-2026-1001","PkgName":"alpha","InstalledVersion":"1","Severity":"HIGH","PkgIdentifier":{"PURL":"pkg:npm/alpha@1"}}]}]}`))
	if err != nil || len(findings) != 5 {
		t.Fatalf("fixture: count=%d err=%v", len(findings), err)
	}
	seen := map[string]bool{}
	for _, f := range findings {
		if f.PackageName == "" || f.PackageVersion == "" || f.PackageEcosystem == "" || f.AdvisoryID == "" || f.PackageURL == "" {
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
	if got, err := parseTrivyOutput([]byte(`{}`)); err != nil || len(got) != 0 {
		t.Fatalf("empty output invented findings: %v", err)
	}
	if _, err := parseTrivyOutput([]byte(`{`)); err == nil {
		t.Fatal("malformed output accepted")
	}
}

func TestSCADependencyIdentityGrype(t *testing.T) {
	findings, err := parseGrypeOutput([]byte(`{"matches":[{"vulnerability":{"id":"CVE-2026-1001","severity":"High"},"artifact":{"name":"alpha","version":"1","type":"npm","purl":"pkg:npm/alpha@1","locations":[{"path":"package-lock.json"},{"path":"other/package-lock.json"}]}},{"vulnerability":{"id":"CVE-2026-1002","severity":"High"},"artifact":{"name":"alpha","version":"1","type":"npm","purl":"pkg:npm/alpha@1","locations":[{"path":"package-lock.json"}]}},{"vulnerability":{"id":"CVE-2026-1001","severity":"High"},"artifact":{"name":"beta","version":"1","type":"npm","purl":"pkg:npm/beta@1","locations":[{"path":"package-lock.json"}]}},{"vulnerability":{"id":"CVE-2026-1001","severity":"High"},"artifact":{"name":"alpha","version":"2","type":"npm","purl":"pkg:npm/alpha@2","locations":[{"path":"package-lock.json"}]}},{"vulnerability":{"id":"CVE-2026-1001","severity":"High"},"artifact":{"name":"alpha","version":"1","type":"npm","purl":"pkg:npm/alpha@1","locations":[{"path":"nested/package-lock.json"}]}}]}`))
	if err != nil || len(findings) != 6 {
		t.Fatalf("fixture: count=%d err=%v", len(findings), err)
	}
	seen := map[string]bool{}
	for _, f := range findings {
		if f.PackageName == "" || f.PackageVersion == "" || f.PackageEcosystem == "" || f.AdvisoryID == "" || f.PackageURL == "" {
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
	if got, err := parseGrypeOutput([]byte(`{}`)); err != nil || len(got) != 0 {
		t.Fatalf("empty output invented findings: %v", err)
	}
	if _, err := parseGrypeOutput([]byte(`{`)); err == nil {
		t.Fatal("malformed output accepted")
	}
}
