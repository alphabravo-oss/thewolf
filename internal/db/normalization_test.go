package db

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	"github.com/google/uuid"
)

func TestNormalizedEvidencePersistence(t *testing.T) {
	var store Store = newClusterPostgresStore(t)
	user, repo, scan := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := store.CreateUser(t.Context(), &models.User{ID: user, Email: user + "@example.test", PasswordHash: "unused"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(t.Context(), &models.Repo{ID: repo, UserID: user, Name: "normalization"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateScan(t.Context(), &models.Scan{ID: scan, RepoID: repo, UserID: user, Status: models.ScanStatusRunning}); err != nil {
		t.Fatal(err)
	}
	severity, provenance := plugin.ResolveSeverity("", []plugin.CVSS{{Type: "CVSS_V3", Score: "9.8"}}, models.SeverityHigh)
	var rows []models.Finding
	for _, hash := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		for _, file := range []string{"/scan/one.py", "/scan/two.py"} {
			rows = append(rows, models.Finding{ID: uuid.NewString(), RepoID: repo, ScanID: scan, ToolName: "detect-secrets", RuleID: "AWS Access Key:" + hash, Category: models.CategorySecrets, FineCategory: "hardcoded-secret", FilePath: file, LineStart: 10, Severity: models.SeverityHigh, Status: models.StatusOpen, CWEID: "798"})
		}
	}
	rows = append(rows, models.Finding{ID: uuid.NewString(), RepoID: repo, ScanID: scan, ToolName: "osv-scanner", Category: models.CategorySCA, RuleID: "CVE-1", PackageName: "package", Severity: severity, Description: provenance, Status: models.StatusOpen})
	if err := store.CreateFindings(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.ListFindingsByScan(t.Context(), scan)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeFindingClusters(t.Context(), scan, "", runner.Deduplicate(persisted)); err != nil {
		t.Fatal(err)
	}
	primaries, err := store.ListFindingsByScan(t.Context(), scan)
	if err != nil || len(primaries) != 3 {
		t.Fatalf("lost distinct findings: %+v %v", primaries, err)
	}
	for _, f := range primaries {
		if f.ToolName == "osv-scanner" {
			if f.Severity != models.SeverityCritical || f.Description != provenance {
				t.Fatalf("lost severity evidence: %+v", f)
			}
			continue
		}
		if f.IdentityVersion != 3 || f.CWEID != "CWE-798" || f.Confidence != "low" {
			t.Fatalf("lost normalization: %+v", f)
		}
		members, err := store.ListFindingClusterEvidence(t.Context(), f.ID)
		if err != nil || len(members) != 2 || members[0].FilePath == members[1].FilePath {
			t.Fatalf("lost locations: %+v %v", members, err)
		}
	}
}
