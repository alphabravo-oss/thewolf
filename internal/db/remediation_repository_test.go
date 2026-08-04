package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

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

// TestTransitionRemediationSessionCAS is the direct regression guard for the
// TOCTOU gap a plain UPDATE left open: two callers who both read a session
// as (say) plan_review must not both be able to advance it. A matching
// fromStatus succeeds and writes every column UpdateRemediationSession does;
// a stale fromStatus (the row already moved on) fails with sql.ErrNoRows
// rather than silently overwriting whatever the first write committed.
func TestTransitionRemediationSessionCAS(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	s := &models.RemediationSession{
		ID: "rs-cas-1", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPlanReview, MaxTurns: 20,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, s); err != nil {
		t.Fatalf("CreateRemediationSession: %v", err)
	}

	s.Status = models.RemediationExecuting
	s.TurnsUsedPlan = 3
	if err := store.TransitionRemediationSession(ctx, s, models.RemediationPlanReview); err != nil {
		t.Fatalf("TransitionRemediationSession (matching fromStatus): %v", err)
	}
	got, err := store.GetRemediationSession(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetRemediationSession: %v", err)
	}
	if got.Status != models.RemediationExecuting || got.TurnsUsedPlan != 3 {
		t.Fatalf("got = %+v, want status=executing, turns_used_plan=3", got)
	}

	// The row is now "executing", not "plan_review" — a second caller still
	// holding the stale "plan_review" read must be refused, not silently
	// allowed to clobber the write above.
	stale := &models.RemediationSession{ID: s.ID, Status: models.RemediationFailed, MaxTurns: 20}
	err = store.TransitionRemediationSession(ctx, stale, models.RemediationPlanReview)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("TransitionRemediationSession (stale fromStatus) = %v, want sql.ErrNoRows", err)
	}
	// And the row must be exactly as the FIRST write left it — untouched by
	// the second, refused attempt.
	after, _ := store.GetRemediationSession(ctx, s.ID)
	if after.Status != models.RemediationExecuting {
		t.Fatalf("Status = %q after a refused CAS, want unchanged %q", after.Status, models.RemediationExecuting)
	}
}

// TestApproveRemediationPatches proves the write ApprovePatches/RejectPatches
// otherwise silently discard: approved_by/approved_at land on every patch
// row for the session, not just the first or last.
func TestApproveRemediationPatches(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seed := &models.RemediationSession{
		ID: "rs-patches-1", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPatchReview, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	patches := []models.RemediationPatch{
		{CommitSHA: "c1", Message: "fix 1", CreatedAt: time.Now()},
		{CommitSHA: "c2", Message: "fix 2", CreatedAt: time.Now()},
	}
	if err := store.SaveRemediationPatches(ctx, seed.ID, patches); err != nil {
		t.Fatalf("SaveRemediationPatches: %v", err)
	}

	if err := store.ApproveRemediationPatches(ctx, seed.ID, "u-approver"); err != nil {
		t.Fatalf("ApproveRemediationPatches: %v", err)
	}

	got, err := store.ListRemediationPatches(ctx, seed.ID)
	if err != nil {
		t.Fatalf("ListRemediationPatches: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	for i, p := range got {
		if p.ApprovedBy != "u-approver" {
			t.Errorf("got[%d].ApprovedBy = %q, want %q", i, p.ApprovedBy, "u-approver")
		}
		if p.ApprovedAt == nil {
			t.Errorf("got[%d].ApprovedAt not recorded", i)
		}
	}
}

// A session that reached patch_review with an empty patch set (the agent
// found nothing to commit) must still be approvable/rejectable — zero rows
// affected is a valid outcome here, not an error.
func TestApproveRemediationPatchesNoPatchesIsNotAnError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seed := &models.RemediationSession{
		ID: "rs-patches-2", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPatchReview, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := store.ApproveRemediationPatches(ctx, seed.ID, "u-approver"); err != nil {
		t.Fatalf("ApproveRemediationPatches on a patchless session: %v", err)
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

// preCloneRootMigration051 is migration 051 as of commit 5afc244 — the
// state of every database that ran this server BEFORE clone_root was added.
// A literal copy, not derived from the current file: deriving it would make
// this test track future edits instead of pinning the historical shape it
// exists to reproduce.
const preCloneRootMigration051 = `
CREATE TABLE IF NOT EXISTS remediation_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    scan_id TEXT NOT NULL,
    loop_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    plan_gate_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    patch_gate_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    max_turns INTEGER NOT NULL DEFAULT 20,
    turns_used_plan INTEGER NOT NULL DEFAULT 0,
    turns_used_execute INTEGER NOT NULL DEFAULT 0,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    cost_used REAL NOT NULL DEFAULT 0,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    pr_url TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP,
    completed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_remediation_sessions_user ON remediation_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_remediation_sessions_scan ON remediation_sessions(scan_id);
CREATE INDEX IF NOT EXISTS idx_remediation_sessions_status ON remediation_sessions(status);

CREATE TABLE IF NOT EXISTS remediation_plans (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    plan_json TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    approved_by TEXT NOT NULL DEFAULT '',
    approved_at TIMESTAMP,
    rejected_reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_remediation_plans_session ON remediation_plans(session_id);

CREATE TABLE IF NOT EXISTS remediation_patches (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    files_changed TEXT NOT NULL DEFAULT '',
    finding_ids TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    approved_by TEXT NOT NULL DEFAULT '',
    approved_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_remediation_patches_session ON remediation_patches(session_id);

CREATE TABLE IF NOT EXISTS remediation_events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    type TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_remediation_events_session_seq ON remediation_events(session_id, seq);
`

// TestMigration051AddsCloneRootToAnAlreadyMigratedDatabase reproduces the
// exact defect the reviewer flagged: execAdditiveMigration has no migration
// ledger, so it re-executes every statement on every startup rather than
// tracking which version last ran. On a database that already has
// remediation_sessions, CREATE TABLE IF NOT EXISTS is a silent NO-OP — not
// an error the swallow-list can recognize — so simply adding clone_root
// inside that CREATE TABLE would never reach a database that ran an earlier
// version of this migration. This builds exactly that database (via the
// pinned pre-fix SQL above, not the current file, so this test doesn't
// silently stop testing anything the next time 051 changes), then runs the
// CURRENT migration051SQL against it through the real execAdditiveMigration
// path Migrate() itself uses, and asserts clone_root actually appears.
func TestMigration051AddsCloneRootToAnAlreadyMigratedDatabase(t *testing.T) {
	db, err := sqlx.Open("sqlite3", sqliteConnectionDSN(":memory:"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1) // :memory: only persists for one connection
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(preCloneRootMigration051); err != nil {
		t.Fatalf("apply pre-clone_root migration 051: %v", err)
	}

	hasColumn := func() bool {
		t.Helper()
		var n int
		if err := db.Get(&n,
			"SELECT COUNT(*) FROM pragma_table_info('remediation_sessions') WHERE name = 'clone_root'"); err != nil {
			t.Fatalf("check clone_root column: %v", err)
		}
		return n == 1
	}
	if hasColumn() {
		t.Fatal("test setup invalid: clone_root already present before applying the current migration")
	}

	// The real path Migrate() uses for this migration.
	if err := execAdditiveMigration(db, migration051SQL); err != nil {
		t.Fatalf("apply current migration 051 to an already-migrated database: %v", err)
	}

	if !hasColumn() {
		t.Fatal("clone_root is still missing after running the current migration against a database that already had remediation_sessions — CREATE TABLE IF NOT EXISTS alone cannot reach it")
	}
}
