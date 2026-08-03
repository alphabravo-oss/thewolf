package db

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestRemediationSessionRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	s := &models.RemediationSession{
		ID: "rs-1", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPending, MaxTurns: 20,
		PlanGateEnabled: true, PatchGateEnabled: true,
		Provider: "grok", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, s); err != nil {
		t.Fatalf("CreateRemediationSession: %v", err)
	}

	got, err := store.GetRemediationSession(ctx, "rs-1")
	if err != nil {
		t.Fatalf("GetRemediationSession: %v", err)
	}
	if got.Status != models.RemediationPending || got.MaxTurns != 20 {
		t.Errorf("round trip mismatch: %+v", got)
	}

	got.Status = models.RemediationPlanning
	got.TurnsUsedPlan = 5
	if err := store.UpdateRemediationSession(ctx, got); err != nil {
		t.Fatalf("UpdateRemediationSession: %v", err)
	}
	again, _ := store.GetRemediationSession(ctx, "rs-1")
	if again.Status != models.RemediationPlanning || again.TurnsUsedPlan != 5 {
		t.Errorf("update not persisted: %+v", again)
	}
}

func TestRemediationEventsOrderedBySeq(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seed := &models.RemediationSession{
		ID: "rs-2", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 3; i >= 1; i-- {
		e := &models.RemediationEvent{
			ID: string(rune('a'+i)) + "-ev", SessionID: "rs-2", Seq: i,
			Type: "assistant", CreatedAt: time.Now(),
		}
		if err := store.AppendRemediationEvent(ctx, e); err != nil {
			t.Fatalf("AppendRemediationEvent: %v", err)
		}
	}

	events, err := store.ListRemediationEvents(ctx, "rs-2", 0)
	if err != nil {
		t.Fatalf("ListRemediationEvents: %v", err)
	}
	for i, e := range events {
		if e.Seq != i+1 {
			t.Fatalf("events[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}
}
