// Package remediate orchestrates agentic remediation sessions: a read-only
// triage run that emits a plan, then a scoped-write run that executes it.
// Nothing is held open across an approval gate, so a pending approval is a
// database row that survives a restart.
package remediate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// ErrWrongSessionState means a caller tried to advance a remediation session
// from a status it is not currently in — either the session never reached
// the expected state, or a concurrent caller already moved it past that
// state first (a double-clicked approve, or an approval racing Run's own
// claim after a restart). Both cases are indistinguishable to the caller and
// handled identically; Task 10 maps this to HTTP 409.
var ErrWrongSessionState = errors.New("remediation session is not in the expected state for this action")

// ErrRemediationDisabled means the operator kill switch
// (WOLF_REMEDIATE_ENABLED=false) blocked an action that needs the driver.
// An admin flipping this off is a foreseeable, deliberate action — not a
// server malfunction — so Task 10 maps it to HTTP 403 rather than a generic
// 500.
var ErrRemediationDisabled = errors.New("remediation is disabled (WOLF_REMEDIATE_ENABLED=false)")

// Runner drives one session through its phases.
type Runner struct {
	store  db.Store
	driver driver.Driver
	cfg    Config
}

// NewRunner returns a Runner bound to a store and driver.
func NewRunner(store db.Store, d driver.Driver, cfg Config) *Runner {
	return &Runner{store: store, driver: d, cfg: cfg}
}

// driverPreflight enforces the operator kill switch and the wall-clock
// session bound before any call that reaches the driver — a container-backed
// agent run with scoped writes. It is shared by every entry point that can
// invoke the driver (Run, and the approval methods that resume a session
// straight into a driver-calling phase), not just Run's own first phase:
// without this, a held session resuming via ApprovePlan/ApprovePatches would
// ignore WOLF_REMEDIATE_ENABLED entirely and run unbounded by
// WOLF_REMEDIATE_SESSION_TIMEOUT, even though Run's own first phase enforces
// both. ClampTurns still bounds turns regardless, so a missed call here is a
// missing kill-switch/wall-clock guard, not a fully unbounded run — but both
// must apply uniformly.
//
// A zero SessionTimeout must not become an immediately-expiring context:
// context.WithTimeout(ctx, 0) sets a deadline of "now" and its internal
// timer fires almost instantly, so the session is bounded only when a
// positive timeout was actually configured. LoadConfig always sets one; only
// a hand-built Config (e.g. in tests) can leave this zero.
func (r *Runner) driverPreflight(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if !r.cfg.Enabled {
		return nil, nil, ErrRemediationDisabled
	}
	if r.cfg.SessionTimeout > 0 {
		ctx, cancel := context.WithTimeout(ctx, r.cfg.SessionTimeout)
		return ctx, cancel, nil
	}
	return ctx, func() {}, nil
}

// RecoverOrphanSessions fails sessions that were mid-run when the process
// died. ApprovePlan/ApprovePatches dispatch their execute phase to a
// background goroutine (Task 10b) that dies with the process, so a restart
// is the only place left to notice — nothing else watches these rows once
// the goroutine that would have finished them is gone. Sessions sitting in a
// review state hold no goroutine and are left untouched; that statelessness
// is the entire point of the gate design (see the package doc), so
// recovering them here would be wrong, not just unnecessary.
//
// Each write goes through the same compare-and-swap TransitionRemediation-
// Session uses everywhere else, not a blind UpdateRemediationSession: a
// blind write would let recovery race a session that is concurrently,
// legitimately advancing (e.g. its own Run reaching a review gate between
// this function's read and write) and clobber wherever it actually landed.
// A CAS loss (sql.ErrNoRows) means exactly that happened, so that row is
// skipped rather than escalated — it is already correct, just not what this
// function expected to find when it listed it.
//
// RemediationPending is included even though it looks like "not started yet"
// rather than "mid-run": CreateRemediation writes the row as pending and
// then dispatches Run in a background goroutine in the same breath (see
// routes.CreateRemediation) — pending has no other way out, no API
// re-triggers a session sitting there, so a crash between that write and
// Run's own first transition strands it exactly like any other stuck status.
// Including it here is only safe because this function runs before
// s.httpServer.ListenAndServe() in Server.Start(): no request handler can be
// concurrently creating a genuinely-new pending session while this runs, so
// every row this function finds in pending is guaranteed to be left over
// from a previous process, never a live one it would be racing.
func RecoverOrphanSessions(ctx context.Context, store db.Store) error {
	stuck := []models.RemediationStatus{
		models.RemediationPending,
		models.RemediationPlanning,
		models.RemediationExecuting,
		models.RemediationApplying,
		models.RemediationRescanning,
	}
	for _, status := range stuck {
		sessions, err := store.ListRemediationSessionsByStatus(ctx, status)
		if err != nil {
			return err
		}
		for i := range sessions {
			next := sessions[i]
			next.Status = models.RemediationFailed
			next.FailureReason = "server restarted while the session was running"
			now := time.Now()
			next.UpdatedAt = now
			next.CompletedAt = &now
			if err := store.TransitionRemediationSession(ctx, &next, status); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				return err
			}
		}
	}
	return nil
}

// Run advances a pending session as far as its gates allow. With both gates
// off it runs to completion; with a gate on it stops at the corresponding
// review state and returns nil, to be resumed by an approval. Only a
// pending session may be started this way — see the status check below.
func (r *Runner) Run(ctx context.Context, sessionID string) error {
	ctx, cancel, err := r.driverPreflight(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	sess, err := r.store.GetRemediationSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	// Run always starts a session's plan phase from scratch. Calling it on
	// anything but a fresh session would resurrect a terminal one (re-plan a
	// completed run, clear FailureReason, write a second patch set) or
	// re-plan a session already sitting in plan_review instead of resuming
	// it. Task 9 widens this to the resumable approval states explicitly;
	// until then only a pending session may start.
	if sess.Status != models.RemediationPending {
		return fmt.Errorf("%w: session %s is %s, not pending", ErrWrongSessionState, sessionID, sess.Status)
	}
	if (!sess.PlanGateEnabled || !sess.PatchGateEnabled) && !r.cfg.AllowYolo {
		return errors.New("gates disabled but WOLF_REMEDIATE_ALLOW_YOLO=false")
	}

	if err := r.runPlanPhase(ctx, sess); err != nil {
		return err
	}
	if sess.PlanGateEnabled {
		return r.transition(ctx, sess, models.RemediationPlanReview, "")
	}
	return r.runExecutePhase(ctx, sess)
}

// runPlanPhase runs the read-only triage pass and saves its plan. The
// deferred call below catches every error return so a transient failure
// (a DB blip loading findings, a failed plan write) marks the session
// failed instead of leaving it stuck in "planning" forever — the orphaned
// row Task 10a's recovery would otherwise have to clean up.
func (r *Runner) runPlanPhase(ctx context.Context, sess *models.RemediationSession) (err error) {
	started := time.Now()
	sess.StartedAt = &started
	if terr := r.transition(ctx, sess, models.RemediationPlanning, ""); terr != nil {
		return terr
	}

	// prepareWorkspace runs AFTER the CAS claim above, not before: two
	// racing Run calls on the same pending session both pass the pending
	// check, but only one wins this transition. Preparing first would let
	// the loser clone+worktree anyway, with nothing left to clean up
	// afterward. prepareWorkspace's own defer already fails the session on
	// error (repo missing, clone failed, strip failed), so its failure
	// returns directly rather than needing the defer below, which is
	// registered next.
	//
	// The returned workspace is intentionally not torn down here: a gated
	// session pauses at plan_review/patch_review for a human and resumes in
	// a LATER Runner (ApprovePlan/ApprovePatches, Task 10b's async
	// dispatch), reusing the same worktree/branch all the way through to
	// landing (push + PR, Task 13) — the first point nothing still needs
	// it. Cleaning up here would delete a live workspace out from under a
	// session that has not finished with it.
	if _, werr := r.prepareWorkspace(ctx, sess); werr != nil {
		return werr
	}

	// prepareWorkspace only sets WorktreePath/BranchName/CloneRoot in
	// memory; without a write here they would sit unpersisted for the
	// entire driver.Plan call below — an agentic LLM run, potentially
	// minutes. If the process dies in that window, RecoverOrphanSessions
	// marks this row failed with those fields still empty: exactly the
	// "leaked clone with no persisted handle for cleanup" IMPORTANT 2 was
	// raised about, reopened in the one scenario RecoverOrphanSessions
	// exists to handle. A self-transition (still "planning") reuses the
	// same CAS write path transition() already provides rather than adding
	// a second one (e.g. a bare UpdateRemediationSession). Kept before the
	// defer below, matching the top claim above: a CAS loss here means
	// something else (a concurrent cancel) legitimately moved the session,
	// so this returns directly rather than having failSession stomp on it.
	if terr := r.transition(ctx, sess, models.RemediationPlanning, ""); terr != nil {
		return terr
	}

	defer func() {
		if err != nil {
			status := models.RemediationFailed
			if errors.Is(err, driver.ErrBudgetExhausted) {
				status = models.RemediationExhausted
			}
			r.failSession(ctx, sess, status, err)
		}
	}()

	findings, ferr := r.store.ListFindingsByScan(ctx, sess.ScanID)
	if ferr != nil {
		return fmt.Errorf("load findings: %w", ferr)
	}

	req := driver.PlanRequest{
		WorktreePath: sess.WorktreePath,
		Findings:     findings,
		MaxTurns:     r.cfg.ClampTurns(sess.MaxTurns),
		Provider:     sess.Provider,
		Model:        sess.Model,
		OnEvent:      r.eventSink(ctx, sess.ID),
	}
	budget := req.MaxTurns
	p, usage, perr := r.driver.Plan(ctx, req)
	sess.TurnsUsedPlan = usage.Turns
	sess.TokensUsed += usage.Tokens
	sess.CostUsed += usage.Cost
	// One repair attempt: the agent ran, spent turns, and produced output
	// that did not parse as a plan. Telling it plainly what went wrong and
	// asking again is worth one retry; a second failure means the agent
	// cannot produce valid plan JSON for this finding set, and burning more
	// turns on a third attempt is not worth it. The failed attempt's own
	// usage is added above, before the retry, rather than overwritten by
	// it — it was real, billed driver spend, not a no-op, and must count
	// toward the session the same way both phases' usage already does.
	//
	// The retry gets what's LEFT of the phase's budget (budget minus what
	// the first attempt already spent), not a fresh MaxTurns — driver.Plan
	// builds a brand-new meter per call, so reusing MaxTurns verbatim would
	// let one plan phase spend up to 2x its configured budget, silently
	// defeating the cost control max_turns exists to enforce. A budget-
	// exhausted first attempt returns driver.ErrBudgetExhausted, not
	// ErrUnparseablePlan, so it never reaches this branch — remaining is
	// always positive on the path that does. The <= 0 guard is defensive
	// only, for a future change to the meter's own accounting.
	if perr != nil && errors.Is(perr, driver.ErrUnparseablePlan) {
		if remaining := budget - usage.Turns; remaining > 0 {
			req.RepairHint = "Your previous response was not valid plan JSON. " +
				"Respond with the plan object only, no prose."
			req.MaxTurns = remaining
			p, usage, perr = r.driver.Plan(ctx, req)
			sess.TurnsUsedPlan += usage.Turns
			sess.TokensUsed += usage.Tokens
			sess.CostUsed += usage.Cost
		}
	}
	if perr != nil {
		return perr
	}
	return r.savePlan(ctx, sess, p)
}

// failSession marks a session terminal after an error from a phase function,
// logging rather than discarding a failure to persist that transition — an
// unlogged write failure here would leave a broken session that never shows
// up as broken, the same hazard the event sink below guards against.
func (r *Runner) failSession(ctx context.Context, sess *models.RemediationSession, status models.RemediationStatus, cause error) {
	if err := r.transition(ctx, sess, status, cause.Error()); err != nil {
		wolflog.L().Error().Err(err).Str("session", sess.ID).
			Str("target_status", string(status)).
			Msg("transition remediation session after failure")
	}
}

// savePlan persists the triage plan. sess.TurnsUsedPlan was already set by
// the caller; it reaches the database via the transition call that follows
// (either the plan-review stop or the start of the execute phase), so this
// does not need a second session write of its own.
func (r *Runner) savePlan(ctx context.Context, sess *models.RemediationSession, p *plan.Plan) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}
	if err := r.store.SaveRemediationPlan(ctx, &models.RemediationPlan{
		SessionID: sess.ID,
		PlanJSON:  string(data),
		CreatedAt: time.Now(),
	}); err != nil {
		return fmt.Errorf("save plan: %w", err)
	}
	return nil
}

// runExecutePhase runs the scoped-write fix and records its output. It
// reloads the plan from the store rather than reusing runPlanPhase's return
// value: per the package doc, nothing is held open across the plan-review
// gate, so a resumed session (a fresh Runner, possibly after a restart) must
// be able to reach this phase with only the database row to go on.
//
// As in runPlanPhase, every error return below is caught by the deferred
// failSession call so a transient failure marks the session failed instead
// of leaving it stuck in "executing" forever.
//
// Phase 3 replaces the completed branch below with apply/rescan/PR.
func (r *Runner) runExecutePhase(ctx context.Context, sess *models.RemediationSession) (err error) {
	if terr := r.transition(ctx, sess, models.RemediationExecuting, ""); terr != nil {
		return terr
	}
	defer func() {
		if err != nil {
			status := models.RemediationFailed
			if errors.Is(err, driver.ErrBudgetExhausted) {
				status = models.RemediationExhausted
			}
			r.failSession(ctx, sess, status, err)
		}
	}()

	findings, ferr := r.store.ListFindingsByScan(ctx, sess.ScanID)
	if ferr != nil {
		return fmt.Errorf("load findings: %w", ferr)
	}
	saved, perr := r.store.GetRemediationPlan(ctx, sess.ID)
	if perr != nil {
		return fmt.Errorf("load plan: %w", perr)
	}
	p, perr := plan.Parse([]byte(saved.PlanJSON))
	if perr != nil {
		return fmt.Errorf("parse saved plan: %w", perr)
	}

	series, usage, eerr := r.driver.Execute(ctx, driver.ExecuteRequest{
		WorktreePath: sess.WorktreePath,
		Plan:         p,
		Findings:     findings,
		MaxTurns:     r.cfg.ClampTurns(sess.MaxTurns),
		Provider:     sess.Provider,
		Model:        sess.Model,
		OnEvent:      r.eventSink(ctx, sess.ID),
	})
	sess.TurnsUsedExecute = usage.Turns
	sess.TokensUsed += usage.Tokens
	sess.CostUsed += usage.Cost
	if eerr != nil {
		// A budget-exhausted run still returns whatever the agent actually
		// committed: driver/exec.go collects those real, paid-for commits on
		// purpose rather than discarding them on timeout. Salvage them here,
		// best-effort, before the deferred call above marks the session
		// exhausted — otherwise a genuine partial fix silently vanishes from
		// the patch table. A save failure here is logged, not escalated: the
		// exhaustion itself is still the true, and more important, outcome.
		if errors.Is(eerr, driver.ErrBudgetExhausted) && series != nil {
			if serr := r.savePatches(ctx, sess.ID, series.Patches); serr != nil {
				wolflog.L().Error().Err(serr).Str("session", sess.ID).
					Msg("save salvaged patches after budget exhaustion")
			}
		}
		return eerr
	}

	if serr := r.savePatches(ctx, sess.ID, series.Patches); serr != nil {
		return serr
	}
	if sess.PatchGateEnabled {
		return r.transition(ctx, sess, models.RemediationPatchReview, "")
	}
	return r.transition(ctx, sess, models.RemediationCompleted, "")
}

// ClaimPlanApproval performs ApprovePlan's synchronous portion — the
// operator kill switch check, the session's CAS-guarded status check, and
// recording who approved the plan — without running the execute phase
// itself. It returns the claimed session for the caller to hand to
// ExecutePlanPhase.
//
// This split exists for Task 10's HTTP layer: the execute phase is a
// container-backed driver call that can run for minutes, and an HTTP
// handler must not hold the connection open for it (the server's
// WriteTimeout is shorter than the default SessionTimeout). The API layer
// claims synchronously here — so a double-clicked approve is still rejected
// with 409 before anything is dispatched — then runs ExecutePlanPhase in
// its own background goroutine. ApprovePlan below is unchanged in every
// observable way; it is now just this claim immediately followed by that
// execute call, for callers that want the fully-synchronous behavior (e.g.
// package tests).
func (r *Runner) ClaimPlanApproval(ctx context.Context, sessionID, approverID string) (*models.RemediationSession, error) {
	ctx, cancel, err := r.driverPreflight(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	sess, err := r.store.GetRemediationSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.Status != models.RemediationPlanReview {
		return nil, fmt.Errorf("%w: session %s is %s, not awaiting plan approval", ErrWrongSessionState, sessionID, sess.Status)
	}
	if err := r.store.ApproveRemediationPlan(ctx, sessionID, approverID); err != nil {
		return nil, err
	}
	return sess, nil
}

// ExecutePlanPhase runs the execute phase for a session already claimed via
// ClaimPlanApproval. Gated by driverPreflight for the same reason ApprovePlan
// itself was: the kill switch and wall-clock bound must apply to the driver
// call regardless of which path reaches it. Resumption reloads everything
// from sess (passed in) and the store inside runExecutePhase; no in-memory
// state survives from the original Run call, so this works identically
// after a server restart.
func (r *Runner) ExecutePlanPhase(ctx context.Context, sess *models.RemediationSession) error {
	ctx, cancel, err := r.driverPreflight(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return r.runExecutePhase(ctx, sess)
}

// ApprovePlan records approval on the plan row and resumes the session into
// its execute phase, blocking until the phase completes. This is the
// sanctioned way to advance a session sitting in plan_review — Run refuses
// anything but a pending session, specifically so a held session can only
// move forward through here.
func (r *Runner) ApprovePlan(ctx context.Context, sessionID, approverID string) error {
	sess, err := r.ClaimPlanApproval(ctx, sessionID, approverID)
	if err != nil {
		return err
	}
	return r.ExecutePlanPhase(ctx, sess)
}

// RejectPlan terminates the session without ever reaching the execute phase,
// so no code is written for a plan a human declined. The session is
// terminated FIRST, before the best-effort write of the reason onto the plan
// row: a human's decision to reject must not be defeated by a bookkeeping
// write. If RejectRemediationPlan then fails, that is logged rather than
// returned — escalating it would leave the session stuck in plan_review,
// where every retry of this same call re-hits the very failure that stranded
// it, since the session's own status has already moved to rejected.
//
// Deliberately not gated by driverPreflight: this never calls the driver, so
// there is nothing for the kill switch or wall-clock bound to protect
// against, and an admin who set WOLF_REMEDIATE_ENABLED=false to stop
// remediation activity must still be able to terminate a session sitting in
// review — refusing that would be the kill switch working against itself.
func (r *Runner) RejectPlan(ctx context.Context, sessionID, approverID, reason string) error {
	sess, err := r.store.GetRemediationSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.Status != models.RemediationPlanReview {
		return fmt.Errorf("%w: session %s is %s, not awaiting plan approval", ErrWrongSessionState, sessionID, sess.Status)
	}
	if terr := r.transition(ctx, sess, models.RemediationRejected, reason); terr != nil {
		return terr
	}
	// The plan is what a human reviewed and declined, so the rejection
	// belongs beside it too — otherwise remediation_plans.rejected_reason is
	// never written by anything. But this write is best-effort now that the
	// session itself is already terminated; see the doc comment above.
	if err := r.store.RejectRemediationPlan(ctx, sessionID, approverID, reason); err != nil {
		wolflog.L().Error().Err(err).Str("session", sessionID).
			Msg("record plan rejection reason after session was terminated")
	}
	return nil
}

// ClaimPatchesApproval is ClaimPlanApproval's patch-review counterpart: the
// synchronous kill-switch check, CAS-guarded status check, and recording who
// approved the patch set, without running the landing phase. See
// ClaimPlanApproval's doc comment for why this is split from ApprovePatches.
func (r *Runner) ClaimPatchesApproval(ctx context.Context, sessionID, approverID string) (*models.RemediationSession, error) {
	ctx, cancel, err := r.driverPreflight(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	sess, err := r.store.GetRemediationSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.Status != models.RemediationPatchReview {
		return nil, fmt.Errorf("%w: session %s is %s, not awaiting patch approval", ErrWrongSessionState, sessionID, sess.Status)
	}
	if err := r.store.ApproveRemediationPatches(ctx, sessionID, approverID); err != nil {
		return nil, err
	}
	return sess, nil
}

// ExecuteLandingPhase runs the landing phase for a session already claimed
// via ClaimPatchesApproval. Gated by driverPreflight for the same reason as
// ExecutePlanPhase — the kill switch and wall-clock bound must apply
// uniformly across every resume path — even though runLandingPhase itself
// does not call r.driver: it pushes the approved branch and records the scan
// delta (see Runner.land), not another agentic run. An admin who has
// disabled remediation must still be able to stop a session from advancing
// here, the same as any other phase.
func (r *Runner) ExecuteLandingPhase(ctx context.Context, sess *models.RemediationSession) error {
	ctx, cancel, err := r.driverPreflight(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return r.runLandingPhase(ctx, sess)
}

// ApprovePatches records who approved and resumes the session into the
// landing phase, blocking until it completes — the patch-review counterpart
// to ApprovePlan.
func (r *Runner) ApprovePatches(ctx context.Context, sessionID, approverID string) error {
	sess, err := r.ClaimPatchesApproval(ctx, sessionID, approverID)
	if err != nil {
		return err
	}
	return r.ExecuteLandingPhase(ctx, sess)
}

// RejectPatches terminates the session without landing any patch. The patch
// rows already saved to remediation_patches are left in place — they are the
// audit trail of what the agent produced and a human declined, not scratch
// state to discard. Ordered the same way as RejectPlan: the session is
// terminated first, and the best-effort write of who rejected it onto those
// rows is logged rather than escalated on failure, for the same reason. Also
// not gated by driverPreflight, for the same reason as RejectPlan: no driver
// call happens here, and termination must stay available even when
// remediation is administratively disabled.
func (r *Runner) RejectPatches(ctx context.Context, sessionID, approverID, reason string) error {
	sess, err := r.store.GetRemediationSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.Status != models.RemediationPatchReview {
		return fmt.Errorf("%w: session %s is %s, not awaiting patch approval", ErrWrongSessionState, sessionID, sess.Status)
	}
	if terr := r.transition(ctx, sess, models.RemediationRejected, reason); terr != nil {
		return terr
	}
	if err := r.store.ApproveRemediationPatches(ctx, sessionID, approverID); err != nil {
		wolflog.L().Error().Err(err).Str("session", sessionID).
			Msg("record patch reviewer after session was terminated")
	}
	return nil
}

// runLandingPhase claims the session into its landing phase, then pushes the
// approved branch and records the scan delta — see Runner.land's doc comment
// for exactly where this intentionally stops (PR creation is deferred).
//
// Claims BEFORE registering the defer below, matching runPlanPhase/
// runExecutePhase: real work (the push) now runs between the claim and the
// final transition, so claiming first is what keeps a second, concurrent
// caller (a double-clicked approve landing in the gap between
// ClaimPatchesApproval's read and this function's own write) from also
// passing that check and running this phase a second time — the same CAS
// transition() itself protects everywhere else. Task 9's review caught this
// function registering its defer before its only transition while the body
// was still a no-op stub; that ordering would have reopened exactly that
// race the moment real work landed here.
func (r *Runner) runLandingPhase(ctx context.Context, sess *models.RemediationSession) (err error) {
	if terr := r.transition(ctx, sess, models.RemediationApplying, ""); terr != nil {
		return terr
	}
	defer func() {
		if err != nil {
			r.failSession(ctx, sess, models.RemediationFailed, err)
		}
	}()

	d, derr := r.landingDelta(ctx, sess)
	if derr != nil {
		return derr
	}
	if lerr := r.land(ctx, sess, d); lerr != nil {
		return lerr
	}
	return r.transition(ctx, sess, models.RemediationCompleted, "")
}

// savePatches converts the driver's patch series into stored rows.
// FilesChanged and FindingIDs are JSON-encoded to text to match
// models.RemediationPatch's column format; nil slices are normalized to
// empty ones first so the stored text is "[]", not "null" per the field's
// documented empty representation.
func (r *Runner) savePatches(ctx context.Context, sessionID string, patches []driver.Patch) error {
	rows := make([]models.RemediationPatch, 0, len(patches))
	for _, p := range patches {
		filesChanged := p.FilesChanged
		if filesChanged == nil {
			filesChanged = []string{}
		}
		findingIDs := p.FindingIDs
		if findingIDs == nil {
			findingIDs = []string{}
		}
		files, err := json.Marshal(filesChanged)
		if err != nil {
			return fmt.Errorf("marshal files_changed: %w", err)
		}
		ids, err := json.Marshal(findingIDs)
		if err != nil {
			return fmt.Errorf("marshal finding_ids: %w", err)
		}
		rows = append(rows, models.RemediationPatch{
			SessionID:    sessionID,
			CommitSHA:    p.CommitSHA,
			FilesChanged: string(files),
			FindingIDs:   string(ids),
			Message:      p.Message,
			CreatedAt:    time.Now(),
		})
	}
	if err := r.store.SaveRemediationPatches(ctx, sessionID, rows); err != nil {
		return fmt.Errorf("save patches: %w", err)
	}
	return nil
}

// eventSink persists each observed event, redacted, for SSE replay and audit.
//
// The sequence is session-scoped and monotonic across phases, not per-call.
// A sink is built once per phase, so a counter starting at zero each time
// would have execute-phase event 1 collide with plan-phase event 1: the ID is
// derived from (session, seq) and is the primary key, and (session_id, seq)
// is UNIQUE, so the second phase's every write fails. Seeding from what is
// already persisted keeps one continuous sequence per session.
func (r *Runner) eventSink(ctx context.Context, sessionID string) func(meter.Event) {
	seq := r.lastEventSeq(ctx, sessionID)
	return func(e meter.Event) {
		seq++
		// Part carries only the step's stop reason, token counts, and cost —
		// no credential can reach it — so it's safe to persist verbatim as
		// the event's redacted payload, giving SSE replay more than a bare
		// type string. json.Marshal on this concrete struct cannot fail in
		// practice; a failure here still leaves the event's other fields
		// worth persisting, so it's logged rather than dropping the event.
		payload, merr := json.Marshal(e.Part)
		if merr != nil {
			wolflog.L().Error().Err(merr).
				Str("session", sessionID).Int("seq", seq).
				Msg("marshal remediation event payload")
		}
		r.appendEvent(ctx, sessionID, seq, e.Type, payload)
	}
}

// lastEventSeq returns the highest seq already stored for a session so a sink
// built for a later phase continues that session's single sequence. Events
// come back ordered by seq, so the tail holds the maximum. A read failure
// falls back to 0: the unique index then rejects the duplicate loudly rather
// than letting the sink overwrite an earlier phase.
func (r *Runner) lastEventSeq(ctx context.Context, sessionID string) int {
	events, err := r.store.ListRemediationEvents(ctx, sessionID, 0)
	if err != nil || len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

// recordEvent appends a single audit event outside the driver's own replayed
// stream — e.g. a worktree hygiene action taken before the driver is ever
// called. It shares eventSink's session-scoped sequencing (lastEventSeq), so
// the two interleave into one ordered timeline regardless of call order.
func (r *Runner) recordEvent(ctx context.Context, sessionID, eventType, detail string) {
	seq := r.lastEventSeq(ctx, sessionID) + 1
	payload, merr := json.Marshal(struct {
		Path string `json:"path"`
	}{Path: detail})
	if merr != nil {
		wolflog.L().Error().Err(merr).
			Str("session", sessionID).Int("seq", seq).
			Msg("marshal remediation event payload")
	}
	r.appendEvent(ctx, sessionID, seq, eventType, payload)
}

// appendEvent is eventSink and recordEvent's shared tail: build the row and
// persist it, logging rather than returning a store failure. One best-effort
// audit write must never abort a phase that has otherwise made real
// progress, and a dropped append is a hole in the audit trail SSE replay
// cannot tell apart from "no activity" — which is exactly how the per-call
// sequence bug (fixed when eventSink gained lastEventSeq) stayed invisible,
// so this is logged loudly rather than silently discarded.
func (r *Runner) appendEvent(ctx context.Context, sessionID string, seq int, eventType string, payload []byte) {
	if err := r.store.AppendRemediationEvent(ctx, &models.RemediationEvent{
		ID:          fmt.Sprintf("%s-%d", sessionID, seq),
		SessionID:   sessionID,
		Seq:         seq,
		Type:        eventType,
		PayloadJSON: string(payload),
		CreatedAt:   time.Now(),
	}); err != nil {
		wolflog.L().Error().Err(err).
			Str("session", sessionID).Int("seq", seq).
			Msg("persist remediation event")
	}
}

// transition moves sess to a new status, guarded by a compare-and-swap on
// the status sess still carries in memory. That value is always the last
// status actually committed for this sess, because sess is only mutated
// below once the database write has actually landed — a failed write leaves
// sess (and so the "from" a subsequent transition call computes, e.g.
// failSession's, chained right after this one returns) pointing at the true
// committed state rather than a phantom one.
//
// The CAS is what keeps two racing callers — a double-clicked approve, or an
// approval landing the instant Run's own claim commits — from both observing
// the same review state and both proceeding: only the first write wins, and
// the loser's ErrWrongSessionState looks identical to a stale precondition,
// which is exactly how it should be handled.
func (r *Runner) transition(ctx context.Context, sess *models.RemediationSession, status models.RemediationStatus, reason string) error {
	from := sess.Status
	next := *sess
	next.Status = status
	next.FailureReason = reason
	next.UpdatedAt = time.Now()
	switch status {
	case models.RemediationCompleted, models.RemediationFailed,
		models.RemediationExhausted, models.RemediationCancelled,
		models.RemediationRejected:
		now := time.Now()
		next.CompletedAt = &now
	}
	if err := r.store.TransitionRemediationSession(ctx, &next, from); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: session %s is no longer %s", ErrWrongSessionState, sess.ID, from)
		}
		return err
	}
	*sess = next
	return nil
}
