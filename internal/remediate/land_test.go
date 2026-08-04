package remediate

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
)

func TestDeltaTableReportsCounts(t *testing.T) {
	d := Delta{
		Fixed:     []models.Finding{f("a"), f("b")},
		Remaining: []models.Finding{f("c")},
		New:       []models.Finding{f("d")},
	}
	body := DeltaTable(d)

	for _, want := range []string{"Fixed", "Remaining", "New", "2", "1"} {
		if !strings.Contains(body, want) {
			t.Errorf("DeltaTable missing %q:\n%s", want, body)
		}
	}
}

func TestDeltaTableFlagsRegression(t *testing.T) {
	d := Delta{
		Fixed: []models.Finding{f("a")},
		New:   []models.Finding{f("b"), f("c"), f("d")},
	}
	body := DeltaTable(d)
	if !strings.Contains(strings.ToLower(body), "regress") {
		t.Fatalf("regressed delta not flagged in PR body:\n%s", body)
	}
}

// The PR body is rendered from scan data and must never carry credentials.
func TestDeltaTableNeverRendersCredentials(t *testing.T) {
	d := Delta{Fixed: []models.Finding{{ID: "a", Title: `secret=dckr_pat_never_render`}}}
	body := DeltaTable(d)
	if strings.Contains(body, "dckr_pat_never_render") {
		t.Fatal("credential-shaped finding text rendered into PR body")
	}
}

// findingsStore wraps a real db.Store and returns a fixed finding set from
// ListFindingsByScan, so a test can give runLandingPhase real findings to
// diff against without seeding a full scans-table row (ScanID here, "sc-1",
// never needs to resolve to a real scan — the same shortcut session_test.go's
// own seedSession already relies on).
type findingsStore struct {
	db.Store
	findings []models.Finding
}

func (s *findingsStore) ListFindingsByScan(ctx context.Context, scanID string) ([]models.Finding, error) {
	return s.findings, nil
}

// TestRunLandingPhasePushesBranchAndRecordsDelta drives a patch-gated
// session through to completion (landing only runs on the gated path — see
// runExecutePhase: a yolo, both-gates-off session transitions straight from
// executing to completed and never reaches the landing phase at all, which
// is pre-existing behavior this task does not change) and checks landing's
// three concrete promises: the branch actually lands in the repo
// prepareWorkspace cloned from (the scratch clone's origin — see
// cloneLocalForRemediation's doc comment), sess.PRURL stays empty (PR
// creation is deferred by this task's scope), and the scan delta is
// recorded on the session's audit trail as counts only — never raw finding
// text, which can carry a real credential.
func TestRunLandingPhasePushesBranchAndRecordsDelta(t *testing.T) {
	store := newTestStore(t)
	repoPath := seedRepo(t, store, "r-1")
	fs := &findingsStore{
		Store: store,
		findings: []models.Finding{
			{ID: "f-1", Title: "SQL injection"},
			{ID: "f-2", Title: "XSS"},
		},
	}
	sess := seedSession(t, fs, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = true
	})

	d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
	// Only f-1 is claimed fixed by the patch; f-2 must surface as Remaining.
	d.Series = &driver.PatchSeries{Patches: []driver.Patch{{CommitSHA: "c1", Message: "fix", FindingIDs: []string{"f-1"}}}}
	r := NewRunner(fs, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	held, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if held.Status != models.RemediationPatchReview {
		t.Fatalf("Status = %q, want %q", held.Status, models.RemediationPatchReview)
	}
	resumeRunner := NewRunner(fs, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, MaxTurns: 10, AllowYolo: true})
	if err := resumeRunner.ApprovePatches(context.Background(), sess.ID, "u-approver"); err != nil {
		t.Fatalf("ApprovePatches: %v", err)
	}

	got, err := store.GetRemediationSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetRemediationSession: %v", err)
	}
	if got.Status != models.RemediationCompleted {
		t.Fatalf("Status = %q, want %q", got.Status, models.RemediationCompleted)
	}
	if got.PRURL != "" {
		t.Errorf("PRURL = %q, want empty — PR creation is deferred by scope, not an error", got.PRURL)
	}

	branch := BranchName(sess.ID)
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/"+branch)
	cmd.Dir = repoPath
	if out, verr := cmd.CombinedOutput(); verr != nil {
		t.Fatalf("branch %s not found in origin %s: %v\n%s", branch, repoPath, verr, out)
	}

	events, err := store.ListRemediationEvents(context.Background(), sess.ID, 0)
	if err != nil {
		t.Fatalf("ListRemediationEvents: %v", err)
	}
	var deltaEvent *models.RemediationEvent
	for i := range events {
		if events[i].Type == "landing.delta" {
			deltaEvent = &events[i]
		}
	}
	if deltaEvent == nil {
		t.Fatal("no landing.delta event recorded on the session")
	}
	if strings.Contains(deltaEvent.PayloadJSON, "SQL injection") || strings.Contains(deltaEvent.PayloadJSON, "XSS") {
		t.Errorf("landing.delta payload leaked finding content: %s", deltaEvent.PayloadJSON)
	}
	var payload struct {
		Fixed     int  `json:"fixed"`
		Remaining int  `json:"remaining"`
		New       int  `json:"new"`
		Regressed bool `json:"regressed"`
	}
	if uerr := json.Unmarshal([]byte(deltaEvent.PayloadJSON), &payload); uerr != nil {
		t.Fatalf("unmarshal delta payload: %v", uerr)
	}
	if payload.Fixed != 1 || payload.Remaining != 1 || payload.New != 0 || payload.Regressed {
		t.Errorf("payload = %+v, want Fixed=1 Remaining=1 New=0 Regressed=false (f-1 fixed by the patch, f-2 remaining)", payload)
	}
}

// A push failure (here, a worktree path that no longer exists) must mark the
// session failed rather than leave it stuck in "applying" — the same
// orphaned-row hazard runPlanPhase/runExecutePhase already guard against.
func TestRunLandingPhaseFailsSessionOnPushFailure(t *testing.T) {
	store := newTestStore(t)
	sess := seedSessionWithStatus(t, store, models.RemediationPatchReview)
	sess.WorktreePath = filepath.Join(t.TempDir(), "does-not-exist")
	sess.BranchName = BranchName(sess.ID)
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	if err := r.runLandingPhase(context.Background(), sess); err == nil {
		t.Fatal("runLandingPhase succeeded despite an unreachable worktree, want error")
	}

	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationFailed {
		t.Fatalf("Status = %q, want %q (not stuck in applying)", got.Status, models.RemediationFailed)
	}
	if got.FailureReason == "" {
		t.Error("FailureReason not recorded")
	}
}

// raceGatePatchStore is ApprovePlanIsSafeUnderConcurrentApproval's raceGate-
// Store, mirrored for the patch/landing gate: it blocks the FIRST caller
// immediately after ApproveRemediationPatches writes its row — the real gap
// between ClaimPatchesApproval's read and runLandingPhase's own claim
// write — so a second, concurrent caller can be driven through that window
// deterministically.
type raceGatePatchStore struct {
	db.Store
	once    sync.Once
	proceed chan struct{}
	entered chan struct{}
}

func newRaceGatePatchStore(store db.Store) *raceGatePatchStore {
	return &raceGatePatchStore{Store: store, proceed: make(chan struct{}), entered: make(chan struct{})}
}

func (s *raceGatePatchStore) ApproveRemediationPatches(ctx context.Context, sessionID, approverID string) error {
	if err := s.Store.ApproveRemediationPatches(ctx, sessionID, approverID); err != nil {
		return err
	}
	s.once.Do(func() {
		close(s.entered)
		<-s.proceed
	})
	return nil
}

// TestApprovePatchesIsSafeUnderConcurrentApproval is TestApprovePlanIsSafe-
// UnderConcurrentApproval's counterpart for the patch/landing gate. Before
// this task, runLandingPhase's stub body registered its defer BEFORE its
// only transition (flagged by Task 9's review); once real work — the push —
// runs between ClaimPatchesApproval's read and the final transition, that
// ordering would let two racing ApprovePatches calls both land. This proves
// the fix: only one of two concurrent callers may complete the landing
// phase.
func TestApprovePatchesIsSafeUnderConcurrentApproval(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = true
	})
	cfg := Config{Enabled: true, MaxTurns: 10, AllowYolo: true}
	newExecuteDriver := func() *driver.Fake {
		d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
		d.Series = &driver.PatchSeries{Patches: []driver.Patch{{CommitSHA: "c1", Message: "fix"}}}
		return d
	}
	planRunner := NewRunner(store, newExecuteDriver(), cfg)
	if err := planRunner.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	held, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if held.Status != models.RemediationPatchReview {
		t.Fatalf("Status = %q, want %q", held.Status, models.RemediationPatchReview)
	}

	rg := newRaceGatePatchStore(store)
	blockedRunner := NewRunner(rg, driver.NewFake(nil, fixturePlan()), cfg)
	freeRunner := NewRunner(store, driver.NewFake(nil, fixturePlan()), cfg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- blockedRunner.ApprovePatches(context.Background(), sess.ID, "u-A")
	}()

	<-rg.entered // wait until goroutine A is parked right before runLandingPhase's own claim write
	errB := freeRunner.ApprovePatches(context.Background(), sess.ID, "u-B")
	close(rg.proceed) // let A resume and attempt its now-stale claim
	errA := <-errCh

	if errB != nil {
		t.Fatalf("second (unblocked) ApprovePatches failed: %v", errB)
	}
	if errA == nil {
		t.Fatal("first (blocked) ApprovePatches succeeded despite the session already being advanced by the second — duplicate landing run")
	}
	if !errors.Is(errA, ErrWrongSessionState) {
		t.Errorf("errA = %v, want errors.Is(errA, ErrWrongSessionState)", errA)
	}

	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationCompleted {
		t.Fatalf("Status = %q, want %q (only the winner's landing run should have landed)", got.Status, models.RemediationCompleted)
	}
}
