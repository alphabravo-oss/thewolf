package general

import (
	"os"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestParseSemgrepOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/semgrep_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseSemgrepOutput(data)
	if err != nil {
		t.Fatalf("parseSemgrepOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "semgrep" {
		t.Errorf("expected tool name semgrep, got %s", f.ToolName)
	}
	if f.Category != models.CategorySAST {
		t.Errorf("expected category sast, got %s", f.Category)
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("expected severity high, got %s", f.Severity)
	}
	if f.FilePath != "app/server.py" {
		t.Errorf("expected file path app/server.py, got %s", f.FilePath)
	}
	if f.LineStart != 42 {
		t.Errorf("expected line start 42, got %d", f.LineStart)
	}
	if f.CWEID != "CWE-78" {
		t.Errorf("expected CWE-78, got %s", f.CWEID)
	}
	if f.RuleID != "python.lang.security.audit.dangerous-system-call" {
		t.Errorf("expected rule ID python.lang.security.audit.dangerous-system-call, got %s", f.RuleID)
	}

	// Second finding should be WARNING -> medium
	if findings[1].Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for second finding, got %s", findings[1].Severity)
	}
}

func TestParseTrivyOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/trivy_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseTrivyOutput(data)
	if err != nil {
		t.Fatalf("parseTrivyOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "trivy" {
		t.Errorf("expected tool name trivy, got %s", f.ToolName)
	}
	if f.Category != models.CategorySCA {
		t.Errorf("expected category sca, got %s", f.Category)
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("expected severity high, got %s", f.Severity)
	}
	if f.RuleID != "CVE-2023-1234" {
		t.Errorf("expected rule ID CVE-2023-1234, got %s", f.RuleID)
	}
	if f.FilePath != "requirements.txt" {
		t.Errorf("expected file path requirements.txt, got %s", f.FilePath)
	}
}

func TestParseGitleaksOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/gitleaks_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseGitleaksOutput(data)
	if err != nil {
		t.Fatalf("parseGitleaksOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "gitleaks" {
		t.Errorf("expected tool name gitleaks, got %s", f.ToolName)
	}
	if f.Category != models.CategorySecrets {
		t.Errorf("expected category secrets, got %s", f.Category)
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("expected severity high, got %s", f.Severity)
	}
	if f.RuleID != "aws-access-key-id" {
		t.Errorf("expected rule ID aws-access-key-id, got %s", f.RuleID)
	}
}

func TestParseGrypeOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/grype_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseGrypeOutput(data)
	if err != nil {
		t.Fatalf("parseGrypeOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "grype" {
		t.Errorf("expected tool name grype, got %s", f.ToolName)
	}
	if f.Severity != models.SeverityCritical {
		t.Errorf("expected severity critical, got %s", f.Severity)
	}
	if f.RuleID != "CVE-2022-9999" {
		t.Errorf("expected rule ID CVE-2022-9999, got %s", f.RuleID)
	}
	if f.FilePath != "package-lock.json" {
		t.Errorf("expected file path package-lock.json, got %s", f.FilePath)
	}
}

func TestParseTrufflehogOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/trufflehog_output.jsonl")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseTrufflehogOutput(data)
	if err != nil {
		t.Fatalf("parseTrufflehogOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	// First finding is verified -> critical
	if findings[0].Severity != models.SeverityCritical {
		t.Errorf("expected severity critical for verified secret, got %s", findings[0].Severity)
	}
	if findings[0].FilePath != "env/.env" {
		t.Errorf("expected file path env/.env, got %s", findings[0].FilePath)
	}

	// Second finding is unverified -> medium
	if findings[1].Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for unverified secret, got %s", findings[1].Severity)
	}
}

func TestPluginMetadata(t *testing.T) {
	plugins := []struct {
		plugin   models.Plugin
		name     string
		category models.Category
	}{
		{&SemgrepPlugin{}, "semgrep", models.CategorySAST},
		{&TrivyPlugin{}, "trivy", models.CategorySCA},
		{&GitleaksPlugin{}, "gitleaks", models.CategorySecrets},
		{&GrypePlugin{}, "grype", models.CategorySCA},
		{&TrufflehogPlugin{}, "trufflehog", models.CategorySecrets},
	}

	for _, tc := range plugins {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plugin.Name() != tc.name {
				t.Errorf("expected name %s, got %s", tc.name, tc.plugin.Name())
			}
			if tc.plugin.Category() != tc.category {
				t.Errorf("expected category %s, got %s", tc.category, tc.plugin.Category())
			}
			if langs := tc.plugin.Languages(); len(langs) != 0 {
				t.Errorf("expected empty languages, got %v", langs)
			}
		})
	}
}
