package remediate

import (
	"context"
	"errors"
	"sync"
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

// failingRejectPlanStore wraps a real db.Store and fails only
// RejectRemediationPlan, to prove RejectPlan's session-first ordering: the
// session must still terminate even when the best-effort plan-row write
// fails.
type failingRejectPlanStore struct {
	db.Store
	err error
}

func (s *failingRejectPlanStore) RejectRemediationPlan(ctx context.Context, sessionID, approverID, reason string) error {
	return s.err
}

// raceGateStore wraps a real db.Store and, on its FIRST call to
// ApproveRemediationPlan, blocks until proceed is closed — right after the
// underlying write lands, which is the exact gap in production between
// ApprovePlan's read of the session and runExecutePhase's own
// compare-and-swap claim into "executing". entered is closed the moment the
// caller is parked there, so a test can deterministically drive a second,
// concurrent caller through that window instead of relying on goroutine
// scheduling.
type raceGateStore struct {
	db.Store
	once    sync.Once
	proceed chan struct{}
	entered chan struct{}
}

func newRaceGateStore(store db.Store) *raceGateStore {
	return &raceGateStore{Store: store, proceed: make(chan struct{}), entered: make(chan struct{})}
}

func (s *raceGateStore) ApproveRemediationPlan(ctx context.Context, sessionID, approverID string) error {
	if err := s.Store.ApproveRemediationPlan(ctx, sessionID, approverID); err != nil {
		return err
	}
	s.once.Do(func() {
		close(s.entered)
		<-s.proceed
	})
	return nil
}

// deadlineCapturingDriver wraps a driver.Fake and records whether the
// context passed to Execute carries a deadline, to prove driverPreflight's
// SessionTimeout bound actually reaches the driver call on the resume path,
// not just Run's own first phase.
type deadlineCapturingDriver struct {
	*driver.Fake
	sawDeadline bool
}

func (d *deadlineCapturingDriver) Execute(ctx context.Context, req driver.ExecuteRequest) (*driver.PatchSeries, meter.Usage, error) {
	_, d.sawDeadline = ctx.Deadline()
	return d.Fake.Execute(ctx, req)
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
// the execute phase purely from the persisted session/plan rows.
//
// The approval step uses a SECOND, freshly constructed Runner sharing only
// the store — not the Runner that ran the plan phase — so this genuinely
// pins the "nothing held open across a gate, survives a restart" invariant
// the package doc promises, rather than merely holding by accident of
// reusing one Go value.
func TestPlanGateHoldsThenResumes(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
		s.PatchGateEnabled = false
	})
	cfg := Config{Enabled: true, MaxTurns: 10, AllowYolo: true}
	planRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()), cfg)

	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	held, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if held.Status != models.RemediationPlanReview {
		t.Fatalf("Status = %q, want %q", held.Status, models.RemediationPlanReview)
	}

	resumeRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()), cfg)
	if err := resumeRunner.ApprovePlan(context.Background(), sess.ID, "u-approver"); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	done, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if done.Status != models.RemediationCompleted {
		t.Fatalf("Status after approval = %q, want %q", done.Status, models.RemediationCompleted)
	}
}

// Rejecting a held plan terminates the session without ever reaching the
// execute phase. Uses a fresh Runner for the same reason as the approve case
// above.
func TestRejectPlanTerminatesSession(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
	})
	cfg := Config{Enabled: true, MaxTurns: 10, AllowYolo: true}
	planRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()), cfg)
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	resumeRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()), cfg)
	if err := resumeRunner.RejectPlan(context.Background(), sess.ID, "u-approver", "wrong approach"); err != nil {
		t.Fatalf("RejectPlan: %v", err)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationRejected {
		t.Errorf("Status = %q, want %q", got.Status, models.RemediationRejected)
	}
	plan, err := store.GetRemediationPlan(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetRemediationPlan: %v", err)
	}
	if plan.RejectedReason != "wrong approach" {
		t.Errorf("plan.RejectedReason = %q, want %q", plan.RejectedReason, "wrong approach")
	}
	if plan.ApprovedBy != "u-approver" {
		t.Errorf("plan.ApprovedBy = %q, want %q (records the actor, not the verdict)", plan.ApprovedBy, "u-approver")
	}
}

// A RejectRemediationPlan failure (e.g. a DB blip writing the plan row) must
// not defeat a human's decision to terminate the session: the session still
// ends up rejected, even though the plan row's rejected_reason never gets
// written. This is the regression guard for the reordering in RejectPlan —
// terminate first, log the bookkeeping write's own failure rather than
// escalating it.
func TestRejectPlanTerminatesSessionEvenIfPlanRowWriteFails(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
	})
	cfg := Config{Enabled: true, MaxTurns: 10, AllowYolo: true}
	planRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()), cfg)
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	failing := &failingRejectPlanStore{Store: store, err: errors.New("boom")}
	resumeRunner := NewRunner(failing, driver.NewFake(nil, fixturePlan()), cfg)
	if err := resumeRunner.RejectPlan(context.Background(), sess.ID, "u-approver", "wrong approach"); err != nil {
		t.Fatalf("RejectPlan: %v", err)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationRejected {
		t.Fatalf("Status = %q, want %q (session must terminate despite the plan-row write failing)", got.Status, models.RemediationRejected)
	}
}

// ApprovePlan is the sanctioned way to advance a plan_review session; it must
// refuse a session that never reached that state (here, still pending)
// rather than resurrecting it the way Run's own precondition already
// forbids for a different reason. The error must be detectable via
// errors.Is so Task 10 can map it to HTTP 409 without substring matching.
func TestApprovePlanRejectsWrongState(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil) // still pending
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	err := r.ApprovePlan(context.Background(), sess.ID, "u-1")
	if err == nil {
		t.Fatal("ApprovePlan on a pending session succeeded, want error")
	}
	if !errors.Is(err, ErrWrongSessionState) {
		t.Errorf("err = %v, want errors.Is(err, ErrWrongSessionState)", err)
	}
}

// RejectPlan must also refuse a session outside plan_review.
func TestRejectPlanRejectsWrongState(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil) // still pending
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	err := r.RejectPlan(context.Background(), sess.ID, "u-1", "no")
	if err == nil {
		t.Fatal("RejectPlan on a pending session succeeded, want error")
	}
	if !errors.Is(err, ErrWrongSessionState) {
		t.Errorf("err = %v, want errors.Is(err, ErrWrongSessionState)", err)
	}
}

// A concurrent, double-clicked ApprovePlan is the exact hazard the
// compare-and-swap in transition() exists to close: two callers both read
// the session as plan_review and both attempt to advance it. Without the
// CAS, both would reach runExecutePhase, both save a patch set, and the
// second event sink would collide on the (session_id, seq) unique index.
// raceGateStore blocks the FIRST caller immediately after it writes the
// plan-approval row — the real gap between ApprovePlan's read and
// runExecutePhase's own claim write — so a second, concurrent caller can be
// driven through that exact window deterministically instead of relying on
// goroutine scheduling luck.
func TestApprovePlanIsSafeUnderConcurrentApproval(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
		s.PatchGateEnabled = false
	})
	cfg := Config{Enabled: true, MaxTurns: 10, AllowYolo: true}
	planRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()), cfg)
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// NewFake defaults Series to an empty PatchSeries; give each a real patch
	// so the "only one winner's patches landed" assertion below actually
	// exercises something (savePatches no-ops on an empty slice).
	newExecuteDriver := func() *driver.Fake {
		d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
		d.Series = &driver.PatchSeries{Patches: []driver.Patch{{CommitSHA: "c1", Message: "fix"}}}
		return d
	}
	rg := newRaceGateStore(store)
	blockedRunner := NewRunner(rg, newExecuteDriver(), cfg)
	freeRunner := NewRunner(store, newExecuteDriver(), cfg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- blockedRunner.ApprovePlan(context.Background(), sess.ID, "u-A")
	}()

	<-rg.entered // wait until goroutine A is parked right before its own claim write
	errB := freeRunner.ApprovePlan(context.Background(), sess.ID, "u-B")
	close(rg.proceed) // let A resume and attempt its now-stale claim
	errA := <-errCh

	if errB != nil {
		t.Fatalf("second (unblocked) ApprovePlan failed: %v", errB)
	}
	if errA == nil {
		t.Fatal("first (blocked) ApprovePlan succeeded despite the session already being advanced by the second — duplicate execute run")
	}
	if !errors.Is(errA, ErrWrongSessionState) {
		t.Errorf("errA = %v, want errors.Is(errA, ErrWrongSessionState)", errA)
	}

	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationCompleted {
		t.Fatalf("Status = %q, want %q (only the winner's execute run should have landed)", got.Status, models.RemediationCompleted)
	}
	patches := listPatches(t, store, sess.ID)
	if len(patches) != 1 {
		t.Fatalf("len(patches) = %d, want 1 — a second winner would have saved a duplicate set", len(patches))
	}
}

// The operator kill switch must stop a held session from resuming, not just
// a fresh one from starting: ApprovePlan resumes straight into a driver
// call, so it must refuse exactly like Run does when remediation is
// administratively disabled.
func TestApprovePlanRejectsWhenDisabled(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
		s.PatchGateEnabled = false
	})
	planRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		Config{Enabled: true, MaxTurns: 10, AllowYolo: true})
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	disabledRunner := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: false})
	if err := disabledRunner.ApprovePlan(context.Background(), sess.ID, "u-approver"); err == nil {
		t.Fatal("ApprovePlan succeeded with Enabled=false, want error")
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationPlanReview {
		t.Fatalf("Status = %q, want unchanged %q (refusal must not touch the session)", got.Status, models.RemediationPlanReview)
	}
}

// Same guard, patch side: ApprovePatches resumes into the landing phase,
// which will itself call the driver once Task 13 lands, so it must honor the
// kill switch too.
func TestApprovePatchesRejectsWhenDisabled(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = true
	})
	planRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		Config{Enabled: true, MaxTurns: 10, AllowYolo: true})
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	disabledRunner := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: false})
	if err := disabledRunner.ApprovePatches(context.Background(), sess.ID, "u-approver"); err == nil {
		t.Fatal("ApprovePatches succeeded with Enabled=false, want error")
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationPatchReview {
		t.Fatalf("Status = %q, want unchanged %q (refusal must not touch the session)", got.Status, models.RemediationPatchReview)
	}
}

// Rejection must stay available even when remediation is administratively
// disabled — an admin flipping WOLF_REMEDIATE_ENABLED=false to stop activity
// must still be able to terminate a session sitting in review, not be locked
// out of the one action that does strictly less work.
func TestRejectPlanWorksWhenDisabled(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
	})
	planRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		Config{Enabled: true, MaxTurns: 10, AllowYolo: true})
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	disabledRunner := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: false})
	if err := disabledRunner.RejectPlan(context.Background(), sess.ID, "u-approver", "stop"); err != nil {
		t.Fatalf("RejectPlan with Enabled=false: %v", err)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationRejected {
		t.Fatalf("Status = %q, want %q", got.Status, models.RemediationRejected)
	}
}

// ApprovePlan resumes straight into runExecutePhase's driver.Execute call, so
// it must bound that call with the configured SessionTimeout exactly as
// Run's own first phase is bounded — otherwise a gated session's resumed
// phase would run unbounded by wall clock while an ungated session's would
// not. deadlineCapturingDriver records whether the context Execute actually
// received carries a deadline, proving the bound reaches the driver call
// rather than just existing on paper.
func TestApprovePlanAppliesSessionTimeout(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
		s.PatchGateEnabled = false
	})
	planRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		Config{Enabled: true, MaxTurns: 10, AllowYolo: true})
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dcd := &deadlineCapturingDriver{Fake: driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())}
	resumeRunner := NewRunner(store, dcd, Config{Enabled: true, MaxTurns: 10, AllowYolo: true, SessionTimeout: time.Minute})
	if err := resumeRunner.ApprovePlan(context.Background(), sess.ID, "u-approver"); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	if !dcd.sawDeadline {
		t.Error("ApprovePlan did not bound the resumed execute phase with the configured SessionTimeout")
	}
}

// A patch-gated session holds at patch_review and, once approved, resumes
// into the landing phase — a stub that transitions straight to completed
// until Task 13 replaces it with apply/rescan/PR. Uses a fresh Runner for
// the resume step, same reasoning as TestPlanGateHoldsThenResumes.
func TestPatchGateHoldsThenResumes(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = true
	})
	cfg := Config{Enabled: true, MaxTurns: 10, AllowYolo: true}
	planRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()), cfg)

	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	held, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if held.Status != models.RemediationPatchReview {
		t.Fatalf("Status = %q, want %q", held.Status, models.RemediationPatchReview)
	}

	resumeRunner := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()), cfg)
	if err := resumeRunner.ApprovePatches(context.Background(), sess.ID, "u-approver"); err != nil {
		t.Fatalf("ApprovePatches: %v", err)
	}
	done, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if done.Status != models.RemediationCompleted {
		t.Fatalf("Status after approval = %q, want %q", done.Status, models.RemediationCompleted)
	}
}

// Rejecting held patches terminates the session; the patch rows already
// written to remediation_patches are left in place as an audit trail rather
// than deleted. Uses a fresh Runner for the resume step, same reasoning as
// TestPlanGateHoldsThenResumes.
func TestRejectPatchesTerminatesSession(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = true
	})
	cfg := Config{Enabled: true, MaxTurns: 10, AllowYolo: true}
	newFakeWithPatch := func() *driver.Fake {
		d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
		// NewFake defaults Series to an empty PatchSeries; give it a real
		// patch so savePatches (which no-ops on an empty slice) actually
		// writes a row for this test to prove is preserved.
		d.Series = &driver.PatchSeries{Patches: []driver.Patch{{CommitSHA: "c1", Message: "fix"}}}
		return d
	}
	planRunner := NewRunner(store, newFakeWithPatch(), cfg)
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	resumeRunner := NewRunner(store, newFakeWithPatch(), cfg)
	if err := resumeRunner.RejectPatches(context.Background(), sess.ID, "u-approver", "regressed tests"); err != nil {
		t.Fatalf("RejectPatches: %v", err)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationRejected {
		t.Errorf("Status = %q, want %q", got.Status, models.RemediationRejected)
	}
	patches := listPatches(t, store, sess.ID)
	if len(patches) == 0 {
		t.Fatal("rejection deleted the patch audit trail, want it preserved")
	}
	if patches[0].ApprovedBy != "u-approver" {
		t.Errorf("patches[0].ApprovedBy = %q, want %q (records the reviewer, not the verdict)", patches[0].ApprovedBy, "u-approver")
	}
}

// ApprovePatches must record who approved on every patch row, mirroring
// RejectPatches's actor-recording proven above — this is the write Important
// 4 flagged as silently discarded.
func TestApprovePatchesRecordsApprover(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = true
	})
	cfg := Config{Enabled: true, MaxTurns: 10, AllowYolo: true}
	d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
	d.Series = &driver.PatchSeries{Patches: []driver.Patch{{CommitSHA: "c1", Message: "fix"}}}
	planRunner := NewRunner(store, d, cfg)
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	resumeRunner := NewRunner(store, driver.NewFake(nil, fixturePlan()), cfg)
	if err := resumeRunner.ApprovePatches(context.Background(), sess.ID, "u-approver"); err != nil {
		t.Fatalf("ApprovePatches: %v", err)
	}
	patches := listPatches(t, store, sess.ID)
	if len(patches) == 0 || patches[0].ApprovedBy != "u-approver" {
		t.Fatalf("patches = %+v, want ApprovedBy = %q", patches, "u-approver")
	}
}

// ApprovePatches/RejectPatches are the sanctioned way to advance a
// patch_review session; both must refuse any other state, detectably via
// errors.Is.
func TestApprovePatchesRejectsWrongState(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil) // still pending
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	err := r.ApprovePatches(context.Background(), sess.ID, "u-1")
	if err == nil {
		t.Fatal("ApprovePatches on a pending session succeeded, want error")
	}
	if !errors.Is(err, ErrWrongSessionState) {
		t.Errorf("err = %v, want errors.Is(err, ErrWrongSessionState)", err)
	}
}

func TestRejectPatchesRejectsWrongState(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil) // still pending
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	err := r.RejectPatches(context.Background(), sess.ID, "u-1", "no")
	if err == nil {
		t.Fatal("RejectPatches on a pending session succeeded, want error")
	}
	if !errors.Is(err, ErrWrongSessionState) {
		t.Errorf("err = %v, want errors.Is(err, ErrWrongSessionState)", err)
	}
}
