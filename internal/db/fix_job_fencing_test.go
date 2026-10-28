package db

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestFixJobFencesStaleTransitionsAndHeartbeats(t *testing.T) {
	var store, peer Store
	store = newClusterPostgresStore(t)
	peer = store
	ctx := t.Context()
	job := &models.FixJob{ID: "fenced", RepoID: "repo", UserID: "user"}
	if err := store.EnqueueFixJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	original, err := store.ClaimNextFixJob(ctx, "worker")
	if err != nil || original == nil {
		t.Fatalf("claim: %+v %v", original, err)
	}
	original.Status = models.FixJobRunning
	original.Summary = "live progress"
	if err := store.UpdateFixJob(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err := peer.HeartbeatFixJob(ctx, original.ID, original.ClaimedBy, *original.ClaimedAt); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetFixJobByID(ctx, job.ID)
	if err != nil || current.Summary != "live progress" || current.Revision != original.Revision || current.Status != models.FixJobRunning {
		t.Fatalf("heartbeat rewrote progress: %+v %v", current, err)
	}
	// Reclaim and reuse even the same worker label. The old generation cannot
	// renew, publish, or overwrite the replacement claim.
	if n, err := peer.ReclaimStaleJobs(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("reclaim: %d %v", n, err)
	}
	replacement, err := peer.ClaimNextFixJob(ctx, "worker")
	if err != nil || replacement == nil {
		t.Fatalf("replacement: %+v %v", replacement, err)
	}
	if err := store.HeartbeatFixJob(ctx, original.ID, original.ClaimedBy, *original.ClaimedAt); !errors.Is(err, ErrFixJobConflict) {
		t.Fatalf("stale heartbeat: %v", err)
	}
	original.Status = models.FixJobSucceeded
	if err := store.UpdateFixJob(ctx, original); !errors.Is(err, ErrFixJobConflict) {
		t.Fatalf("stale result: %v", err)
	}
	if err := peer.HeartbeatFixJob(ctx, replacement.ID, replacement.ClaimedBy, *replacement.ClaimedAt); err != nil {
		t.Fatal(err)
	}

	// Two writers holding the same revision can produce only one transition.
	left, right := *replacement, *replacement
	left.Status, right.Status = models.FixJobAwaitingPush, models.FixJobCancelled
	outcomes := make([]error, 2)
	var group sync.WaitGroup
	group.Go(func() { outcomes[0] = store.UpdateFixJob(ctx, &left) })
	group.Go(func() { outcomes[1] = peer.UpdateFixJob(ctx, &right) })
	group.Wait()
	wins, conflicts := 0, 0
	for _, err := range outcomes {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrFixJobConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("transition outcomes: %v", outcomes)
	}
	current, err = store.GetFixJobByID(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != models.FixJobCancelled {
		current.Status = models.FixJobCancelled
		if err := store.UpdateFixJob(ctx, current); err != nil {
			t.Fatal(err)
		}
	}
	current.Status = models.FixJobQueued
	if err := store.UpdateFixJob(ctx, current); !errors.Is(err, ErrFixJobConflict) {
		t.Fatalf("cancel resurrection: %v", err)
	}

	// Concurrent claimers may run separate branches, but never share one.
	for _, item := range []struct{ id, branch string }{{"shared-a", "shared"}, {"shared-b", "shared"}, {"separate", "separate"}} {
		if err := store.EnqueueFixJob(ctx, &models.FixJob{ID: item.id, RepoID: "repo", TargetBranch: item.branch}); err != nil {
			t.Fatal(err)
		}
	}
	claimed := make([]*models.FixJob, 8)
	claimErrors := make([]error, 8)
	writers := []Store{store, peer}
	for i := range claimed {
		group.Go(func() { claimed[i], claimErrors[i] = writers[i%2].ClaimNextFixJob(ctx, "parallel") })
	}
	group.Wait()
	branches := map[string]*models.FixJob{}
	for i, j := range claimed {
		if claimErrors[i] != nil {
			t.Fatal(claimErrors[i])
		}
		if j == nil {
			continue
		}
		if branches[j.TargetBranch] != nil {
			t.Fatalf("two claims on %s", j.TargetBranch)
		}
		branches[j.TargetBranch] = j
	}
	if len(branches) != 2 {
		t.Fatalf("independent branches blocked: %+v", branches)
	}
	paused := branches["shared"]
	paused.Status = models.FixJobAwaitingPush
	if err := store.UpdateFixJob(ctx, paused); err != nil {
		t.Fatal(err)
	}
	next, err := peer.ClaimNextFixJob(ctx, "next")
	if err != nil || next == nil || next.TargetBranch != "shared" {
		t.Fatalf("branch not released: %+v %v", next, err)
	}
	paused.Status = models.FixJobQueued
	if err := store.UpdateFixJob(ctx, paused); !errors.Is(err, ErrFixBusy) {
		t.Fatalf("resumed busy branch: %v", err)
	}

	// Resume and creation share the repository lock: exactly one can take
	// over a paused remediation, including across SQLite connections.
	user, repo, scan := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "unused"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "resume"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateScan(ctx, &models.Scan{ID: scan, RepoID: repo, UserID: user, Status: models.ScanStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	newJob := func() *models.FixJob {
		return &models.FixJob{ID: uuid.NewString(), UserID: user, RepoID: repo, ScanID: scan}
	}
	review, _, err := store.CreateFixJob(ctx, newJob(), true)
	if err != nil {
		t.Fatal(err)
	}
	review.Status = models.FixJobAwaitingPush
	if err := store.UpdateFixJob(ctx, review); err != nil {
		t.Fatal(err)
	}
	review.Status = models.FixJobQueued
	group.Go(func() { outcomes[0] = store.UpdateFixJob(ctx, review) })
	group.Go(func() { _, _, outcomes[1] = peer.CreateFixJob(ctx, newJob(), true) })
	group.Wait()
	wins = 0
	for _, err := range outcomes {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrFixJobConflict) && !errors.Is(err, ErrFixBusy) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("creation/resume outcomes: %v", outcomes)
	}

	// Freshly read terminal rows are also immutable across status changes.
	for _, status := range []string{models.FixJobSuperseded, models.FixJobSucceeded, models.FixJobFailed} {
		terminal := &models.FixJob{ID: status, RepoID: "repo", Status: status}
		if err := store.EnqueueFixJob(ctx, terminal); err != nil {
			t.Fatal(err)
		}
		terminal.Status = models.FixJobQueued
		if err := store.UpdateFixJob(ctx, terminal); !errors.Is(err, ErrFixJobConflict) {
			t.Fatalf("%s resurrection: %v", status, err)
		}
	}
}
