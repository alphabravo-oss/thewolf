package lineage

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// MaybeEnqueueNextRun starts the next sequential agent job after a child
// scan of a finished run completes. No-op unless the previous job asked
// for more than one run.
func MaybeEnqueueNextRun(ctx context.Context, store db.Store, child *models.Scan) (*models.FixJob, error) {
	if store == nil || child == nil {
		return nil, nil
	}
	if child.Status != models.ScanStatusCompleted {
		return nil, nil
	}
	if child.FixJobID == "" || child.OriginScanID == "" {
		return nil, nil
	}
	prev, err := store.GetFixJobByID(ctx, child.FixJobID)
	if err != nil || prev == nil {
		return nil, err
	}
	if prev.PlannedRuns <= 1 || prev.RunIndex >= prev.PlannedRuns {
		return nil, nil
	}
	switch prev.Status {
	case models.FixJobAwaitingPush, models.FixJobSucceeded:
	default:
		return nil, nil
	}
	if prev.RemediationID == "" {
		return nil, nil
	}
	rem, err := store.GetRemediationByID(ctx, prev.RemediationID)
	if err != nil || rem == nil || rem.State != models.RemediationOpen {
		return nil, err
	}
	existing, err := store.ListFixJobsByRemediation(ctx, rem.ID)
	if err != nil {
		return nil, err
	}
	for _, j := range existing {
		if models.RemediationBusy(j.Status) {
			return nil, nil
		}
		if j.RunIndex > prev.RunIndex && j.PlannedRuns == prev.PlannedRuns {
			return nil, nil
		}
	}

	findings, ferr := store.ListFindingsByScan(ctx, child.ID)
	if ferr != nil {
		return nil, ferr
	}
	ids := make([]string, 0, len(findings))
	for _, f := range findings {
		ids = append(ids, f.ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()
	next := &models.FixJob{
		ID:             uuid.NewString(),
		UserID:         prev.UserID,
		Type:           "fix",
		RepoID:         prev.RepoID,
		ScanID:         child.ID,
		RemediationID:  rem.ID,
		FindingIDList:  ids,
		TargetBranch:   rem.Branch,
		Engine:         prev.Engine,
		Mode:           prev.Mode,
		SeverityFloor:  prev.SeverityFloor,
		MaxAttempts:    prev.MaxAttempts,
		MaxLoops:       prev.MaxLoops,
		HumanInTheLoop: false,
		WorkspacePath:  firstNonEmpty(rem.WorkspacePath, prev.WorkspacePath),
		Model:          prev.Model,
		Effort:         prev.Effort,
		Variant:        prev.Variant,
		PlannedRuns:    prev.PlannedRuns,
		RunIndex:       prev.RunIndex + 1,
		Status:         models.FixJobQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := store.EnqueueFixJob(ctx, next); err != nil {
		return nil, fmt.Errorf("lineage: enqueue next run: %w", err)
	}
	_ = SupersedeSiblings(ctx, store, next)
	return next, nil
}
