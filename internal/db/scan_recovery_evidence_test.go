package db

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestRecoveryEvidenceDeletionSQLite(t *testing.T) {
	testRecoveryEvidenceDeletion(t, newTestStore(t))
}

func TestRecoveryEvidenceDeletionPostgres(t *testing.T) {
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "recovery_evidence_" + uuid.NewString()
	if _, err := admin.db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); _ = admin.Close() })
	store, err := NewPostgres(postgresDSNWithSearchPath(t, dsn, schema))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	testRecoveryEvidenceDeletion(t, store)
}

func testRecoveryEvidenceDeletion(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	userID, repoID := uuid.NewString(), uuid.NewString()
	if err := store.CreateUser(ctx, &models.User{ID: userID, Email: userID + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{ID: repoID, UserID: userID, Name: "recovery", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{ID: uuid.NewString(), UserID: userID, RepoID: repoID, Status: models.ScanStatusPending, MaxAttempts: 3}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	old, err := store.ClaimNextScan(ctx, "lost", "docker", time.Now().Add(-time.Minute))
	if err != nil || old == nil {
		t.Fatalf("old claim: %v", err)
	}
	if _, err := store.ReclaimStaleScans(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	current, err := store.ClaimNextScan(ctx, "current", "docker", time.Now().Add(time.Minute))
	if err != nil || current == nil {
		t.Fatalf("current claim: %v", err)
	}
	for _, tool := range []string{"done", "incomplete"} {
		if err := store.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repoID, ToolName: tool, Fingerprint: tool, Title: tool, Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen}); err != nil {
			t.Fatal(err)
		}
	}
	for _, token := range []string{old.LeaseToken, "", "wrong-lease"} {
		if err := store.DeleteFindingsByScanTool(ctx, scan.ID, "incomplete", token); !errors.Is(err, ErrStaleScanLease) {
			t.Fatalf("unowned deletion accepted: %v", err)
		}
	}
	findings, err := store.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(findings) != 2 {
		t.Fatalf("stale deletion changed findings: %d, %v", len(findings), err)
	}
	if err := store.DeleteFindingsByScanTool(ctx, scan.ID, "incomplete", current.LeaseToken); err != nil {
		t.Fatal(err)
	}
	findings, err = store.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(findings) != 1 || findings[0].ToolName != "done" {
		t.Fatalf("wrong evidence deleted: %+v, %v", findings, err)
	}
	if err := store.RequestScanCancellation(ctx, scan.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFindingsByScanTool(ctx, scan.ID, "done", current.LeaseToken); !errors.Is(err, ErrStaleScanLease) {
		t.Fatalf("cancelled deletion accepted: %v", err)
	}
	findings, err = store.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(findings) != 1 {
		t.Fatalf("cancelled evidence lost: %d, %v", len(findings), err)
	}
}
