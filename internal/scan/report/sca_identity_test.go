package report

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/finding/sarifio"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestDependencyIdentitySurvivesJSONAndSARIF(t *testing.T) {
	var findings []models.Finding
	for _, pkg := range []string{"alpha", "beta"} {
		f := models.Finding{ID: pkg, ClusterID: pkg, Category: models.CategorySCA, ToolName: "trivy", PackageName: pkg, PackageVersion: "1", PackageEcosystem: "npm", PackageURL: "pkg:npm/" + pkg + "@1", AdvisoryID: "CVE-2026-1001", RuleID: "CVE-2026-1001", FilePath: "package-lock.json", Severity: models.SeverityHigh}
		identity.Apply(&f)
		findings = append(findings, f)
	}
	cfg := ReportConfig{Findings: findings}
	data, err := GenerateJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Findings []models.Finding `json:"findings"`
	}
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if len(exported.Findings) != 2 {
		t.Fatalf("JSON findings missing: %s", data)
	}
	sarif, err := renderSARIF(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(sarif, []byte(`"clusterId": "alpha"`)) {
		t.Fatal("SARIF lost cluster provenance")
	}
	if exported.Findings[0].ClusterID != "alpha" {
		t.Fatal("JSON lost cluster provenance")
	}
	imported, err := sarifio.Import(sarif)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Findings) != 2 {
		t.Fatal("SARIF occurrences collapsed")
	}
	for _, f := range imported.Findings {
		if f.ClusterID != "" {
			t.Fatal("SARIF import trusted foreign cluster membership")
		}
	}
	for _, set := range [][]models.Finding{exported.Findings, imported.Findings} {
		for i, f := range set {
			want := findings[i]
			if f.PackageName != want.PackageName || f.PackageVersion != want.PackageVersion || f.PackageEcosystem != want.PackageEcosystem || f.PackageURL != want.PackageURL || f.AdvisoryID != want.AdvisoryID {
				t.Fatalf("exported dependency identity missing: %+v", f)
			}
			if identity.Build(f).Stable != want.StableFingerprint {
				t.Fatal("export changed dependency identity")
			}
		}
	}
}
