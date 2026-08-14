package db

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestAutofixFlagDefaultsOff(t *testing.T) {
	store := newTestStore(t)
	v, err := store.GetSetting(context.Background(), "autofix_enabled")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if v != "false" {
		t.Errorf("autofix_enabled should default to false, got %q", v)
	}
}

func TestFixJobEnqueueGetList(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	job := &models.FixJob{
		ID:            "job-1",
		UserID:        "u1",
		Type:          "fix",
		RepoID:        "r1",
		FindingIDList: []string{"f1", "f2"},
		Engine:        "auto",
		Mode:          models.FixModeDryRun,
		MaxAttempts:   2,
	}
	if err := store.EnqueueFixJob(ctx, job); err != nil {
		t.Fatalf("EnqueueFixJob: %v", err)
	}

	got, err := store.GetFixJobByID(ctx, "job-1")
	if err != nil {
		t.Fatalf("GetFixJobByID: %v", err)
	}
	if got.Status != models.FixJobQueued {
		t.Errorf("new job should be queued, got %q", got.Status)
	}
	if len(got.FindingIDList) != 2 || got.FindingIDList[0] != "f1" {
		t.Errorf("finding ids did not round-trip: %v", got.FindingIDList)
	}

	list, err := store.ListFixJobs(ctx, "r1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListFixJobs(r1): %d jobs, err %v", len(list), err)
	}
	if other, _ := store.ListFixJobs(ctx, "r2"); len(other) != 0 {
		t.Errorf("ListFixJobs(r2) should be empty, got %d", len(other))
	}
}

func TestUpdateFixJobDoesNotResurrectCancelled(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	job := &models.FixJob{ID: "job-cancel", UserID: "u1", RepoID: "r1", Engine: "auto"}
	if err := store.EnqueueFixJob(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	got, _ := store.GetFixJobByID(ctx, "job-cancel")
	got.Status = models.FixJobCancelled
	if err := store.UpdateFixJob(ctx, got); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got.Status = models.FixJobAwaitingPush
	got.PauseReason = "ready to push"
	got.ResultBranch = "wolf-fix/job-cancel"
	if err := store.UpdateFixJob(ctx, got); err != nil {
		t.Fatalf("stale write: %v", err)
	}
	back, _ := store.GetFixJobByID(ctx, "job-cancel")
	if back.Status != models.FixJobCancelled {
		t.Fatalf("cancelled job was overwritten to %s", back.Status)
	}
}

func TestClaimNextFixJobIsAtomicAndOrdered(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// Enqueue two jobs; oldest should be claimed first.
	for i, id := range []string{"job-a", "job-b"} {
		j := &models.FixJob{ID: id, UserID: "u1", RepoID: "r1", Engine: "auto", MaxAttempts: 1,
			CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second)}
		if err := store.EnqueueFixJob(ctx, j); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}

	first, err := store.ClaimNextFixJob(ctx, "worker-1")
	if err != nil || first == nil {
		t.Fatalf("first claim: %v (job %v)", err, first)
	}
	if first.ID != "job-a" {
		t.Errorf("expected oldest (job-a) claimed first, got %s", first.ID)
	}
	if first.Status != models.FixJobClaimed || first.ClaimedBy != "worker-1" {
		t.Errorf("claimed job not marked: status=%s by=%s", first.Status, first.ClaimedBy)
	}

	// A different worker must get the OTHER job, never the same one.
	second, err := store.ClaimNextFixJob(ctx, "worker-2")
	if err != nil || second == nil {
		t.Fatalf("second claim: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("two workers double-claimed job %s", first.ID)
	}

	// Queue now empty → (nil, nil).
	third, err := store.ClaimNextFixJob(ctx, "worker-3")
	if err != nil {
		t.Fatalf("third claim errored on empty queue: %v", err)
	}
	if third != nil {
		t.Errorf("empty queue should return nil, got %s", third.ID)
	}
}

func TestReclaimStaleJobs(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	j := &models.FixJob{ID: "stale", UserID: "u1", RepoID: "r1", Engine: "auto"}
	if err := store.EnqueueFixJob(ctx, j); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, _ := store.ClaimNextFixJob(ctx, "dead-worker")
	if claimed == nil {
		t.Fatal("expected to claim the job")
	}
	// Backdate the heartbeat so it looks stale.
	old := time.Now().UTC().Add(-10 * time.Minute)
	claimed.HeartbeatAt = &old
	if err := store.UpdateFixJob(ctx, claimed); err != nil {
		t.Fatalf("update: %v", err)
	}

	n, err := store.ReclaimStaleJobs(ctx, time.Now().UTC().Add(-5*time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("ReclaimStaleJobs: reclaimed %d, err %v", n, err)
	}
	back, _ := store.GetFixJobByID(ctx, "stale")
	if back.Status != models.FixJobQueued {
		t.Errorf("stale job should be requeued, got %s", back.Status)
	}
}

func TestFixAttemptRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	job := &models.FixJob{ID: "j", UserID: "u1", RepoID: "r1", Engine: "auto"}
	_ = store.EnqueueFixJob(ctx, job)

	att := &models.FixAttempt{
		ID: "a1", JobID: "j", FindingID: "f1", AttemptNo: 1,
		EngineUsed: "cli:claude-code", Built: true, FindingCleared: true,
		Outcome: models.FixOutcomeKept, FilesChanged: `["app/x.go"]`,
	}
	if err := store.CreateFixAttempt(ctx, att); err != nil {
		t.Fatalf("CreateFixAttempt: %v", err)
	}
	list, err := store.ListFixAttempts(ctx, "j")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListFixAttempts: %d, err %v", len(list), err)
	}
	if list[0].Outcome != models.FixOutcomeKept || !list[0].Built {
		t.Errorf("attempt did not round-trip: %+v", list[0])
	}
}
