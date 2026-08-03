// Package remediate orchestrates agentic remediation sessions: a read-only
// triage run that emits a plan, then a scoped-write run that executes it.
// Nothing is held open across an approval gate, so a pending approval is a
// database row that survives a restart.
package remediate

import (
	"context"
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

// Run advances a pending session as far as its gates allow. With both gates
// off it runs to completion; with a gate on it stops at the corresponding
// review state and returns nil, to be resumed by an approval. Only a
// pending session may be started this way — see the status check below.
func (r *Runner) Run(ctx context.Context, sessionID string) error {
	if !r.cfg.Enabled {
		return errors.New("remediation is disabled (WOLF_REMEDIATE_ENABLED=false)")
	}
	// A zero SessionTimeout must not become an immediately-expiring context:
	// context.WithTimeout(ctx, 0) sets a deadline of "now" and its internal
	// timer fires almost instantly, so bound the session only when a positive
	// timeout was actually configured. LoadConfig always sets one; only a
	// hand-built Config (e.g. in tests) can leave this zero.
	if r.cfg.SessionTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.SessionTimeout)
		defer cancel()
	}

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
		return fmt.Errorf("session %s is not pending (status=%q)", sessionID, sess.Status)
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

	p, usage, perr := r.driver.Plan(ctx, driver.PlanRequest{
		WorktreePath: sess.WorktreePath,
		Findings:     findings,
		MaxTurns:     r.cfg.ClampTurns(sess.MaxTurns),
		Provider:     sess.Provider,
		Model:        sess.Model,
		OnEvent:      r.eventSink(ctx, sess.ID),
	})
	sess.TurnsUsedPlan = usage.Turns
	sess.TokensUsed += usage.Tokens
	sess.CostUsed += usage.Cost
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
		// Never discard this error. A dropped append is a hole in the audit
		// trail and a gap SSE replay cannot tell apart from "no activity",
		// which is exactly how the per-call sequence bug stayed invisible.
		if err := r.store.AppendRemediationEvent(ctx, &models.RemediationEvent{
			ID:          fmt.Sprintf("%s-%d", sessionID, seq),
			SessionID:   sessionID,
			Seq:         seq,
			Type:        e.Type,
			PayloadJSON: string(payload),
			CreatedAt:   time.Now(),
		}); err != nil {
			wolflog.L().Error().Err(err).
				Str("session", sessionID).Int("seq", seq).
				Msg("persist remediation event")
		}
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

func (r *Runner) transition(ctx context.Context, sess *models.RemediationSession, status models.RemediationStatus, reason string) error {
	sess.Status = status
	sess.FailureReason = reason
	sess.UpdatedAt = time.Now()
	switch status {
	case models.RemediationCompleted, models.RemediationFailed,
		models.RemediationExhausted, models.RemediationCancelled,
		models.RemediationRejected:
		now := time.Now()
		sess.CompletedAt = &now
	}
	return r.store.UpdateRemediationSession(ctx, sess)
}
