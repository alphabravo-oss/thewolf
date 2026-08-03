// Package controller orchestrates the scan->fix->re-scan loop with guardrails.
// It coordinates the scan runner, fix engine, and finding tracker to iteratively
// improve a codebase until guardrails trigger or max iterations are reached.
package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	fixgit "github.com/alphabravocompany/thewolf/internal/fix/git"
	"github.com/alphabravocompany/thewolf/internal/fix/planner"
	"github.com/alphabravocompany/thewolf/internal/fix/pr"
	"github.com/alphabravocompany/thewolf/internal/fix/validator"
	"github.com/alphabravocompany/thewolf/internal/loop/tracker"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

// Config holds all configuration for a loop run.
type Config struct {
	RepoPath       string
	MaxIterations  int
	Severities     []models.Severity
	RescanStrategy models.RescanStrategy
	ScanConfig     runner.RunConfig
	FixEngine      engine.SubprocessEngine
	FixTimeout     time.Duration

	// PR creation settings. When CreatePR is true, a PR/MR is created after
	// the loop completes with fixes. RemoteType should be "github" or "gitlab".
	CreatePR   bool
	RemoteType string // "github" (default) or "gitlab"
	BranchName string // fix branch name; if empty, auto-generated
	BaseBranch string // target branch for the PR; defaults to "main"

	// Callbacks for external consumers (CLI output, SSE, etc.).
	OnIterationStart func(iteration int)
	OnIterationDone  func(iteration int, diff *tracker.IterationDiff, warnings []string)
}

// state represents the controller's current operational state.
type state int

const (
	stateRunning state = iota
	statePaused
	stateStopped
)

// Controller orchestrates the scan->fix->re-scan loop.
type Controller struct {
	cfg     Config
	tracker *tracker.Tracker

	mu      sync.Mutex
	state   state
	pauseCh chan struct{}
	stopCh  chan struct{}
}

// New creates a new loop Controller.
func New(cfg Config) *Controller {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 5
	}
	if cfg.RescanStrategy == "" {
		cfg.RescanStrategy = models.RescanFull
	}
	if cfg.FixTimeout == 0 {
		cfg.FixTimeout = 5 * time.Minute
	}

	return &Controller{
		cfg:     cfg,
		tracker: tracker.New(),
		state:   stateRunning,
		pauseCh: make(chan struct{}, 1),
		stopCh:  make(chan struct{}, 1),
	}
}

// Pause pauses the loop after the current iteration completes.
func (c *Controller) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == stateRunning {
		c.state = statePaused
	}
}

// Resume resumes a paused loop.
func (c *Controller) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == statePaused {
		c.state = stateRunning
		select {
		case c.pauseCh <- struct{}{}:
		default:
		}
	}
}

// Stop stops the loop after the current iteration completes.
func (c *Controller) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != stateStopped {
		c.state = stateStopped
		select {
		case c.stopCh <- struct{}{}:
		default:
		}
		// Also unblock any pause wait.
		select {
		case c.pauseCh <- struct{}{}:
		default:
		}
	}
}

// Run executes the main scan->fix->re-scan loop. It returns a Loop model
// summarizing all iterations and their outcomes.
func (c *Controller) Run(ctx context.Context) (*models.Loop, error) {
	now := time.Now()
	loop := &models.Loop{
		ID:             uuid.New().String(),
		Status:         models.LoopStatusRunning,
		MaxIterations:  c.cfg.MaxIterations,
		SeverityFilter: severityFilterString(c.cfg.Severities),
		RescanStrategy: c.cfg.RescanStrategy,
		StartedAt:      &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	var allWarnings []string
	noProgressCount := 0

	for i := 0; i < c.cfg.MaxIterations; i++ {
		// Check for stop/pause.
		if err := c.checkState(ctx); err != nil {
			loop.Status = models.LoopStatusStopped
			break
		}

		loop.CurrentIteration = i + 1

		if c.cfg.OnIterationStart != nil {
			c.cfg.OnIterationStart(i + 1)
		}

		// --- Step 1: Scan ---
		scanCfg := c.cfg.ScanConfig
		scanCfg.RepoPath = c.cfg.RepoPath

		// For targeted rescan after first iteration, limit to changed files.
		if i > 0 && c.cfg.RescanStrategy == models.RescanTargeted {
			changedFiles, _ := fixgit.ChangedFiles(c.cfg.RepoPath)
			if len(changedFiles) > 0 {
				scanCfg.IncludePaths = changedFiles
			}
		}

		scanResult, err := runner.Run(ctx, scanCfg)
		if err != nil {
			loop.Status = models.LoopStatusFailed
			completedAt := time.Now()
			loop.CompletedAt = &completedAt
			return loop, fmt.Errorf("scan failed on iteration %d: %w", i+1, err)
		}

		findings := scanResult.Findings

		// Record initial count on first iteration.
		if i == 0 {
			loop.TotalFindingsInitial = len(findings)
		}

		// Filter by severity.
		findings = filterBySeverity(findings, c.cfg.Severities)

		// Update tracker.
		c.tracker.Update(i, findings)

		// If no findings, we are done.
		if len(findings) == 0 {
			loop.Status = models.LoopStatusCompleted
			loop.TotalFindingsRemaining = 0
			if c.cfg.OnIterationDone != nil {
				c.cfg.OnIterationDone(i+1, &tracker.IterationDiff{}, nil)
			}
			break
		}

		// --- Step 2: Fix ---
		if c.cfg.FixEngine != nil {
			c.runFixes(ctx, findings)
		}

		// --- Step 3: Re-scan ---
		rescanResult, err := runner.Run(ctx, scanCfg)
		if err != nil {
			loop.Status = models.LoopStatusFailed
			completedAt := time.Now()
			loop.CompletedAt = &completedAt
			return loop, fmt.Errorf("rescan failed on iteration %d: %w", i+1, err)
		}

		rescanFindings := filterBySeverity(rescanResult.Findings, c.cfg.Severities)
		c.tracker.Update(i+1, rescanFindings) // store post-fix findings with offset

		// --- Step 4: Compute diff ---
		// We compare pre-fix (i) to post-fix (i+1 in tracker space).
		diff := c.tracker.Diff(i, i+1)

		// Update loop totals.
		loop.TotalFindingsFixed += diff.FixedCount
		loop.TotalFindingsNew += diff.NewCount
		loop.TotalFindingsRemaining = diff.RemainingCount + diff.NewCount

		// --- Step 5: Guardrails ---
		var iterWarnings []string

		// Regression: new findings introduced.
		if diff.NewCount > 0 {
			w := fmt.Sprintf("iteration %d: %d new finding(s) introduced (regression)", i+1, diff.NewCount)
			iterWarnings = append(iterWarnings, w)
		}

		// Regression exceeds fixes: pause.
		if diff.NewCount > diff.FixedCount && diff.NewCount > 0 {
			w := fmt.Sprintf("iteration %d: regressions (%d) exceed fixes (%d) — pausing", i+1, diff.NewCount, diff.FixedCount)
			iterWarnings = append(iterWarnings, w)
			loop.Status = models.LoopStatusPaused
			c.Pause()
		}

		// Diminishing returns: no progress for 2 consecutive iterations.
		if diff.FixedCount == 0 {
			noProgressCount++
		} else {
			noProgressCount = 0
		}
		if noProgressCount >= 2 {
			w := fmt.Sprintf("iteration %d: no findings fixed in last 2 iterations (diminishing returns) — stopping", i+1)
			iterWarnings = append(iterWarnings, w)
			loop.Status = models.LoopStatusCompleted
		}

		allWarnings = append(allWarnings, iterWarnings...)

		if c.cfg.OnIterationDone != nil {
			c.cfg.OnIterationDone(i+1, diff, iterWarnings)
		}

		// Use post-fix findings as the starting point for the next iteration.
		// Shift tracker: next iteration's scan will be stored at i+2, but we
		// need the re-scan results to be the "previous" for the next loop pass.
		// We re-index: overwrite i+1 findings as the base for next iteration scan.
		// Actually, we need to restructure: next iteration's scan result will be
		// stored at iteration (i+1) and compared against the current post-fix (i+1).
		// So for the next pass, the "scan" step stores at i+1 (overwriting), which
		// is correct since the loop increments i.

		// If loop was paused or completed by guardrails, break.
		if loop.Status == models.LoopStatusCompleted || loop.Status == models.LoopStatusStopped {
			break
		}

		// Check pause — wait for resume or stop.
		if loop.Status == models.LoopStatusPaused {
			if err := c.waitForResumeOrStop(ctx); err != nil {
				loop.Status = models.LoopStatusStopped
				break
			}
			loop.Status = models.LoopStatusRunning
		}
	}

	if loop.Status == models.LoopStatusRunning {
		loop.Status = models.LoopStatusCompleted
	}

	if len(allWarnings) > 0 {
		loop.GuardrailWarnings = strings.Join(allWarnings, "\n")
	}

	completedAt := time.Now()
	loop.CompletedAt = &completedAt
	loop.UpdatedAt = completedAt

	// Create PR if configured and there were fixes.
	if c.cfg.CreatePR && loop.TotalFindingsFixed > 0 {
		if prErr := c.createPR(ctx, loop); prErr != nil {
			allWarnings = append(allWarnings, fmt.Sprintf("PR creation failed: %v", prErr))
			loop.GuardrailWarnings = strings.Join(allWarnings, "\n")
		}
	}

	return loop, nil
}

// createPR pushes the fix branch and creates a PR/MR.
func (c *Controller) createPR(ctx context.Context, loop *models.Loop) error {
	branchName := c.cfg.BranchName
	if branchName == "" {
		branchName = fixgit.BranchName(loop.ID, "mixed")
	}

	baseBranch := c.cfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Push the branch to the remote.
	if err := pr.PushBranch(ctx, c.cfg.RepoPath, branchName); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}

	req := pr.PRRequest{
		RepoPath:   c.cfg.RepoPath,
		BranchName: branchName,
		BaseBranch: baseBranch,
		Category:   models.Category("mixed"),
		Validation: fmt.Sprintf("%d fixed, %d remaining", loop.TotalFindingsFixed, loop.TotalFindingsRemaining),
	}

	remoteType := c.cfg.RemoteType
	if remoteType == "" {
		remoteType = "github"
	}

	switch remoteType {
	case "gitlab":
		_, err := pr.CreateGitLabMR(ctx, req)
		return err
	default:
		_, err := pr.CreateGitHubPR(ctx, req)
		return err
	}
}

// runFixes applies fixes to the given findings using the configured fix engine.
func (c *Controller) runFixes(ctx context.Context, findings []models.Finding) {
	plan := planner.Plan(findings, c.cfg.Severities)
	eng := c.cfg.FixEngine
	val := validator.NewValidator()

	for _, group := range plan.Groups {
		for _, finding := range group.Findings {
			if ctx.Err() != nil {
				return
			}

			result, err := eng.Fix(ctx, engine.FixRequest{
				Finding:  finding,
				RepoPath: c.cfg.RepoPath,
				Timeout:  c.cfg.FixTimeout,
			})
			if err != nil || !result.Success {
				continue
			}

			// Validate the fix.
			changedFiles, _ := fixgit.ChangedFiles(c.cfg.RepoPath)
			if len(changedFiles) > 0 {
				vResult, _ := val.Validate(ctx, finding.ToolName, c.cfg.RepoPath, changedFiles)
				if vResult != nil && !vResult.Pass {
					// Revert changes if validation fails.
					_ = fixgit.RevertChanges(c.cfg.RepoPath)
					continue
				}

				// Commit the fix.
				category := string(group.Category)
				msg := fmt.Sprintf("fix(%s): %s in %s:%d",
					category, finding.Title, finding.FilePath, finding.LineStart)
				_ = fixgit.CommitAll(c.cfg.RepoPath, msg)
			}
		}
	}
}

// checkState checks if the controller has been stopped or the context cancelled.
func (c *Controller) checkState(ctx context.Context) error {
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()

	if s == stateStopped {
		return fmt.Errorf("stopped")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// waitForResumeOrStop blocks until the controller is resumed or stopped.
func (c *Controller) waitForResumeOrStop(ctx context.Context) error {
	select {
	case <-c.pauseCh:
		c.mu.Lock()
		s := c.state
		c.mu.Unlock()
		if s == stateStopped {
			return fmt.Errorf("stopped")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// filterBySeverity returns findings matching the given severities.
// If severities is empty, all findings are returned.
func filterBySeverity(findings []models.Finding, severities []models.Severity) []models.Finding {
	if len(severities) == 0 {
		return findings
	}
	allowed := make(map[models.Severity]bool, len(severities))
	for _, s := range severities {
		allowed[s] = true
	}
	var filtered []models.Finding
	for _, f := range findings {
		if allowed[f.Severity] {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

// severityFilterString joins severities into a comma-separated string.
func severityFilterString(severities []models.Severity) string {
	parts := make([]string, len(severities))
	for i, s := range severities {
		parts[i] = string(s)
	}
	return strings.Join(parts, ",")
}
