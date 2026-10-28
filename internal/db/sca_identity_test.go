package db

import (
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestDependencyIdentityPostgres(t *testing.T) {
	store := NewTestPostgres(t)
	testDependencyIdentity(t, store)
}
func testDependencyIdentity(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "dependencies", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	scan := &models.Scan{ID: uuid.NewString(), UserID: user, RepoID: repo, Branch: "main", Status: models.ScanStatusCompleted, CompletedAt: &now, ToolsSelected: `["trivy"]`, ToolsCompleted: `["trivy"]`}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	// Simulate the preceding schema with a persisted v1 finding. The additive
	// upgrade must preserve that row instead of guessing missing package data.
	legacy := &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "trivy", Category: models.CategorySCA, RuleID: "CVE-legacy", FilePath: "legacy.lock", Title: "legacy", Status: models.StatusOpen, Severity: models.SeverityHigh, Fingerprint: "legacy", StableFingerprint: "legacy", IdentityVersion: 1}
	if err := store.CreateFinding(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"package_name", "package_version", "package_ecosystem", "package_url", "advisory_id"} {
		if _, err := store.(*PostgresStore).db.Exec("ALTER TABLE findings DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	reconnectPostgresTestStore(store)
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	old, err := store.GetFindingByID(ctx, legacy.ID)
	if err != nil || old.StableFingerprint != "legacy" || old.IdentityVersion != 1 || old.PackageName != "" {
		t.Fatalf("legacy row changed during upgrade: %+v %v", old, err)
	}
	var input []models.Finding
	for _, pkg := range []string{"alpha", "beta"} {
		for _, advisory := range []string{"CVE-2026-1001", "CVE-2026-1002"} {
			input = append(input, models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "trivy", Category: models.CategorySCA, FilePath: "package-lock.json", PackageName: pkg, PackageVersion: "1", PackageEcosystem: "npm", PackageURL: "pkg:npm/" + pkg + "@1", AdvisoryID: advisory, RuleID: advisory, Title: advisory, Status: models.StatusOpen, Severity: models.SeverityHigh})
		}
	}
	if err := store.CreateFinding(ctx, &input[0]); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateFindings(ctx, input[1:]); err != nil {
		t.Fatal(err)
	}
	// Repeated additive migration must retain stored identities and be safe to resume.
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	findings, err := store.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(findings) != 5 {
		t.Fatalf("persisted occurrences: %d %v", len(findings), err)
	}
	keys := map[string]bool{}
	for _, f := range findings {
		if f.ID == legacy.ID {
			continue
		}
		if f.PackageName == "" || f.PackageVersion != "1" || f.PackageEcosystem != "npm" || f.PackageURL == "" || f.AdvisoryID != f.RuleID || f.IdentityVersion != 2 || keys[f.StableFingerprint] {
			t.Fatalf("dependency identity was lost: %+v", f)
		}
		keys[f.StableFingerprint] = true
		f.ModuleName = "enriched module"
		f.CompositeScore = 8
		if err := store.UpdateFinding(ctx, &f); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetFindingByID(ctx, f.ID)
		if err != nil || got.PackageName != f.PackageName || got.PackageURL != f.PackageURL || got.AdvisoryID != f.AdvisoryID || got.StableFingerprint != f.StableFingerprint {
			t.Fatalf("enrichment changed identity: %+v %v", got, err)
		}
	}
	current, err := store.ListCurrentOpenFindings(ctx, user, false, "")
	if err != nil || len(current) != 5 {
		t.Fatalf("fleet collapsed dependency occurrences: %d %v", len(current), err)
	}
}
