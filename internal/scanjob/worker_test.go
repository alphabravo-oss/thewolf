package scanjob

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func queuedTestScan(t *testing.T) (*db.SQLiteStore, *models.Scan) {
	t.Helper()
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	userID := uuid.NewString()
	repoID := uuid.NewString()
	if err := store.CreateUser(ctx, &models.User{
		ID: userID, Email: userID + "@example.test", PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{
		ID: repoID, UserID: userID, Name: "repo", SourceType: models.SourceTypeLocal,
		SourcePath: t.TempDir(), DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	scan := &models.Scan{
		ID: uuid.NewString(), UserID: userID, RepoID: repoID, Branch: "main",
		Status: models.ScanStatusPending,
	}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	return store, scan
}

func TestWorkerOnceClaimsAndExecutesScan(t *testing.T) {
	store, scan := queuedTestScan(t)
	executed := make(chan *models.Scan, 1)
	worker, err := New(Config{
		Store: store, WorkerID: "test-worker", Backend: "docker", Once: true,
		Heartbeat: time.Millisecond, Lease: 20 * time.Millisecond,
		Executor: func(ctx context.Context, claimed *models.Scan) error {
			executed <- claimed
			now := time.Now().UTC()
			claimed.Status = models.ScanStatusCompleted
			claimed.Phase = "completed"
			claimed.CompletedAt = &now
			return store.UpdateScan(ctx, claimed)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	claimed := <-executed
	if claimed.ID != scan.ID || claimed.Attempt != 1 || claimed.LeaseToken == "" {
		t.Fatalf("unexpected claimed scan: %#v", claimed)
	}
	got, err := store.GetScanByID(context.Background(), scan.ID)
	if err != nil || got.Status != models.ScanStatusCompleted {
		t.Fatalf("completed scan: got=%v err=%v", got, err)
	}
	workers, err := store.ListScanWorkers(context.Background(), time.Now().Add(-time.Minute))
	if err != nil || len(workers) != 0 {
		t.Fatalf("stopped worker must not remain active: workers=%v err=%v", workers, err)
	}
}

func TestWorkerExecutorErrorFailsOwnedClaim(t *testing.T) {
	store, scan := queuedTestScan(t)
	worker, err := New(Config{
		Store: store, Once: true,
		Executor: func(context.Context, *models.Scan) error {
			return context.DeadlineExceeded
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := store.GetScanByID(context.Background(), scan.ID)
	if err != nil {
		t.Fatalf("GetScanByID: %v", err)
	}
	if got.Status != models.ScanStatusFailed || got.FailureCode != "executor_error" {
		t.Fatalf("expected executor failure, got status=%s code=%s", got.Status, got.FailureCode)
	}
}

func TestWorkerCancelsExecutorBeforeUnrenewedLeaseExpires(t *testing.T) {
	store, _ := queuedTestScan(t)
	executorCancelled := make(chan struct{})
	worker, err := New(Config{
		Store: store, Once: true, Heartbeat: 2 * time.Millisecond, Lease: 20 * time.Millisecond,
		Executor: func(ctx context.Context, _ *models.Scan) error {
			_ = store.Close()
			select {
			case <-ctx.Done():
				close(executorCancelled)
				return ctx.Err()
			case <-time.After(time.Second):
				return context.DeadlineExceeded
			}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case <-executorCancelled:
	default:
		t.Fatal("executor context was not cancelled")
	}
}

func TestWorkerCleansWorkspaceAfterStaleReclaim(t *testing.T) {
	store, scan := queuedTestScan(t)
	oldClaim, err := store.ClaimNextScan(
		context.Background(), "lost-worker", "docker", time.Now().Add(-time.Minute),
	)
	if err != nil || oldClaim == nil {
		t.Fatalf("seed stale claim: scan=%v err=%v", oldClaim, err)
	}
	oldClaim.PreparedWorkspace = "/validated/stale-workspace"
	if err := store.UpdateScan(context.Background(), oldClaim); err != nil {
		t.Fatal(err)
	}
	var cleaned string
	worker, err := New(Config{
		Store: store, Once: true,
		CleanupWorkspace: func(path string) error {
			cleaned = path
			return nil
		},
		Executor: func(ctx context.Context, claimed *models.Scan) error {
			now := time.Now().UTC()
			claimed.Status = models.ScanStatusCompleted
			claimed.Phase = "completed"
			claimed.CompletedAt = &now
			ok, finalizeErr := store.FinalizeScan(ctx, claimed, claimed.LeaseToken)
			if finalizeErr != nil {
				return finalizeErr
			}
			if !ok {
				return errors.New("retried scan did not finalize")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cleaned != oldClaim.PreparedWorkspace {
		t.Fatalf("cleaned workspace = %q, want %q", cleaned, oldClaim.PreparedWorkspace)
	}
	got, err := store.GetScanByID(context.Background(), scan.ID)
	if err != nil || got.Status != models.ScanStatusCompleted || got.Attempt != 2 ||
		got.FailureCode != "" || got.FailureMessage != "" {
		t.Fatalf("retried scan = %#v err=%v", got, err)
	}
}

func TestWorkerRejectsMissingDependencies(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected missing store error")
	}
	store, _ := queuedTestScan(t)
	if _, err := New(Config{Store: store}); err == nil {
		t.Fatal("expected missing executor error")
	}
}
