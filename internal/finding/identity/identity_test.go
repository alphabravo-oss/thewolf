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
		ToolName:     "trivy",
		Category:     models.CategorySCA,
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
