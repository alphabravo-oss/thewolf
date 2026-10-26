package identity

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"./app/main.go":               "app/main.go",
		"/scan/app/main.go":           "app/main.go",
		"/workspace/pkg/service.py":   "pkg/service.py",
		"file:///repo/ui/src/App.tsx": "ui/src/App.tsx",
		"internal\\service\\auth.go":  "internal/service/auth.go",
		" /src/terraform/main.tf ":    "terraform/main.tf",
	}

	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Fatalf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildStableFingerprintIgnoresLineShift(t *testing.T) {
	base := models.Finding{
		ToolName:     "gosec",
		Category:     models.CategorySAST,
		RuleID:       "G201",
		FineCategory: "sql-injection",
		FilePath:     "/scan/app/db.go",
		LineStart:    10,
		LineEnd:      10,
		FunctionName: "loadUser",
		CodeSnippet:  "db.Query(\"select * from users where id=\" + id)",
	}
	moved := base
	moved.LineStart = 42
	moved.LineEnd = 42

	baseFP := Build(base)
	movedFP := Build(moved)

	if baseFP.Stable != movedFP.Stable {
		t.Fatalf("stable fingerprint changed after line shift: %s != %s", baseFP.Stable, movedFP.Stable)
	}
	if baseFP.Location == movedFP.Location {
		t.Fatalf("location fingerprint should change when line changes")
	}
}

func TestBuildStableFingerprintCanCorrelateAcrossToolsWithFineCategory(t *testing.T) {
	a := models.Finding{
		ToolName:     "gosec",
		Category:     models.CategorySAST,
		RuleID:       "G201",
		FineCategory: "sql-injection",
		FilePath:     "app/db.go",
		LineStart:    10,
		FunctionName: "loadUser",
		CodeSnippet:  "db.Query(query)",
	}
	b := a
	b.ToolName = "semgrep"
	b.RuleID = "go.lang.security.audit.database.string-sqli"

	if Build(a).Stable != Build(b).Stable {
		t.Fatal("stable fingerprints should match across tools when fine category and evidence match")
	}
	if Build(a).Evidence == Build(b).Evidence {
		t.Fatal("evidence fingerprints should remain tool-specific")
	}
}

func TestBuild_SCAUsesCVENotFineCategory(t *testing.T) {
	a := models.Finding{
		ToolName: "trivy",
		Category: models.CategorySCA, PackageName: "fixture", PackageVersion: "1", PackageEcosystem: "golang",
		RuleID:       "CVE-1",
		FineCategory: "vulnerable-dependency",
		FilePath:     "go.mod",
	}
	b := a
	b.RuleID = "CVE-2"
	if Build(a).Stable == Build(b).Stable {
		t.Fatal("SCA fingerprints should differ when CVE RuleIDs differ")
	}
	c := a
	c.ToolName = "grype"
	if Build(a).Stable != Build(c).Stable {
		t.Fatal("SCA fingerprints should match across tools for the same CVE")
	}
}

func TestApplyBackfillsLegacyFingerprintWhenEmpty(t *testing.T) {
	f := models.Finding{ToolName: "semgrep", RuleID: "no-eval", FilePath: "/scan/main.py"}
	Apply(&f)

	if f.Fingerprint == "" {
		t.Fatal("expected Fingerprint to be populated")
	}
	if f.StableFingerprint == "" || f.LocationFingerprint == "" || f.SemanticFingerprint == "" {
		t.Fatalf("expected durable fingerprints to be populated: %+v", f)
	}
	if f.IdentityVersion != Version {
		t.Fatalf("identity version = %d, want %d", f.IdentityVersion, Version)
	}
	if f.FilePath != "main.py" {
		t.Fatalf("path = %q, want main.py", f.FilePath)
	}
}

func TestSCAV2PreservesPackageLocationCaseAndEvidence(t *testing.T) {
	base := models.Finding{Category: models.CategorySCA, ToolName: "trivy", FilePath: "/scan/Lock.json", PackageName: "alpha", PackageVersion: "1", PackageEcosystem: "gomod", RuleID: "CVE-2026-1001", AdvisoryID: "CVE-2026-1001"}
	other := base
	other.ToolName = "grype"
	other.PackageEcosystem = "go-module"
	other.Title = "different display"
	other.ModuleName = "different enrichment"
	if Build(base).Stable != Build(other).Stable || Build(base).Evidence == Build(other).Evidence {
		t.Fatal("package identity and tool evidence were conflated")
	}
	for _, mutate := range []func(*models.Finding){
		func(f *models.Finding) { f.PackageName = "beta" }, func(f *models.Finding) { f.PackageVersion = "2" }, func(f *models.Finding) { f.FilePath = "lock.json" }, func(f *models.Finding) { f.PackageName = "Alpha" }, func(f *models.Finding) { f.PackageEcosystem = "npm" }, func(f *models.Finding) { f.AdvisoryID = "CVE-2026-1002" }, func(f *models.Finding) { f.PackageURL = "pkg:golang/alpha@1?arch=arm64" },
	} {
		other = base
		mutate(&other)
		if Build(base).Stable == Build(other).Stable {
			t.Fatalf("distinct occurrence collapsed: %+v", other)
		}
	}
	other = base
	other.PackageName = "alpha\x00beta"
	other.PackageVersion = "1"
	third := base
	third.PackageName = "alpha"
	third.PackageVersion = "beta\x001"
	if Build(other).Stable == Build(third).Stable {
		t.Fatal("binary component boundaries are ambiguous")
	}
	legacy := base
	legacy.Fingerprint = "legacy"
	legacy.StableFingerprint = "legacy"
	legacy.IdentityVersion = 1
	Apply(&legacy)
	if legacy.Fingerprint != "legacy" || legacy.StableFingerprint != "legacy" || legacy.IdentityVersion != 1 {
		t.Fatal("legacy identity silently rewritten")
	}
}

func TestNormalizePathDoesNotStripPrefixSiblings(t *testing.T) {
	for input, want := range map[string]string{"/repository/lock": "repository/lock", "/src-other/lock": "src-other/lock", "/workspace-old/lock": "workspace-old/lock", "/scanner/lock": "scanner/lock", "/scan": ""} {
		if got := NormalizePath(input); got != want {
			t.Fatalf("%q -> %q, want %q", input, got, want)
		}
	}
}

func TestCanonicalAdvisoryAliasOrderIsStable(t *testing.T) {
	a := CanonicalAdvisory("GHSA-abcd", "CVE-2026-1002", "CVE-2026-1001")
	b := CanonicalAdvisory("GHSA-abcd", "cve-2026-1001", "CVE-2026-1002")
	if a != "CVE-2026-1001" || a != b {
		t.Fatal("explicit alias order changes identity")
	}
}

func TestClusterKeyPreservesComponentBoundariesAndCategory(t *testing.T) {
	a := models.Finding{Category: models.CategorySAST, FilePath: "a:1", LineStart: 2, RuleID: "rule"}
	b := models.Finding{Category: models.CategorySAST, FilePath: "a", LineStart: 1, RuleID: "2:rule"}
	if ClusterKey(a) == ClusterKey(b) {
		t.Fatal("path/line/rule boundaries collapsed")
	}
	b = a
	b.Category = models.CategorySecrets
	if ClusterKey(a) == ClusterKey(b) {
		t.Fatal("unrelated scanner categories collapsed")
	}
}

func TestCWEAndPathNormalization(t *testing.T) {
	for _, cwe := range []string{"89", "CWE-89", "cwe-089", "CWE-89: SQL Injection"} {
		f := models.Finding{CWEID: cwe, FilePath: `file:///scan/src\main.go`}
		Apply(&f)
		if f.CWEID != "CWE-89" || f.FilePath != "src/main.go" {
			t.Fatalf("%s: %+v", cwe, f)
		}
	}
}
