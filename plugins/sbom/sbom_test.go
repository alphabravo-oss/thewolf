package sbom

import (
	"os"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestParseSyftOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/syft_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseSyftOutput(data)
	if err != nil {
		t.Fatalf("parseSyftOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "syft" {
		t.Errorf("expected tool name syft, got %s", f.ToolName)
	}
	if f.Category != models.CategorySBOM {
		t.Errorf("expected category sbom, got %s", f.Category)
	}
	if f.Severity != models.SeverityInfo {
		t.Errorf("expected severity info, got %s", f.Severity)
	}
	if f.FilePath != "node_modules/express/package.json" {
		t.Errorf("expected file path node_modules/express/package.json, got %s", f.FilePath)
	}
}

func TestSyftPluginMetadata(t *testing.T) {
	p := &SyftPlugin{}
	if p.Name() != "syft" {
		t.Errorf("expected name syft, got %s", p.Name())
	}
	if p.Category() != models.CategorySBOM {
		t.Errorf("expected category sbom, got %s", p.Category())
	}
	if langs := p.Languages(); len(langs) != 0 {
		t.Errorf("expected empty languages, got %v", langs)
	}
}
