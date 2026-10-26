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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/fix"
	"github.com/alphabravocompany/thewolf/internal/fix/budget"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/hygiene"
	"github.com/alphabravocompany/thewolf/internal/fix/pr"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

var githubPR = pr.CreateGitHubPR

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
	BranchChangedFiles(ctx context.Context) ([]string, error)
	Checkpoint(ctx context.Context) error
	Head(ctx context.Context) (string, error)
	Diff(ctx context.Context) (string, error)
	Cleanup(ctx context.Context) error
	Commit(ctx context.Context, message string) error
	Push(ctx context.Context) (sha string, err error)
}

// Verification must include committed agent edits. Cleanup and ordinary Git
// operations retain the workspace's HEAD-relative dirty-file semantics.
type branchVerificationWorkspace struct{ Workspace }

func (w branchVerificationWorkspace) ChangedFiles(ctx context.Context) ([]string, error) {
	return w.Workspace.BranchChangedFiles(ctx)
}

// WorkspacePreparer materialises an isolated, writable working tree on a NEW
// fix branch for the job's repo. Production wiring adapts
// workspace.Prepare; tests return a fake. branch is the fix branch to create.
type WorkspacePreparer interface {
	Prepare(ctx context.Context, repo *models.Repo, branch, baseBranch string) (Workspace, error)
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
	VerifyBranch(ctx context.Context, ws verify.Workspace, baseline, cleared []models.Finding, tools []string) ([]verify.StageResult, error)
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
	GetSetting(ctx context.Context, key string) (string, error)
	GetRepoByID(ctx context.Context, id string) (*models.Repo, error)
	GetFixJobByID(ctx context.Context, id string) (*models.FixJob, error)
	GetFindingByID(ctx context.Context, id string) (*models.Finding, error)
	GetScanByID(ctx context.Context, id string) (*models.Scan, error)
	ListFindingsByScan(ctx context.Context, scanID string) ([]models.Finding, error)
	UpdateFixJob(ctx context.Context, job *models.FixJob) error
	CreateFixAttempt(ctx context.Context, attempt *models.FixAttempt) error
	UpdateFixAttempt(ctx context.Context, attempt *models.FixAttempt) error
	ListFixAttempts(ctx context.Context, jobID string) ([]models.FixAttempt, error)
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
	pendingAttempts  map[string]models.FixAttempt
	attemptNumbers   map[string]int
	priorAttempts    []models.FixAttempt
	Store            Store
	Writability      WritabilityChecker
	Workspaces       WorkspacePreparer
	Engines          EngineSelector
	Verifier         Verifier
	GitApply         GitApplier // applies an API engine's returned diff; defaults to git apply
	Diffs            DiffStore
	Rescan           Rescanner
	GitHubToken      func(context.Context, *models.Repo) string
	PublicationReady func(context.Context, *models.Repo) (bool, string)
	CLIEnv           []string // API keys injected into CLI engines
	Model            string
	Effort           string
	Variant          string
	PromptInitial    string
	PromptFollowup   string
	Log              Logf
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
	// cleanup runs only after the result is durably saved. Failed/stale runs
	// retain the checkout so recovery never depends on an unsaved result.
	cleanup        func(context.Context) error
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
	FinalVerification []verify.StageResult `json:"final_verification,omitempty"`
	TotalFindings     int                  `json:"total_findings"`
	Considered        int                  `json:"considered"`
	Skipped           int                  `json:"skipped"`
	Kept              int                  `json:"kept"`
	Unfixable         int                  `json:"unfixable"`
	Muted             int                  `json:"muted,omitempty"`
	Remaining         int                  `json:"remaining"`
	Loops             int                  `json:"loops"`
	Pushed            bool                 `json:"pushed,omitempty"`
	PushSHA           string               `json:"push_sha,omitempty"`
	PublicationError  string               `json:"publication_error,omitempty"`
	Addressed         []FindingNote        `json:"addressed,omitempty"`
	PRURL             string               `json:"pr_url,omitempty"`
	Branch            string               `json:"branch"`
	StopReason        string               `json:"stop_reason,omitempty"`
	Notes             []string             `json:"notes,omitempty"`
	Rounds            []RoundSummary       `json:"rounds,omitempty"`
	Tools             []ToolReport         `json:"tools,omitempty"`
	Open              []FindingNote        `json:"open,omitempty"`
	ReportMarkdown    string               `json:"report_markdown,omitempty"`
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
// Publication requires push mode or an explicit push resume action.
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
	if err := fix.CheckEnabled(ctx, deps.Store); err != nil {
		return nil, err
	}
	logf := deps.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}

	now := time.Now().UTC()
	job.Status = models.FixJobRunning
	job.Error = ""
	job.StartedAt = &now
	if err := deps.Store.UpdateFixJob(ctx, job); err != nil {
		return nil, fmt.Errorf("orchestrator: mark job running: %w", err)
	}
	var res *Result
	runErr := restoreAttemptHistory(ctx, job, &deps)
	if runErr == nil {
		res, runErr = run(ctx, job, deps, logf)
	}
	if res != nil && runErr == nil && res.PauseStatus == models.FixJobAwaitingReview {
		res.Attempts, runErr = deps.Store.ListFixAttempts(ctx, job.ID)
	}
	finish := time.Now().UTC()
	job.FinishedAt = &finish

	if latest, _ := deps.Store.GetFixJobByID(context.WithoutCancel(ctx), job.ID); latest != nil && latest.Status == models.FixJobCancelled {
		// The worker owns cancellation cleanup after engines stop. Keep our
		// in-memory path in case cancellation raced workspace persistence.
		job.Status = models.FixJobCancelled
		return res, errors.Join(runErr, context.Canceled)
	}

	if runErr != nil {
		job.Status = models.FixJobFailed
		job.Error = runErr.Error()
		// Persist even if the run context was cancelled (deploy, stall kill).
		if err := deps.Store.UpdateFixJob(context.WithoutCancel(ctx), job); err != nil {
			return res, errors.Join(runErr, fmt.Errorf("persist failed job; workspace retained: %w", err))
		}
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
		return res, fmt.Errorf("orchestrator: persist job result; workspace retained: %w", err)
	}
	if res != nil && res.cleanup != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		if err := res.cleanup(cleanupCtx); err != nil {
			job.Error = fmt.Sprintf("workspace cleanup failed; recovery path retained: %v", err)
			logf("job %s: %s", job.ID, job.Error)
		} else {
			job.WorkspacePath = ""
		}
		if err := deps.Store.UpdateFixJob(cleanupCtx, job); err != nil {
			logf("job %s: result saved, but cleanup metadata could not be saved: %v", job.ID, err)
		}
		cancel()
	}
	if job.Status == models.FixJobSucceeded {
		logf("job %s complete: %d kept, %d unfixable on branch %s",
			job.ID, res.Summary.Kept, res.Summary.Unfixable, res.Branch)
	} else {
		logf("job %s paused (%s): %s", job.ID, job.Status, job.PauseReason)
	}
	return res, nil
}

// run is the core flow, factored out so Run can wrap it with status bookkeeping.
func run(ctx context.Context, job *models.FixJob, deps Deps, logf Logf) (result *Result, runErr error) {
	repo, err := deps.Store.GetRepoByID(ctx, job.RepoID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: load repo %s: %w", job.RepoID, err)
	}
	if repo == nil {
		return nil, fmt.Errorf("orchestrator: repo %s not found", job.RepoID)
	}

	if deps.Writability != nil && job.ResumeAction != models.FixResumePush {
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

	if job.ResumeAction == models.FixResumePush && job.WorkspacePath == "" {
		return nil, errors.New("orchestrator: reviewed workspace is missing")
	}
	ws, resumed, err := openOrPrepare(ctx, job, repo, branch, deps)
	if err != nil {
		return nil, err
	}
	// A remediation owns the checkout until publish/discard.
	cleanup := !resumed && job.RemediationID == ""
	defer func() {
		if cleanup && runErr == nil && result != nil {
			result.cleanup = ws.Cleanup
		}
	}()
	job.WorkspacePath = ws.Path()
	job.ResultBranch = ws.Branch()
	if err := deps.Store.UpdateFixJob(ctx, job); err != nil {
		cleanup = false
		return nil, fmt.Errorf("persist workspace ownership: %w", err)
	}
	if job.RemediationID != "" {
		rem, err := deps.Store.GetRemediationByID(ctx, job.RemediationID)
		if err != nil {
			return nil, fmt.Errorf("load remediation workspace; workspace retained: %w", err)
		}
		if rem == nil {
			return nil, errors.New("remediation missing; workspace retained")
		}
		rem.WorkspacePath = ws.Path()
		rem.Branch = ws.Branch()
		if err := deps.Store.UpdateRemediation(ctx, rem); err != nil {
			return nil, fmt.Errorf("persist remediation workspace; workspace retained: %w", err)
		}
	}
	logf("job %s: workspace ready on branch %s", job.ID, ws.Branch())

	// Resume-to-push: human approved the branch; do not run more engines.
	if job.ResumeAction == models.FixResumePush {
		cleanup = false
		var summary Summary
		if err := json.Unmarshal([]byte(job.Summary), &summary); err != nil || summary.Branch != ws.Branch() {
			return nil, errors.New("orchestrator: missing or invalid review summary; workspace retained")
		}
		return finishWithPush(ctx, job, repo, ws, deps, logf, summary, &cleanup)
	}
	if _, err := ws.BranchChangedFiles(ctx); err != nil {
		return nil, fmt.Errorf("read verification baseline; workspace retained: %w", err)
	}
	ws = branchVerificationWorkspace{ws}

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
	if resumed && job.Summary != "" {
		var previous Summary
		if err := json.Unmarshal([]byte(job.Summary), &previous); err != nil || previous.Branch != ws.Branch() {
			return nil, errors.New("invalid retained verification summary; workspace retained")
		}
		summary.Rounds = previous.Rounds
		for _, f := range prFindings(previous) {
			original, err := deps.Store.GetFindingByID(ctx, f.ID)
			if err != nil || original == nil {
				return nil, fmt.Errorf("restore accepted finding %s; workspace retained: %w", f.ID, errors.Join(err, errors.New("finding history unavailable")))
			}
			rep.note(*original, models.FixOutcomeKept, "previously verified; final branch check pending")
		}
	}

	active := make([]models.Finding, 0, len(findings))
	// The journal can be newer than the per-tool summary after a worker stops.
	byID := make(map[string]models.Finding, len(findings))
	for _, f := range findings {
		byID[f.ID] = f
	}
	for _, att := range deps.priorAttempts {
		if f, ok := byID[att.FindingID]; ok {
			if att.Outcome == models.FixOutcomeKept {
				rep.note(f, models.FixOutcomeKept, "previously verified; final branch check pending")
			} else if att.Outcome == models.FixOutcomeRolledBack || att.Outcome == models.FixOutcomeUnfixable {
				rep.note(f, "open", att.DiffExcerpt)
			}
		}
	}
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
	if startLoop > 0 || (resumed && len(deps.priorAttempts) > 0) {
		seed := RoundSummary{Round: startLoop}
		active, err = rescanRound(ctx, ws, deps, logf, active, unfixable, &seed)
		if err != nil {
			return nil, fmt.Errorf("resume verification; workspace retained: %w", err)
		}
		logf("job %s: resumed after loop %d with %d findings still open", job.ID, startLoop, len(active))
	}

	var attempts []models.FixAttempt
	for loop := startLoop + 1; loop <= maxLoops && len(active) > 0; loop++ {
		job.CurrentLoop = loop
		summary.Loops = loop
		if err := deps.Store.UpdateFixJob(ctx, job); err != nil {
			cleanup = false
			return nil, fmt.Errorf("persist loop progress: %w", err)
		}
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
				hyOut, leftover, files, hygieneErr := applyToolHygiene(ctx, job, ws, group, deps, logf, rep)
				if hygieneErr != nil {
					return nil, hygieneErr
				}
				outcomes = hyOut
				if len(files) > 0 {
					scrubReviewFiles(ws.Path())
					msg := fmt.Sprintf("wolf-fix: %s hygiene (loop %d)", group.Tool, loop)
					if err := commitWorkspace(ctx, ws, msg, logf); err != nil {
						cleanup = false
						return nil, fmt.Errorf("commit %s hygiene; workspace retained: %w", group.Tool, err)
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
			att, more, toolErr := fixOneTool(ctx, job, ws, work, deps, tracker, logf, loop, rep)
			attempts = append(attempts, att...)
			if toolErr != nil {
				cleanup = false
				return nil, fmt.Errorf("fix %s; workspace retained: %w", group.Tool, toolErr)
			}
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
					cleanup = false
					return nil, fmt.Errorf("commit %s fixes; workspace retained: %w", group.Tool, err)
				} else {
					logf("  %s: committed %d kept finding(s) on %s", group.Tool, keptThisTool, ws.Branch())
					if derr := persistDiff(ctx, job, ws, deps); derr != nil {
						cleanup = false
						return nil, fmt.Errorf("persist %s fixes; workspace retained: %w", group.Tool, derr)
					}
				}
			}
			rememberKeptLocations(fixedLocs, group.Findings, outcomes)
			foldOutcomes(group.Findings, outcomes, &summary, &still, unfixable, &roundKept, &roundUnfix, rep)
			if err := persistLiveSummary(ctx, job, deps, summary, rep); err != nil {
				cleanup = false
				return nil, fmt.Errorf("persist live summary: %w", err)
			}
		}

		round := RoundSummary{Round: loop, Kept: roundKept, Unfixable: roundUnfix}
		active, err = rescanRound(ctx, ws, deps, logf, active, unfixable, &round)
		if err != nil {
			return nil, fmt.Errorf("final round verification; workspace retained: %w", err)
		}
		summary.Rounds = append(summary.Rounds, round)
		summary.Remaining = len(active)
		summary.Addressed = rep.keptList()
		summary.Kept = len(summary.Addressed)
		logf("job %s: loop %d rescan: %d remaining, %d cleared this round",
			job.ID, loop, round.Remaining, len(round.Cleared))

		if len(active) == 0 {
			break
		}
		if loop < maxLoops && job.HumanInTheLoop && job.ResumeAction != models.FixResumeContinue {
			job.PauseReason = fmt.Sprintf("loop %d complete; %d findings still open — continue or inspect", loop, len(active))
			cleanup = false
			if err := persistDiff(ctx, job, ws, deps); err != nil {
				return nil, fmt.Errorf("persist review diff; workspace retained: %w", err)
			}
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

	summary.Addressed = rep.keptList()
	summary.Kept = len(summary.Addressed)
	summary.Tools = rep.tools()
	summary.Open = rep.openList()
	summary.ReportMarkdown = rep.markdown()
	for _, line := range strings.Split(strings.TrimSpace(summary.ReportMarkdown), "\n") {
		if strings.TrimSpace(line) != "" {
			logf("%s", line)
		}
	}

	return finishWithPush(ctx, job, repo, ws, deps, logf, summary, &cleanup)
}

func openOrPrepare(ctx context.Context, job *models.FixJob, repo *models.Repo, branch string, deps Deps) (Workspace, bool, error) {
	if job.WorkspacePath != "" && deps.Workspaces != nil {
		ws, err := deps.Workspaces.Open(ctx, job.WorkspacePath, repo)
		if err != nil {
			return nil, true, fmt.Errorf("orchestrator: reopen retained workspace: %w", err)
		}
		if ws == nil {
			return nil, true, errors.New("orchestrator: reopen retained workspace returned no workspace")
		}
		return ws, true, nil
	}
	ws, err := deps.Workspaces.Prepare(ctx, repo, branch, job.BaseBranch)
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
	cleanup *bool,
) (*Result, error) {
	// Read the complete durable history before publication, including attempts
	// from earlier workers and hygiene passes, with their persisted identities.
	attempts, err := deps.Store.ListFixAttempts(ctx, job.ID)
	if err != nil {
		if cleanup != nil {
			*cleanup = false
		}
		return nil, fmt.Errorf("read final attempt history; workspace retained: %w", err)
	}
	if err := persistDiff(ctx, job, ws, deps); err != nil {
		if cleanup != nil {
			*cleanup = false
		}
		return nil, fmt.Errorf("persist final diff; workspace retained: %w", err)
	}
	stages, err := verifyFinalBranch(ctx, job, ws, deps, summary)
	if err != nil {
		if cleanup != nil {
			*cleanup = false
		}
		return nil, fmt.Errorf("final branch verification; workspace retained: %w", err)
	}
	summary.FinalVerification = stages
	// Publication must use the tree that just passed verification, including
	// surviving extra-file edits that no per-tool keep happened to commit.
	dirtyWS := ws
	if wrapped, ok := ws.(branchVerificationWorkspace); ok {
		dirtyWS = wrapped.Workspace
	}
	dirty, err := dirtyWS.ChangedFiles(ctx)
	if err == nil && job.Pushed {
		var head string
		head, err = ws.Head(ctx)
		if err == nil && (len(dirty) != 0 || head != job.PushSHA) {
			err = errors.New("published workspace changed; cannot retry PR for a different tree")
		}
	} else if err == nil && len(dirty) > 0 {
		err = ws.Commit(ctx, "wolf-fix: final verified branch")
	}
	if err != nil {
		if cleanup != nil {
			*cleanup = false
		}
		return nil, fmt.Errorf("assemble verified branch; workspace retained: %w", err)
	}
	result := &Result{Branch: ws.Branch(), DiffArtifactID: job.DiffArtifactID, Summary: summary, Attempts: attempts}

	wantPush := job.Mode == models.FixModePush || job.ResumeAction == models.FixResumePush
	if wantPush {
		if err := fix.CheckEnabled(ctx, deps.Store); err != nil {
			if cleanup != nil {
				*cleanup = false
			}
			return nil, fmt.Errorf("publication stopped; workspace retained: %w", err)
		}
		result.Summary.PublicationError = ""
		if repo != nil && repo.SourceType == models.SourceTypeGitHub {
			ready, reason := false, "publication readiness check is not configured"
			if deps.PublicationReady != nil {
				ready, reason = deps.PublicationReady(ctx, repo)
			}
			if !ready {
				if cleanup != nil {
					*cleanup = false
				}
				job.Error = reason
				job.PauseReason = "publication not ready: " + reason
				result.PauseStatus = models.FixJobPushFailed
				result.Summary.PublicationError = "publication_not_ready"
				logf("job %s: %s", job.ID, job.PauseReason)
				return result, nil
			}
		}
		if !job.Pushed {
			sha, err := ws.Push(ctx)
			if err != nil {
				if cleanup != nil {
					*cleanup = false
				}
				result.Summary.PublicationError = "push_failed"
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
		} else {
			result.Summary.Pushed = true
			result.Summary.PushSHA = job.PushSHA
		}
		if repo != nil && repo.SourceType == models.SourceTypeGitHub {
			base := job.BaseBranch
			if base == "" {
				base = repo.DefaultBranch
			}
			if base == "" {
				base = "main"
			}
			token := ""
			if deps.GitHubToken != nil {
				token = deps.GitHubToken(ctx, repo)
			}
			prRes, prErr := githubPR(ctx, pr.PRRequest{
				Token:      token,
				Source:     repo.SourcePath,
				RepoPath:   ws.Path(),
				BranchName: ws.Branch(),
				BaseBranch: base,
				Category:   prCategory(summary),
				Findings:   prFindings(summary),
				Evidence:   prEvidence(summary),
				JobID:      job.ID,
				ScanID:     job.ScanID,
				Validation: fmt.Sprintf("%d kept, %d remaining", summary.Kept, summary.Remaining),
			})
			if prErr == nil && prRes != nil && prRes.Error != "" {
				prErr = errors.New(prRes.Error)
			}
			if prErr == nil && (prRes == nil || strings.TrimSpace(prRes.URL) == "") {
				prErr = errors.New("PR creation returned no URL")
			}
			if prErr != nil {
				if cleanup != nil {
					*cleanup = false
				}
				result.Summary.PublicationError = "pr_create_failed"
				job.Error = prErr.Error()
				job.PauseReason = "pr create failed: " + prErr.Error()
				result.PauseStatus = models.FixJobPushFailed
				logf("job %s: GitHub PR create failed: %s", job.ID, prErr)
			} else {
				result.Summary.PRURL = prRes.URL
				logf("job %s: GitHub PR %s", job.ID, prRes.URL)
			}
		}
		if cleanup != nil && result.PauseStatus == "" && job.RemediationID == "" {
			*cleanup = true
		}
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

func prFindings(summary Summary) []models.Finding {
	var out []models.Finding
	for _, n := range summary.Addressed {
		out = append(out, models.Finding{ID: n.ID, ToolName: n.Tool, RuleID: n.Rule, Category: models.Category(n.Category), Severity: models.Severity(n.Severity), Title: n.Title, FilePath: n.File, LineStart: n.Line})
	}
	if len(out) > 0 {
		return out
	}
	// Older summaries retain IDs but have no detailed verification evidence.
	seen := map[string]bool{}
	for _, round := range summary.Rounds {
		for _, id := range round.Cleared {
			if !seen[id] {
				out = append(out, models.Finding{ID: id, Title: id})
				seen[id] = true
			}
		}
	}
	return out
}

func prEvidence(summary Summary) map[string]string {
	evidence := make(map[string]string, len(summary.Addressed))
	for _, n := range summary.Addressed {
		evidence[n.ID] = n.Validation
	}
	return evidence
}

func prCategory(summary Summary) models.Category {
	var category models.Category
	for _, n := range summary.Addressed {
		if n.Category == "" {
			continue
		}
		if category != "" && string(category) != n.Category {
			return "multiple categories"
		}
		category = models.Category(n.Category)
	}
	if category == "" {
		return "code"
	}
	return category
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
) ([]models.Finding, error) {
	if deps.Rescan == nil || len(active) == 0 {
		var still []models.Finding
		for _, f := range active {
			if !unfixable[f.ID] {
				still = append(still, f)
				round.StillOpen = append(round.StillOpen, f.ID)
			}
		}
		round.Remaining = len(still)
		return still, nil
	}
	tools := uniqueTools(active)
	reported, err := deps.Rescan.Rescan(ctx, ws.Path(), tools)
	if err != nil {
		return nil, fmt.Errorf("branch rescan failed: %w", err)
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
	return still, nil
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
) (records []models.FixAttempt, outcomes map[string]string, runErr error) {
	outcomes = map[string]string{}
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, finishPendingAttempts(finishCtx, deps, runErr != nil))
	}()

	take := append([]models.Finding(nil), group.Findings...)
	sortFindings(take)

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
			if err := persist(ctx, deps, &rec); err != nil {
				return records, outcomes, err
			}
			outcomes[f.ID] = models.FixOutcomeUnfixable
			rep.note(f, models.FixOutcomeUnfixable, "no engine available")
			logf("decide SKIP %s — no engine available", engine.FindingLabel(f))
		}
		return records, outcomes, nil
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
		if err := fix.CheckEnabled(ctx, deps.Store); err != nil {
			return records, outcomes, err
		}
		if err := ws.Checkpoint(ctx); err != nil {
			return records, outcomes, fmt.Errorf("checkpoint agent workspace: %w", err)
		}
		reviewRel, reviewFile, err := writeFindingsReview(ws.Path(), group.Tool, work)
		if err != nil {
			return records, outcomes, fmt.Errorf("write agent findings review: %w", err)
		}
		logf("  %s: review file %s (%s)", group.Tool, reviewRel, reviewFile)

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
		if err := beginAttempts(ctx, job, deps, work, eng); err != nil {
			cancel()
			return records, outcomes, err
		}
		fr, fixErr := eng.Fix(fixCtx, req)
		cancel()
		scrubReviewFiles(ws.Path())
		elapsed := int(time.Since(start).Milliseconds())
		if fixErr != nil {
			return records, outcomes, fmt.Errorf("engine %s failed: %w", eng.Name(), fixErr)
		}

		if fr != nil && len(work) > 15 && engine.CountTaskToolEvents(fr.Output) == 0 {
			logf("  %s: warning: no task events on a large batch (%d findings)", group.Tool, len(work))
		}

		skipBy, fixBy := map[string]string{}, map[string]string{}
		if fr != nil {
			for _, d := range engine.ParseDecisions(fr.Output) {
				id := engine.MatchDecisionID(d.ID, work)
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
		// A patch describes the entire turn, not one patch per finding.
		if fr != nil && !fr.EditsInPlace && strings.TrimSpace(fr.Diff) != "" {
			if deps.GitApply == nil {
				return records, outcomes, errors.New("cannot apply engine patch: no Git applier configured")
			}
			if err := deps.GitApply.Apply(ctx, ws.Path(), fr.Diff); err != nil {
				return records, outcomes, fmt.Errorf("apply engine patch: %w", err)
			}
		}
		changed, err := ws.ChangedFiles(ctx)
		if err != nil {
			return records, outcomes, fmt.Errorf("inspect agent edits; workspace retained: %w", err)
		}
		if fr == nil {
			fr = &engine.FixResult{}
		}
		fr.FilesChanged = changed
		changedN := len(changed)
		if changedN > 0 {
			if err := formatChangedGo(ctx, ws.Path(), changed); err != nil {
				return records, outcomes, fmt.Errorf("write formatted Go source: %w", err)
			}
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
				break
			}
			eng = chain.Next()
			attemptNo++
			if eng == nil || attemptNo > maxAttempts {
				logf("  %s: leaving %d findings open after a silent engine turn", group.Tool, len(work))
			}
			continue
		}

		var toVerify []models.Finding
		notes := map[string]string{}
		var next []models.Finding
		var untouched []models.Finding
		for _, f := range work {
			if reason, ok := skipBy[f.ID]; ok && !findingTouched(f, fr) {
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
				if err := persist(ctx, deps, &att); err != nil {
					return records, outcomes, err
				}
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
			toVerify = append(toVerify, f)
		}
		// Later edits and rollback can invalidate earlier keeps in another file.
		// Rejudge every accepted finding from this tool, not just same-file peers.
		for _, f := range take {
			if outcomes[f.ID] == models.FixOutcomeKept {
				toVerify = append(toVerify, f)
			}
		}

		batch := map[string]*verify.VerifyResult{}
		var batchErr error
		if len(toVerify) > 0 {
			batch, batchErr = deps.Verifier.VerifyBatch(ctx, ws, toVerify)
			if batchErr != nil {
				logf("  %s: verify batch error: %v", group.Tool, batchErr)
			}
		}
		// Decide at file granularity before recording any keeps. An unavailable
		// verifier is a failed job with a retained workspace, never a verified fix.
		rejectedFiles := map[string]string{}
		for _, f := range toVerify {
			vr := batch[f.ID]
			switch verifyVerdict(vr, batchErr) {
			case verifyUnjudged:
				why := verifyWhy(vr, batchErr)
				att := baseAttempt(job, f, attemptNo, eng, deps, fr, elapsed)
				att.Outcome = models.FixOutcomeUnfixable
				att.DiffExcerpt = "verification incomplete; workspace retained: " + why
				records = append(records, att)
				if err := persist(context.WithoutCancel(ctx), deps, &att); err != nil {
					return records, outcomes, err
				}
				return records, outcomes, fmt.Errorf("verification incomplete for %s: %s", f.ID, why)
			case verifyRollback:
				rejectedFiles[filepath.Clean(f.FilePath)] = verifyWhy(vr, batchErr)
			}
		}
		rolledBack := map[string]bool{}
		// Persist every rollback first, then recheck the surviving combined
		// edits before recording any keep based on the pre-rollback checkout.
		for _, rollbackPhase := range []bool{true, false} {
			if !rollbackPhase && len(rolledBack) > 0 {
				var surviving []models.Finding
				for _, f := range toVerify {
					if _, rejected := rejectedFiles[filepath.Clean(f.FilePath)]; !rejected {
						surviving = append(surviving, f)
					}
				}
				if len(surviving) > 0 {
					batch, batchErr = deps.Verifier.VerifyBatch(ctx, ws, surviving)
					for _, f := range surviving {
						if verifyVerdict(batch[f.ID], batchErr) != verifyKeep {
							why := "combined verification after rollback failed: " + verifyWhy(batch[f.ID], batchErr)
							att := baseAttempt(job, f, attemptNo, eng, deps, fr, elapsed)
							att.Outcome, att.DiffExcerpt = models.FixOutcomeUnfixable, why
							records = append(records, att)
							if err := persist(context.WithoutCancel(ctx), deps, &att); err != nil {
								return records, outcomes, err
							}
							return records, outcomes, fmt.Errorf("%s for %s; workspace retained", why, f.ID)
						}
					}
				}
			}
			for _, f := range toVerify {
				_, rejected := rejectedFiles[filepath.Clean(f.FilePath)]
				if rejected != rollbackPhase {
					continue
				}
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
				file := filepath.Clean(f.FilePath)
				if why, rejected := rejectedFiles[file]; rejected {
					logf("  %s: verify rollback %s — %s", group.Tool, engine.FindingLabel(f), why)
					var rerr error
					if !rolledBack[file] {
						rerr = ws.Rollback(ctx, f.FilePath)
					}
					if rerr != nil {
						logf("finding %s: rollback %s failed: %v", f.ID, f.FilePath, rerr)
						att.Outcome = models.FixOutcomeUnfixable
						att.DiffExcerpt = "rollback failed; workspace retained: " + rerr.Error()
						records = append(records, att)
						if err := persist(context.WithoutCancel(ctx), deps, &att); err != nil {
							return records, outcomes, err
						}
						return records, outcomes, fmt.Errorf("rollback %q: %w", f.FilePath, rerr)
					}
					rolledBack[file] = true
					delete(outcomes, f.ID)
					att.Outcome = models.FixOutcomeRolledBack
					att.DiffExcerpt = "rolled back: " + why
					records = append(records, att)
					if err := persist(ctx, deps, &att); err != nil {
						return records, outcomes, err
					}
					rep.note(f, "open", "edited but "+why+" — rolled back")
					next = append(next, f)
				} else {
					att.Outcome = models.FixOutcomeKept
					if note := notes[f.ID]; note != "" {
						att.DiffExcerpt = "FIX: " + note
					}
					records = append(records, att)
					if err := persist(ctx, deps, &att); err != nil {
						return records, outcomes, err
					}
					outcomes[f.ID] = models.FixOutcomeKept
					rep.note(f, models.FixOutcomeKept, "verified and kept on the branch")
					rep.verification(f.ID, vr)
				}
			}
		}

		if stalled {
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
				if err := persist(ctx, deps, &att); err != nil {
					return records, outcomes, err
				}
				outcomes[f.ID] = models.FixOutcomeUnfixable
				rep.note(f, models.FixOutcomeUnfixable, "still open after engine chain")
				logf("decide SKIP %s — still open after engine chain", engine.FindingLabel(f))
			}
			remaining = nil
		}
	}
	return records, outcomes, nil
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

// verifyWhy preserves verifier diagnostics for both failed and incomplete checks.
func verifyWhy(vr *verify.VerifyResult, batchErr error) string {
	if batchErr != nil {
		return batchErr.Error()
	}
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
	return "verify did not pass"
}

func verifyVerdict(vr *verify.VerifyResult, batchErr error) verifyDecision {
	if batchErr != nil {
		return verifyUnjudged
	}
	if vr != nil && vr.BuildFailed() {
		return verifyRollback
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

func formatChangedGo(ctx context.Context, repoPath string, files []string) error {
	root, err := os.OpenRoot(repoPath)
	if err != nil {
		return nil // Formatting is best-effort; verification still judges the edit.
	}
	defer func() { _ = root.Close() }()
	for _, f := range files {
		if filepath.IsAbs(f) {
			f, err = filepath.Rel(repoPath, f)
			if err != nil {
				continue
			}
		}
		f = filepath.Clean(f)
		if !filepath.IsLocal(f) || !strings.HasSuffix(f, ".go") || hygiene.ProtectedRel(f) || strings.Contains("/"+filepath.ToSlash(f)+"/", "/.git/") {
			continue
		}
		if err := formatGoFile(ctx, root, f, repoPath); err != nil {
			return err
		}
	}
	return nil
}

func formatGoFile(ctx context.Context, root *os.Root, name, repoPath string) error {
	file, err := root.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return nil // Deleted files and escaping symlinks must not be formatted.
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	source, err := io.ReadAll(file)
	if err != nil {
		return nil
	}
	bin := "gofmt"
	if _, err := exec.LookPath("goimports"); err == nil {
		bin = "goimports"
	}
	// Never give the formatter agent-controlled output paths or flags. Read
	// and write through the same confined descriptor even if a path is replaced.
	cmd := plugin.CommandContext(ctx, bin) // #nosec G204 -- fixed formatter names, source on stdin.
	cmd.Dir = repoPath
	cmd.Stdin = bytes.NewReader(source)
	formatted, err := cmd.Output()
	if err != nil || bytes.Equal(source, formatted) {
		return nil
	}
	if _, err := file.WriteAt(formatted, 0); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return file.Truncate(int64(len(formatted)))
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
func persistLiveSummary(ctx context.Context, job *models.FixJob, deps Deps, summary Summary, rep *jobReport) error {
	if job == nil || deps.Store == nil {
		return nil
	}
	summary.Addressed = rep.keptList()
	summary.Kept = len(summary.Addressed)
	summary.Tools = rep.tools()
	summary.Open = rep.openList()
	data, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	job.Summary = string(data)
	return deps.Store.UpdateFixJob(ctx, job)
}

// persist records an attempt before later commit/publication can rely on it.
func persist(ctx context.Context, deps Deps, att *models.FixAttempt) error {
	if deps.Store == nil || att == nil {
		return errors.New("attempt persistence requires a store and attempt")
	}
	if att.ID == "" {
		if pending, ok := deps.pendingAttempts[att.FindingID]; ok {
			att.ID, att.AttemptNo, att.CreatedAt = pending.ID, pending.AttemptNo, pending.CreatedAt
			if err := deps.Store.UpdateFixAttempt(ctx, att); err != nil {
				return fmt.Errorf("finalize fix attempt; workspace retained: %w", err)
			}
			delete(deps.pendingAttempts, att.FindingID)
			return nil
		}
		att.ID = uuid.NewString()
	}
	if deps.attemptNumbers != nil {
		att.AttemptNo = deps.attemptNumbers[att.FindingID] + 1
	}
	if err := deps.Store.CreateFixAttempt(ctx, att); err != nil {
		return fmt.Errorf("persist fix attempt; workspace retained: %w", err)
	}
	if deps.attemptNumbers != nil {
		deps.attemptNumbers[att.FindingID] = att.AttemptNo
	}
	return nil
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
