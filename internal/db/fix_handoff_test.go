package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestFixHandoffExcludesWorkspaceUsers(t *testing.T) {
	var store, peer Store
	store = newClusterPostgresStore(t)
	peer = store
	ctx := t.Context()
	user, repo, origin := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "unused"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "handoff"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateScan(ctx, &models.Scan{ID: origin, RepoID: repo, UserID: user, Status: models.ScanStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	newJob := func() *models.FixJob {
		return &models.FixJob{ID: uuid.NewString(), UserID: user, RepoID: repo, ScanID: origin}
	}
	job, _, err := store.CreateFixJob(ctx, newJob(), true)
	if err != nil {
		t.Fatal(err)
	}
	rem, err := store.GetRemediationByID(ctx, job.RemediationID)
	if err != nil {
		t.Fatal(err)
	}
	rem.WorkspacePath = "/retained/worktree"
	if err := store.UpdateRemediation(ctx, rem); err != nil {
		t.Fatal(err)
	}
	job.Status, job.WorkspacePath = models.FixJobAwaitingReview, rem.WorkspacePath
	if err := store.UpdateFixJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	child := &models.Scan{ID: uuid.NewString(), UserID: user, RepoID: repo, OriginScanID: origin, RemediationID: rem.ID, PreparedWorkspace: rem.WorkspacePath, Status: models.ScanStatusPending}
	if err := store.CreateScan(ctx, child); err != nil {
		t.Fatal(err)
	}
	called := false
	cleanup := func(context.Context, *models.Remediation, *models.Repo) (string, error) {
		called = true
		return "commit", nil
	}
	if err := store.AcceptFixRemediation(ctx, rem, cleanup); !errors.Is(err, ErrFixBusy) || called {
		t.Fatalf("handoff deleted active scan: %v", err)
	}
	if _, _, err := peer.CreateFixJob(ctx, newJob(), true); !errors.Is(err, ErrFixBusy) {
		t.Fatalf("creation ignored child scan: %v", err)
	}
	resume := *job
	resume.Status = models.FixJobQueued
	if err := peer.UpdateFixJob(ctx, &resume); !errors.Is(err, ErrFixBusy) {
		t.Fatalf("resume ignored child scan: %v", err)
	}
	queued := newJob()
	queued.RemediationID, queued.WorkspacePath, queued.TargetBranch = rem.ID, rem.WorkspacePath, rem.Branch
	if err := store.EnqueueFixJob(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if claim, err := peer.ClaimNextFixJob(ctx, "worker"); err != nil || claim != nil {
		t.Fatalf("claim ignored child scan: %+v %v", claim, err)
	}
	queued.Status = models.FixJobSuperseded
	if err := store.UpdateFixJob(ctx, queued); err != nil {
		t.Fatal(err)
	}
	child.Status = models.ScanStatusCompleted
	if err := store.UpdateScan(ctx, child); err != nil {
		t.Fatal(err)
	}

	failure := errors.New("worktree is locked")
	if err := store.AcceptFixRemediation(ctx, rem, func(context.Context, *models.Remediation, *models.Repo) (string, error) { return "", failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	remove := failFixJobAcknowledgement(t, store)
	if err := store.AcceptFixRemediation(ctx, rem, cleanup); err == nil || !called {
		t.Fatal("late acknowledgement failure did not fire")
	}
	remove()
	retained, err := store.GetRemediationByID(ctx, rem.ID)
	if err != nil || retained.State != models.RemediationOpen || retained.WorkspacePath != rem.WorkspacePath {
		t.Fatalf("partial handoff committed: %+v %v", retained, err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	stale := *rem
	go func() {
		done <- store.AcceptFixRemediation(ctx, rem, func(context.Context, *models.Remediation, *models.Repo) (string, error) {
			close(entered)
			<-release
			return "accepted-commit", nil
		})
	}()
	<-entered
	created := make(chan error, 1)
	go func() { _, _, err := peer.CreateFixJob(ctx, newJob(), true); created <- err }()
	select {
	case err := <-created:
		close(release)
		<-done
		t.Fatalf("creation escaped handoff: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-created; !errors.Is(err, ErrFixFrozen) {
		t.Fatalf("creation after handoff: %v", err)
	}
	if rem.State != models.RemediationFrozen || rem.WorkspacePath != "" || rem.PublishedSHA != "accepted-commit" {
		t.Fatalf("handoff receipt: %+v", rem)
	}
	saved, err := store.GetFixJobByID(ctx, job.ID)
	if err != nil || saved.Status != models.FixJobSucceeded || saved.WorkspacePath != "" {
		t.Fatalf("paused job remained resumable: %+v %v", saved, err)
	}
	if err := peer.UpdateRemediation(ctx, &stale); !errors.Is(err, ErrFixFrozen) {
		t.Fatalf("stale update reopened handoff: %v", err)
	}
	child.ID = uuid.NewString()
	child.Status = models.ScanStatusPending
	if err := peer.CreateScan(ctx, child); !errors.Is(err, ErrFixFrozen) {
		t.Fatalf("child scan reopened handed-off workspace: %v", err)
	}
	called = false
	if err := peer.AcceptFixRemediation(ctx, &stale, cleanup); err != nil || called {
		t.Fatalf("repeat handoff repeated deletion: %v", err)
	}
}
