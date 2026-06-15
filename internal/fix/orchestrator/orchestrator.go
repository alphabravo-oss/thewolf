// Package orchestrator runs one autonomous fix job end-to-end (design §9). It
// is the brain that ties together the pieces the earlier phases built: the
// writability preflight (Phase 2), the engine chain (Phase 3), and the
// workspace + verification gate (Phase 4). The worker (Phase 6) hands it a
// claimed models.FixJob; it produces a reviewable fix branch, a diff artifact,
// and a summary — and, in v1, STOPS there: no push, no PR.
//
// The load-bearing principle from the design is enforced here: success is
// judged by the verification gate over what landed on disk, NEVER by an
// engine's self-report. The orchestrator's job is the attempt → verify →
// keep-or-rollback → escalate cycle, per finding:
//
//	preflight writability        → fail fast with a reason if not fixable
//	prepare an isolated workspace → a new fix branch (worktree / clone-for-write)
//	for each finding (≥ severity floor, not triaged away):
//	  bounded prompt + engine.Fix → CLI edits in place, or API returns a diff
//	                                 the orchestrator git-applies
//	  verify.Gate                 → built? finding cleared? no regressions?
//	  keep on branch  OR  rollback + escalate to the next engine (more context),
//	                      up to job.MaxAttempts  OR  mark unfixable
//	  record a models.FixAttempt every time (the audit trail)
//	assemble the branch → persist the diff as an artifact + write a summary
//	v1: STOP (branch-only). No push. No PR.
//
// Budgets (iterations, per-fix timeout, wall-clock, cost) are enforced through
// internal/loop/budget. Every external dependency is injected as an interface
// so the worker wires the real implementations and tests inject stubs — no real
// agent CLIs, docker, or network in tests.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/loop/budget"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// defaultPerFixTimeout bounds a single finding's engine attempt when the job
// carries no explicit per-fix budget. It mirrors engine.DefaultTimeout.
const defaultPerFixTimeout = 5 * time.Minute

// defaultMaxAttempts is used when a job leaves MaxAttempts at zero. Each finding
// gets at least one engine attempt; escalation walks the engine chain up to this
// bound.
const defaultMaxAttempts = 3

// Workspace is the slice of a prepared working tree the orchestrator drives.
// The concrete *workspace.Workspace satisfies it; tests supply a fake. It is
// the union of what the orchestrator needs directly (Path/Branch/Diff/Cleanup)
// and what it hands to the verification gate (verify.Workspace).
type Workspace interface {
	verify.Workspace // Path, ChangedFiles, Rollback
	Branch() string
	Diff(ctx context.Context) (string, error)
	Cleanup(ctx context.Context) error
}

// WorkspacePreparer materialises an isolated, writable working tree on a NEW
// fix branch for the job's repo. Production wiring adapts
// workspace.Prepare; tests return a fake. branch is the fix branch to create.
type WorkspacePreparer interface {
	Prepare(ctx context.Context, repo *models.Repo, branch string) (Workspace, error)
}

// EngineChain is the ordered list of fix engines for a job (CLI-first,
// API-fallback). The orchestrator runs Current(); on a verification failure it
// calls Next() to escalate to the next tier. The concrete *engine.Chain
// satisfies it.
type EngineChain interface {
	Current() engine.SubprocessEngine
	Next() engine.SubprocessEngine
}

// EngineSelector builds the engine chain for a job. Production wiring calls
// engine.SelectEngine; tests inject a stub chain. Returning a fresh chain per
// call lets each finding start at the top tier.
type EngineSelector interface {
	Select(ctx context.Context, job *models.FixJob) (EngineChain, error)
}

// Verifier is the verification gate. The concrete implementation calls
// verify.Gate; tests stub it. It judges what landed on disk for one finding.
type Verifier interface {
	Verify(ctx context.Context, ws verify.Workspace, finding models.Finding) (*verify.VerifyResult, error)
}

// WritabilityChecker is the preflight: can wolf write a fix branch to this
// repo's source? Production wiring calls writability.Check; tests stub it.
type WritabilityChecker interface {
	Check(ctx context.Context, repo *models.Repo) (writable bool, reason string)
}

// DiffStore persists the assembled branch diff as a durable artifact and
// returns its ID. The orchestrator records the ID on the job. Production wiring
// writes to the artifact store; tests capture it in memory.
type DiffStore interface {
	SaveDiff(ctx context.Context, jobID, diff string) (artifactID string, err error)
}

// Store is the slice of db.Store the orchestrator needs: resolve the repo +
// findings, record every attempt, and persist the job's final state. The full
// db.Store satisfies it.
type Store interface {
	GetRepoByID(ctx context.Context, id string) (*models.Repo, error)
	GetFindingByID(ctx context.Context, id string) (*models.Finding, error)
	UpdateFixJob(ctx context.Context, job *models.FixJob) error
	CreateFixAttempt(ctx context.Context, attempt *models.FixAttempt) error
}

// Logf streams a human-readable progress line. The worker relays these to the
// server's SSE broker; tests pass a collector or nil. A nil Logf is a no-op.
type Logf func(format string, args ...any)

// Deps bundles the orchestrator's injected collaborators. Every field is an
// interface so the worker wires real implementations and tests inject stubs.
type Deps struct {
	Store       Store
	Writability WritabilityChecker
	Workspaces  WorkspacePreparer
	Engines     EngineSelector
	Verifier    Verifier
	GitApply    GitApplier // applies an API engine's returned diff; defaults to git apply
	Diffs       DiffStore
	Log         Logf
}

// GitApplier applies a unified diff to a workspace's working tree (for the API
// engine, which returns a diff instead of editing in place). Production wiring
// runs `git apply`; tests stub it.
type GitApplier interface {
	Apply(ctx context.Context, repoPath, diff string) error
}

// Result is the orchestrator's outcome for a job. It mirrors what gets persisted
// onto the FixJob (result branch, diff artifact, summary) plus the in-memory
// attempt records for callers/tests that want them without a store round-trip.
type Result struct {
	Branch         string
	DiffArtifactID string
	Summary        Summary
	Attempts       []models.FixAttempt
}

// Summary is the job-level rollup persisted as FixJob.Summary (JSON). It is the
// reviewable "what happened" the UI renders alongside the diff.
type Summary struct {
	TotalFindings int      `json:"total_findings"`
	Considered    int      `json:"considered"`
	Skipped       int      `json:"skipped"`
	Kept          int      `json:"kept"`
	Unfixable     int      `json:"unfixable"`
	Branch        string   `json:"branch"`
	StopReason    string   `json:"stop_reason,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

// Run executes one fix job to completion: preflight, prepare a workspace,
// attempt+verify+escalate per finding, assemble the branch, persist the diff
// and summary. It updates the job's status on the store as it goes and returns
// the Result. A non-nil error means the job FAILED before producing a branch
// (preflight or workspace failure); per-finding failures are normal outcomes
// recorded as attempts, not errors.
//
// v1 is branch-only: Run NEVER pushes or opens a PR.
func Run(ctx context.Context, job *models.FixJob, deps Deps) (*Result, error) {
	if job == nil {
		return nil, errors.New("orchestrator: job is required")
	}
	if deps.Store == nil {
		return nil, errors.New("orchestrator: store dependency is required")
	}
	if deps.Workspaces == nil || deps.Engines == nil || deps.Verifier == nil {
		return nil, errors.New("orchestrator: workspace, engine, and verifier dependencies are required")
	}
	logf := deps.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}

	now := time.Now().UTC()
	job.Status = models.FixJobRunning
	job.StartedAt = &now
	if err := deps.Store.UpdateFixJob(ctx, job); err != nil {
		return nil, fmt.Errorf("orchestrator: mark job running: %w", err)
	}

	res, runErr := run(ctx, job, deps, logf)
	finish := time.Now().UTC()
	job.FinishedAt = &finish

	if runErr != nil {
		job.Status = models.FixJobFailed
		job.Error = runErr.Error()
		// Best-effort persist of the failure; the original error is what matters.
		_ = deps.Store.UpdateFixJob(ctx, job)
		return res, runErr
	}

	job.Status = models.FixJobSucceeded
	job.ResultBranch = res.Branch
	job.DiffArtifactID = res.DiffArtifactID
	if data, err := json.Marshal(res.Summary); err == nil {
		job.Summary = string(data)
	}
	if err := deps.Store.UpdateFixJob(ctx, job); err != nil {
		return res, fmt.Errorf("orchestrator: persist job result: %w", err)
	}
	logf("job %s complete: %d kept, %d unfixable on branch %s",
		job.ID, res.Summary.Kept, res.Summary.Unfixable, res.Branch)
	return res, nil
}

// run is the core flow, factored out so Run can wrap it with status bookkeeping.
func run(ctx context.Context, job *models.FixJob, deps Deps, logf Logf) (*Result, error) {
	// --- Resolve the repo. ---
	repo, err := deps.Store.GetRepoByID(ctx, job.RepoID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load repo %s: %w", job.RepoID, err)
	}
	if repo == nil {
		return nil, fmt.Errorf("orchestrator: repo %s not found", job.RepoID)
	}

	// --- Preflight writability (fail fast with a reason). ---
	if deps.Writability != nil {
		writable, reason := deps.Writability.Check(ctx, repo)
		if !writable {
			logf("job %s: repo not fixable: %s", job.ID, reason)
			return nil, fmt.Errorf("orchestrator: repo not fixable: %s", reason)
		}
		logf("job %s: preflight ok (%s)", job.ID, reason)
	}

	// --- Prepare an isolated workspace on a new fix branch. ---
	branch := job.TargetBranch
	if strings.TrimSpace(branch) == "" {
		branch = fmt.Sprintf("wolf-fix/%s", job.ID)
	}
	ws, err := deps.Workspaces.Prepare(ctx, repo, branch)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: prepare workspace: %w", err)
	}
	defer func() { _ = ws.Cleanup(ctx) }()
	logf("job %s: workspace ready on branch %s", job.ID, ws.Branch())

	// --- Resolve the target findings, filtered by severity floor + triage. ---
	findings, err := storeFindings{deps.Store}.gatherFindings(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: gather findings: %w", err)
	}

	summary := Summary{TotalFindings: len(findings), Branch: ws.Branch()}
	floor := severityRank(models.Severity(job.SeverityFloor))

	// --- Budget tracker (iterations = findings considered). ---
	tracker := budget.New(budget.Ceilings{
		MaxIterations: maxIterations(job, len(findings)),
		PerInvocation: perFixTimeout(job),
	})

	var attempts []models.FixAttempt

	for _, f := range findings {
		// Severity floor + triage filter.
		if floor > 0 && severityRank(f.Severity) < floor {
			summary.Skipped++
			logf("finding %s: below severity floor (%s < %s), skipping", f.ID, f.Severity, job.SeverityFloor)
			continue
		}
		if triagedAway(f) {
			summary.Skipped++
			logf("finding %s: triaged away (%s), skipping", f.ID, f.Status)
			continue
		}

		// Budget gate before spending an iteration.
		if reason := tracker.StopReason(); reason != "" {
			summary.StopReason = reason
			summary.Notes = append(summary.Notes, "stopped early: "+reason)
			logf("job %s: %s — stopping", job.ID, reason)
			break
		}
		tracker.StartIteration()
		summary.Considered++

		att, outcome := fixOneFinding(ctx, job, ws, f, deps, tracker, logf)
		attempts = append(attempts, att...)
		switch outcome {
		case models.FixOutcomeKept:
			summary.Kept++
		case models.FixOutcomeUnfixable:
			summary.Unfixable++
		}
	}

	// --- Assemble the branch: persist the diff as an artifact + a summary. ---
	diff, derr := ws.Diff(ctx)
	if derr != nil {
		return nil, fmt.Errorf("orchestrator: assemble branch diff: %w", derr)
	}
	result := &Result{Branch: ws.Branch(), Summary: summary, Attempts: attempts}
	if deps.Diffs != nil {
		id, aerr := deps.Diffs.SaveDiff(ctx, job.ID, diff)
		if aerr != nil {
			return nil, fmt.Errorf("orchestrator: persist diff artifact: %w", aerr)
		}
		result.DiffArtifactID = id
	}
	logf("job %s: branch %s assembled (%d kept, %d unfixable)", job.ID, ws.Branch(), summary.Kept, summary.Unfixable)

	// v1: STOP HERE. No push, no PR. The deliverable is the branch + diff + summary.
	return result, nil
}

// fixOneFinding runs the attempt → verify → keep-or-rollback → escalate cycle
// for a single finding, walking the engine chain up to job.MaxAttempts. It
// records a models.FixAttempt for every engine attempt (the audit trail) and
// returns those records plus the finding's final outcome (kept | unfixable).
func fixOneFinding(
	ctx context.Context,
	job *models.FixJob,
	ws Workspace,
	finding models.Finding,
	deps Deps,
	tracker *budget.Tracker,
	logf Logf,
) ([]models.FixAttempt, string) {
	var records []models.FixAttempt

	chain, err := deps.Engines.Select(ctx, job)
	if err != nil {
		logf("finding %s: no engine available: %v", finding.ID, err)
		rec := models.FixAttempt{
			JobID:     job.ID,
			FindingID: finding.ID,
			AttemptNo: 1,
			Outcome:   models.FixOutcomeUnfixable,
			CreatedAt: time.Now().UTC(),
		}
		records = append(records, rec)
		persist(ctx, deps, &rec)
		return records, models.FixOutcomeUnfixable
	}

	maxAttempts := job.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}

	// Each attempt is persisted exactly once. We hold the attempt until we know
	// whether it kept (terminal) or rolled back (escalate). When the chain is
	// exhausted, the FINAL rolled-back attempt is upgraded to the terminal
	// unfixable verdict before it is persisted — so the audit trail carries one
	// row per engine attempt, the last reflecting the finding's resolution.
	eng := chain.Current()
	for attemptNo := 1; attemptNo <= maxAttempts && eng != nil; attemptNo++ {
		start := time.Now()
		logf("finding %s: attempt %d with engine %s", finding.ID, attemptNo, eng.Name())

		att := models.FixAttempt{
			JobID:      job.ID,
			FindingID:  finding.ID,
			AttemptNo:  attemptNo,
			EngineUsed: engineUsedLabel(eng),
			CreatedAt:  time.Now().UTC(),
		}

		outcome := runAttempt(ctx, ws, finding, eng, deps, tracker, &att, logf)
		att.DurationMS = int(time.Since(start).Milliseconds())

		if outcome == models.FixOutcomeKept {
			records = append(records, att)
			persist(ctx, deps, &att)
			logf("finding %s: KEPT (attempt %d, engine %s)", finding.ID, attemptNo, eng.Name())
			return records, models.FixOutcomeKept
		}

		// Rolled back: escalate to the next engine tier (more context / stronger
		// model / the other engine). When the chain is exhausted (no next tier or
		// max attempts reached), this rolled-back attempt is the terminal one and
		// becomes the unfixable verdict.
		eng = chain.Next()
		exhausted := eng == nil || attemptNo == maxAttempts
		if exhausted {
			att.Outcome = models.FixOutcomeUnfixable
			records = append(records, att)
			persist(ctx, deps, &att)
			logf("finding %s: UNFIXABLE after %d attempt(s)", finding.ID, attemptNo)
			return records, models.FixOutcomeUnfixable
		}

		records = append(records, att)
		persist(ctx, deps, &att)
		logf("finding %s: rolled back (attempt %d, engine %s); escalating", finding.ID, attemptNo, eng.Name())
	}

	// Defensive: loop fell through without a terminal verdict (e.g. maxAttempts
	// <= 0 was somehow passed). Treat as unfixable.
	return records, models.FixOutcomeUnfixable
}

// runAttempt performs one engine attempt and the verification gate for it,
// mutating att with the verify results and returning the outcome (kept |
// rolled_back). It NEVER trusts the engine's self-report: it applies the API
// engine's diff (when needed), then judges by verify.Gate, and rolls back on
// any failure.
func runAttempt(
	ctx context.Context,
	ws Workspace,
	finding models.Finding,
	eng engine.SubprocessEngine,
	deps Deps,
	tracker *budget.Tracker,
	att *models.FixAttempt,
	logf Logf,
) string {
	// Per-fix timeout from the budget.
	fixCtx := ctx
	if to := tracker.InvocationTimeout(); to > 0 {
		var cancel context.CancelFunc
		fixCtx, cancel = context.WithTimeout(ctx, to)
		defer cancel()
	}

	req := engine.FixRequest{
		Finding:  finding,
		RepoPath: ws.Path(),
		Timeout:  tracker.InvocationTimeout(),
	}
	fr, err := eng.Fix(fixCtx, req)
	if err != nil {
		logf("finding %s: engine %s error: %v", finding.ID, eng.Name(), err)
		att.Outcome = models.FixOutcomeRolledBack
		return models.FixOutcomeRolledBack
	}
	if fr == nil || !fr.Success {
		detail := ""
		if fr != nil {
			detail = fr.Error
		}
		logf("finding %s: engine %s did not produce a fix: %s", finding.ID, eng.Name(), detail)
		att.Outcome = models.FixOutcomeRolledBack
		return models.FixOutcomeRolledBack
	}

	// The API engine returns a diff WITHOUT touching the filesystem; apply it
	// ourselves before verifying. CLI engines edit in place (EditsInPlace=true).
	if !fr.EditsInPlace && strings.TrimSpace(fr.Diff) != "" {
		if deps.GitApply == nil {
			logf("finding %s: engine returned a diff but no git-apply backend is configured", finding.ID)
			att.Outcome = models.FixOutcomeRolledBack
			return models.FixOutcomeRolledBack
		}
		if aerr := deps.GitApply.Apply(fixCtx, ws.Path(), fr.Diff); aerr != nil {
			logf("finding %s: git apply failed: %v", finding.ID, aerr)
			att.Outcome = models.FixOutcomeRolledBack
			return models.FixOutcomeRolledBack
		}
	}

	// --- Verify by what landed on disk (the heart). ---
	vr, verr := deps.Verifier.Verify(fixCtx, ws, finding)
	if vr != nil {
		att.Built = vr.Built
		att.FindingCleared = vr.FindingCleared
		if vr.NewFindings {
			att.NewFindings = 1
		}
		att.FilesChanged = strings.Join(vr.ChangedFiles, ",")
	}
	if verr != nil || vr == nil || !vr.Passed {
		// Roll back EVERY file this attempt changed so a failed fix never
		// pollutes the branch or the next attempt.
		rollbackChanged(ctx, ws, vr, logf, finding.ID)
		att.Outcome = models.FixOutcomeRolledBack
		return models.FixOutcomeRolledBack
	}

	att.Outcome = models.FixOutcomeKept
	return models.FixOutcomeKept
}

// rollbackChanged restores each file a failed attempt touched. It prefers the
// verify result's changed-file list; when that's empty it falls back to the
// workspace's own changed-file enumeration so an engine that edited a file the
// gate didn't surface is still cleaned up.
func rollbackChanged(ctx context.Context, ws Workspace, vr *verify.VerifyResult, logf Logf, findingID string) {
	files := []string(nil)
	if vr != nil {
		files = vr.ChangedFiles
	}
	if len(files) == 0 {
		if cf, err := ws.ChangedFiles(ctx); err == nil {
			files = cf
		}
	}
	for _, f := range files {
		if err := ws.Rollback(ctx, f); err != nil {
			logf("finding %s: rollback %s failed: %v", findingID, f, err)
		}
	}
}

// gatherFindings resolves the job's target findings. It is a method on the
// Store-shaped value via a small adapter so callers needn't duplicate the
// finding-id loop. Defined here (not on the interface) so the interface stays
// minimal; it uses only GetFindingByID.
func (s storeFindings) gatherFindings(ctx context.Context, job *models.FixJob) ([]models.Finding, error) {
	ids := job.FindingIDList
	if len(ids) == 0 {
		ids = decodeFindingIDs(job.FindingIDs)
	}
	var out []models.Finding
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		f, err := s.GetFindingByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load finding %s: %w", id, err)
		}
		if f == nil {
			continue
		}
		out = append(out, *f)
	}
	return out, nil
}

// storeFindings adapts the Store interface so gatherFindings can hang off it.
type storeFindings struct{ Store }

// decodeFindingIDs parses a JSON array of finding IDs as stored on the job. A
// non-JSON value is treated as a single id (defensive).
func decodeFindingIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err == nil {
		return ids
	}
	return []string{raw}
}

// triagedAway reports whether a finding has been resolved/dismissed by triage so
// the orchestrator should not spend a fix on it.
func triagedAway(f models.Finding) bool {
	switch f.Status {
	case models.StatusFixed, models.StatusWontFix, models.StatusFalsePositive:
		return true
	default:
		return false
	}
}

// engineUsedLabel maps an engine to the FixAttempt.engine_used label
// (cli:<name> | api | custom:<cmd>).
func engineUsedLabel(eng engine.SubprocessEngine) string {
	name := eng.Name()
	switch name {
	case "api", "api-patch":
		return "api"
	case "claude-code", "codex":
		return "cli:" + name
	default:
		return name
	}
}

// maxIterations is the budget's iteration ceiling: the smaller of the job's
// MaxAttempts*findings envelope and the finding count, but at least 1.
func maxIterations(job *models.FixJob, findingCount int) int {
	if findingCount <= 0 {
		return 1
	}
	return findingCount
}

// perFixTimeout resolves the per-finding engine timeout for the budget.
func perFixTimeout(job *models.FixJob) time.Duration {
	return defaultPerFixTimeout
}

// persist records a FixAttempt via the store, swallowing the error since a
// failed audit-write must not abort the job. Each attempt is persisted exactly
// once, after its terminal outcome (kept | rolled_back | unfixable) is set.
func persist(ctx context.Context, deps Deps, att *models.FixAttempt) {
	if deps.Store == nil || att == nil {
		return
	}
	_ = deps.Store.CreateFixAttempt(ctx, att)
}

// severityRank returns a numeric rank for severity comparison; higher is more
// severe. An empty/unknown severity ranks 0 so a zero floor disables filtering.
func severityRank(s models.Severity) int {
	switch s {
	case models.SeverityCritical:
		return 5
	case models.SeverityHigh:
		return 4
	case models.SeverityMedium:
		return 3
	case models.SeverityLow:
		return 2
	case models.SeverityInfo:
		return 1
	default:
		return 0
	}
}
