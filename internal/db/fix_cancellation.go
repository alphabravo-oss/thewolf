package db

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

func (s *SQLiteStore) CompleteFixCancellation(ctx context.Context, job *models.FixJob, cleanup func(context.Context, *models.FixJob, *models.Repo) error) error {
	return completeFixCancellation(ctx, s.db, job, cleanup)
}
func (s *PostgresStore) CompleteFixCancellation(ctx context.Context, job *models.FixJob, cleanup func(context.Context, *models.FixJob, *models.Repo) error) error {
	return completeFixCancellation(ctx, s.db, job, cleanup)
}

func completeFixCancellation(ctx context.Context, database *sqlx.DB, job *models.FixJob, cleanup func(context.Context, *models.FixJob, *models.Repo) error) error {
	if job == nil || cleanup == nil {
		return errors.New("fix cancellation requires a job and cleanup")
	}
	// ponytail: rare cleanup holds claim/repository exclusion during local I/O.
	// A fenced cleanup worker can replace this if large checkouts delay claims.
	tx, repo, err := beginFixWorkspaceChange(ctx, database, job.RepoID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var saved models.FixJob
	if err := tx.GetContext(ctx, &saved, tx.Rebind("SELECT * FROM fix_jobs WHERE id=?"), job.ID); err != nil {
		return err
	}
	if saved.Revision != job.Revision || saved.RepoID != job.RepoID || saved.Status != models.FixJobCancelled || saved.PauseReason != models.FixPauseCancellationCleanup {
		return ErrFixJobConflict
	}
	branch := saved.ResultBranch
	if branch == "" {
		branch = saved.TargetBranch
	}
	var busy int
	if err := tx.GetContext(ctx, &busy, tx.Rebind(`SELECT COUNT(*) FROM fix_jobs WHERE id<>? AND repo_id=?
 AND status IN ('queued','claimed','running','awaiting_review','awaiting_push','push_failed')
 AND ((?<>'' AND remediation_id=?) OR (?<>'' AND (result_branch=? OR target_branch=?)) OR (?<>'' AND workspace_path=?))`),
		saved.ID, saved.RepoID, saved.RemediationID, saved.RemediationID, branch, branch, branch, saved.WorkspacePath, saved.WorkspacePath); err != nil {
		return err
	}
	if busy != 0 {
		return ErrFixBusy
	}
	if err := checkFixWorkspaceScans(ctx, tx, saved.RepoID, saved.RemediationID, saved.WorkspacePath); err != nil {
		return err
	}
	saved.FindingIDList = decodeStrings(saved.FindingIDs)
	// The engine has stopped. Finalize any journal entry whose last write was
	// interrupted before releasing its checkout; retry and cleanup are atomic.
	if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE fix_attempts SET outcome='interrupted',
 diff_excerpt='Job cancelled before verification completed.' WHERE job_id=? AND outcome='running'`), saved.ID); err != nil {
		return err
	}
	if err := cleanup(ctx, &saved, repo); err != nil {
		return err
	}
	now := time.Now().UTC()
	if saved.RemediationID != "" {
		if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE remediations SET state='discarded',workspace_path='',updated_at=? WHERE id=? AND repo_id=? AND state='open'`), now, saved.RemediationID, saved.RepoID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE fix_jobs SET revision=revision+1,workspace_path='',result_branch='',claimed_by='',pause_reason='',resume_action='',error='',updated_at=? WHERE id=?`), now, saved.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	saved.Revision++
	saved.WorkspacePath, saved.ResultBranch, saved.ClaimedBy, saved.PauseReason, saved.ResumeAction, saved.Error = "", "", "", "", "", ""
	saved.UpdatedAt = now
	*job = saved
	return nil
}
