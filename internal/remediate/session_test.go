package remediate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

// newTestStore returns an isolated in-memory SQLite store, matching the
// pattern internal/db's own tests use.
func newTestStore(t *testing.T) db.Store {
	t.Helper()
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("db.NewSQLite: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// seedSession creates a session with both gates on and a real turn budget,
// then lets the caller adjust it before it's persisted.
func seedSession(t *testing.T, store db.Store, mutate func(*models.RemediationSession)) *models.RemediationSession {
	t.Helper()
	sess := &models.RemediationSession{
		ID:               uuid.NewString(),
		UserID:           "u-1",
		RepoID:           "r-1",
		ScanID:           "sc-1",
		Status:           models.RemediationPending,
		PlanGateEnabled:  true,
		PatchGateEnabled: true,
		MaxTurns:         20,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	if mutate != nil {
		mutate(sess)
	}
	if err := store.CreateRemediationSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateRemediationSession: %v", err)
	}
	return sess
}

func listPatches(t *testing.T, store db.Store, sessionID string) []models.RemediationPatch {
	t.Helper()
	patches, err := store.ListRemediationPatches(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ListRemediationPatches: %v", err)
	}
	return patches
}

func fixturePlan() *plan.Plan {
	return &plan.Plan{
		Summary: "1 actionable",
		Items:   []plan.Item{{FindingID: "f-1", Action: plan.ActionFix, Rationale: "sqli"}},
	}
}

// failingFindingsStore wraps a real db.Store and fails only
// ListFindingsByScan, to exercise the orchestrator's handling of a
// transient store failure mid-phase without needing a store that can be
// told to fail on demand. Embedding the db.Store interface (not a concrete
// type) promotes every other method unmodified.
type failingFindingsStore struct {
	db.Store
	err error
}

func (s *failingFindingsStore) ListFindingsByScan(ctx context.Context, scanID string) ([]models.Finding, error) {
	return nil, s.err
}

// planSucceedsExecuteExhausts is a hand-written driver.Driver double, not a
// driver.Fake, because driver.Fake replays one shared event list against the
// same per-session budget for both phases: there is no way to make a Fake's
// Plan succeed while its Execute exhausts. This double gives independent
// control so the execute-phase exhaustion/salvage path can be exercised on
// its own, matching the shape driver/exec.go actually produces: a non-nil
// *PatchSeries alongside ErrBudgetExhausted, from patches already committed
// before the run was cut off.
type planSucceedsExecuteExhausts struct {
	plan   *plan.Plan
	series *driver.PatchSeries
}

func (d *planSucceedsExecuteExhausts) Plan(_ context.Context, req driver.PlanRequest) (*plan.Plan, meter.Usage, error) {
	if req.OnEvent != nil {
		req.OnEvent(meter.Event{Type: "step_finish"})
	}
	return d.plan, meter.Usage{Turns: 1}, nil
}

func (d *planSucceedsExecuteExhausts) Execute(_ context.Context, req driver.ExecuteRequest) (*driver.PatchSeries, meter.Usage, error) {
	if req.OnEvent != nil {
		req.OnEvent(meter.Event{Type: "step_finish"})
	}
	return d.series, meter.Usage{Turns: 1}, driver.ErrBudgetExhausted
}

// With both gates off, a session runs straight through to completion.
func TestRunYoloReachesCompleted(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
	})

	// step_finish is the meter's real turn signal (see meter.go); anything
	// else counts zero turns and never exhausts a budget. Tokens/cost are
	// set so spend accumulation (usage.Tokens/usage.Cost -> TokensUsed/
	// CostUsed) has something nonzero to prove it isn't dropped.
	ev := meter.Event{Type: "step_finish"}
	ev.Part.Tokens.Total = 42
	ev.Part.Cost = 0.5
	d := driver.NewFake([]meter.Event{ev}, fixturePlan())
	// AllowYolo must be true: Run refuses a gates-off session otherwise.
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationCompleted {
		t.Fatalf("Status = %q, want %q", got.Status, models.RemediationCompleted)
	}
	if got.TurnsUsedPlan == 0 {
		t.Error("TurnsUsedPlan not recorded")
	}
	if got.StartedAt == nil {
		t.Error("StartedAt not recorded")
	}
	// The fixture event carries tokens/cost on both the plan and execute
	// replay, so both phases' spend must be accumulated, not just the last.
	if got.TokensUsed != 84 {
		t.Errorf("TokensUsed = %d, want 84 (42 from each phase)", got.TokensUsed)
	}
	if got.CostUsed != 1.0 {
		t.Errorf("CostUsed = %v, want 1.0 (0.5 from each phase)", got.CostUsed)
	}
}

// A budget-exhausted plan run marks the session exhausted and never executes.
func TestRunExhaustedStopsBeforeExecute(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
		s.MaxTurns = 1
	})

	events := []meter.Event{{Type: "step_finish"}, {Type: "step_finish"}}
	d := driver.NewFake(events, fixturePlan())
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 1, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err == nil {
		t.Fatal("Run succeeded, want budget error")
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationExhausted {
		t.Fatalf("Status = %q, want %q", got.Status, models.RemediationExhausted)
	}
	if len(listPatches(t, store, sess.ID)) != 0 {
		t.Error("patches written despite exhausted plan run")
	}
}

// A disabled runner refuses to start.
func TestRunRejectsWhenDisabled(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil)
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: false})

	if err := r.Run(context.Background(), sess.ID); err == nil {
		t.Fatal("Run succeeded with Enabled=false, want error")
	}
}

// The most security-relevant branch in this file: a session with either
// gate disabled must not run unless an admin has explicitly opted into
// AllowYolo. Unlike TestRunRejectsWhenDisabled (Enabled=false), this proves
// the yolo refusal itself, and that refusing leaves the session untouched.
func TestRunRejectsYoloWithoutAllowYolo(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
	})
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, MaxTurns: 10})

	if err := r.Run(context.Background(), sess.ID); err == nil {
		t.Fatal("Run succeeded with gates off and AllowYolo unset, want error")
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationPending {
		t.Fatalf("Status = %q, want unchanged %q (refusal must not touch the session)", got.Status, models.RemediationPending)
	}
}

// Run must not resurrect a session that has already reached a terminal (or
// gate-review) state: re-running a completed session would re-plan it,
// clear FailureReason, and (with gates off) write a second patch set.
func TestRunRejectsNonPendingSession(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
		s.Status = models.RemediationCompleted
	})
	d := driver.NewFake([]meter.Event{{Type: "step_finish"}}, fixturePlan())
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err == nil {
		t.Fatal("Run succeeded on a non-pending session, want error")
	}
	if _, err := store.GetRemediationPlan(context.Background(), sess.ID); err == nil {
		t.Error("re-running a non-pending session wrote a plan row")
	}
}

// A store failure mid-phase (here, loading findings) must mark the session
// failed rather than leave it stuck in "planning" — an orphaned row that
// looks like a hung run rather than a completed failure.
func TestRunPlanPhaseStoreFailureMarksSessionFailed(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
	})
	failing := &failingFindingsStore{Store: store, err: errors.New("boom")}
	r := NewRunner(failing, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err == nil {
		t.Fatal("Run succeeded despite a findings-load failure, want error")
	}
	got, err := store.GetRemediationSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetRemediationSession: %v", err)
	}
	if got.Status != models.RemediationFailed {
		t.Fatalf("Status = %q, want %q (not stuck in planning)", got.Status, models.RemediationFailed)
	}
}

// A budget-exhausted execute run still salvages whatever the agent already
// committed — driver/exec.go collects those real, paid-for commits on
// purpose rather than discarding them — so the session's patch rows must
// reflect that even though the session itself ends up exhausted.
func TestRunExecuteExhaustionSalvagesPatches(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
	})

	salvaged := &driver.PatchSeries{Patches: []driver.Patch{{CommitSHA: "abc123", Message: "partial fix"}}}
	d := &planSucceedsExecuteExhausts{plan: fixturePlan(), series: salvaged}
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err == nil {
		t.Fatal("Run succeeded despite execute-phase exhaustion, want error")
	}

	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationExhausted {
		t.Fatalf("Status = %q, want %q", got.Status, models.RemediationExhausted)
	}
	patches := listPatches(t, store, sess.ID)
	if len(patches) != 1 || patches[0].CommitSHA != "abc123" {
		t.Fatalf("patches = %+v, want the salvaged commit preserved despite exhaustion", patches)
	}
}

// Every observed event is persisted for SSE replay and audit, in order, as
// one session-scoped sequence spanning both phases. If the sink regressed
// to a per-call counter, execute-phase appends would violate the
// (session_id, seq) UNIQUE index, the resulting error is logged-and-
// swallowed by design (see eventSink), and a weaker "len(stored) != 0"
// assertion would still pass on the plan phase's rows alone — so the
// sequencing this task's amendment specifically mandates needs a stronger
// check than presence.
func TestRunPersistsEvents(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
	})
	events := []meter.Event{{Type: "assistant"}, {Type: "tool.start"}}
	r := NewRunner(store, driver.NewFake(events, fixturePlan()),
		Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	stored, err := store.ListRemediationEvents(context.Background(), sess.ID, 0)
	if err != nil {
		t.Fatalf("ListRemediationEvents: %v", err)
	}
	// 2 fixture events, replayed once per phase (plan, then execute).
	if len(stored) != 4 {
		t.Fatalf("len(stored) = %d, want 4 (2 events x 2 phases)", len(stored))
	}
	for i, e := range stored {
		if e.Seq != i+1 {
			t.Fatalf("stored[%d].Seq = %d, want %d — sequence is not one continuous run across phases", i, e.Seq, i+1)
		}
		if e.PayloadJSON == "" {
			t.Errorf("stored[%d].PayloadJSON is empty, want the event's redacted Part", i)
		}
	}
}

// A plan-gated session holds at plan_review and, once approved, resumes into
// the execute phase purely from the persisted session/plan rows — proving
// the "nothing held open across a gate" invariant the package doc promises.
func TestPlanGateHoldsThenResumes(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
		s.PatchGateEnabled = false
	})
	d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	held, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if held.Status != models.RemediationPlanReview {
		t.Fatalf("Status = %q, want %q", held.Status, models.RemediationPlanReview)
	}

	if err := r.ApprovePlan(context.Background(), sess.ID, "u-approver"); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	done, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if done.Status != models.RemediationCompleted {
		t.Fatalf("Status after approval = %q, want %q", done.Status, models.RemediationCompleted)
	}
}

// Rejecting a held plan terminates the session without ever reaching the
// execute phase.
func TestRejectPlanTerminatesSession(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
	})
	r := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		Config{Enabled: true, MaxTurns: 10, AllowYolo: true})
	_ = r.Run(context.Background(), sess.ID)

	if err := r.RejectPlan(context.Background(), sess.ID, "u-approver", "wrong approach"); err != nil {
		t.Fatalf("RejectPlan: %v", err)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationRejected {
		t.Errorf("Status = %q, want %q", got.Status, models.RemediationRejected)
	}
}

// ApprovePlan is the sanctioned way to advance a plan_review session; it must
// refuse a session that never reached that state (here, still pending)
// rather than resurrecting it the way Run's own precondition already
// forbids for a different reason.
func TestApprovePlanRejectsWrongState(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil) // still pending
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	if err := r.ApprovePlan(context.Background(), sess.ID, "u-1"); err == nil {
		t.Fatal("ApprovePlan on a pending session succeeded, want error")
	}
}

// RejectPlan must also refuse a session outside plan_review.
func TestRejectPlanRejectsWrongState(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil) // still pending
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	if err := r.RejectPlan(context.Background(), sess.ID, "u-1", "no"); err == nil {
		t.Fatal("RejectPlan on a pending session succeeded, want error")
	}
}

// A patch-gated session holds at patch_review and, once approved, resumes
// into the landing phase — a stub that transitions straight to completed
// until Task 13 replaces it with apply/rescan/PR.
func TestPatchGateHoldsThenResumes(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = true
	})
	d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	held, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if held.Status != models.RemediationPatchReview {
		t.Fatalf("Status = %q, want %q", held.Status, models.RemediationPatchReview)
	}

	if err := r.ApprovePatches(context.Background(), sess.ID, "u-approver"); err != nil {
		t.Fatalf("ApprovePatches: %v", err)
	}
	done, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if done.Status != models.RemediationCompleted {
		t.Fatalf("Status after approval = %q, want %q", done.Status, models.RemediationCompleted)
	}
}

// Rejecting held patches terminates the session; the patch rows already
// written to remediation_patches are left in place as an audit trail rather
// than deleted.
func TestRejectPatchesTerminatesSession(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = true
	})
	d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
	// NewFake defaults Series to an empty PatchSeries; give it a real patch so
	// savePatches (which no-ops on an empty slice) actually writes a row for
	// this test to prove is preserved.
	d.Series = &driver.PatchSeries{Patches: []driver.Patch{{CommitSHA: "c1", Message: "fix"}}}
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})
	_ = r.Run(context.Background(), sess.ID)

	if err := r.RejectPatches(context.Background(), sess.ID, "u-approver", "regressed tests"); err != nil {
		t.Fatalf("RejectPatches: %v", err)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationRejected {
		t.Errorf("Status = %q, want %q", got.Status, models.RemediationRejected)
	}
	if len(listPatches(t, store, sess.ID)) == 0 {
		t.Error("rejection deleted the patch audit trail, want it preserved")
	}
}

// ApprovePatches/RejectPatches are the sanctioned way to advance a
// patch_review session; both must refuse any other state.
func TestApprovePatchesRejectsWrongState(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil) // still pending
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	if err := r.ApprovePatches(context.Background(), sess.ID, "u-1"); err == nil {
		t.Fatal("ApprovePatches on a pending session succeeded, want error")
	}
}

func TestRejectPatchesRejectsWrongState(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil) // still pending
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	if err := r.RejectPatches(context.Background(), sess.ID, "u-1", "no"); err == nil {
		t.Fatal("RejectPatches on a pending session succeeded, want error")
	}
}
