package goplug

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

func TestParseGosecOutput(t *testing.T) {
	data, err := os.ReadFile(testdataPath("gosec_output.json"))
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}

	findings, err := parseGosecOutput(data)
	if err != nil {
		t.Fatalf("parseGosecOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	// First finding: G201, SQL injection
	f := findings[0]
	if f.Severity != models.SeverityHigh {
		t.Errorf("finding[0].Severity = %q, want %q", f.Severity, models.SeverityHigh)
	}
	if f.ToolName != "gosec" {
		t.Errorf("finding[0].ToolName = %q, want %q", f.ToolName, "gosec")
	}
	if f.RuleID != "G201" {
		t.Errorf("finding[0].RuleID = %q, want %q", f.RuleID, "G201")
	}
	if f.CWEID != "89" {
		t.Errorf("finding[0].CWEID = %q, want %q", f.CWEID, "89")
	}
	if f.FilePath != "db/queries.go" {
		t.Errorf("finding[0].FilePath = %q, want %q", f.FilePath, "db/queries.go")
	}
	if f.LineStart != 25 {
		t.Errorf("finding[0].LineStart = %d, want %d", f.LineStart, 25)
	}

	// Second finding: G401, weak crypto
	f = findings[1]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[1].Severity = %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.RuleID != "G401" {
		t.Errorf("finding[1].RuleID = %q, want %q", f.RuleID, "G401")
	}
	if f.CWEID != "326" {
		t.Errorf("finding[1].CWEID = %q, want %q", f.CWEID, "326")
	}
	if f.FilePath != "crypto/hash.go" {
		t.Errorf("finding[1].FilePath = %q, want %q", f.FilePath, "crypto/hash.go")
	}
	if f.LineStart != 15 {
		t.Errorf("finding[1].LineStart = %d, want %d", f.LineStart, 15)
	}
}

func TestParseStaticcheckOutput(t *testing.T) {
	data, err := os.ReadFile(testdataPath("staticcheck_output.jsonl"))
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}

	findings, err := parseStaticcheckOutput(data)
	if err != nil {
		t.Fatalf("parseStaticcheckOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	// First finding: SA1000, error severity
	f := findings[0]
	if f.Severity != models.SeverityHigh {
		t.Errorf("finding[0].Severity = %q, want %q", f.Severity, models.SeverityHigh)
	}
	if f.ToolName != "staticcheck" {
		t.Errorf("finding[0].ToolName = %q, want %q", f.ToolName, "staticcheck")
	}
	if f.RuleID != "SA1000" {
		t.Errorf("finding[0].RuleID = %q, want %q", f.RuleID, "SA1000")
	}
	if f.FilePath != "main.go" {
		t.Errorf("finding[0].FilePath = %q, want %q", f.FilePath, "main.go")
	}
	if f.LineStart != 15 {
		t.Errorf("finding[0].LineStart = %d, want %d", f.LineStart, 15)
	}

	// Second finding: SA4006, warning severity
	f = findings[1]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[1].Severity = %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.RuleID != "SA4006" {
		t.Errorf("finding[1].RuleID = %q, want %q", f.RuleID, "SA4006")
	}
	if f.FilePath != "utils.go" {
		t.Errorf("finding[1].FilePath = %q, want %q", f.FilePath, "utils.go")
	}
	if f.LineStart != 30 {
		t.Errorf("finding[1].LineStart = %d, want %d", f.LineStart, 30)
	}
}
