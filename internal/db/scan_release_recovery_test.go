package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestSQLiteRestartPreservesQueuedAndActiveReleaseAssignments(t *testing.T) {
	path := t.TempDir() + "/scan-recovery.db"
	runScanReleaseRestartContract(t, func() (Store, error) { return NewSQLite(path) })
}

func TestPostgresRestartPreservesQueuedAndActiveReleaseAssignments(t *testing.T) {
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "scan_recovery_" + uuid.NewString()
	if _, err := admin.db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
		_ = admin.Close()
	})
	isolatedDSN := postgresDSNWithSearchPath(t, dsn, schema)
	runScanReleaseRestartContract(t, func() (Store, error) { return NewPostgres(isolatedDSN) })
}

func runScanReleaseRestartContract(t *testing.T, open func() (Store, error)) {
	t.Helper()
	ctx := context.Background()
	store, err := open()
	if err != nil {
		t.Fatal(err)
	}
	userID, repoID := uuid.NewString(), uuid.NewString()
	if err := store.CreateUser(ctx, &models.User{
		ID: userID, Email: userID + "@example.test", PasswordHash: "hash",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{
		ID: repoID, UserID: userID, Name: "release-recovery-" + repoID,
		SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	newAssignedScan := func(releaseID, manifest string) *models.Scan {
		return &models.Scan{
			ID: uuid.NewString(), UserID: userID, RepoID: repoID, Branch: "main",
			Status: models.ScanStatusPending, ScannerReleaseID: releaseID,
			ReleaseManifestDigest: manifest, MaxAttempts: 3,
		}
	}
	active := newAssignedScan("release-active", "sha256:"+repeatHex("a"))
	queued := newAssignedScan("release-queued", "sha256:"+repeatHex("b"))
	if err := store.CreateScan(ctx, active); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := store.CreateScan(ctx, queued); err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimNextScan(ctx, "worker-before-restart", "docker", time.Now().Add(-time.Second))
	if err != nil || claim == nil || claim.ID != active.ID {
		t.Fatalf("claim before restart = %#v, err=%v", claim, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = open()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recoveredActive, err := store.GetScanByID(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	recoveredQueued, err := store.GetScanByID(ctx, queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertScanReleaseAssignment(t, recoveredActive, active)
	assertScanReleaseAssignment(t, recoveredQueued, queued)
	if recoveredActive.Status != models.ScanStatusRunning ||
		recoveredQueued.Status != models.ScanStatusPending {
		t.Fatalf("restart changed queue states: active=%s queued=%s",
			recoveredActive.Status, recoveredQueued.Status)
	}
	if n, err := store.ReclaimStaleScans(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("reclaim after restart = %d, err=%v", n, err)
	}
	retry, err := store.ClaimNextScan(ctx, "worker-after-restart", "docker", time.Now().Add(time.Minute))
	if err != nil || retry == nil || retry.ID != active.ID {
		t.Fatalf("retry claim after restart = %#v, err=%v", retry, err)
	}
	assertScanReleaseAssignment(t, retry, active)
	if retry.Attempt != 2 {
		t.Fatalf("automatic retry attempt = %d, want 2", retry.Attempt)
	}
}

func assertScanReleaseAssignment(t *testing.T, got, want *models.Scan) {
	t.Helper()
	if got.ScannerReleaseID != want.ScannerReleaseID ||
		got.ReleaseManifestDigest != want.ReleaseManifestDigest {
		t.Fatalf("release assignment changed: got=(%s,%s) want=(%s,%s)",
			got.ScannerReleaseID, got.ReleaseManifestDigest,
			want.ScannerReleaseID, want.ReleaseManifestDigest)
	}
}

func repeatHex(character string) string {
	result := ""
	for i := 0; i < 64; i++ {
		result += character
	}
	return result
}
