package python

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "testdata", name)
}

func loadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(testdataPath(name))
	if err != nil {
		t.Fatalf("failed to read testdata %s: %v", name, err)
	}
	return data
}

func TestParseBanditOutput(t *testing.T) {
	data := loadTestdata(t, "bandit_output.json")
	findings, err := parseBanditOutput(data)
	if err != nil {
		t.Fatalf("parseBanditOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.Severity != models.SeverityCritical {
		t.Errorf("finding[0] severity: got %q, want %q", f.Severity, models.SeverityCritical)
	}
	if f.ToolName != "bandit" {
		t.Errorf("finding[0] tool: got %q, want %q", f.ToolName, "bandit")
	}
	if f.RuleID != "B301" {
		t.Errorf("finding[0] rule: got %q, want %q", f.RuleID, "B301")
	}
	if f.CWEID != "CWE-502" {
		t.Errorf("finding[0] CWE: got %q, want %q", f.CWEID, "CWE-502")
	}
	if f.FilePath != "app/config.py" {
		t.Errorf("finding[0] file: got %q, want %q", f.FilePath, "app/config.py")
	}
	if f.LineStart != 42 {
		t.Errorf("finding[0] line: got %d, want %d", f.LineStart, 42)
	}

	f = findings[1]
	if f.Severity != models.SeverityLow {
		t.Errorf("finding[1] severity: got %q, want %q", f.Severity, models.SeverityLow)
	}
	if f.RuleID != "B105" {
		t.Errorf("finding[1] rule: got %q, want %q", f.RuleID, "B105")
	}
	if f.CWEID != "CWE-259" {
		t.Errorf("finding[1] CWE: got %q, want %q", f.CWEID, "CWE-259")
	}
	if f.FilePath != "app/auth.py" {
		t.Errorf("finding[1] file: got %q, want %q", f.FilePath, "app/auth.py")
	}
	if f.LineStart != 15 {
		t.Errorf("finding[1] line: got %d, want %d", f.LineStart, 15)
	}
}

func TestParseBanditRealE2EOutput(t *testing.T) {
	path := os.Getenv("WOLF_BANDIT_E2E_OUTPUT")
	if path == "" {
		t.Skip("WOLF_BANDIT_E2E_OUTPUT is set only by gated Compose/Kind scanner execution")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := parseBanditOutput(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("real Bandit scanner output produced no normalized findings")
	}
	for _, finding := range findings {
		if finding.ToolName != "bandit" || finding.RuleID == "" ||
			finding.FilePath == "" || finding.LineStart < 1 {
			t.Fatalf("malformed normalized Bandit finding: %#v", finding)
		}
	}
}

func TestParseRuffOutput(t *testing.T) {
	data := loadTestdata(t, "ruff_output.json")
	findings, err := parseRuffOutput(data)
	if err != nil {
		t.Fatalf("parseRuffOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.Severity != models.SeverityHigh {
		t.Errorf("finding[0] severity: got %q, want %q", f.Severity, models.SeverityHigh)
	}
	if f.ToolName != "ruff" {
		t.Errorf("finding[0] tool: got %q, want %q", f.ToolName, "ruff")
	}
	if f.RuleID != "F401" {
		t.Errorf("finding[0] rule: got %q, want %q", f.RuleID, "F401")
	}
	if f.FilePath != "app/main.py" {
		t.Errorf("finding[0] file: got %q, want %q", f.FilePath, "app/main.py")
	}
	if f.LineStart != 1 {
		t.Errorf("finding[0] line: got %d, want %d", f.LineStart, 1)
	}

	f = findings[1]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[1] severity: got %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.RuleID != "E501" {
		t.Errorf("finding[1] rule: got %q, want %q", f.RuleID, "E501")
	}
	if f.FilePath != "app/utils.py" {
		t.Errorf("finding[1] file: got %q, want %q", f.FilePath, "app/utils.py")
	}
	if f.LineStart != 45 {
		t.Errorf("finding[1] line: got %d, want %d", f.LineStart, 45)
	}
}

func TestParseMypyOutput(t *testing.T) {
	data := loadTestdata(t, "mypy_output.txt")
	findings, err := parseMypyOutput(data)
	if err != nil {
		t.Fatalf("parseMypyOutput returned error: %v", err)
	}

	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.Severity != models.SeverityHigh {
		t.Errorf("finding[0] severity: got %q, want %q", f.Severity, models.SeverityHigh)
	}
	if f.ToolName != "mypy" {
		t.Errorf("finding[0] tool: got %q, want %q", f.ToolName, "mypy")
	}
	if f.FilePath != "app/main.py" {
		t.Errorf("finding[0] file: got %q, want %q", f.FilePath, "app/main.py")
	}
	if f.LineStart != 10 {
		t.Errorf("finding[0] line: got %d, want %d", f.LineStart, 10)
	}
	if f.RuleID != "return-value" {
		t.Errorf("finding[0] rule: got %q, want %q", f.RuleID, "return-value")
	}

	f = findings[1]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[1] severity: got %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.FilePath != "app/utils.py" {
		t.Errorf("finding[1] file: got %q, want %q", f.FilePath, "app/utils.py")
	}
	if f.LineStart != 25 {
		t.Errorf("finding[1] line: got %d, want %d", f.LineStart, 25)
	}

	f = findings[2]
	if f.Severity != models.SeverityLow {
		t.Errorf("finding[2] severity: got %q, want %q", f.Severity, models.SeverityLow)
	}
	if f.FilePath != "app/models.py" {
		t.Errorf("finding[2] file: got %q, want %q", f.FilePath, "app/models.py")
	}
	if f.LineStart != 5 {
		t.Errorf("finding[2] line: got %d, want %d", f.LineStart, 5)
	}
}

func TestParseRadonOutput(t *testing.T) {
	data := loadTestdata(t, "radon_output.json")
	findings, err := parseRadonOutput(data)
	if err != nil {
		t.Fatalf("parseRadonOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (complexity <= 10 excluded), got %d", len(findings))
	}

	// Radon iterates a map, so order is not guaranteed. Index by file path.
	byFile := make(map[string]models.Finding, len(findings))
	for _, f := range findings {
		byFile[f.FilePath] = f
	}

	f, ok := byFile["app/complex.py"]
	if !ok {
		t.Fatalf("expected finding for app/complex.py")
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("app/complex.py severity: got %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.LineStart != 10 {
		t.Errorf("app/complex.py line: got %d, want %d", f.LineStart, 10)
	}

	f, ok = byFile["app/handler.py"]
	if !ok {
		t.Fatalf("expected finding for app/handler.py")
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("app/handler.py severity: got %q, want %q", f.Severity, models.SeverityHigh)
	}
	if f.LineStart != 20 {
		t.Errorf("app/handler.py line: got %d, want %d", f.LineStart, 20)
	}
	if !strings.Contains(f.Title, "RequestHandler.handle_request") {
		t.Errorf("app/handler.py title should contain %q, got %q", "RequestHandler.handle_request", f.Title)
	}
}

func TestParseVultureOutput(t *testing.T) {
	data := loadTestdata(t, "vulture_output.txt")
	findings, err := parseVultureOutput(data)
	if err != nil {
		t.Fatalf("parseVultureOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[0] severity: got %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.FilePath != "app/models.py" {
		t.Errorf("finding[0] file: got %q, want %q", f.FilePath, "app/models.py")
	}
	if f.LineStart != 15 {
		t.Errorf("finding[0] line: got %d, want %d", f.LineStart, 15)
	}

	f = findings[1]
	if f.Severity != models.SeverityLow {
		t.Errorf("finding[1] severity: got %q, want %q", f.Severity, models.SeverityLow)
	}
	if f.FilePath != "app/utils.py" {
		t.Errorf("finding[1] file: got %q, want %q", f.FilePath, "app/utils.py")
	}
	if f.LineStart != 42 {
		t.Errorf("finding[1] line: got %d, want %d", f.LineStart, 42)
	}
}

func TestParsePipAuditOutput(t *testing.T) {
	data := loadTestdata(t, "pip_audit_output.json")
	findings, err := parsePipAuditOutput(data)
	if err != nil {
		t.Fatalf("parsePipAuditOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (flask has no vulns), got %d", len(findings))
	}

	f := findings[0]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[0] severity: got %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.ToolName != "pip-audit" {
		t.Errorf("finding[0] tool: got %q, want %q", f.ToolName, "pip-audit")
	}
	if f.RuleID != "PYSEC-2023-1234" {
		t.Errorf("finding[0] rule: got %q, want %q", f.RuleID, "PYSEC-2023-1234")
	}
	if f.CWEID != "CVE-2023-32681" {
		t.Errorf("finding[0] CWE: got %q, want %q", f.CWEID, "CVE-2023-32681")
	}
}
