package swift

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPluginMetadata(t *testing.T) {
	p := &SwiftLintPlugin{}

	if p.Name() != "swiftlint" {
		t.Errorf("expected name swiftlint, got %s", p.Name())
	}
	if p.Category() != models.CategoryQuality {
		t.Errorf("expected category quality, got %s", p.Category())
	}
	langs := p.Languages()
	if len(langs) != 1 || langs[0] != models.LangSwift {
		t.Errorf("expected [swift], got %v", langs)
	}
}

func TestParseSwiftLintOutput(t *testing.T) {
	data := []byte(`[
		{
			"rule_id": "force_cast",
			"reason": "Force casts should be avoided",
			"file": "Sources/App/ViewController.swift",
			"line": 42,
			"severity": "Error",
			"type": "Lint"
		},
		{
			"rule_id": "line_length",
			"reason": "Line should be 120 characters or less",
			"file": "Sources/App/Model.swift",
			"line": 15,
			"severity": "Warning",
			"type": "Style"
		}
	]`)

	findings, err := parseSwiftLintOutput(data)
	if err != nil {
		t.Fatalf("parseSwiftLintOutput returned error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "swiftlint" {
		t.Errorf("expected tool name swiftlint, got %s", f.ToolName)
	}
	if f.Category != models.CategoryQuality {
		t.Errorf("expected category quality, got %s", f.Category)
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("expected severity high for Error, got %s", f.Severity)
	}
	if f.RuleID != "force_cast" {
		t.Errorf("expected rule ID force_cast, got %s", f.RuleID)
	}
	if f.FilePath != "Sources/App/ViewController.swift" {
		t.Errorf("expected file path Sources/App/ViewController.swift, got %s", f.FilePath)
	}
	if f.LineStart != 42 {
		t.Errorf("expected line 42, got %d", f.LineStart)
	}

	if findings[1].Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for Warning, got %s", findings[1].Severity)
	}
}

func TestMapSwiftLintSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected models.Severity
	}{
		{"Error", models.SeverityHigh},
		{"Warning", models.SeverityMedium},
		{"other", models.SeverityLow},
	}

	for _, tc := range tests {
		got := mapSwiftLintSeverity(tc.input)
		if got != tc.expected {
			t.Errorf("mapSwiftLintSeverity(%q) = %s, want %s", tc.input, got, tc.expected)
		}
	}
}

func TestParseSwiftLintOutput_Empty(t *testing.T) {
	data := []byte(`[]`)
	findings, err := parseSwiftLintOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(findings))
	}
}
