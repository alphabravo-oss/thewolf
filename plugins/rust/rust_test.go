package rust

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

func TestParseCargoAuditOutput(t *testing.T) {
	data, err := os.ReadFile(testdataPath("cargo_audit_output.json"))
	if err != nil {
		t.Fatal(err)
	}
	findings, err := parseCargoAuditOutput(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ToolName != "cargo-audit" || findings[0].RuleID != "RUSTSEC-2020-0071" || findings[0].Severity != models.SeverityHigh {
		t.Fatalf("cargo-audit finding = %#v", findings)
	}
}

func TestParseCargoDenyOutput(t *testing.T) {
	data, err := os.ReadFile(testdataPath("cargo_deny_output.json"))
	if err != nil {
		t.Fatal(err)
	}
	findings, err := parseCargoDenyOutput(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ToolName != "cargo-deny" || findings[0].RuleID != "vulnerability" || findings[0].Severity != models.SeverityHigh {
		t.Fatalf("cargo-deny finding = %#v", findings)
	}
}

func TestParseClippyOutput(t *testing.T) {
	data, err := os.ReadFile(testdataPath("clippy_output.jsonl"))
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}

	findings, err := parseClippyOutput(data)
	if err != nil {
		t.Fatalf("parseClippyOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	// First finding: unused_variables
	f := findings[0]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[0].Severity = %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.ToolName != "clippy" {
		t.Errorf("finding[0].ToolName = %q, want %q", f.ToolName, "clippy")
	}
	if f.RuleID != "unused_variables" {
		t.Errorf("finding[0].RuleID = %q, want %q", f.RuleID, "unused_variables")
	}
	if f.FilePath != "src/main.rs" {
		t.Errorf("finding[0].FilePath = %q, want %q", f.FilePath, "src/main.rs")
	}
	if f.LineStart != 10 {
		t.Errorf("finding[0].LineStart = %d, want %d", f.LineStart, 10)
	}

	// Second finding: clippy::needless_return
	f = findings[1]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[1].Severity = %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.RuleID != "clippy::needless_return" {
		t.Errorf("finding[1].RuleID = %q, want %q", f.RuleID, "clippy::needless_return")
	}
	if f.FilePath != "src/lib.rs" {
		t.Errorf("finding[1].FilePath = %q, want %q", f.FilePath, "src/lib.rs")
	}
	if f.LineStart != 20 {
		t.Errorf("finding[1].LineStart = %d, want %d", f.LineStart, 20)
	}
}
