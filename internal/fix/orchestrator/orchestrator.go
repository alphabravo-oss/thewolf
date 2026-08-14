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
// internal/fix/budget. Every external dependency is injected as an interface
// so the worker wires the real implementations and tests inject stubs — no real
// agent CLIs, docker, or network in tests.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/fix/budget"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/hygiene"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// defaultPerFixTimeout bounds a single-finding engine attempt when the job
// carries no explicit per-fix budget. It mirrors engine.DefaultTimeout.
const defaultPerFixTimeout = 5 * time.Minute

// defaultPerToolTimeout bounds one scanner's review+fix turn (every finding
// from that tool in one agent call). Silence is killed much sooner (4m) by
// the OpenCode stream watchdog; this is the hard cap.
const defaultPerToolTimeout = 25 * time.Minute

// defaultMaxAttempts is used when a job leaves MaxAttempts at zero. Each
// leftover finding after a tool turn can escalate to the next engine.
const defaultMaxAttempts = 3

// maxSameEngineLeftoverTurns is extra same-engine passes after a partial
// turn so leftover files get another look. Hands-off: keep going while
// the remaining set shrinks.
const maxSameEngineLeftoverTurns = 8

// defaultJobWallClock stops the job even if tools remain. Also applied as a
// context deadline so a single turn cannot overshoot.
const defaultJobWallClock = 6 * time.Hour

// Workspace is the slice of a prepared working tree the orchestrator drives.
// The concrete *workspace.Workspace satisfies it; tests supply a fake. It is
// the union of what the orchestrator needs directly (Path/Branch/Diff/Cleanup)
// and what it hands to the verification gate (verify.Workspace).
type Workspace interface {
	verify.Workspace // Path, ChangedFiles, Rollback
	Branch() string
	Diff(ctx context.Context) (string, error)
	Cleanup(ctx context.Context) error
	Commit(ctx context.Context, message string) error
	Push(ctx context.Context) (sha string, err error)
}

// WorkspacePreparer materialises an isolated, writable working tree on a NEW
// fix branch for the job's repo. Production wiring adapts
// workspace.Prepare; tests return a fake. branch is the fix branch to create.
type WorkspacePreparer interface {
	Prepare(ctx context.Context, repo *models.Repo, branch string) (Workspace, error)
	Open(ctx context.Context, path string, repo *models.Repo) (Workspace, error)
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
// verify.Gate / GateBatch; tests stub it. It judges what landed on disk.
type Verifier interface {
	Verify(ctx context.Context, ws verify.Workspace, finding models.Finding) (*verify.VerifyResult, error)
	VerifyBatch(ctx context.Context, ws verify.Workspace, findings []models.Finding) (map[string]*verify.VerifyResult, error)
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
	GetFixJobByID(ctx context.Context, id string) (*models.FixJob, error)
	GetFindingByID(ctx context.Context, id string) (*models.Finding, error)
	ListFindingsByScan(ctx context.Context, scanID string) ([]models.Finding, error)
	UpdateFixJob(ctx context.Context, job *models.FixJob) error
	CreateFixAttempt(ctx context.Context, attempt *models.FixAttempt) error
	GetRemediationByID(ctx context.Context, id string) (*models.Remediation, error)
	UpdateRemediation(ctx context.Context, rem *models.Remediation) error
	CreateFindingSuppression(ctx context.Context, s *models.FindingSuppression) error
}

// Rescanner re-runs scanners against the fix-branch worktree so the next
// loop (and the UI) can see what is still open. Optional: a nil Rescanner
// skips the branch rescan.
type Rescanner interface {
	Rescan(ctx context.Context, repoPath string, tools []string) ([]models.Finding, error)
}

// Logf streams a human-readable progress line. The worker relays these to the
// server's SSE broker; tests pass a collector or nil. A nil Logf is a no-op.
type Logf func(format string, args ...any)

// Deps bundles the orchestrator's injected collaborators. Every field is an
// interface so the worker wires real implementations and tests inject stubs.
type Deps struct {
	Store          Store
	Writability    WritabilityChecker
	Workspaces     WorkspacePreparer
	Engines        EngineSelector
	Verifier       Verifier
	GitApply       GitApplier // applies an API engine's returned diff; defaults to git apply
	Diffs          DiffStore
	Rescan         Rescanner
	CLIEnv         []string // API keys injected into CLI engines
	Model          string
	Effort         string
	Variant        string
	PromptInitial  string
	PromptFollowup string
	Log            Logf
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
	// PauseStatus is awaiting_review or awaiting_push when the job should
	// stop for a human instead of succeeding.
	PauseStatus string
}

// Summary is the job-level rollup persisted as FixJob.Summary (JSON). It is the
// reviewable "what happened" the UI renders alongside the diff.
type Summary struct {
	TotalFindings  int            `json:"total_findings"`
	Considered     int            `json:"considered"`
	Skipped        int            `json:"skipped"`
	Kept           int            `json:"kept"`
	Unfixable      int            `json:"unfixable"`
	Muted          int            `json:"muted,omitempty"`
	Remaining      int            `json:"remaining"`
	Loops          int            `json:"loops"`
	Pushed         bool           `json:"pushed,omitempty"`
	PushSHA        string         `json:"push_sha,omitempty"`
	Branch         string         `json:"branch"`
	StopReason     string         `json:"stop_reason,omitempty"`
	Notes          []string       `json:"notes,omitempty"`
	Rounds         []RoundSummary `json:"rounds,omitempty"`
	Tools          []ToolReport   `json:"tools,omitempty"`
	Open           []FindingNote  `json:"open,omitempty"`
	ReportMarkdown string         `json:"report_markdown,omitempty"`
}

// RoundSummary is one fix→rescan cycle inside a job.
type RoundSummary struct {
	Round     int      `json:"round"`
	Kept      int      `json:"kept"`
	Unfixable int      `json:"unfixable"`
	Remaining int      `json:"remaining"`
	Cleared   []string `json:"cleared,omitempty"`
	StillOpen []string `json:"still_open,omitempty"`
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

	if latest, _ := deps.Store.GetFixJobByID(ctx, job.ID); latest != nil && latest.Status == models.FixJobCancelled {
		logf("job %s: cancelled — discarding branch work", job.ID)
		discardCancelledWorkspace(ctx, job, deps, logf)
		job.Status = models.FixJobCancelled
		job.PauseReason = ""
		job.ResumeAction = ""
		job.ResultBranch = ""
		job.WorkspacePath = ""
		_ = deps.Store.UpdateFixJob(ctx, job)
		if runErr != nil {
			return res, runErr
		}
		return res, context.Canceled
	}

	if runErr != nil {
		job.Status = models.FixJobFailed
		job.Error = runErr.Error()
		// Persist even if the run context was cancelled (deploy, stall kill).
		_ = deps.Store.UpdateFixJob(context.WithoutCancel(ctx), job)
		return res, runErr
	}

	if res != nil && models.FixJobPaused(res.PauseStatus) {
		job.Status = res.PauseStatus
		job.FinishedAt = nil
	} else {
		job.Status = models.FixJobSucceeded
	}
	if res != nil {
		job.ResultBranch = res.Branch
		job.DiffArtifactID = res.DiffArtifactID
		job.Pushed = res.Summary.Pushed
		job.PushSHA = res.Summary.PushSHA
		if data, err := json.Marshal(res.Summary); err == nil {
			job.Summary = string(data)
		}
	}
	if err := deps.Store.UpdateFixJob(ctx, job); err != nil {
		return res, fmt.Errorf("orchestrator: persist job result: %w", err)
	}
	if job.Status == models.FixJobSucceeded {
		logf("job %s complete: %d kept, %d unfixable on branch %s",
			job.ID, res.Summary.Kept, res.Summary.Unfixable, res.Branch)
	} else {
		logf("job %s paused (%s): %s", job.ID, job.Status, job.PauseReason)
	}
	return res, nil
}

func discardCancelledWorkspace(ctx context.Context, job *models.FixJob, deps Deps, logf Logf) {
	if err := workspace.DiscardPath(ctx, job.WorkspacePath, job.ResultBranch); err != nil {
		logf("job %s: discard workspace: %v", job.ID, err)
	}
	if deps.Store == nil || job.RepoID == "" {
		return
	}
	repo, err := deps.Store.GetRepoByID(ctx, job.RepoID)
	if err != nil || repo == nil || repo.SourceType != models.SourceTypeLocal {
		return
	}
	if err := workspace.DiscardLocalBranch(ctx, repo.SourcePath, job.ResultBranch); err != nil {
		logf("job %s: discard local branch: %v", job.ID, err)
	}
}

// run is the core flow, factored out so Run can wrap it with status bookkeeping.
func run(ctx context.Context, job *models.FixJob, deps Deps, logf Logf) (*Result, error) {
	repo, err := deps.Store.GetRepoByID(ctx, job.RepoID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load repo %s: %w", job.RepoID, err)
	}
	if repo == nil {
		return nil, fmt.Errorf("orchestrator: repo %s not found", job.RepoID)
	}

	if deps.Writability != nil {
		writable, reason := deps.Writability.Check(ctx, repo)
		if !writable {
			logf("job %s: repo not fixable: %s", job.ID, reason)
			return nil, fmt.Errorf("orchestrator: repo not fixable: %s", reason)
		}
		logf("job %s: preflight ok (%s)", job.ID, reason)
	}

	branch := job.TargetBranch
	if strings.TrimSpace(branch) == "" {
		if job.ScanID != "" {
			branch = fmt.Sprintf("wolf-fix/%s", job.ScanID)
		} else {
			branch = fmt.Sprintf("wolf-fix/%s", job.ID)
		}
	}

	ws, resumed, err := openOrPrepare(ctx, job, repo, branch, deps)
	if err != nil {
		return nil, err
	}
	// A remediation owns the checkout until publish/discard.
	cleanup := !resumed && job.RemediationID == ""
	defer func() {
		if cleanup {
			_ = ws.Cleanup(ctx)
		}
	}()
	job.WorkspacePath = ws.Path()
	job.ResultBranch = ws.Branch()
	_ = deps.Store.UpdateFixJob(ctx, job)
	if job.RemediationID != "" {
		if rem, rerr := deps.Store.GetRemediationByID(ctx, job.RemediationID); rerr == nil && rem != nil {
			rem.WorkspacePath = ws.Path()
			rem.Branch = ws.Branch()
			_ = deps.Store.UpdateRemediation(ctx, rem)
		}
	}
	logf("job %s: workspace ready on branch %s", job.ID, ws.Branch())

	// Resume-to-push: human approved the branch; do not run more engines.
	if job.ResumeAction == models.FixResumePush {
		return finishWithPush(ctx, job, repo, ws, deps, logf, Summary{Branch: ws.Branch()}, nil, &cleanup)
	}

	findings, err := storeFindings{deps.Store}.gatherFindings(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: gather findings: %w", err)
	}

	// Job-level deadline so one mute turn cannot overshoot the wall.
	ctx, cancel := context.WithTimeout(ctx, defaultJobWallClock)
	defer cancel()

	summary := Summary{TotalFindings: len(findings), Branch: ws.Branch()}
	floor := severityRank(models.Severity(job.SeverityFloor))
	rep := newJobReport()

	active := make([]models.Finding, 0, len(findings))
	unfixable := map[string]bool{}
	for _, f := range findings {
		if f.Suppressed {
			summary.Skipped++
			rep.note(f, "open", "suppressed: "+f.SuppressedReason)
			logf("finding %s: suppressed (%s), skipping", f.ID, f.SuppressedReason)
			continue
		}
		if floor > 0 && severityRank(f.Severity) < floor {
			summary.Skipped++
			rep.note(f, "open", "below severity floor")
			logf("finding %s: below severity floor (%s < %s), skipping", f.ID, f.Severity, job.SeverityFloor)
			continue
		}
		if triagedAway(f) {
			summary.Skipped++
			rep.note(f, "open", "already triaged ("+string(f.Status)+")")
			logf("finding %s: triaged away (%s), skipping", f.ID, f.Status)
			continue
		}
		active = append(active, f)
	}
	// Hands-off: visit every scanner that still has findings. Iteration
	// count is not a product cap — 6h wall clock and 25m/tool are.
	tracker := budget.New(budget.Ceilings{
		MaxIterations: 1024,
		PerInvocation: defaultPerToolTimeout,
		WallClock:     defaultJobWallClock,
	})

	maxLoops := job.MaxLoops
	if maxLoops <= 0 {
		maxLoops = 1
	}
	startLoop := job.CurrentLoop
	if startLoop < 0 {
		startLoop = 0
	}
	if startLoop > 0 {
		seed := RoundSummary{Round: startLoop}
		active = rescanRound(ctx, ws, deps, logf, active, unfixable, &seed)
		logf("job %s: resumed after loop %d with %d findings still open", job.ID, startLoop, len(active))
	}

	var attempts []models.FixAttempt
	for loop := startLoop + 1; loop <= maxLoops && len(active) > 0; loop++ {
		job.CurrentLoop = loop
		summary.Loops = loop
		_ = deps.Store.UpdateFixJob(ctx, job)
		kind := "initial"
		if loop >= 2 {
			kind = "follow-up"
		}
		tools := groupFindingsByTool(active)
		logf("job %s: loop %d/%d (%d findings across %d tools, %s prompt) — agent reviews one scanner at a time",
			job.ID, loop, maxLoops, len(active), len(tools), kind)

		roundKept, roundUnfix := 0, 0
		var still []models.Finding
		fixedLocs := map[string]bool{}
		for i, group := range tools {
			if reason := tracker.StopReason(); reason != "" {
				summary.StopReason = reason
				summary.Notes = append(summary.Notes, "stopped early: "+reason)
				logf("job %s: %s — remaining tools deferred", job.ID, reason)
				for _, g := range tools[i:] {
					still = append(still, g.Findings...)
				}
				break
			}
			workList, overlap := splitAlreadyFixed(group.Findings, fixedLocs)
			if len(overlap) > 0 {
				logf("  %s: skipping %d finding(s) already fixed at this location this loop", group.Tool, len(overlap))
				for _, f := range overlap {
					logf("  skip %s — same location already fixed this loop", engine.FindingLabel(f))
					rep.note(f, "open", "same location already fixed this loop — confirm on rescan")
					still = append(still, f)
				}
			}
			if len(workList) == 0 {
				continue
			}
			group.Findings = workList
			tracker.StartIteration()
			summary.Considered += len(group.Findings)
			kind := hygiene.Classify(group.Tool)
			logf("job %s: tool %d/%d %s (%s) — %d findings (%d critical, %d high)",
				job.ID, i+1, len(tools), group.Tool, kind, len(group.Findings), group.Critical, group.High)

			outcomes := map[string]string{}
			work := group
			if kind != hygiene.KindCode {
				hyOut, leftover, files := applyToolHygiene(ctx, job, ws, group, deps, logf, rep)
				outcomes = hyOut
				if len(files) > 0 {
					scrubReviewFiles(ws.Path())
					msg := fmt.Sprintf("wolf-fix: %s hygiene (loop %d)", group.Tool, loop)
					if err := commitWorkspace(ctx, ws, msg, logf); err != nil {
						logf("  %s: hygiene commit failed: %v", group.Tool, err)
					} else {
						logf("  %s: committed hygiene files on %s", group.Tool, ws.Branch())
					}
				}
				if len(leftover) == 0 {
					rememberKeptLocations(fixedLocs, group.Findings, outcomes)
					foldOutcomes(group.Findings, outcomes, &summary, &still, unfixable, &roundKept, &roundUnfix, rep)
					continue
				}
				work.Findings = leftover
				logf("  %s: %d leftover finding(s) after hygiene — sending to the agent", group.Tool, len(leftover))
			}

			for _, f := range work.Findings {
				logf("  queued: %s", engine.FindingLabel(f))
			}
			att, more := fixOneTool(ctx, job, ws, work, deps, tracker, logf, loop, rep)
			attempts = append(attempts, att...)
			for id, o := range more {
				outcomes[id] = o
			}
			keptThisTool := 0
			for _, f := range group.Findings {
				if outcomes[f.ID] == models.FixOutcomeKept {
					keptThisTool++
				}
			}
			if keptThisTool > 0 {
				scrubReviewFiles(ws.Path())
				msg := fmt.Sprintf("wolf-fix: %s (loop %d)", group.Tool, loop)
				if err := commitWorkspace(ctx, ws, msg, logf); err != nil {
					logf("  %s: commit failed: %v", group.Tool, err)
				} else {
					logf("  %s: committed %d kept finding(s) on %s", group.Tool, keptThisTool, ws.Branch())
					if derr := persistDiff(ctx, job, ws, deps); derr != nil {
						logf("  %s: persist live diff: %v", group.Tool, derr)
					}
				}
			}
			rememberKeptLocations(fixedLocs, group.Findings, outcomes)
			foldOutcomes(group.Findings, outcomes, &summary, &still, unfixable, &roundKept, &roundUnfix, rep)
			persistLiveSummary(ctx, job, deps, summary, rep)
		}

		round := RoundSummary{Round: loop, Kept: roundKept, Unfixable: roundUnfix}
		active = rescanRound(ctx, ws, deps, logf, active, unfixable, &round)
		summary.Rounds = append(summary.Rounds, round)
		summary.Remaining = len(active)
		logf("job %s: loop %d rescan: %d remaining, %d cleared this round",
			job.ID, loop, round.Remaining, len(round.Cleared))

		if len(active) == 0 {
			break
		}
		if loop < maxLoops && job.HumanInTheLoop && job.ResumeAction != models.FixResumeContinue {
			job.PauseReason = fmt.Sprintf("loop %d complete; %d findings still open — continue or inspect", loop, len(active))
			_ = persistDiff(ctx, job, ws, deps)
			cleanup = false
			return &Result{
				Branch:         ws.Branch(),
				DiffArtifactID: job.DiffArtifactID,
				Summary:        summary,
				Attempts:       attempts,
				PauseStatus:    models.FixJobAwaitingReview,
			}, nil
		}
		job.ResumeAction = "" // consume a one-shot continue
	}

	summary.Tools = rep.tools()
	summary.Open = rep.openList()
	summary.ReportMarkdown = rep.markdown()
	for _, line := range strings.Split(strings.TrimSpace(summary.ReportMarkdown), "\n") {
		if strings.TrimSpace(line) != "" {
			logf("%s", line)
		}
	}

	return finishWithPush(ctx, job, repo, ws, deps, logf, summary, attempts, &cleanup)
}

func openOrPrepare(ctx context.Context, job *models.FixJob, repo *models.Repo, branch string, deps Deps) (Workspace, bool, error) {
	if job.WorkspacePath != "" && deps.Workspaces != nil {
		if ws, err := deps.Workspaces.Open(ctx, job.WorkspacePath, repo); err == nil && ws != nil {
			return ws, true, nil
		}
	}
	ws, err := deps.Workspaces.Prepare(ctx, repo, branch)
	if err != nil {
		return nil, false, fmt.Errorf("orchestrator: prepare workspace: %w", err)
	}
	return ws, false, nil
}

func finishWithPush(
	ctx context.Context,
	job *models.FixJob,
	repo *models.Repo,
	ws Workspace,
	deps Deps,
	logf Logf,
	summary Summary,
	attempts []models.FixAttempt,
	cleanup *bool,
) (*Result, error) {
	if err := persistDiff(ctx, job, ws, deps); err != nil {
		return nil, err
	}
	result := &Result{Branch: ws.Branch(), DiffArtifactID: job.DiffArtifactID, Summary: summary, Attempts: attempts}

	wantPush := job.Mode == models.FixModePush || job.ResumeAction == models.FixResumePush
	if wantPush {
		sha, err := ws.Push(ctx)
		if err != nil {
			if cleanup != nil {
				*cleanup = false
			}
			job.Error = err.Error()
			job.PauseReason = pushFailureReason(err)
			result.PauseStatus = models.FixJobPushFailed
			logf("job %s: GitHub push failed — fixes kept on %s: %s", job.ID, ws.Branch(), job.PauseReason)
			return result, nil
		}
		result.Summary.Pushed = true
		result.Summary.PushSHA = sha
		job.Pushed = true
		job.PushSHA = sha
		logf("job %s: pushed branch %s (%s)", job.ID, ws.Branch(), sha)
		return result, nil
	}

	if !wantPush && summary.Kept > 0 && repo != nil && repo.SourceType == models.SourceTypeGitHub {
		job.PauseReason = "verified branch is ready to push for review"
		result.PauseStatus = models.FixJobAwaitingPush
		if cleanup != nil {
			*cleanup = false
		}
		return result, nil
	}
	logf("job %s: branch %s assembled (%d kept, %d unfixable)", job.ID, ws.Branch(), summary.Kept, summary.Unfixable)
	return result, nil
}

func pushFailureReason(err error) string {
	if err == nil {
		return "GitHub push failed"
	}
	s := err.Error()
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "workflow") && strings.Contains(low, "scope"):
		return "GitHub rejected the push because this branch updates a workflow file. The stored token needs the workflow scope (classic PAT) or Workflows: Read and write (fine-grained). The fixes are still on the local branch — update the token in Settings → Secrets, then retry the push."
	case strings.Contains(low, "permission") || strings.Contains(low, "protected branch") || strings.Contains(low, "403"):
		return "GitHub rejected the push (permission). The token needs write access to repository contents. Fixes are still on the local branch."
	case strings.Contains(low, "authentication") || strings.Contains(low, "401"):
		return "GitHub rejected the token. Re-save a valid PAT in Settings → Secrets, then retry the push. Fixes are still on the local branch."
	default:
		return "GitHub push failed. The agent already kept the fixes on the local branch. " + strings.TrimSpace(s)
	}
}

func commitWorkspace(ctx context.Context, ws Workspace, msg string, logf Logf) error {
	if ws == nil {
		return nil
	}
	if restored := hygiene.RestoreProtected(ctx, ws.Path()); len(restored) > 0 && logf != nil {
		logf("  restored protected Helm/API files: %s", strings.Join(restored, ", "))
	}
	return ws.Commit(ctx, msg)
}

func persistDiff(ctx context.Context, job *models.FixJob, ws Workspace, deps Deps) error {
	ctx = context.WithoutCancel(ctx)
	if ws != nil {
		scrubReviewFiles(ws.Path())
		_ = hygiene.RestoreProtected(ctx, ws.Path())
	}
	diff, err := ws.Diff(ctx)
	if err != nil {
		return fmt.Errorf("orchestrator: assemble branch diff: %w", err)
	}
	if deps.Diffs == nil {
		return nil
	}
	id, err := deps.Diffs.SaveDiff(ctx, job.ID, diff)
	if err != nil {
		return fmt.Errorf("orchestrator: persist diff artifact: %w", err)
	}
	job.DiffArtifactID = id
	return nil
}

func rescanRound(
	ctx context.Context,
	ws Workspace,
	deps Deps,
	logf Logf,
	active []models.Finding,
	unfixable map[string]bool,
	round *RoundSummary,
) []models.Finding {
	if deps.Rescan == nil || len(active) == 0 {
		var still []models.Finding
		for _, f := range active {
			if !unfixable[f.ID] {
				still = append(still, f)
				round.StillOpen = append(round.StillOpen, f.ID)
			}
		}
		round.Remaining = len(still)
		return still
	}
	tools := uniqueTools(active)
	reported, err := deps.Rescan.Rescan(ctx, ws.Path(), tools)
	if err != nil {
		logf("rescan failed: %v — treating all non-unfixable as still open", err)
		var still []models.Finding
		for _, f := range active {
			if !unfixable[f.ID] {
				still = append(still, f)
				round.StillOpen = append(round.StillOpen, f.ID)
			}
		}
		round.Remaining = len(still)
		return still
	}
	present := map[string]bool{}
	for _, f := range reported {
		present[findingKey(f)] = true
	}
	var still []models.Finding
	for _, f := range active {
		if unfixable[f.ID] {
			continue
		}
		if present[findingKey(f)] {
			still = append(still, f)
			round.StillOpen = append(round.StillOpen, f.ID)
			continue
		}
		round.Cleared = append(round.Cleared, f.ID)
	}
	round.Remaining = len(still)
	return still
}

func uniqueTools(findings []models.Finding) []string {
	seen := map[string]bool{}
	var tools []string
	for _, f := range findings {
		if f.ToolName == "" || seen[f.ToolName] {
			continue
		}
		seen[f.ToolName] = true
		tools = append(tools, f.ToolName)
	}
	return tools
}

func findingKey(f models.Finding) string {
	return strings.ToLower(f.ToolName) + "|" + f.RuleID + "|" + f.FilePath
}

// locationKey is the same line of code across scanners. Tool and rule are
// ignored so a Bearer keep can skip a later Semgrep hit on that line.
// Line 0 / empty path are not locations — those findings stay distinct.
func locationKey(f models.Finding) string {
	path := strings.TrimSpace(f.FilePath)
	path = strings.TrimPrefix(path, "./")
	path = filepath.ToSlash(strings.ToLower(path))
	if path == "" || f.LineStart <= 0 {
		return ""
	}
	return path + "\x00" + strconv.Itoa(f.LineStart)
}

func splitAlreadyFixed(findings []models.Finding, locs map[string]bool) (work, overlap []models.Finding) {
	if len(locs) == 0 {
		return findings, nil
	}
	for _, f := range findings {
		if k := locationKey(f); k != "" && locs[k] {
			overlap = append(overlap, f)
			continue
		}
		work = append(work, f)
	}
	return work, overlap
}

func rememberKeptLocations(locs map[string]bool, findings []models.Finding, outcomes map[string]string) {
	for _, f := range findings {
		if outcomes[f.ID] != models.FixOutcomeKept {
			continue
		}
		if k := locationKey(f); k != "" {
			locs[k] = true
		}
	}
}

func commitMessage(f models.Finding) string {
	title := strings.TrimSpace(f.Title)
	if title == "" {
		title = f.RuleID
	}
	if title == "" {
		title = f.ID
	}
	return fmt.Sprintf("fix(%s): %s in %s:%d", f.ToolName, title, f.FilePath, f.LineStart)
}

// fixOneTool gives the engine every finding from one scanner, then classifies
// each as SKIP / FIX from the model's output and a targeted verify.
func fixOneTool(
	ctx context.Context,
	job *models.FixJob,
	ws Workspace,
	group toolGroup,
	deps Deps,
	tracker *budget.Tracker,
	logf Logf,
	loop int,
	rep *jobReport,
) ([]models.FixAttempt, map[string]string) {
	outcomes := map[string]string{}
	var records []models.FixAttempt

	take := append([]models.Finding(nil), group.Findings...)
	sortFindings(take)

	reviewRel, reviewFile, err := writeFindingsReview(ws.Path(), group.Tool, take)
	if err != nil {
		logf("  %s: could not write review file: %v", group.Tool, err)
	} else {
		logf("  %s: review file %s (%s)", group.Tool, reviewRel, reviewFile)
	}
	defer scrubReviewFiles(ws.Path())

	chain, err := deps.Engines.Select(ctx, job)
	if err != nil {
		logf("  %s: no engine available: %v", group.Tool, err)
		for _, f := range take {
			rec := models.FixAttempt{
				JobID: job.ID, FindingID: f.ID, AttemptNo: 1,
				Outcome: models.FixOutcomeUnfixable, CreatedAt: time.Now().UTC(),
				DiffExcerpt: "SKIP: no engine available",
			}
			records = append(records, rec)
			persist(ctx, deps, &rec)
			outcomes[f.ID] = models.FixOutcomeUnfixable
			rep.note(f, models.FixOutcomeUnfixable, "no engine available")
			logf("decide SKIP %s — no engine available", engine.FindingLabel(f))
		}
		return records, outcomes
	}

	maxAttempts := job.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}

	remaining := append([]models.Finding(nil), take...)
	eng := chain.Current()
	attemptNo := 1
	leftoverTurns := 0
	for eng != nil && len(remaining) > 0 && attemptNo <= maxAttempts {
		logf("  %s: attempt %d with engine %s (%d findings)", group.Tool, attemptNo, eng.Name(), len(remaining))

		work := remaining

		fixTO := tracker.InvocationTimeout()
		if fixTO <= 0 || fixTO > defaultPerToolTimeout {
			fixTO = defaultPerToolTimeout
		}
		fixCtx, cancel := context.WithTimeout(ctx, fixTO)
		start := time.Now()
		req := engine.FixRequest{
			Finding:      work[0],
			Findings:     work,
			FindingsFile: reviewRel,
			Tool:         group.Tool,
			RepoPath:     ws.Path(),
			Timeout:      fixTO,
			Env:          deps.CLIEnv,
			Model:        deps.Model,
			Effort:       deps.Effort,
			Variant:      deps.Variant,
			Phase:        "fix",
			Instructions: engine.InstructionForLoop(loop, deps.PromptInitial, deps.PromptFollowup),
			Progress: func(msg string) {
				logf("  opencode: %s", msg)
			},
		}
		fr, _ := eng.Fix(fixCtx, req)
		cancel()
		elapsed := int(time.Since(start).Milliseconds())

		if fr != nil && len(work) > 15 && engine.CountTaskToolEvents(fr.Output) == 0 {
			logf("  %s: warning: no task events on a large batch (%d findings)", group.Tool, len(work))
		}

		skipBy, fixBy := map[string]string{}, map[string]string{}
		if fr != nil {
			for _, d := range engine.ParseDecisions(fr.Output) {
				id := engine.MatchDecisionID(d.ID, work)
				if id == "" && len(work) == 1 {
					id = work[0].ID
				}
				if id == "" {
					continue
				}
				if d.Kind == "skip" {
					skipBy[id] = d.Note
				}
				if d.Kind == "fix" {
					fixBy[id] = d.Note
				}
			}
			if fr.Skipped && len(work) == 1 {
				reason := fr.SkipReason
				if reason == "" {
					reason = fr.Error
				}
				skipBy[work[0].ID] = reason
			}
		}

		if fr != nil && strings.TrimSpace(fr.Output) != "" && (len(skipBy)+len(fixBy) == 0) {
			logf("  %s: engine output (no SKIP/FIX lines):\n%s", group.Tool, clipLog(fr.Output, 1500))
		}
		if fr != nil && strings.TrimSpace(fr.Error) != "" {
			logf("  %s: engine error: %s", group.Tool, clipLog(fr.Error, 400))
		}
		changedN := 0
		if fr != nil {
			changedN = len(fr.FilesChanged)
		}
		if changedN > 0 {
			formatChangedGo(ctx, ws.Path(), fr.FilesChanged)
		}
		stalled := fr != nil && engine.IsStallMessage(fr.Error)
		if stalled {
			logf("  %s: stall — this scanner turn failed; moving on", group.Tool)
		}
		if changedN > 0 && len(skipBy) == 0 && len(fixBy) == 0 {
			logf("  %s: engine edited %d files without FIX/SKIP lines — verifying touched findings", group.Tool, changedN)
		}
		silent := len(skipBy) == 0 && len(fixBy) == 0 && changedN == 0
		if silent {
			why := "edit turn produced no FIX/SKIP lines and no file changes"
			if stalled {
				why = "edit turn stalled (no OpenCode events)"
			}
			for _, f := range work {
				rep.note(f, "open", why)
			}
			logf("  %s: engine produced no decisions and no file changes — leaving the %d findings open", group.Tool, len(work))
			if stalled {
				remaining = nil
				break
			}
			eng = chain.Next()
			attemptNo++
			if eng == nil || attemptNo > maxAttempts {
				logf("  %s: leaving %d findings open after a silent engine turn", group.Tool, len(work))
				remaining = nil
			}
			continue
		}

		var toVerify []models.Finding
		notes := map[string]string{}
		var next []models.Finding
		var untouched []models.Finding
		for _, f := range work {
			if reason, ok := skipBy[f.ID]; ok {
				att := baseAttempt(job, f, attemptNo, eng, deps, fr, elapsed)
				logf("decide SKIP %s — %s", engine.FindingLabel(f), reason)
				if hygiene.NoiseReason(reason) {
					muteFinding(ctx, job, ws, f, reason, deps, rep)
					att.Outcome = models.FixOutcomeMuted
					att.DiffExcerpt = "MUTE: " + reason
					outcomes[f.ID] = models.FixOutcomeMuted
					logf("  muted %s so the next scan stays clean", engine.FindingLabel(f))
				} else {
					att.Outcome = models.FixOutcomeUnfixable
					att.DiffExcerpt = "SKIP: " + reason
					outcomes[f.ID] = models.FixOutcomeUnfixable
					rep.note(f, models.FixOutcomeUnfixable, "agent skipped: "+reason)
				}
				records = append(records, att)
				persist(ctx, deps, &att)
				continue
			}
			touched := findingTouched(f, fr)
			note, markedFix := fixBy[f.ID]
			if !markedFix && !touched {
				untouched = append(untouched, f)
				continue
			}
			if strings.TrimSpace(f.FilePath) == "" {
				logf("decide FIX  %s — empty file_path, skipping verify/rollback", engine.FindingLabel(f))
				rep.note(f, "open", "no file_path — cannot verify a code edit")
				continue
			}
			if markedFix {
				logf("decide FIX  %s — %s (verifying)", engine.FindingLabel(f), note)
				notes[f.ID] = note
			} else {
				logf("decide FIX  %s — files changed, verifying", engine.FindingLabel(f))
			}
			if fr != nil && !fr.EditsInPlace && strings.TrimSpace(fr.Diff) != "" && deps.GitApply != nil {
				if aerr := deps.GitApply.Apply(ctx, ws.Path(), fr.Diff); aerr != nil {
					logf("decide FIX  %s — git apply failed: %v", engine.FindingLabel(f), aerr)
					att := baseAttempt(job, f, attemptNo, eng, deps, fr, elapsed)
					att.Outcome = models.FixOutcomeRolledBack
					records = append(records, att)
					persist(ctx, deps, &att)
					next = append(next, f)
					continue
				}
			}
			toVerify = append(toVerify, f)
		}

		batch := map[string]*verify.VerifyResult{}
		var batchErr error
		if len(toVerify) > 0 {
			batch, batchErr = deps.Verifier.VerifyBatch(ctx, ws, toVerify)
			if batchErr != nil {
				logf("  %s: verify batch error: %v", group.Tool, batchErr)
			}
		}
		for _, f := range toVerify {
			att := baseAttempt(job, f, attemptNo, eng, deps, fr, elapsed)
			vr := batch[f.ID]
			if vr != nil {
				att.Built = vr.Built
				att.FindingCleared = vr.FindingCleared
				if vr.NewFindings {
					att.NewFindings = 1
				}
				if f.FilePath != "" {
					att.FilesChanged = f.FilePath
				} else {
					att.FilesChanged = strings.Join(vr.ChangedFiles, ",")
				}
			}
			switch verifyVerdict(vr, batchErr) {
			case verifyRollback:
				why := verifyWhy(vr, batchErr)
				logf("  %s: verify rollback %s — %s", group.Tool, engine.FindingLabel(f), why)
				if rerr := ws.Rollback(ctx, f.FilePath); rerr != nil {
					logf("finding %s: rollback %s failed: %v", f.ID, f.FilePath, rerr)
				}
				att.Outcome = models.FixOutcomeRolledBack
				att.DiffExcerpt = "rolled back: " + why
				records = append(records, att)
				persist(ctx, deps, &att)
				rep.note(f, "open", "edited but "+why+" — rolled back")
				next = append(next, f)
			case verifyUnjudged:
				att.Outcome = models.FixOutcomeKept
				att.DiffExcerpt = "FIX (unverified): built; scanner could not confirm — left on the branch"
				if note := notes[f.ID]; note != "" {
					att.DiffExcerpt = "FIX (unverified): " + note
				}
				records = append(records, att)
				persist(ctx, deps, &att)
				outcomes[f.ID] = models.FixOutcomeKept
				rep.note(f, models.FixOutcomeKept, "edited and built; scanner could not verify — kept")
				logf("decide KEEP %s — built, scanner could not verify — left on the branch", engine.FindingLabel(f))
			default:
				att.Outcome = models.FixOutcomeKept
				if note := notes[f.ID]; note != "" {
					att.DiffExcerpt = "FIX: " + note
				}
				records = append(records, att)
				persist(ctx, deps, &att)
				outcomes[f.ID] = models.FixOutcomeKept
			}
		}

		if stalled {
			remaining = nil
			break
		}
		remaining = append(append([]models.Finding(nil), next...), untouched...)
		if len(remaining) == 0 {
			break
		}

		// Partial coverage: send leftovers back to the same engine so we
		// actually look at every file, not just the ones this turn edited.
		if len(untouched) > 0 && len(remaining) < len(work) && leftoverTurns < maxSameEngineLeftoverTurns {
			leftoverTurns++
			if rel, _, err := writeFindingsReview(ws.Path(), group.Tool, remaining); err == nil {
				reviewRel = rel
			}
			logf("  %s: %d leftover after partial turn — sending back to the agent", group.Tool, len(remaining))
			continue
		}

		for _, f := range untouched {
			rep.note(f, "open", "edit turn did not touch this finding")
		}
		if len(next) == 0 {
			if len(untouched) > 0 {
				logf("  %s: leaving %d finding(s) open after partial coverage", group.Tool, len(untouched))
			}
			remaining = nil
			continue
		}

		remaining = next
		last := eng
		eng = chain.Next()
		attemptNo++
		if eng == nil || attemptNo > maxAttempts {
			// Leftover requeues already used this engine. Leave rollbacks
			// open for the next loop instead of marking them unfixable.
			if leftoverTurns > 0 {
				for _, f := range remaining {
					rep.note(f, "open", "still open after this scanner pass")
				}
				logf("  %s: leaving %d finding(s) open after leftover turns", group.Tool, len(remaining))
				remaining = nil
				continue
			}
			for _, f := range remaining {
				label := ""
				if last != nil {
					label = engineUsedLabel(last)
				}
				att := models.FixAttempt{
					JobID: job.ID, FindingID: f.ID, AttemptNo: attemptNo,
					EngineUsed:  label,
					Outcome:     models.FixOutcomeUnfixable,
					CreatedAt:   time.Now().UTC(),
					DiffExcerpt: "SKIP: still open after engine chain",
				}
				records = append(records, att)
				persist(ctx, deps, &att)
				outcomes[f.ID] = models.FixOutcomeUnfixable
				logf("decide SKIP %s — still open after engine chain", engine.FindingLabel(f))
			}
			remaining = nil
		}
	}
	return records, outcomes
}

func baseAttempt(job *models.FixJob, f models.Finding, attemptNo int, eng engine.SubprocessEngine, deps Deps, fr *engine.FixResult, elapsed int) models.FixAttempt {
	att := models.FixAttempt{
		JobID:      job.ID,
		FindingID:  f.ID,
		AttemptNo:  attemptNo,
		EngineUsed: engineUsedLabel(eng),
		CreatedAt:  time.Now().UTC(),
		DurationMS: elapsed,
	}
	if fr != nil {
		if fr.Usage.Model != "" {
			att.Model = fr.Usage.Model
		} else if deps.Model != "" {
			att.Model = deps.Model
		}
		att.CostUSD = fr.Usage.CostUSD
		att.InputTokens = fr.Usage.InputTokens
		att.OutputTokens = fr.Usage.OutputTokens
	}
	return att
}

type verifyDecision int

const (
	verifyKeep verifyDecision = iota
	verifyUnjudged
	verifyRollback
)

// verifyVerdict keeps a build-clean edit when the scanner cannot judge
// it (missing image, transport error). Rollback only when the build
// broke or a successful rescan still sees the finding / a regression.
func verifyWhy(vr *verify.VerifyResult, batchErr error) string {
	if vr != nil {
		for _, s := range vr.Stages {
			if !s.Skipped && !s.Passed && s.Detail != "" {
				return string(s.Stage) + ": " + s.Detail
			}
		}
		if vr.BuildFailed() {
			return "build failed"
		}
		if vr.UnableToVerify {
			return "scanner could not verify"
		}
		if !vr.FindingCleared {
			return "finding still present after fix"
		}
	}
	if batchErr != nil {
		return batchErr.Error()
	}
	return "verify did not pass"
}

func verifyVerdict(vr *verify.VerifyResult, batchErr error) verifyDecision {
	if vr != nil && vr.BuildFailed() {
		return verifyRollback
	}
	if batchErr != nil {
		return verifyUnjudged
	}
	if vr == nil {
		return verifyUnjudged
	}
	if vr.UnableToVerify {
		return verifyUnjudged
	}
	if vr.Passed {
		return verifyKeep
	}
	return verifyRollback
}

func foldOutcomes(
	findings []models.Finding,
	outcomes map[string]string,
	summary *Summary,
	still *[]models.Finding,
	unfixable map[string]bool,
	roundKept, roundUnfix *int,
	rep *jobReport,
) {
	for _, f := range findings {
		switch outcomes[f.ID] {
		case models.FixOutcomeKept:
			summary.Kept++
			*roundKept++
			if _, ok := rep.notes[f.ID]; !ok {
				rep.note(f, models.FixOutcomeKept, "verified and kept on the branch")
			}
		case models.FixOutcomeMuted:
			summary.Muted++
			unfixable[f.ID] = true
			if _, ok := rep.notes[f.ID]; !ok {
				rep.note(f, models.FixOutcomeMuted, "muted")
			}
		case models.FixOutcomeUnfixable:
			summary.Unfixable++
			*roundUnfix++
			unfixable[f.ID] = true
			if _, ok := rep.notes[f.ID]; !ok {
				rep.note(f, models.FixOutcomeUnfixable, "marked unfixable")
			}
		default:
			*still = append(*still, f)
			if _, ok := rep.notes[f.ID]; !ok {
				rep.note(f, "open", "left open")
			}
		}
	}
}

var formatChangedGo = func(ctx context.Context, repoPath string, files []string) {
	var goFiles []string
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			goFiles = append(goFiles, f)
		}
	}
	if len(goFiles) == 0 || repoPath == "" {
		return
	}
	args := append([]string{"-w"}, goFiles...)
	bin := "gofmt"
	if _, err := exec.LookPath("goimports"); err == nil {
		bin = "goimports"
	}
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204
	cmd.Dir = repoPath
	_ = cmd.Run()
}

func findingTouched(f models.Finding, fr *engine.FixResult) bool {
	if fr == nil {
		return false
	}
	for _, p := range fr.FilesChanged {
		if p == f.FilePath || strings.HasSuffix(p, "/"+f.FilePath) || strings.HasSuffix(f.FilePath, "/"+p) {
			return true
		}
	}
	return false
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
	loop int,
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

		outcome := runAttempt(ctx, ws, finding, eng, deps, tracker, &att, logf, loop)
		att.DurationMS = int(time.Since(start).Milliseconds())

		if outcome == models.FixOutcomeKept {
			records = append(records, att)
			persist(ctx, deps, &att)
			logf("finding %s: KEPT (attempt %d, engine %s)", finding.ID, attemptNo, eng.Name())
			return records, models.FixOutcomeKept
		}
		if outcome == models.FixOutcomeUnfixable {
			records = append(records, att)
			persist(ctx, deps, &att)
			logf("finding %s: skipped as not a real/fixable issue (engine %s)", finding.ID, eng.Name())
			return records, models.FixOutcomeUnfixable
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
	loop int,
) string {
	// Per-fix timeout from the budget.
	fixCtx := ctx
	if to := tracker.InvocationTimeout(); to > 0 {
		var cancel context.CancelFunc
		fixCtx, cancel = context.WithTimeout(ctx, to)
		defer cancel()
	}

	req := engine.FixRequest{
		Finding:      finding,
		RepoPath:     ws.Path(),
		Timeout:      tracker.InvocationTimeout(),
		Env:          deps.CLIEnv,
		Model:        deps.Model,
		Effort:       deps.Effort,
		Variant:      deps.Variant,
		Instructions: engine.InstructionForLoop(loop, deps.PromptInitial, deps.PromptFollowup),
	}
	fr, err := eng.Fix(fixCtx, req)
	if err != nil {
		logf("finding %s: engine %s error: %v", finding.ID, eng.Name(), err)
		att.Outcome = models.FixOutcomeRolledBack
		return models.FixOutcomeRolledBack
	}
	if fr != nil {
		if fr.Usage.Model != "" {
			att.Model = fr.Usage.Model
		} else if deps.Model != "" {
			att.Model = deps.Model
		}
		att.CostUSD = fr.Usage.CostUSD
		att.InputTokens = fr.Usage.InputTokens
		att.OutputTokens = fr.Usage.OutputTokens
	}
	if fr != nil && fr.Skipped {
		reason := fr.SkipReason
		if reason == "" {
			reason = fr.Error
		}
		logf("finding %s: engine %s skipped (not a real/fixable issue): %s", finding.ID, eng.Name(), reason)
		att.Outcome = models.FixOutcomeUnfixable
		return models.FixOutcomeUnfixable
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
		if finding.FilePath != "" {
			att.FilesChanged = finding.FilePath
		} else {
			att.FilesChanged = strings.Join(vr.ChangedFiles, ",")
		}
	}
	switch verifyVerdict(vr, verr) {
	case verifyRollback:
		rollbackChanged(ctx, ws, vr, logf, finding.ID)
		att.Outcome = models.FixOutcomeRolledBack
		return models.FixOutcomeRolledBack
	case verifyUnjudged:
		att.Outcome = models.FixOutcomeKept
		att.DiffExcerpt = "FIX (unverified): built; scanner could not confirm — left on the branch"
		return models.FixOutcomeKept
	default:
		att.Outcome = models.FixOutcomeKept
		return models.FixOutcomeKept
	}
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
	if len(ids) == 0 && job.ScanID != "" {
		return s.ListFindingsByScan(ctx, job.ScanID)
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
	case "claude-code", "codex", "opencode":
		return "cli:" + name
	case "grok", "xai":
		return "api:grok"
	default:
		return name
	}
}

type toolGroup struct {
	Tool     string
	Findings []models.Finding
	Critical int
	High     int
	Medium   int
}

// groupFindingsByTool lists every finding from a scanner as one turn.
// Lint/format groups run first, then bumps, then policy, then the code
// agent. Within a kind, scanners with more critical/high findings go first.
func groupFindingsByTool(findings []models.Finding) []toolGroup {
	index := map[string]int{}
	var groups []toolGroup
	for _, f := range findings {
		tool := strings.TrimSpace(f.ToolName)
		if tool == "" {
			tool = "unknown"
		}
		i, ok := index[tool]
		if !ok {
			i = len(groups)
			index[tool] = i
			groups = append(groups, toolGroup{Tool: tool})
		}
		groups[i].Findings = append(groups[i].Findings, f)
		switch models.Severity(strings.ToLower(string(f.Severity))) {
		case models.SeverityCritical:
			groups[i].Critical++
		case models.SeverityHigh:
			groups[i].High++
		case models.SeverityMedium:
			groups[i].Medium++
		}
	}
	for i := range groups {
		sortFindings(groups[i].Findings)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if ka, kb := hygiene.KindRank(a.Tool), hygiene.KindRank(b.Tool); ka != kb {
			return ka < kb
		}
		if a.Critical != b.Critical {
			return a.Critical > b.Critical
		}
		if a.High != b.High {
			return a.High > b.High
		}
		if a.Medium != b.Medium {
			return a.Medium > b.Medium
		}
		if len(a.Findings) != len(b.Findings) {
			return len(a.Findings) > len(b.Findings)
		}
		return a.Tool < b.Tool
	})
	return groups
}

func writeFindingsReview(wsPath, tool string, findings []models.Finding) (rel, abs string, err error) {
	dir := filepath.Join(wsPath, ".wolf", "findings")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", err
	}
	var b strings.Builder
	for _, r := range tool {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	name := b.String()
	if name == "" {
		name = "tool"
	}
	abs = filepath.Join(dir, name+".md")
	rel = filepath.ToSlash(filepath.Join(".wolf", "findings", name+".md"))
	return rel, abs, os.WriteFile(abs, []byte(engine.FormatFindingsFile(tool, findings)), 0o600)
}

func scrubReviewFiles(wsPath string) {
	if strings.TrimSpace(wsPath) == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(wsPath, ".wolf"))
}

func clipLog(s string, max int) string {
	s = strings.TrimSpace(s)
	if max > 0 && len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func sortFindings(findings []models.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra > rb
		}
		if a.CompositeScore != b.CompositeScore {
			return a.CompositeScore > b.CompositeScore
		}
		if ca, cb := confidenceRank(a.Confidence), confidenceRank(b.Confidence); ca != cb {
			return ca > cb
		}
		return len(a.CorroboratedBy) > len(b.CorroboratedBy)
	})
}

func confidenceRank(c string) int {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// perFixTimeout resolves the per-finding engine timeout for the budget.
func perFixTimeout(job *models.FixJob) time.Duration {
	return defaultPerFixTimeout
}

// persistLiveSummary writes the per-tool table onto the job so the UI can
// render it while the agent is still running.
func persistLiveSummary(ctx context.Context, job *models.FixJob, deps Deps, summary Summary, rep *jobReport) {
	if job == nil || deps.Store == nil {
		return
	}
	summary.Tools = rep.tools()
	summary.Open = rep.openList()
	data, err := json.Marshal(summary)
	if err != nil {
		return
	}
	job.Summary = string(data)
	_ = deps.Store.UpdateFixJob(ctx, job)
}

// persist records a FixAttempt via the store, swallowing the error since a
// failed audit-write must not abort the job. Each attempt is persisted exactly
// once, after its terminal outcome (kept | rolled_back | unfixable) is set.
func persist(ctx context.Context, deps Deps, att *models.FixAttempt) {
	if deps.Store == nil || att == nil {
		return
	}
	if att.ID == "" {
		att.ID = uuid.NewString()
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
