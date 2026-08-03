package docs

import (
	"os"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestParseSpectralOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/spectral_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseSpectralOutput(data)
	if err != nil {
		t.Fatalf("parseSpectralOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "spectral" {
		t.Errorf("expected tool name spectral, got %s", f.ToolName)
	}
	if f.Category != models.CategoryDocs {
		t.Errorf("expected category docs, got %s", f.Category)
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for severity 1, got %s", f.Severity)
	}
	if f.LineStart != 21 {
		t.Errorf("expected line start 21 (0-based + 1), got %d", f.LineStart)
	}
	if f.RuleID != "operation-description" {
		t.Errorf("expected rule ID operation-description, got %s", f.RuleID)
	}
}

func TestParseValeOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/vale_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseValeOutput(data)
	if err != nil {
		t.Fatalf("parseValeOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "vale" {
		t.Errorf("expected tool name vale, got %s", f.ToolName)
	}
	if f.Category != models.CategoryDocs {
		t.Errorf("expected category docs, got %s", f.Category)
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for warning, got %s", f.Severity)
	}
	if f.FilePath != "README.md" {
		t.Errorf("expected file path README.md, got %s", f.FilePath)
	}
	if f.LineStart != 5 {
		t.Errorf("expected line 5, got %d", f.LineStart)
	}
	if f.RuleID != "Microsoft.We" {
		t.Errorf("expected rule ID Microsoft.We, got %s", f.RuleID)
	}
}

func TestParseMarkdownlintOutput(t *testing.T) {
	findings := parseMarkdownlintOutput([]byte("README.md:5:1 MD013/line-length Line length [Expected: 80; Actual: 120]\n"))
	if len(findings) != 1 || findings[0].ToolName != "markdownlint" || findings[0].RuleID != "MD013" || findings[0].FilePath != "README.md" || findings[0].LineStart != 5 {
		t.Fatalf("markdownlint finding = %#v", findings)
	}
}

func TestDocsPluginMetadata(t *testing.T) {
	plugins := []struct {
		plugin models.Plugin
		name   string
	}{
		{&SpectralPlugin{}, "spectral"},
		{&ValePlugin{}, "vale"},
	}

	for _, tc := range plugins {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plugin.Name() != tc.name {
				t.Errorf("expected name %s, got %s", tc.name, tc.plugin.Name())
			}
			if tc.plugin.Category() != models.CategoryDocs {
				t.Errorf("expected category docs, got %s", tc.plugin.Category())
			}
			if langs := tc.plugin.Languages(); len(langs) != 0 {
				t.Errorf("expected empty languages, got %v", langs)
			}
		})
	}
}
