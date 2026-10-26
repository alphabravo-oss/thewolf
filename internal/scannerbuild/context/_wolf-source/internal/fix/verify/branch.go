package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// BranchScanner runs every requested tool over the whole checkout. Missing,
// failed or skipped tools must return an error, never an empty successful scan.
type BranchScanner interface {
	Rescan(context.Context, string, []string) ([]models.Finding, error)
}

// GateBranch validates the final assembled tree, independently of turn-level
// verdicts. Existing findings may remain; accepted fixes must stay cleared and
// the original tool set must not report new findings elsewhere in the tree.
func GateBranch(ctx context.Context, ws Workspace, baseline, cleared []models.Finding, tools []string, scanner BranchScanner) ([]StageResult, error) {
	changed, err := ws.ChangedFiles(ctx)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 && len(cleared) == 0 {
		return []StageResult{{Stage: StageFilesChanged, Passed: true, Skipped: true, Detail: "branch has no changes"}}, nil
	}
	if scanner == nil || len(tools) == 0 {
		return nil, errors.New("original scanner scope is unavailable")
	}
	// Build every changed language, including files outside targeted findings.
	// Deleted files still select their language but are not passed to parsers.
	var existing []string
	for _, file := range changed {
		if _, err := os.Stat(filepath.Join(ws.Path(), file)); err == nil {
			existing = append(existing, file)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	var stages []StageResult
	seen := map[string]bool{}
	for _, file := range changed {
		lang := langForFile(file)
		if filepath.Base(file) == "go.mod" || filepath.Base(file) == "go.sum" {
			lang, file = "go", "main.go"
		}
		if seen[lang] {
			continue
		}
		seen[lang] = true
		stage := buildStage(ctx, ws.Path(), models.Finding{FilePath: file}, existing)
		stages = append(stages, stage)
		if err := ctx.Err(); err != nil {
			return stages, err
		}
		if !stage.Passed {
			return stages, fmt.Errorf("final build check failed: %s", stage.Detail)
		}
	}
	post, err := scanner.Rescan(ctx, ws.Path(), tools)
	if err != nil {
		return stages, fmt.Errorf("final branch rescan failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return stages, err
	}
	// Counts preserve multiple occurrences while tolerating line shifts caused
	// by unrelated edits, matching the loop rescan's rule/file identity.
	counts, accepted := map[string]int{}, map[string]bool{}
	for _, f := range baseline {
		counts[branchFindingKey(f)]++
	}
	for _, f := range cleared {
		accepted[branchFindingKey(f)] = true
	}
	for _, f := range post {
		key := branchFindingKey(f)
		if accepted[key] {
			return stages, fmt.Errorf("previously accepted fix is still reported: %s %s in %s", f.ToolName, f.RuleID, f.FilePath)
		}
		counts[key]--
		if counts[key] < 0 {
			return stages, fmt.Errorf("branch introduced a finding: %s %s in %s", f.ToolName, f.RuleID, f.FilePath)
		}
	}
	return append(stages,
		StageResult{Stage: StageFindingCleared, Passed: true, Detail: "accepted fixes remain cleared in final branch scan"},
		StageResult{Stage: StageNoRegressions, Passed: true, Detail: "no new findings from original scan tools"}), nil
}

func branchFindingKey(f models.Finding) string {
	return strings.ToLower(f.ToolName) + "\x00" + f.RuleID + "\x00" + filepath.ToSlash(filepath.Clean(f.FilePath))
}
