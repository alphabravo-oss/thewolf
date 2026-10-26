package routes_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestFindingAPIKeepsDependencyOccurrencesAndIdentity(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	repo := env.createRepo(t)
	scan := &models.Scan{ID: uuid.NewString(), UserID: env.UserID, RepoID: repo, Status: models.ScanStatusCompleted}
	if err := env.Store.CreateScan(t.Context(), scan); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"alpha", "beta"} {
		f := &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, Category: models.CategorySCA, ToolName: "trivy", RuleID: "CVE-2026-1001", AdvisoryID: "CVE-2026-1001", FilePath: "package-lock.json", PackageName: pkg, PackageVersion: "1", PackageEcosystem: "npm", PackageURL: "pkg:npm/" + pkg + "@1", Status: models.StatusOpen, Severity: models.SeverityHigh}
		if err := env.Store.CreateFinding(t.Context(), f); err != nil {
			t.Fatal(err)
		}
	}
	routes.DualWriteVulnerabilities(t.Context(), routes.DefaultHandler, scan)
	vulnerabilities, err := env.Store.ListVulnerabilitiesByScan(t.Context(), scan.ID)
	if err != nil || len(vulnerabilities) != 2 {
		t.Fatalf("canonical vulnerabilities collapsed: %d %v", len(vulnerabilities), err)
	}
	for _, v := range vulnerabilities {
		evidence, err := env.Store.ListEvidenceByVulnerability(t.Context(), v.ID)
		if err != nil || len(evidence) != 1 {
			t.Fatalf("package evidence crossed identities: %+v %v", evidence, err)
		}
	}
	response := env.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/findings", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("findings: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data []models.Finding `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 2 || payload.Meta.Total != 2 {
		t.Fatalf("occurrences collapsed: %s", response.Body.String())
	}
	names := map[string]bool{}
	for _, f := range payload.Data {
		if f.PackageVersion != "1" || f.PackageEcosystem != "npm" || f.PackageURL == "" || f.AdvisoryID != f.RuleID || f.IdentityVersion != 2 {
			t.Fatalf("API lost identity: %+v", f)
		}
		names[f.PackageName] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Fatal("API lost a package")
	}
}
