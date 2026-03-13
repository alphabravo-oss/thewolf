package ruby

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPluginMetadata(t *testing.T) {
	plugins := []struct {
		plugin   models.Plugin
		name     string
		category models.Category
		langs    []models.Language
	}{
		{&BrakemanPlugin{}, "brakeman", models.CategorySAST, []models.Language{models.LangRuby}},
		{&RubocopPlugin{}, "rubocop", models.CategoryQuality, []models.Language{models.LangRuby}},
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
			if len(langs) != len(tc.langs) {
				t.Fatalf("expected %d languages, got %d", len(tc.langs), len(langs))
			}
			for i, l := range tc.langs {
				if langs[i] != l {
					t.Errorf("language[%d] = %s, want %s", i, langs[i], l)
				}
			}
		})
	}
}

func TestParseBrakemanOutput(t *testing.T) {
	data := []byte(`{
		"warnings": [
			{
				"warning_type": "SQL Injection",
				"warning_code": 0,
				"message": "Possible SQL injection",
				"file": "app/models/user.rb",
				"line": 25,
				"link": "https://brakemanscanner.org/docs/warning_types/sql_injection/",
				"code": "User.where(params[:query])",
				"confidence": "High",
				"cwe_id": [89]
			},
			{
				"warning_type": "Cross-Site Scripting",
				"warning_code": 2,
				"message": "Unescaped output",
				"file": "app/views/index.erb",
				"line": 10,
				"link": "",
				"code": "",
				"confidence": "Weak",
				"cwe_id": [79]
			}
		]
	}`)

	findings, err := parseBrakemanOutput(data)
	if err != nil {
		t.Fatalf("parseBrakemanOutput returned error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "brakeman" {
		t.Errorf("expected tool name brakeman, got %s", f.ToolName)
	}
	if f.Category != models.CategorySAST {
		t.Errorf("expected category sast, got %s", f.Category)
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("expected severity high for High confidence, got %s", f.Severity)
	}
	if f.CWEID != "CWE-89" {
		t.Errorf("expected CWE-89, got %s", f.CWEID)
	}
	if f.FilePath != "app/models/user.rb" {
		t.Errorf("expected file path app/models/user.rb, got %s", f.FilePath)
	}
	if f.LineStart != 25 {
		t.Errorf("expected line 25, got %d", f.LineStart)
	}

	if findings[1].Severity != models.SeverityLow {
		t.Errorf("expected severity low for Weak confidence, got %s", findings[1].Severity)
	}
}

func TestMapBrakemanConfidence(t *testing.T) {
	tests := []struct {
		input    string
		expected models.Severity
	}{
		{"High", models.SeverityHigh},
		{"Medium", models.SeverityMedium},
		{"Weak", models.SeverityLow},
		{"Unknown", models.SeverityInfo},
	}

	for _, tc := range tests {
		got := mapBrakemanConfidence(tc.input)
		if got != tc.expected {
			t.Errorf("mapBrakemanConfidence(%q) = %s, want %s", tc.input, got, tc.expected)
		}
	}
}

func TestParseRubocopOutput(t *testing.T) {
	data := []byte(`{
		"files": [
			{
				"path": "app/models/user.rb",
				"offenses": [
					{
						"severity": "warning",
						"message": "Avoid using sleep",
						"cop_name": "Lint/Sleep",
						"location": {"start_line": 10, "start_column": 5, "last_line": 10}
					},
					{
						"severity": "convention",
						"message": "Use 2 spaces for indentation",
						"cop_name": "Layout/IndentationWidth",
						"location": {"start_line": 15, "start_column": 1, "last_line": 15}
					}
				]
			}
		]
	}`)

	findings, err := parseRubocopOutput(data)
	if err != nil {
		t.Fatalf("parseRubocopOutput returned error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "rubocop" {
		t.Errorf("expected tool name rubocop, got %s", f.ToolName)
	}
	if f.Category != models.CategoryQuality {
		t.Errorf("expected category quality, got %s", f.Category)
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for warning, got %s", f.Severity)
	}
	if f.RuleID != "Lint/Sleep" {
		t.Errorf("expected rule ID Lint/Sleep, got %s", f.RuleID)
	}

	if findings[1].Severity != models.SeverityLow {
		t.Errorf("expected severity low for convention, got %s", findings[1].Severity)
	}
}

func TestMapRubocopSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected models.Severity
	}{
		{"fatal", models.SeverityHigh},
		{"error", models.SeverityHigh},
		{"warning", models.SeverityMedium},
		{"convention", models.SeverityLow},
		{"refactor", models.SeverityLow},
		{"other", models.SeverityInfo},
	}

	for _, tc := range tests {
		got := mapRubocopSeverity(tc.input)
		if got != tc.expected {
			t.Errorf("mapRubocopSeverity(%q) = %s, want %s", tc.input, got, tc.expected)
		}
	}
}
