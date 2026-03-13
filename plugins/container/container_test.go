package container

import (
	"os"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestParseDockleOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/dockle_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseDockleOutput(data)
	if err != nil {
		t.Fatalf("parseDockleOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "dockle" {
		t.Errorf("expected tool name dockle, got %s", f.ToolName)
	}
	if f.Category != models.CategoryContainer {
		t.Errorf("expected category container, got %s", f.Category)
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for WARN, got %s", f.Severity)
	}
	if f.RuleID != "CIS-DI-0001" {
		t.Errorf("expected rule ID CIS-DI-0001, got %s", f.RuleID)
	}

	if findings[1].Severity != models.SeverityLow {
		t.Errorf("expected severity low for INFO, got %s", findings[1].Severity)
	}
}

func TestParseCheckovOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/checkov_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseCheckovOutput(data)
	if err != nil {
		t.Fatalf("parseCheckovOutput returned error: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "checkov" {
		t.Errorf("expected tool name checkov, got %s", f.ToolName)
	}
	if f.RuleID != "CKV_DOCKER_2" {
		t.Errorf("expected rule ID CKV_DOCKER_2, got %s", f.RuleID)
	}
	if f.LineStart != 1 || f.LineEnd != 15 {
		t.Errorf("expected line range 1-15, got %d-%d", f.LineStart, f.LineEnd)
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("expected severity medium, got %s", f.Severity)
	}
}

func TestParseHadolintOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/hadolint_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseHadolintOutput(data)
	if err != nil {
		t.Fatalf("parseHadolintOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "hadolint" {
		t.Errorf("expected tool name hadolint, got %s", f.ToolName)
	}
	if f.Category != models.CategoryContainer {
		t.Errorf("expected category container, got %s", f.Category)
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for warning, got %s", f.Severity)
	}
	if f.RuleID != "DL3008" {
		t.Errorf("expected rule ID DL3008, got %s", f.RuleID)
	}
	if f.LineStart != 3 {
		t.Errorf("expected line 3, got %d", f.LineStart)
	}

	if findings[1].Severity != models.SeverityLow {
		t.Errorf("expected severity low for info, got %s", findings[1].Severity)
	}
}

func TestContainerPluginMetadata(t *testing.T) {
	plugins := []struct {
		plugin   models.Plugin
		name     string
	}{
		{&DocklePlugin{}, "dockle"},
		{&CheckovPlugin{}, "checkov"},
		{&HadolintPlugin{}, "hadolint"},
	}

	for _, tc := range plugins {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plugin.Name() != tc.name {
				t.Errorf("expected name %s, got %s", tc.name, tc.plugin.Name())
			}
			if tc.plugin.Category() != models.CategoryContainer {
				t.Errorf("expected category container, got %s", tc.plugin.Category())
			}
			if langs := tc.plugin.Languages(); len(langs) != 0 {
				t.Errorf("expected empty languages, got %v", langs)
			}
		})
	}
}
