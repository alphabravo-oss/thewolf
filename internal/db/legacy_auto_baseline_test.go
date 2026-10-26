package db

import (
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestLegacyAutoBaselineSQLite(t *testing.T) {
	s := newTestStore(t)
	testLegacyAutoBaseline(t, s, s.db)
}

func TestLegacyAutoBaselinePostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testLegacyAutoBaseline(t, s, s.db)
}

// Older releases saved the automatic baseline from a scan with failed tools.
// Today's rules reject such a baseline; that must not stop new scans from
// publishing, and the first qualifying scan replaces it (production 2026-10-26).
func testLegacyAutoBaseline(t *testing.T, s Store, database *sqlx.DB) {
	ctx := t.Context()
	user, repo, branch := uuid.NewString(), uuid.NewString(), "main"
	if err := s.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "legacy"}); err != nil {
		t.Fatal(err)
	}
	earlier := time.Now().UTC().Add(-time.Hour)
	legacy := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: user, Branch: branch, Status: models.ScanStatusCompleted, Profile: "standard",
		ToolsSelected: `["a","b"]`, ToolsCompleted: `["a"]`, ToolsFailed: `["b"]`, CompletedAt: &earlier}
	if err := s.CreateScan(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	baselineID := uuid.NewString()
	if _, err := database.Exec(database.Rebind(`INSERT INTO scan_baselines (id, repo_id, branch, name, scan_id, strategy, created_by, created_at, updated_at)
 VALUES (?, ?, ?, 'auto', ?, 'auto-first-success', ?, ?, ?)`), baselineID, repo, branch, legacy.ID, user, earlier, earlier); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	scan := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: user, Branch: branch, Status: models.ScanStatusRunning, Profile: "standard",
		ToolsSelected: `["a"]`, ToolsCompleted: `["a"]`, ToolsFailed: `[]`, LeaseToken: "lease", ClaimedBy: "fixture", LeaseExpiresAt: &now, HeartbeatAt: &now}
	if err := s.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, StableFingerprint: "f1", ToolName: "a", Severity: models.SeverityHigh, Title: "evidence"}); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := s.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The executor derives baseline history before completion; this is where
	// production failed ("saved baseline is not a prior successful ...").
	if err := s.PersistFindingBaseline(ctx, scan.ID, "lease", now); err != nil {
		t.Fatalf("a stale legacy auto baseline blocked baseline derivation: %v", err)
	}
	snapshot, _, err = s.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	scan.FindingsRevision = snapshot.FindingsRevision
	scan.Status, scan.Phase, scan.CompletedAt = models.ScanStatusCompleted, "completed", &now
	gate := &models.QualityGateResult{FindingsRevision: scan.FindingsRevision, ScanStatus: models.ScanStatusCompleted, ID: uuid.NewString(), ScanID: scan.ID, PolicyID: "default", Status: "pass", SummaryJSON: `{}`, MatchedRulesJSON: `[]`}
	artifact := &models.ScanArtifact{ID: uuid.NewString(), ScanID: scan.ID, ArtifactType: models.ArtifactJSON, FilePath: "gate-result.json", StorageKey: "fixture/gate-result.json", ChecksumSHA256: "fixture", FindingsRevision: &scan.FindingsRevision}
	if ok, err := s.CompleteScan(ctx, scan, ScanCompletion{Gate: gate, GateArtifact: artifact, AutoBaseline: true}); err != nil || !ok {
		t.Fatalf("a stale legacy auto baseline blocked publication: %v %v", ok, err)
	}
	var savedScan string
	if err := database.Get(&savedScan, database.Rebind("SELECT scan_id FROM scan_baselines WHERE id = ?"), baselineID); err != nil {
		t.Fatal(err)
	}
	if savedScan != scan.ID {
		t.Fatalf("stale auto baseline was not replaced by the first qualifying scan: %s", savedScan)
	}
}
