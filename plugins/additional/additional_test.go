package additional

import (
	"os"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestParseCodeQLOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/codeql_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseCodeQLOutput(data)
	if err != nil {
		t.Fatalf("parseCodeQLOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "codeql" {
		t.Errorf("expected tool name codeql, got %s", f.ToolName)
	}
	if f.Category != models.CategorySAST {
		t.Errorf("expected category sast, got %s", f.Category)
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("expected severity high for error, got %s", f.Severity)
	}
	if f.RuleID != "js/sql-injection" {
		t.Errorf("expected rule ID js/sql-injection, got %s", f.RuleID)
	}
	if f.FilePath != "src/db.js" {
		t.Errorf("expected file path src/db.js, got %s", f.FilePath)
	}
	if f.LineStart != 45 {
		t.Errorf("expected line 45, got %d", f.LineStart)
	}
	if f.SARIFData == "" {
		t.Error("expected SARIF data to be populated")
	}
}

func TestParsePMDOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/pmd_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parsePMDOutput(data)
	if err != nil {
		t.Fatalf("parsePMDOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "pmd" {
		t.Errorf("expected tool name pmd, got %s", f.ToolName)
	}
	if f.Category != models.CategoryQuality {
		t.Errorf("expected category quality, got %s", f.Category)
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for priority 3, got %s", f.Severity)
	}
	if f.RuleID != "UnusedLocalVariable" {
		t.Errorf("expected rule ID UnusedLocalVariable, got %s", f.RuleID)
	}
	if f.LineStart != 10 || f.LineEnd != 15 {
		t.Errorf("expected lines 10-15, got %d-%d", f.LineStart, f.LineEnd)
	}
}

func TestParseShellcheckOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/shellcheck_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseShellcheckOutput(data)
	if err != nil {
		t.Fatalf("parseShellcheckOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "shellcheck" {
		t.Errorf("expected tool name shellcheck, got %s", f.ToolName)
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for warning, got %s", f.Severity)
	}
	if f.RuleID != "SC2086" {
		t.Errorf("expected rule ID SC2086, got %s", f.RuleID)
	}
	if f.FilePath != "scripts/deploy.sh" {
		t.Errorf("expected file path scripts/deploy.sh, got %s", f.FilePath)
	}
}

func TestParseDetektOutput(t *testing.T) {
	findings, err := parseDetektOutput([]byte(`<?xml version="1.0"?><checkstyle><file name="src/Main.kt"><error line="7" column="2" severity="error" message="Unsafe call" source="detekt.PotentialBug.UnsafeCall"/></file></checkstyle>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ToolName != "detekt" || findings[0].RuleID != "PotentialBug.UnsafeCall" || findings[0].LineStart != 7 || findings[0].Severity != models.SeverityHigh {
		t.Fatalf("detekt finding = %#v", findings)
	}
}

func TestParseYamllintOutput(t *testing.T) {
	findings := parseYamllintOutput([]byte("deployment.yaml:3:5: [error] trailing spaces (trailing-spaces)\n"))
	if len(findings) != 1 || findings[0].ToolName != "yamllint" || findings[0].RuleID != "trailing-spaces" || findings[0].LineStart != 3 || findings[0].Severity != models.SeverityMedium {
		t.Fatalf("yamllint finding = %#v", findings)
	}
}

func TestAdditionalPluginMetadata(t *testing.T) {
	plugins := []struct {
		plugin   models.Plugin
		name     string
		category models.Category
		hasLangs bool
	}{
		{&CodeQLPlugin{}, "codeql", models.CategorySAST, false},
		{&PMDPlugin{}, "pmd", models.CategoryQuality, false},
		{&ShellcheckPlugin{}, "shellcheck", models.CategoryQuality, true},
	}

	for _, tc := range plugins {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plugin.Name() != tc.name {
				t.Errorf("expected name %s, got %s", tc.name, tc.plugin.Name())
			}
			if tc.plugin.Category() != tc.category {
				t.Errorf("expected category %s, got %s", tc.category, tc.plugin.Category())
			}
			langs := tc.plugin.Languages()
			if tc.hasLangs && len(langs) == 0 {
				t.Error("expected non-empty languages")
			}
			if !tc.hasLangs && len(langs) != 0 {
				t.Errorf("expected empty languages, got %v", langs)
			}
		})
	}
}
