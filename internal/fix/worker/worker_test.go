package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/console"
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
	if err := store.SetSetting(t.Context(), "autofix_enabled", "true"); err != nil {
		t.Fatal(err)
	}
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

func TestWorkerAutofixDisabledRetainsQueuedJob(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-claim", true: "after-claim"}[claimed], func(t *testing.T) {
			store := newStore(t)
			job := enqueue(t, store)
			if claimed {
				var err error
				job, err = store.ClaimNextFixJob(t.Context(), "worker")
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SetSetting(t.Context(), "autofix_enabled", "false"); err != nil {
				t.Fatal(err)
			}
			stubRun(t, func(context.Context, *models.FixJob, orchestrator.Deps) (*orchestrator.Result, error) {
				t.Fatal("disabled worker invoked orchestrator")
				return nil, nil
			})
			w, err := New(Config{Store: store, Fixstore: fixstore.New(t.TempDir()), Once: true})
			if err != nil {
				t.Fatal(err)
			}
			if claimed {
				logger := zerolog.Nop()
				w.process(t.Context(), &logger, job)
			} else if err := w.Run(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetFixJobByID(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != models.FixJobQueued || got.ClaimedBy != "" || got.ClaimedAt != nil {
				t.Fatalf("disabled job must remain queued and unclaimed: %+v", got)
			}
		})
	}
}

func TestWorkerAutofixDisableStopsActiveRun(t *testing.T) {
	store := newStore(t)
	enqueue(t, store)
	stopped := false
	stubRun(t, func(ctx context.Context, _ *models.FixJob, _ orchestrator.Deps) (*orchestrator.Result, error) {
		if err := store.SetSetting(t.Context(), "autofix_enabled", "false"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			stopped = true
		case <-time.After(3 * time.Second):
			t.Fatal("disabled active run did not stop")
		}
		return nil, ctx.Err()
	})
	w, err := New(Config{Store: store, Fixstore: fixstore.New(t.TempDir()), Once: true, Heartbeat: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("active run was not exercised")
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

func TestWorkerOnceRunsQueuedConsole(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	cons := &models.FixerConsole{
		ID:     uuid.New().String(),
		UserID: "u1",
		Kind:   models.FixerConsoleShell,
		Status: models.FixerConsoleQueued,
	}
	if err := store.EnqueueFixerConsole(ctx, cons); err != nil {
		t.Fatalf("enqueue console: %v", err)
	}
	orig := console.CommandForTest
	console.CommandForTest = func(*models.FixerConsole) ([]string, error) {
		return []string{"/bin/echo", "wolf-console-ok"}, nil
	}
	t.Cleanup(func() { console.CommandForTest = orig })

	called := false
	stubRun(t, func(ctx context.Context, j *models.FixJob, deps orchestrator.Deps) (*orchestrator.Result, error) {
		called = true
		return nil, nil
	})

	fs := fixstore.New(t.TempDir())
	w, err := New(Config{Store: store, Fixstore: fs, Once: true, Heartbeat: time.Second})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if called {
		t.Fatal("orchestrator should not run when only a console is queued")
	}
	got, err := store.GetFixerConsoleByID(ctx, cons.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != models.FixerConsoleExited {
		t.Fatalf("console status = %s", got.Status)
	}
	log, err := fs.ReadConsole(cons.ID)
	if err != nil {
		t.Fatalf("read console: %v", err)
	}
	if !strings.Contains(log, "wolf-console-ok") {
		t.Fatalf("console log missing expected output:\n%s", log)
	}
}

func TestConsoleHeartbeatStopsExpiredClaim(t *testing.T) {
	store := newStore(t)
	ctx := t.Context()
	cons := &models.FixerConsole{ID: "expired-console", Kind: models.FixerConsoleShell}
	if err := store.EnqueueFixerConsole(ctx, cons); err != nil {
		t.Fatal(err)
	}
	cons, err := store.ClaimNextFixerConsole(ctx, "worker")
	if err != nil || cons == nil {
		t.Fatalf("claim: %+v %v", cons, err)
	}
	w, err := New(Config{Store: store, Fixstore: fixstore.New(t.TempDir()), Heartbeat: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := store.ReclaimStaleConsoles(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := w.startConsoleHeartbeat(runCtx, cons, cancel)
	defer stop()
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("expired claim did not stop console")
	}
	stop() // Joining the heartbeat is safe more than once.
	got, err := store.GetFixerConsoleByID(ctx, cons.ID)
	if err != nil || got.Status != models.FixerConsoleExited || got.Error == "" {
		t.Fatalf("heartbeat rewrote expired state: %+v %v", got, err)
	}
}

// TestWorkerSkipsPreCancelledJob verifies a job cancelled before the worker
// runs it is not handed to the orchestrator.
func TestWorkerSkipsPreCancelledJob(t *testing.T) {
	store := newStore(t)
	_ = enqueue(t, store)
	// Cancel it before the worker claims it.
	job, _ := store.ClaimNextFixJob(context.Background(), "pre")
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

func TestWorkerStopsLostClaimWithoutOverwritingReplacement(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "reclaimed"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			store := newStore(t)
			queued := enqueue(t, store)
			job, err := store.ClaimNextFixJob(t.Context(), "same-worker")
			if err != nil {
				t.Fatal(err)
			}
			var replacement *models.FixJob
			stubRun(t, func(ctx context.Context, j *models.FixJob, deps orchestrator.Deps) (*orchestrator.Result, error) {
				if cancelled {
					replacement, err = store.GetFixJobByID(t.Context(), j.ID)
					if err != nil {
						t.Fatal(err)
					}
					replacement.Status = models.FixJobCancelled
					err = store.UpdateFixJob(t.Context(), replacement)
				} else {
					_, err = store.ReclaimStaleJobs(t.Context(), time.Now().Add(time.Hour))
					if err == nil {
						replacement, err = store.ClaimNextFixJob(t.Context(), "same-worker")
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-ctx.Done():
				case <-time.After(3 * time.Second):
					t.Fatal("worker did not stop after losing its claim")
				}
				return nil, ctx.Err()
			})
			w, err := New(Config{Store: store, Fixstore: fixstore.New(t.TempDir()), Heartbeat: 5 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			logger := zerolog.Nop()
			w.process(t.Context(), &logger, job)
			got, err := store.GetFixJobByID(t.Context(), queued.ID)
			if err != nil || got.Status != replacement.Status || got.Revision != replacement.Revision || got.Error != "" {
				t.Fatalf("old worker overwrote newer state: %+v %v", got, err)
			}
		})
	}
}

func TestWorkerAcknowledgesCancellationAfterEngineStops(t *testing.T) {
	for _, beforeRun := range []bool{false, true} {
		name := "during-run"
		if beforeRun {
			name = "before-run"
		}
		t.Run(name, func(t *testing.T) {
			store := newStore(t)
			if err := store.CreateUser(t.Context(), &models.User{ID: "u1", Email: "cancel@example.test", PasswordHash: "unused"}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateRepo(t.Context(), &models.Repo{ID: "r1", UserID: "u1", Name: "cancel", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			enqueue(t, store)
			job, err := store.ClaimNextFixJob(t.Context(), "worker")
			if err != nil {
				t.Fatal(err)
			}
			markCancelled := func() {
				latest, err := store.GetFixJobByID(t.Context(), job.ID)
				if err != nil {
					t.Fatal(err)
				}
				latest.Status = models.FixJobCancelled
				latest.PauseReason = models.FixPauseCancellationCleanup
				if err := store.UpdateFixJob(t.Context(), latest); err != nil {
					t.Fatal(err)
				}
			}
			if beforeRun {
				markCancelled()
			}
			ran := false
			stubRun(t, func(ctx context.Context, j *models.FixJob, deps orchestrator.Deps) (*orchestrator.Result, error) {
				ran = true
				markCancelled()
				select {
				case <-ctx.Done():
				case <-time.After(3 * time.Second):
					t.Fatal("engine was not cancelled")
				}
				latest, err := store.GetFixJobByID(t.Context(), job.ID)
				if err != nil || latest.PauseReason != models.FixPauseCancellationCleanup {
					t.Fatal("cleanup acknowledged before engine returned")
				}
				return nil, ctx.Err()
			})
			w, err := New(Config{Store: store, Fixstore: fixstore.New(t.TempDir()), Heartbeat: 5 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			logger := zerolog.Nop()
			w.process(t.Context(), &logger, job)
			saved, err := store.GetFixJobByID(t.Context(), job.ID)
			if err != nil || saved.Status != models.FixJobCancelled || saved.PauseReason != "" || saved.ClaimedBy != "" {
				t.Fatalf("cleanup not acknowledged: %+v %v", saved, err)
			}
			if ran == beforeRun {
				t.Fatalf("ran=%v beforeRun=%v", ran, beforeRun)
			}
		})
	}
}
