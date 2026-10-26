package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// Final acceptance includes the original scan, even when the job targeted only
// selected findings. A later tool must not erase earlier keeps or add findings
// in another file/tool that targeted verification never visited.
func verifyFinalBranch(ctx context.Context, job *models.FixJob, ws Workspace, deps Deps, summary Summary) ([]verify.StageResult, error) {
	selected, err := (storeFindings{deps.Store}).gatherFindings(ctx, job)
	if err != nil {
		return nil, err
	}
	baseline := map[string]models.Finding{}
	scans := map[string]bool{}
	if job.ScanID != "" {
		scans[job.ScanID] = true
	}
	for _, f := range selected {
		baseline[f.ID] = f
		if f.ScanID != "" {
			scans[f.ScanID] = true
		}
	}
	toolSet := map[string]bool{}
	for id := range scans {
		scan, err := deps.Store.GetScanByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load verification scan: %w", err)
		}
		if scan == nil || scan.RepoID != job.RepoID {
			return nil, errors.New("original verification scan is missing or belongs to another repository")
		}
		for _, raw := range []string{scan.ToolsSelected, scan.ToolsCompleted} {
			if raw == "" {
				continue
			}
			var tools []string
			if err := json.Unmarshal([]byte(raw), &tools); err != nil {
				return nil, fmt.Errorf("read original scanner scope: %w", err)
			}
			for _, tool := range tools {
				toolSet[tool] = true
			}
		}
		findings, err := deps.Store.ListFindingsByScan(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load verification baseline: %w", err)
		}
		for _, f := range findings {
			baseline[f.ID] = f
		}
	}
	var before, cleared []models.Finding
	for _, f := range baseline {
		before = append(before, f)
		toolSet[f.ToolName] = true
	}
	accepted := map[string]bool{}
	for _, f := range summary.Addressed {
		accepted[f.ID] = true
	}
	for _, round := range summary.Rounds {
		for _, id := range round.Cleared {
			accepted[id] = true
		}
	}
	for id := range accepted {
		original, ok := baseline[id]
		if !ok {
			return nil, fmt.Errorf("accepted finding %s is missing from the verification baseline", id)
		}
		cleared = append(cleared, original)
	}
	if summary.Kept > len(cleared) {
		return nil, errors.New("accepted findings lack complete verification evidence")
	}
	delete(toolSet, "")
	var tools []string
	for tool := range toolSet {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	if deps.Verifier == nil {
		return nil, errors.New("final verifier is not configured")
	}
	diff, err := ws.Diff(ctx)
	if err != nil {
		return nil, err
	}
	stages, err := deps.Verifier.VerifyBranch(ctx, branchVerificationWorkspace{ws}, before, cleared, tools)
	if err != nil {
		return stages, err
	}
	after, err := ws.Diff(ctx)
	if err != nil {
		return stages, err
	}
	if diff != after {
		return stages, errors.New("workspace changed during final verification")
	}
	return stages, nil
}
