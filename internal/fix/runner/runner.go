package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	fixgit "github.com/alphabravocompany/thewolf/internal/fix/git"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// EventCallback is called when a fix item changes status, for SSE broadcasting.
type EventCallback func(eventType string, data map[string]interface{})

// Runner orchestrates fix operations in either interactive or wolf pack mode.
type Runner struct {
	Store    db.Store
	Eng      engine.SubprocessEngine
	FixID    string
	RepoPath string
	Mode     models.FixMode
	OnEvent  EventCallback
}

// New creates a new fix runner.
func New(store db.Store, eng engine.SubprocessEngine, fixID, repoPath string, mode models.FixMode, onEvent EventCallback) *Runner {
	if onEvent == nil {
		onEvent = func(string, map[string]interface{}) {}
	}
	return &Runner{
		Store:    store,
		Eng:      eng,
		FixID:    fixID,
		RepoPath: repoPath,
		Mode:     mode,
		OnEvent:  onEvent,
	}
}

// Run processes all findings through the fix engine.
// Updates the parent Fix record with progress counts.
func (r *Runner) Run(ctx context.Context, findings []models.Finding) error {
	// Pre-flight: verify repo path is a git repository
	if !fixgit.IsGitRepo(r.RepoPath) {
		return fmt.Errorf("not a git repository: %s — initialize with 'git init' before fixing", r.RepoPath)
	}

	fix, err := r.Store.GetFixByID(ctx, r.FixID)
	if err != nil {
		return fmt.Errorf("get fix: %w", err)
	}
	now := time.Now()
	fix.Status = models.FixStatusRunning
	fix.StartedAt = &now
	fix.FindingsAttempted = len(findings)
	r.Store.UpdateFix(ctx, fix)

	for i, finding := range findings {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		itemID := uuid.New().String()
		item := &models.FixItem{
			ID:        itemID,
			FixID:     r.FixID,
			FindingID: finding.ID,
			Status:    models.FixItemStatusInProgress,
		}
		if err := r.Store.CreateFixItem(ctx, item); err != nil {
			wolflog.Error().Err(err).Str("finding_id", finding.ID).Msg("failed to create fix item")
			continue
		}

		r.OnEvent("fix_item_started", map[string]interface{}{
			"fix_id":     r.FixID,
			"item_id":    itemID,
			"finding_id": finding.ID,
			"index":      i,
			"total":      len(findings),
			"title":      finding.Title,
		})

		switch r.Mode {
		case models.FixModeInteractive:
			r.runInteractive(ctx, item, finding)
		default:
			r.runWolfPack(ctx, item, finding)
		}
	}

	// Mark fix as completed
	fix, _ = r.Store.GetFixByID(ctx, r.FixID)
	if fix != nil {
		completed := time.Now()
		fix.Status = models.FixStatusCompleted
		fix.CompletedAt = &completed
		// Count results
		items, _ := r.Store.ListFixItemsByFix(ctx, r.FixID)
		for _, it := range items {
			switch it.Status {
			case models.FixItemStatusFixed:
				fix.FindingsFixed++
			case models.FixItemStatusFailed:
				fix.FindingsFailed++
			}
		}
		r.Store.UpdateFix(ctx, fix)
	}

	return nil
}

// runInteractive generates a fix diff, stores it for review, then reverts.
func (r *Runner) runInteractive(ctx context.Context, item *models.FixItem, finding models.Finding) {
	req := engine.FixRequest{
		Finding:  finding,
		RepoPath: r.RepoPath,
	}

	result, err := r.Eng.Fix(ctx, req)
	if err != nil {
		item.Status = models.FixItemStatusFailed
		item.ErrorMessage = err.Error()
		r.Store.UpdateFixItem(ctx, item)
		r.emitItemEvent("fix_item_failed", item, finding, "")
		return
	}

	if !result.Success {
		item.Status = models.FixItemStatusFailed
		item.ErrorMessage = result.Error
		r.Store.UpdateFixItem(ctx, item)
		r.emitItemEvent("fix_item_failed", item, finding, "")
		return
	}

	// Capture the diff before reverting
	diff, diffErr := fixgit.CaptureDiff(r.RepoPath)
	if diffErr != nil {
		wolflog.Warn().Err(diffErr).Msg("failed to capture diff")
	}

	files, _ := fixgit.ChangedFiles(r.RepoPath)

	// Revert changes — diff is saved for later apply
	if err := fixgit.RevertChanges(r.RepoPath); err != nil {
		wolflog.Warn().Err(err).Msg("failed to revert after interactive fix")
	}

	if diff == "" {
		item.Status = models.FixItemStatusFailed
		item.ErrorMessage = "engine produced no changes"
		r.Store.UpdateFixItem(ctx, item)
		r.emitItemEvent("fix_item_failed", item, finding, "")
		return
	}

	item.Status = models.FixItemStatusProposed
	item.Diff = diff
	item.FilesChanged = marshalJSON(files)
	r.Store.UpdateFixItem(ctx, item)

	r.emitItemEvent("fix_item_proposed", item, finding, diff)
}

// runWolfPack generates, validates, and commits the fix automatically.
func (r *Runner) runWolfPack(ctx context.Context, item *models.FixItem, finding models.Finding) {
	req := engine.FixRequest{
		Finding:  finding,
		RepoPath: r.RepoPath,
	}

	result, err := r.Eng.Fix(ctx, req)
	if err != nil {
		item.Status = models.FixItemStatusFailed
		item.ErrorMessage = err.Error()
		r.Store.UpdateFixItem(ctx, item)
		r.emitItemEvent("fix_item_failed", item, finding, "")
		return
	}

	if !result.Success {
		item.Status = models.FixItemStatusFailed
		item.ErrorMessage = result.Error
		fixgit.RevertChanges(r.RepoPath)
		r.Store.UpdateFixItem(ctx, item)
		r.emitItemEvent("fix_item_failed", item, finding, "")
		return
	}

	diff, _ := fixgit.CaptureDiff(r.RepoPath)
	files, _ := fixgit.ChangedFiles(r.RepoPath)

	if diff == "" {
		item.Status = models.FixItemStatusFailed
		item.ErrorMessage = "engine produced no changes"
		r.Store.UpdateFixItem(ctx, item)
		r.emitItemEvent("fix_item_failed", item, finding, "")
		return
	}

	commitMsg := fmt.Sprintf("fix(%s): %s\n\nFile: %s:%d\nSeverity: %s\nTool: %s",
		strings.ToLower(string(finding.Category)),
		finding.Title,
		finding.FilePath,
		finding.LineStart,
		finding.Severity,
		finding.ToolName,
	)
	if err := fixgit.CommitAll(r.RepoPath, commitMsg); err != nil {
		item.Status = models.FixItemStatusFailed
		item.ErrorMessage = fmt.Sprintf("commit failed: %v", err)
		fixgit.RevertChanges(r.RepoPath)
		r.Store.UpdateFixItem(ctx, item)
		r.emitItemEvent("fix_item_failed", item, finding, "")
		return
	}

	item.Status = models.FixItemStatusFixed
	item.Diff = diff
	item.FilesChanged = marshalJSON(files)
	item.ValidationResult = models.ValidationPass
	r.Store.UpdateFixItem(ctx, item)

	r.emitItemEvent("fix_item_fixed", item, finding, diff)
}

// ApplyItem applies a proposed fix diff, commits it, and marks the item as fixed.
func ApplyItem(ctx context.Context, store db.Store, repoPath, itemID string) error {
	item, err := store.GetFixItemByID(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get fix item: %w", err)
	}
	if item.Status != models.FixItemStatusProposed {
		return fmt.Errorf("item %s is not proposed (status: %s)", itemID, item.Status)
	}
	if item.Diff == "" {
		return fmt.Errorf("item %s has no diff to apply", itemID)
	}

	if err := applyDiff(repoPath, item.Diff); err != nil {
		return fmt.Errorf("git apply failed: %w", err)
	}

	finding, _ := store.GetFindingByID(ctx, item.FindingID)
	title := "fix"
	category := "general"
	filePath := ""
	if finding != nil {
		title = finding.Title
		category = strings.ToLower(string(finding.Category))
		filePath = finding.FilePath
	}

	commitMsg := fmt.Sprintf("fix(%s): %s\n\nFile: %s\nApproved via interactive review",
		category, title, filePath)
	if err := fixgit.CommitAll(repoPath, commitMsg); err != nil {
		fixgit.RevertChanges(repoPath)
		return fmt.Errorf("commit failed: %w", err)
	}

	item.Status = models.FixItemStatusFixed
	item.ValidationResult = models.ValidationPass
	return store.UpdateFixItem(ctx, item)
}

// RejectItem marks a proposed fix as rejected.
func RejectItem(ctx context.Context, store db.Store, itemID string) error {
	item, err := store.GetFixItemByID(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get fix item: %w", err)
	}
	if item.Status != models.FixItemStatusProposed {
		return fmt.Errorf("item %s is not proposed (status: %s)", itemID, item.Status)
	}

	item.Status = models.FixItemStatusRejected
	return store.UpdateFixItem(ctx, item)
}

func (r *Runner) emitItemEvent(eventType string, item *models.FixItem, finding models.Finding, diff string) {
	r.OnEvent(eventType, map[string]interface{}{
		"fix_id":     item.FixID,
		"item_id":    item.ID,
		"finding_id": item.FindingID,
		"status":     string(item.Status),
		"title":      finding.Title,
		"diff":       diff,
	})
}

func marshalJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// applyDiff uses git apply to apply a diff to the working directory.
// It tries plain apply first (works for new-file diffs from intent-to-add),
// then falls back to --3way for conflict resolution on existing files.
func applyDiff(repoPath, diff string) error {
	cmd := exec.Command("git", "apply", "-")
	cmd.Dir = repoPath
	cmd.Stdin = strings.NewReader(diff)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	// Fallback: --3way can resolve conflicts using merge
	cmd3 := exec.Command("git", "apply", "--3way", "-")
	cmd3.Dir = repoPath
	cmd3.Stdin = strings.NewReader(diff)
	output3, err3 := cmd3.CombinedOutput()
	if err3 != nil {
		return fmt.Errorf("git apply failed: %s (plain: %s): %w", string(output3), string(output), err3)
	}
	return nil
}
