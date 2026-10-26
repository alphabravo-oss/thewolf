package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func restoreAttemptHistory(ctx context.Context, job *models.FixJob, deps *Deps) error {
	history, err := deps.Store.ListFixAttempts(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("load fix attempt history: %w", err)
	}
	deps.pendingAttempts = map[string]models.FixAttempt{}
	deps.attemptNumbers = map[string]int{}
	deps.priorAttempts = history
	for _, att := range history {
		deps.attemptNumbers[att.FindingID] = max(deps.attemptNumbers[att.FindingID], att.AttemptNo)
		if att.Outcome == models.FixOutcomeRunning {
			att.Outcome = models.FixOutcomeInterrupted
			att.DiffExcerpt = "Previous execution stopped before verification completed; workspace retained for re-verification."
			if err := deps.Store.UpdateFixAttempt(ctx, &att); err != nil {
				return fmt.Errorf("recover interrupted fix attempt: %w", err)
			}
		}
	}
	// CurrentLoop is saved before execution. Only a completed round proves the
	// loop finished; otherwise restart that loop rather than skip its work.
	if len(history) > 0 && job.ResumeAction != models.FixResumePush {
		var previous Summary
		if job.Summary != "" {
			if err := json.Unmarshal([]byte(job.Summary), &previous); err != nil {
				return fmt.Errorf("restore completed fix rounds: %w", err)
			}
		}
		completed := 0
		for _, round := range previous.Rounds {
			completed = max(completed, round.Round)
		}
		job.CurrentLoop = min(job.CurrentLoop, completed)
	}
	return nil
}

func beginAttempts(ctx context.Context, job *models.FixJob, deps Deps, findings []models.Finding, eng engine.SubprocessEngine) error {
	// A leftover finding sent to another turn gets a new attempt, even if the
	// previous engine left it untouched. No running row is silently replaced.
	if err := finishPendingAttempts(ctx, deps, false); err != nil {
		return err
	}
	for _, f := range findings {
		att := models.FixAttempt{JobID: job.ID, FindingID: f.ID, EngineUsed: engineUsedLabel(eng),
			Model: deps.Model, Outcome: models.FixOutcomeRunning, CreatedAt: time.Now().UTC()}
		if err := persist(ctx, deps, &att); err != nil {
			return err
		}
		deps.pendingAttempts[f.ID] = att
	}
	return nil
}

func finishPendingAttempts(ctx context.Context, deps Deps, interrupted bool) error {
	for id, att := range deps.pendingAttempts {
		att.Outcome = models.FixOutcomeUnverified
		att.DiffExcerpt = "Engine turn ended without an independently verified edit for this finding."
		if interrupted {
			att.Outcome = models.FixOutcomeInterrupted
			att.DiffExcerpt = "Execution stopped before verification completed; workspace retained."
		}
		att.DurationMS = int(time.Since(att.CreatedAt).Milliseconds())
		if err := deps.Store.UpdateFixAttempt(ctx, &att); err != nil {
			return fmt.Errorf("finalize unfinished fix attempt; workspace retained: %w", err)
		}
		delete(deps.pendingAttempts, id)
	}
	return nil
}
