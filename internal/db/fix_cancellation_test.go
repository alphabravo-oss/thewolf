package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestFixCancellationRetainsOwnershipUntilCleanup(t *testing.T) {
	var store, peer Store
	store = newClusterPostgresStore(t)
	peer = store
	ctx := t.Context()
	user, repo, scan := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "unused"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "cleanup"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateScan(ctx, &models.Scan{ID: scan, RepoID: repo, UserID: user, Status: models.ScanStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	newJob := func() *models.FixJob {
		return &models.FixJob{ID: uuid.NewString(), UserID: user, RepoID: repo, ScanID: scan}
	}
	job, _, err := store.CreateFixJob(ctx, newJob(), true)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = models.FixJobCancelled
	job.PauseReason = models.FixPauseCancellationCleanup
	job.WorkspacePath = "/retained/worktree"
	job.ResultBranch = job.TargetBranch
	if err := store.UpdateFixJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("cleanup unavailable")
	if err := store.CreateFixAttempt(ctx, &models.FixAttempt{ID: uuid.NewString(), JobID: job.ID, FindingID: "interrupted-finding", Outcome: models.FixOutcomeRunning}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteFixCancellation(ctx, job, func(context.Context, *models.FixJob, *models.Repo) error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	history, err := store.ListFixAttempts(ctx, job.ID)
	if err != nil || len(history) != 1 || history[0].Outcome != models.FixOutcomeRunning {
		t.Fatalf("failed cleanup partially finalized history: %+v %v", history, err)
	}
	saved, err := store.GetFixJobByID(ctx, job.ID)
	if err != nil || saved.WorkspacePath != job.WorkspacePath || saved.PauseReason != models.FixPauseCancellationCleanup {
		t.Fatalf("cleanup failure lost recovery state: %+v %v", saved, err)
	}
	rem, err := store.GetRemediationByID(ctx, job.RemediationID)
	if err != nil || rem.State != models.RemediationOpen {
		t.Fatalf("cleanup failure discarded remediation: %+v %v", rem, err)
	}
	if _, _, err := peer.CreateFixJob(ctx, newJob(), true); !errors.Is(err, ErrFixBusy) {
		t.Fatalf("creation reused pending checkout: %v", err)
	}
	queued := newJob()
	queued.TargetBranch = job.TargetBranch
	if err := store.EnqueueFixJob(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if claimed, err := peer.ClaimNextFixJob(ctx, "other-worker"); err != nil || claimed != nil {
		t.Fatalf("claim bypassed pending cleanup: %+v %v", claimed, err)
	}
	queued.Status = models.FixJobSuperseded
	if err := store.UpdateFixJob(ctx, queued); err != nil {
		t.Fatal(err)
	}

	// A late acknowledgement failure must roll back the remediation change
	// and keep the durable retry marker after the cleanup callback ran.
	remove := failFixJobAcknowledgement(t, store)
	cleaned := false
	err = store.CompleteFixCancellation(ctx, job, func(context.Context, *models.FixJob, *models.Repo) error { cleaned = true; return nil })
	if err == nil || !cleaned {
		t.Fatalf("late-write fixture did not fail after cleanup: %v", err)
	}
	remove()
	rem, err = store.GetRemediationByID(ctx, job.RemediationID)
	if err != nil || rem.State != models.RemediationOpen {
		t.Fatalf("failed acknowledgement partially committed: %+v %v", rem, err)
	}
	saved, err = store.GetFixJobByID(ctx, job.ID)
	if err != nil || saved.PauseReason != models.FixPauseCancellationCleanup || saved.WorkspacePath != job.WorkspacePath {
		t.Fatalf("failed acknowledgement lost retry state: %+v %v", saved, err)
	}

	// A creator arriving during cleanup must wait for the transaction, then
	// create a new remediation, never reuse the checkout being removed.
	entered, release := make(chan struct{}), make(chan struct{})
	cleanupDone := make(chan error, 1)
	stale := *job
	go func() {
		cleanupDone <- store.CompleteFixCancellation(ctx, job, func(context.Context, *models.FixJob, *models.Repo) error { close(entered); <-release; return nil })
	}()
	<-entered
	created := make(chan *models.FixJob, 1)
	createErrors := make(chan error, 1)
	go func() { next, _, err := peer.CreateFixJob(ctx, newJob(), true); created <- next; createErrors <- err }()
	select {
	case <-created:
		close(release)
		<-cleanupDone
		t.Fatal("creation escaped cleanup lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-cleanupDone; err != nil {
		t.Fatal(err)
	}
	next := <-created
	if err := <-createErrors; err != nil {
		t.Fatal(err)
	}
	if next.RemediationID == job.RemediationID {
		t.Fatal("creation reused discarded remediation")
	}
	if job.WorkspacePath != "" || job.ResultBranch != "" || job.PauseReason != "" {
		t.Fatalf("cleanup acknowledgement incomplete: %+v", job)
	}
	history, err = store.ListFixAttempts(ctx, job.ID)
	if err != nil || len(history) != 1 || history[0].Outcome != models.FixOutcomeInterrupted {
		t.Fatalf("cancelled history stayed running after cleanup: %+v %v", history, err)
	}
	rem, err = store.GetRemediationByID(ctx, job.RemediationID)
	if err != nil || rem.State != models.RemediationDiscarded {
		t.Fatalf("cleanup did not discard remediation: %+v %v", rem, err)
	}
	called := false
	err = peer.CompleteFixCancellation(ctx, &stale, func(context.Context, *models.FixJob, *models.Repo) error { called = true; return nil })
	if !errors.Is(err, ErrFixJobConflict) || called {
		t.Fatalf("stale cleanup could delete new branch: called=%v error=%v", called, err)
	}
}

func failFixJobAcknowledgement(t *testing.T, store Store) func() {
	t.Helper()
	database := store.(*PostgresStore).db
	install := func() {
		if _, err := database.Exec(`CREATE FUNCTION reject_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.pause_reason='' THEN RAISE EXCEPTION 'cleanup persistence failure'; END IF; RETURN NEW; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`CREATE TRIGGER reject_cleanup BEFORE UPDATE ON fix_jobs FOR EACH ROW EXECUTE FUNCTION reject_cleanup()`); err != nil {
			t.Fatal(err)
		}
	}
	remove := func() {
		if _, err := database.Exec("DROP TRIGGER reject_cleanup ON fix_jobs"); err != nil {
			t.Fatal(err)
		}
	}
	install()

	return remove
}
