package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

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
	// Postgres columns are BOOLEAN, not INTEGER: lib/pq encodes a Go bool
	// as the literal "true"/"false" regardless of column OID, and an
	// INTEGER column rejects that text. This is the field pair that
	// silently broke every session write on Postgres.
	if !got.PlanGateEnabled || !got.PatchGateEnabled {
		t.Errorf("gate booleans did not round trip: %+v", got)
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
	if !again.PlanGateEnabled || !again.PatchGateEnabled {
		t.Errorf("gate booleans did not survive update: %+v", again)
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
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	for i, e := range events {
		if e.Seq != i+1 {
			t.Fatalf("events[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}

	// afterSeq is an exclusive lower bound (seq > afterSeq), not inclusive —
	// no event has Seq == 0 above, so that call alone can't tell ">" from
	// ">=". Assert the boundary directly against a seq that does exist.
	after2, err := store.ListRemediationEvents(ctx, "rs-2", 2)
	if err != nil {
		t.Fatalf("ListRemediationEvents(afterSeq=2): %v", err)
	}
	if len(after2) != 1 || after2[0].Seq != 3 {
		t.Fatalf("ListRemediationEvents(afterSeq=2) = %+v, want exactly one event with Seq=3", after2)
	}
}

// seq is the only ordering key SSE replay has, so a session must never hold
// two events sharing one. The unique index is what turns an emitter that
// restarts its sequence per phase — plan and execute both starting at 1 —
// into a visible write failure instead of a replay that silently reorders or
// drops a phase.
func TestRemediationEventSeqIsUniquePerSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seed := &models.RemediationSession{
		ID: "rs-3", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first := &models.RemediationEvent{
		ID: "rs-3-1", SessionID: "rs-3", Seq: 1, Type: "assistant", CreatedAt: time.Now(),
	}
	if err := store.AppendRemediationEvent(ctx, first); err != nil {
		t.Fatalf("AppendRemediationEvent: %v", err)
	}
	// Distinct ID, same (session, seq): the primary key alone would accept
	// this. Only the unique index refuses it.
	clash := &models.RemediationEvent{
		ID: "rs-3-execute-1", SessionID: "rs-3", Seq: 1, Type: "assistant", CreatedAt: time.Now(),
	}
	if err := store.AppendRemediationEvent(ctx, clash); err == nil {
		t.Fatal("duplicate (session_id, seq) was accepted — SSE replay ordering is unguarded")
	}
	// A different session reusing seq 1 is legitimate; the constraint is
	// scoped per session, not global.
	other := &models.RemediationSession{
		ID: "rs-4", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, other); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	if err := store.AppendRemediationEvent(ctx, &models.RemediationEvent{
		ID: "rs-4-1", SessionID: "rs-4", Seq: 1, Type: "assistant", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seq 1 in a second session was rejected: %v", err)
	}
}

// TestRejectRemediationPlan is the regression guard for the gap Task 5 left
// open: remediation_plans.rejected_reason had no writer anywhere in the
// system until this method existed. Approved_by/approved_at are written too
// (recording who acted and when, not that they approved) so the row carries
// a complete record of the decision, matching ApproveRemediationPlan's shape.
func TestRejectRemediationPlan(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seed := &models.RemediationSession{
		ID: "rs-reject-1", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPlanReview, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.SaveRemediationPlan(ctx, &models.RemediationPlan{
		SessionID: seed.ID, PlanJSON: `{"summary":"x"}`, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveRemediationPlan: %v", err)
	}

	if err := store.RejectRemediationPlan(ctx, seed.ID, "u-approver", "wrong approach"); err != nil {
		t.Fatalf("RejectRemediationPlan: %v", err)
	}

	got, err := store.GetRemediationPlan(ctx, seed.ID)
	if err != nil {
		t.Fatalf("GetRemediationPlan: %v", err)
	}
	if got.RejectedReason != "wrong approach" {
		t.Errorf("RejectedReason = %q, want %q", got.RejectedReason, "wrong approach")
	}
	if got.ApprovedBy != "u-approver" {
		t.Errorf("ApprovedBy = %q, want %q", got.ApprovedBy, "u-approver")
	}
	if got.ApprovedAt == nil {
		t.Error("ApprovedAt not recorded")
	}
}

// RejectRemediationPlan targets the latest plan row for a session, matching
// ApproveRemediationPlan/GetRemediationPlan; a session with no plan row at
// all must fail rather than silently succeed.
func TestRejectRemediationPlanNoRows(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seed := &models.RemediationSession{
		ID: "rs-reject-2", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPlanReview, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := store.RejectRemediationPlan(ctx, seed.ID, "u-approver", "no plan yet"); err == nil {
		t.Fatal("RejectRemediationPlan succeeded with no plan row, want error")
	}
}

// TestRemediationSessionBooleansRoundTripPostgres is the direct regression
// guard for the plan_gate_enabled/patch_gate_enabled INTEGER-vs-BOOLEAN
// defect: lib/pq encodes a Go bool as the literal "true"/"false" regardless
// of the target column's OID, so an INTEGER column rejects the write outright
// on Postgres while SQLite (bool -> 1/0) stays silently green. Follows the
// isolated-schema convention in scan_release_recovery_test.go /
// scanner_release_backup_repository_test.go; skips cleanly without
// WOLF_TEST_POSTGRES_DSN so CI without Postgres stays green.
func TestRemediationSessionBooleansRoundTripPostgres(t *testing.T) {
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "remediation_" + uuid.NewString()
	if _, err := admin.db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
		_ = admin.Close()
	})
	store, err := NewPostgres(postgresDSNWithSearchPath(t, dsn, schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	// One gate true, the other false: proves both directions round trip
	// rather than the column merely defaulting to true.
	s := &models.RemediationSession{
		ID: "rs-pg-1", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPending, MaxTurns: 20,
		PlanGateEnabled: true, PatchGateEnabled: false,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, s); err != nil {
		t.Fatalf("CreateRemediationSession: %v", err)
	}
	got, err := store.GetRemediationSession(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetRemediationSession: %v", err)
	}
	if !got.PlanGateEnabled || got.PatchGateEnabled {
		t.Fatalf("gate booleans mismatch on Postgres: %+v", got)
	}
}
