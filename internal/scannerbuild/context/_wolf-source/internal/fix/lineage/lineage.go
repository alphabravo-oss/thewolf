// Package lineage binds origin scans, remediations, and child scans.
package lineage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// BranchName is the shared fix branch for an origin scan.
func BranchName(originScanID string) string {
	return "wolf-fix/" + strings.TrimSpace(originScanID)
}

// OriginID returns the lineage root for a scan (itself if it is a root).
func OriginID(scan *models.Scan) string {
	if scan == nil {
		return ""
	}
	if id := strings.TrimSpace(scan.OriginScanID); id != "" {
		return id
	}
	return scan.ID
}

// EnqueueChildScan records a full scan of the idle remediation workspace after
// an agent run pauses or finishes. It does not run scanners.
func EnqueueChildScan(ctx context.Context, store db.Store, origin *models.Scan, job *models.FixJob, rem *models.Remediation) (*models.Scan, error) {
	if store == nil || origin == nil || rem == nil {
		return nil, fmt.Errorf("lineage: origin, remediation, and store are required")
	}
	workspace := strings.TrimSpace(rem.WorkspacePath)
	if job != nil && strings.TrimSpace(job.WorkspacePath) != "" {
		workspace = strings.TrimSpace(job.WorkspacePath)
	}
	if workspace == "" {
		return nil, fmt.Errorf("lineage: no workspace to scan")
	}
	if st, err := os.Stat(workspace); err != nil || !st.IsDir() {
		// Tests may pass a synthetic path; still record it so the child
		// carries the prepared_workspace field. Live workers skip missing dirs.
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("lineage: workspace %s: %w", workspace, err)
		}
	}

	previous := origin.ID
	if kids, err := store.ListScansByOrigin(ctx, origin.ID); err == nil {
		for _, s := range kids {
			if s.ID == origin.ID {
				continue
			}
			if strings.TrimSpace(s.OriginScanID) == origin.ID {
				previous = s.ID
			}
		}
	}

	now := time.Now().UTC()
	child := &models.Scan{
		ID:                uuid.NewString(),
		UserID:            origin.UserID,
		RepoID:            origin.RepoID,
		CollectionID:      origin.CollectionID,
		Branch:            rem.Branch,
		SourceType:        origin.SourceType,
		RemoteNodeID:      origin.RemoteNodeID,
		SourcePath:        origin.SourcePath,
		SourceFingerprint: origin.SourceFingerprint,
		PreparedWorkspace: workspace,
		RequestJSON:       origin.RequestJSON,
		RequestDigest:     origin.RequestDigest,
		Profile:           origin.Profile,
		Categories:        origin.Categories,
		IncludePaths:      origin.IncludePaths,
		ExcludePaths:      origin.ExcludePaths,
		ToolsSelected:     origin.ToolsSelected,
		Status:            models.ScanStatusPending,
		Phase:             "queued",
		MaxAttempts:       origin.MaxAttempts,
		OriginScanID:      origin.ID,
		PreviousScanID:    previous,
		RemediationID:     rem.ID,
		CommitSHA:         headSHA(workspace),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if job != nil {
		child.FixJobID = job.ID
		child.UserID = firstNonEmpty(child.UserID, job.UserID)
	}
	if err := store.CreateScan(ctx, child); err != nil {
		return nil, fmt.Errorf("lineage: create child scan: %w", err)
	}
	return child, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func headSHA(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// AfterAgentRun enqueues a child scan when the job is paused or succeeded,
// and freezes the remediation after a successful push.
func AfterAgentRun(ctx context.Context, store db.Store, job *models.FixJob) (*models.Scan, error) {
	if store == nil || job == nil || strings.TrimSpace(job.RemediationID) == "" {
		return nil, nil
	}
	rem, err := store.GetRemediationByID(ctx, job.RemediationID)
	if err != nil || rem == nil {
		return nil, err
	}
	if job.WorkspacePath != "" && rem.WorkspacePath != job.WorkspacePath {
		rem.WorkspacePath = job.WorkspacePath
		_ = store.UpdateRemediation(ctx, rem)
	}
	_ = SupersedeSiblings(ctx, store, job)

	pushed := job.Pushed || (job.Status == models.FixJobSucceeded && job.PushSHA != "")
	if pushed {
		if err := Freeze(ctx, rem, job.PushSHA, store); err != nil {
			return nil, err
		}
		// The branch is on the remote. A local workspace rescan here is
		// what made the page look like a scan was still running after push.
		return nil, nil
	}
	switch job.Status {
	case models.FixJobSucceeded, models.FixJobAwaitingReview, models.FixJobAwaitingPush:
	default:
		return nil, nil
	}
	originID := rem.OriginScanID
	origin, err := store.GetScanByID(ctx, originID)
	if err != nil || origin == nil {
		return nil, err
	}
	return EnqueueChildScan(ctx, store, origin, job, rem)
}

// SupersedeSiblings marks older paused jobs on the same fix branch as
// superseded. The branch is shared; only the latest run should sit in
// awaiting_push / awaiting_review.
func SupersedeSiblings(ctx context.Context, store db.Store, current *models.FixJob) error {
	if store == nil || current == nil {
		return nil
	}
	var jobs []models.FixJob
	var err error
	if rem := strings.TrimSpace(current.RemediationID); rem != "" {
		jobs, err = store.ListFixJobsByRemediation(ctx, rem)
	} else if current.RepoID != "" {
		jobs, err = store.ListFixJobs(ctx, current.RepoID)
	} else {
		return nil
	}
	if err != nil {
		return err
	}
	branch := strings.TrimSpace(firstNonEmpty(current.ResultBranch, current.TargetBranch))
	now := time.Now().UTC()
	for i := range jobs {
		j := &jobs[i]
		if j.ID == current.ID {
			continue
		}
		if !models.FixJobPaused(j.Status) {
			continue
		}
		if current.RemediationID == "" {
			jb := strings.TrimSpace(firstNonEmpty(j.ResultBranch, j.TargetBranch))
			if branch == "" || jb != branch {
				continue
			}
		}
		j.Status = models.FixJobSuperseded
		j.PauseReason = "superseded by a later run on this branch"
		j.FinishedAt = &now
		if err := store.UpdateFixJob(ctx, j); err != nil {
			return err
		}
	}
	return nil
}

// Freeze marks a remediation published. Further Fix on this origin is refused.
func Freeze(ctx context.Context, rem *models.Remediation, sha string, store db.Store) error {
	if rem == nil || store == nil {
		return nil
	}
	now := time.Now().UTC()
	rem.State = models.RemediationFrozen
	if strings.TrimSpace(sha) != "" {
		rem.PublishedSHA = sha
	}
	rem.PublishedAt = &now
	return store.UpdateRemediation(ctx, rem)
}

// Discard marks the remediation discarded after the workspace/branch are gone.
func Discard(ctx context.Context, rem *models.Remediation, store db.Store) error {
	if rem == nil || store == nil {
		return nil
	}
	rem.State = models.RemediationDiscarded
	rem.WorkspacePath = ""
	return store.UpdateRemediation(ctx, rem)
}
