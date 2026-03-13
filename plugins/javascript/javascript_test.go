package javascript

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "testdata", name)
}

func TestParseESLintOutput(t *testing.T) {
	data, err := os.ReadFile(testdataPath("eslint_output.json"))
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}

	findings, err := parseESLintOutput(data)
	if err != nil {
		t.Fatalf("parseESLintOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	// First finding: no-unused-vars, severity=high
	f := findings[0]
	if f.Severity != models.SeverityHigh {
		t.Errorf("finding[0].Severity = %q, want %q", f.Severity, models.SeverityHigh)
	}
	if f.ToolName != "eslint" {
		t.Errorf("finding[0].ToolName = %q, want %q", f.ToolName, "eslint")
	}
	if f.RuleID != "no-unused-vars" {
		t.Errorf("finding[0].RuleID = %q, want %q", f.RuleID, "no-unused-vars")
	}
	if f.FilePath != "/src/app.js" {
		t.Errorf("finding[0].FilePath = %q, want %q", f.FilePath, "/src/app.js")
	}
	if f.LineStart != 10 {
		t.Errorf("finding[0].LineStart = %d, want %d", f.LineStart, 10)
	}

	// Second finding: semi, severity=medium
	f = findings[1]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[1].Severity = %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.RuleID != "semi" {
		t.Errorf("finding[1].RuleID = %q, want %q", f.RuleID, "semi")
	}
	if f.LineStart != 15 {
		t.Errorf("finding[1].LineStart = %d, want %d", f.LineStart, 15)
	}
}

func TestParseNPMAuditOutput(t *testing.T) {
	data, err := os.ReadFile(testdataPath("npm_audit_output.json"))
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}

	findings, err := parseNPMAuditOutput(data)
	if err != nil {
		t.Fatalf("parseNPMAuditOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.Severity != models.SeverityHigh {
		t.Errorf("finding[0].Severity = %q, want %q", f.Severity, models.SeverityHigh)
	}
	if f.ToolName != "npm-audit" {
		t.Errorf("finding[0].ToolName = %q, want %q", f.ToolName, "npm-audit")
	}
	if f.Title != "Prototype Pollution" {
		t.Errorf("finding[0].Title = %q, want %q", f.Title, "Prototype Pollution")
	}
	if f.CWEID != "CVE-2020-8203" {
		t.Errorf("finding[0].CWEID = %q, want %q", f.CWEID, "CVE-2020-8203")
	}
}
