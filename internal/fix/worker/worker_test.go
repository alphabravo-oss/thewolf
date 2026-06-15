package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/fix/orchestrator"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// newStore opens an in-memory SQLite store with migrations applied.
func newStore(t *testing.T) db.Store {
	t.Helper()
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func enqueue(t *testing.T, store db.Store) *models.FixJob {
	t.Helper()
	now := time.Now().UTC()
	job := &models.FixJob{
		ID:        uuid.New().String(),
		UserID:    "u1",
		Type:      "fix",
		RepoID:    "r1",
		Mode:      models.FixModeDryRun,
		Status:    models.FixJobQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.EnqueueFixJob(context.Background(), job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return job
}

// stubRun installs a RunOrchestrator stub for the duration of a test and
// restores the original after. No real fixing happens.
func stubRun(t *testing.T, fn func(ctx context.Context, job *models.FixJob, deps orchestrator.Deps) (*orchestrator.Result, error)) {
	t.Helper()
	orig := RunOrchestrator
	RunOrchestrator = fn
	t.Cleanup(func() { RunOrchestrator = orig })
}

// TestWorkerOnceClaimsRunsStreamsAndStoresDiff is the Phase 6 happy path: the
// worker claims a queued job, the (stubbed) orchestrator emits a log line and a
// diff via the injected sinks, succeeds, and the worker exits after one job in
// --once mode. We assert the log + diff landed in the fixstore and the job is
// terminal.
func TestWorkerOnceClaimsRunsStreamsAndStoresDiff(t *testing.T) {
	store := newStore(t)
	job := enqueue(t, store)
	root := t.TempDir()
	fs := fixstore.New(root)

	const wantDiff = "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-bad\n+good\n"
	stubRun(t, func(ctx context.Context, j *models.FixJob, deps orchestrator.Deps) (*orchestrator.Result, error) {
		// The worker must wire the durable log + diff sinks.
		if deps.Log == nil {
			t.Error("worker did not inject a Log sink")
		} else {
			deps.Log("orchestrator: starting on %s", j.RepoID)
		}
		if deps.Diffs == nil {
			t.Fatal("worker did not inject a Diffs sink")
		}
		id, err := deps.Diffs.SaveDiff(ctx, j.ID, wantDiff)
		if err != nil {
			t.Fatalf("save diff: %v", err)
		}
		j.Status = models.FixJobSucceeded
		j.DiffArtifactID = id
		j.ResultBranch = "wolf-fix/" + j.ID
		if err := deps.Store.UpdateFixJob(ctx, j); err != nil {
			t.Fatalf("update job: %v", err)
		}
		return &orchestrator.Result{Branch: j.ResultBranch, DiffArtifactID: id}, nil
	})

	w, err := New(Config{Store: store, Fixstore: fs, Once: true})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("worker run: %v", err)
	}

	// Job is terminal and carries the branch + diff.
	got, err := store.GetFixJobByID(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if got.Status != models.FixJobSucceeded {
		t.Errorf("job status = %s, want succeeded", got.Status)
	}
	if got.ClaimedBy == "" {
		t.Errorf("expected claimed_by to be set after claim")
	}

	// The diff is readable through the fixstore (what GET /fixes/{id}/diff reads).
	diff, err := fs.ReadDiff(job.ID)
	if err != nil {
		t.Fatalf("read diff: %v", err)
	}
	if diff != wantDiff {
		t.Errorf("diff = %q, want %q", diff, wantDiff)
	}

	// The streamed log is durable (what GET /fixes/{id}/stream relays).
	log, err := fs.ReadLog(job.ID)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(log, "orchestrator: starting on r1") {
		t.Errorf("log missing orchestrator line:\n%s", log)
	}
	if !strings.Contains(log, "claimed job "+job.ID) {
		t.Errorf("log missing worker claim line:\n%s", log)
	}
}

// TestWorkerOnceEmptyQueueExits verifies --once exits cleanly when there is
// nothing to claim, without invoking the orchestrator.
func TestWorkerOnceEmptyQueueExits(t *testing.T) {
	store := newStore(t)
	called := false
	stubRun(t, func(ctx context.Context, j *models.FixJob, deps orchestrator.Deps) (*orchestrator.Result, error) {
		called = true
		return nil, nil
	})

	w, err := New(Config{Store: store, Fixstore: fixstore.New(t.TempDir()), Once: true})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("worker run: %v", err)
	}
	if called {
		t.Error("orchestrator was invoked on an empty queue")
	}
}

// TestWorkerRecordsFailure verifies that an orchestrator error is logged and
// does not abort the worker loop (the orchestrator owns persisting the failed
// status; the worker just records the log).
func TestWorkerRecordsFailure(t *testing.T) {
	store := newStore(t)
	job := enqueue(t, store)
	fs := fixstore.New(t.TempDir())

	stubRun(t, func(ctx context.Context, j *models.FixJob, deps orchestrator.Deps) (*orchestrator.Result, error) {
		j.Status = models.FixJobFailed
		j.Error = "repo not fixable"
		_ = deps.Store.UpdateFixJob(ctx, j)
		return nil, context.DeadlineExceeded
	})

	w, err := New(Config{Store: store, Fixstore: fs, Once: true})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("worker run should not surface a per-job failure: %v", err)
	}

	got, _ := store.GetFixJobByID(context.Background(), job.ID)
	if got.Status != models.FixJobFailed {
		t.Errorf("job status = %s, want failed", got.Status)
	}
	log, _ := fs.ReadLog(job.ID)
	if !strings.Contains(log, "job failed") {
		t.Errorf("log missing failure line:\n%s", log)
	}
}

// TestWorkerSkipsPreCancelledJob verifies a job cancelled before the worker
// runs it is not handed to the orchestrator.
func TestWorkerSkipsPreCancelledJob(t *testing.T) {
	store := newStore(t)
	job := enqueue(t, store)
	// Cancel it before the worker claims it.
	job, _ = store.ClaimNextFixJob(context.Background(), "pre")
	job.Status = models.FixJobCancelled
	if err := store.UpdateFixJob(context.Background(), job); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// Re-queue is not needed; the worker claims queued jobs only. Instead drive
	// process directly to assert the pre-cancel guard.
	fs := fixstore.New(t.TempDir())
	w, _ := New(Config{Store: store, Fixstore: fs, Once: true})

	called := false
	stubRun(t, func(ctx context.Context, j *models.FixJob, deps orchestrator.Deps) (*orchestrator.Result, error) {
		called = true
		return nil, nil
	})
	logger := zerolog.Nop()
	w.process(context.Background(), &logger, job)
	if called {
		t.Error("orchestrator invoked for a pre-cancelled job")
	}
}
