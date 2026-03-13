package cpp

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
		{&CppcheckPlugin{}, "cppcheck", models.CategorySAST, []models.Language{models.LangC, models.LangCPP}},
		{&InferPlugin{}, "infer", models.CategorySAST, []models.Language{models.LangC, models.LangCPP, models.LangJava, models.LangObjC}},
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

func TestParseCppcheckOutput(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<results>
  <errors>
    <error id="nullPointer" severity="error" msg="Null pointer dereference" verbose="Possible null pointer dereference" cwe="476">
      <location file="src/main.c" line="25"/>
    </error>
    <error id="unusedVariable" severity="style" msg="Unused variable: x" verbose="Unused variable" cwe="">
      <location file="src/utils.c" line="10"/>
    </error>
  </errors>
</results>`)

	findings, err := parseCppcheckOutput(data)
	if err != nil {
		t.Fatalf("parseCppcheckOutput returned error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "cppcheck" {
		t.Errorf("expected tool name cppcheck, got %s", f.ToolName)
	}
	if f.Category != models.CategorySAST {
		t.Errorf("expected category sast, got %s", f.Category)
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("expected severity high for error, got %s", f.Severity)
	}
	if f.RuleID != "nullPointer" {
		t.Errorf("expected rule ID nullPointer, got %s", f.RuleID)
	}
	if f.CWEID != "476" {
		t.Errorf("expected CWE 476, got %s", f.CWEID)
	}
	if f.FilePath != "src/main.c" {
		t.Errorf("expected file path src/main.c, got %s", f.FilePath)
	}
	if f.LineStart != 25 {
		t.Errorf("expected line 25, got %d", f.LineStart)
	}

	if findings[1].Severity != models.SeverityLow {
		t.Errorf("expected severity low for style, got %s", findings[1].Severity)
	}
}

func TestMapCppcheckSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected models.Severity
	}{
		{"error", models.SeverityHigh},
		{"warning", models.SeverityMedium},
		{"style", models.SeverityLow},
		{"performance", models.SeverityLow},
		{"portability", models.SeverityLow},
		{"information", models.SeverityInfo},
		{"unknown", models.SeverityInfo},
	}

	for _, tc := range tests {
		got := mapCppcheckSeverity(tc.input)
		if got != tc.expected {
			t.Errorf("mapCppcheckSeverity(%q) = %s, want %s", tc.input, got, tc.expected)
		}
	}
}

func TestParseInferOutput(t *testing.T) {
	data := []byte(`[
		{
			"bug_type": "NULL_DEREFERENCE",
			"severity": "ERROR",
			"file": "src/main.c",
			"line": 42,
			"procedure": "process_data",
			"qualifier": "null dereference of pointer p",
			"bug_type_hum": "Null Dereference"
		},
		{
			"bug_type": "DEAD_STORE",
			"severity": "WARNING",
			"file": "src/utils.c",
			"line": 15,
			"procedure": "calc",
			"qualifier": "value written to x is never read",
			"bug_type_hum": "Dead Store"
		}
	]`)

	findings, err := parseInferOutput(data)
	if err != nil {
		t.Fatalf("parseInferOutput returned error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "infer" {
		t.Errorf("expected tool name infer, got %s", f.ToolName)
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("expected severity high for ERROR, got %s", f.Severity)
	}
	if f.RuleID != "NULL_DEREFERENCE" {
		t.Errorf("expected rule ID NULL_DEREFERENCE, got %s", f.RuleID)
	}
	if f.Title != "Null Dereference" {
		t.Errorf("expected title Null Dereference, got %s", f.Title)
	}
	if f.LineStart != 42 {
		t.Errorf("expected line 42, got %d", f.LineStart)
	}

	if findings[1].Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for WARNING, got %s", findings[1].Severity)
	}
}

func TestMapInferSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected models.Severity
	}{
		{"ERROR", models.SeverityHigh},
		{"WARNING", models.SeverityMedium},
		{"INFO", models.SeverityLow},
		{"ADVICE", models.SeverityLow},
		{"OTHER", models.SeverityInfo},
	}

	for _, tc := range tests {
		got := mapInferSeverity(tc.input)
		if got != tc.expected {
			t.Errorf("mapInferSeverity(%q) = %s, want %s", tc.input, got, tc.expected)
		}
	}
}
