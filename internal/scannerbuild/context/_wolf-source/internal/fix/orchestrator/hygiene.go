package orchestrator

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/fix/hygiene"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type storeSuppressor struct {
	ctx   context.Context
	store Store
}

func (s storeSuppressor) CreateFindingSuppression(sup *models.FindingSuppression) error {
	if s.store == nil || sup == nil {
		return nil
	}
	return s.store.CreateFindingSuppression(s.ctx, sup)
}

func suppressor(ctx context.Context, deps Deps) hygiene.SuppressionWriter {
	if deps.Store == nil {
		return nil
	}
	return storeSuppressor{ctx: ctx, store: deps.Store}
}

// applyToolHygiene runs the mechanical lint / bump / policy pass for one
// scanner. Leftover findings (not kept, not muted) should go to the code agent.
func applyToolHygiene(
	ctx context.Context,
	job *models.FixJob,
	ws Workspace,
	group toolGroup,
	deps Deps,
	logf Logf,
	rep *jobReport,
) (outcomes map[string]string, leftover []models.Finding, files []string) {
	outcomes = map[string]string{}
	if ws == nil {
		return outcomes, group.Findings, nil
	}
	kind := hygiene.Classify(group.Tool)
	var res hygiene.Result
	var err error
	switch kind {
	case hygiene.KindLint:
		res, err = hygiene.LintPass(ctx, ws.Path(), group.Tool, group.Findings)
	case hygiene.KindBump:
		res, err = hygiene.BumpPass(ctx, ws.Path(), group.Findings)
	case hygiene.KindPolicy:
		res, err = hygiene.PolicyPass(ws.Path(), group.Findings)
	default:
		return outcomes, group.Findings, nil
	}
	if err != nil {
		logf("  %s: hygiene %s failed: %v — sending leftovers to the agent", group.Tool, kind, err)
		return outcomes, group.Findings, nil
	}
	if res.Message != "" {
		logf("  %s: %s", group.Tool, res.Message)
	}

	for _, f := range group.Findings {
		if note, ok := res.Kept[f.ID]; ok {
			outcomes[f.ID] = models.FixOutcomeKept
			rep.note(f, models.FixOutcomeKept, note)
			persistHygiene(ctx, deps, job, f, models.FixOutcomeKept, "FIX: "+note)
			logf("decide FIX  %s — %s", f.ID, note)
			continue
		}
		if note, ok := res.Muted[f.ID]; ok {
			// Job-local mute only. Do not write a repo-wide rule
			// suppression — that hid renovate-* and yamllint rules
			// on every future scan.
			outcomes[f.ID] = models.FixOutcomeMuted
			rep.note(f, models.FixOutcomeMuted, note)
			persistHygiene(ctx, deps, job, f, models.FixOutcomeMuted, "MUTE: "+note)
			logf("decide MUTE %s — %s", f.ID, note)
			continue
		}
		leftover = append(leftover, f)
	}
	return outcomes, leftover, res.Files
}

func muteFinding(
	ctx context.Context,
	job *models.FixJob,
	ws Workspace,
	f models.Finding,
	reason string,
	deps Deps,
	rep *jobReport,
) []string {
	path := ""
	if ws != nil {
		path = ws.Path()
	}
	files, err := hygiene.Mute(path, job, f, reason, suppressor(ctx, deps))
	if err != nil {
		return nil
	}
	rep.note(f, models.FixOutcomeMuted, "muted as scanner noise: "+reason)
	return files
}

func persistHygiene(ctx context.Context, deps Deps, job *models.FixJob, f models.Finding, outcome, excerpt string) {
	if job == nil {
		return
	}
	att := models.FixAttempt{
		JobID:       job.ID,
		FindingID:   f.ID,
		AttemptNo:   1,
		EngineUsed:  "hygiene",
		Outcome:     outcome,
		DiffExcerpt: excerpt,
		CreatedAt:   time.Now().UTC(),
	}
	persist(ctx, deps, &att)
}
