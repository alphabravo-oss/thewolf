package sbom

import (
	"os"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// TestParseSyftOutput verifies that parseSyftOutput validates a real
// syft JSON payload and intentionally returns NO findings. Syft
// produces an SBOM (inventory of packages), not vulnerabilities — the
// SBOM is persisted as the syft.json scan artifact, not as Finding
// rows. See parseSyftOutput's comment for the rationale.
func TestParseSyftOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/syft_output.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	findings, err := parseSyftOutput(data)
	if err != nil {
		t.Fatalf("parseSyftOutput returned error: %v", err)
	}

	if len(findings) != 0 {
		t.Fatalf("expected 0 findings (syft is inventory, not vulns); got %d", len(findings))
	}
}

// Keep an unused-import guard so the models package stays imported as
// other tests in this file reference it.
var _ = models.CategorySBOM

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
