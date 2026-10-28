package db

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestFixAttemptFinalizationIsAtomic(t *testing.T) {
	var store Store = newClusterPostgresStore(t)
	ctx := t.Context()
	job := &models.FixJob{ID: "journal-job", UserID: "owner", RepoID: "repo", Engine: "auto"}
	if err := store.EnqueueFixJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	att := models.FixAttempt{ID: "journal-attempt", JobID: job.ID, FindingID: "finding", AttemptNo: 3,
		EngineUsed: "cli:test", Outcome: models.FixOutcomeRunning, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := store.CreateFixAttempt(ctx, &att); err != nil {
		t.Fatal(err)
	}
	finished := att
	finished.Outcome, finished.Built, finished.FindingCleared = models.FixOutcomeKept, true, true
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.UpdateFixAttempt(cancelled, &finished); err == nil {
		t.Fatal("cancelled finalization succeeded")
	}
	wrong := finished
	wrong.AttemptNo++
	if err := store.UpdateFixAttempt(ctx, &wrong); err == nil {
		t.Fatal("mismatched attempt identity updated")
	}
	var group sync.WaitGroup
	failures := make([]error, 2)
	for i := range failures {
		group.Go(func() {
			candidate := finished
			if i == 1 {
				candidate.Outcome, candidate.Built, candidate.FindingCleared = models.FixOutcomeInterrupted, false, false
			}
			failures[i] = store.UpdateFixAttempt(ctx, &candidate)
		})
	}
	group.Wait()
	if (failures[0] == nil) == (failures[1] == nil) {
		t.Fatalf("expected exactly one finalization: %v", failures)
	}
	history, err := store.ListFixAttempts(ctx, job.ID)
	if err != nil || len(history) != 1 || history[0].ID != att.ID || history[0].AttemptNo != 3 || !history[0].CreatedAt.Equal(att.CreatedAt) {
		t.Fatalf("identity or history changed: %+v %v", history, err)
	}
	if history[0].Outcome == models.FixOutcomeKept && (!history[0].Built || !history[0].FindingCleared) {
		t.Fatal("verification booleans lost during finalization")
	}
	if err := store.UpdateFixAttempt(ctx, &finished); err == nil {
		t.Fatal("stale writer replaced a final verdict")
	}
}
