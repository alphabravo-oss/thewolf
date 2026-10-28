package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

// beginFixWorkspaceChange serializes filesystem changes with fix claims and
// repository submissions. Callers must commit or roll back the returned transaction.
func beginFixWorkspaceChange(ctx context.Context, database *sqlx.DB, repoID string) (*sqlx.Tx, *models.Repo, error) {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	suffix := ""
	if database.DriverName() == "postgres" {
		_, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('wolf.fix-job-claim', 0))")
		suffix = " FOR UPDATE"
	}
	var repo models.Repo
	if err == nil {
		err = tx.GetContext(ctx, &repo, tx.Rebind("SELECT * FROM repos WHERE id=?"+suffix), repoID)
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	return tx, &repo, nil
}

func checkFixWorkspaceScans(ctx context.Context, tx *sqlx.Tx, repoID, remediationID, path string) error {
	var busy int
	err := tx.GetContext(ctx, &busy, tx.Rebind(`SELECT COUNT(*) FROM scans WHERE repo_id=? AND status IN ('pending','running')
 AND ((?<>'' AND remediation_id=?) OR (?<>'' AND prepared_workspace=?))`), repoID, remediationID, remediationID, path, path)
	if err != nil {
		return err
	}
	if busy != 0 {
		return ErrFixBusy
	}
	return nil
}

func (s *PostgresStore) AcceptFixRemediation(ctx context.Context, rem *models.Remediation, cleanup func(context.Context, *models.Remediation, *models.Repo) (string, error)) error {
	return acceptFixRemediation(ctx, s.db, rem, cleanup)
}
func acceptFixRemediation(ctx context.Context, database *sqlx.DB, rem *models.Remediation, cleanup func(context.Context, *models.Remediation, *models.Repo) (string, error)) error {
	if rem == nil || cleanup == nil {
		return errors.New("remediation handoff requires a remediation and cleanup")
	}
	tx, repo, err := beginFixWorkspaceChange(ctx, database, rem.RepoID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var saved models.Remediation
	if err := tx.GetContext(ctx, &saved, tx.Rebind("SELECT * FROM remediations WHERE id=?"), rem.ID); err != nil {
		return err
	}
	if saved.RepoID != rem.RepoID || saved.UserID != rem.UserID {
		return ErrFixScope
	}
	if saved.State == models.RemediationFrozen && saved.WorkspacePath == "" {
		*rem = saved
		return nil // a repeated handoff cannot clean up another branch
	}
	if saved.State == models.RemediationFrozen {
		// A pushed branch with a failed PR still needs its checkout for retry.
		// Require a durably successful publication before releasing a clone.
		var jobs []models.FixJob
		if err := tx.SelectContext(ctx, &jobs, tx.Rebind(`SELECT * FROM fix_jobs WHERE remediation_id=?
 AND status='succeeded' AND pushed=1 AND push_sha=? AND workspace_path=?`), saved.ID, saved.PublishedSHA, saved.WorkspacePath); err != nil {
			return err
		}
		published := false
		for _, job := range jobs {
			if saved.PublishedSHA == "" || job.RepoID != saved.RepoID || job.UserID != saved.UserID {
				continue
			}
			if repo.SourceType == models.SourceTypeGitHub {
				var summary struct {
					PRURL            string `json:"pr_url"`
					PublicationError string `json:"publication_error"`
				}
				if json.Unmarshal([]byte(job.Summary), &summary) != nil || strings.TrimSpace(summary.PRURL) == "" || summary.PublicationError != "" {
					continue
				}
			}
			published = true
		}
		if !published {
			return ErrFixFrozen
		}
	} else if saved.State != models.RemediationOpen {
		return ErrFixFrozen
	}
	var busy int
	if err := tx.GetContext(ctx, &busy, tx.Rebind(`SELECT COUNT(*) FROM fix_jobs WHERE repo_id=?
 AND (status IN ('queued','claimed','running') OR (status='cancelled' AND pause_reason='cancellation_cleanup'))
 AND (remediation_id=? OR result_branch=? OR target_branch=? OR (?<>'' AND workspace_path=?))`),
		saved.RepoID, saved.ID, saved.Branch, saved.Branch, saved.WorkspacePath, saved.WorkspacePath); err != nil {
		return err
	}
	if busy != 0 {
		return ErrFixBusy
	}
	if err := checkFixWorkspaceScans(ctx, tx, saved.RepoID, saved.ID, saved.WorkspacePath); err != nil {
		return err
	}
	sha, err := cleanup(ctx, &saved, repo)
	if err != nil {
		return err
	}
	if sha == "" {
		return errors.New("handoff did not preserve a local branch commit")
	}
	now := time.Now().UTC()
	publishedAt := &now
	if saved.State == models.RemediationFrozen && saved.PublishedAt != nil {
		publishedAt = saved.PublishedAt
	}
	if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE remediations SET state='frozen',workspace_path='',published_sha=?,published_at=?,updated_at=? WHERE id=?`), sha, publishedAt, now, saved.ID); err != nil {
		return err
	}
	// Paused jobs must no longer offer resume against a removed checkout;
	// successful published jobs must release the same workspace atomically.
	if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE fix_jobs SET revision=revision+1,status='succeeded',workspace_path='',pause_reason='',resume_action='',error='',finished_at=COALESCE(finished_at,?),updated_at=?
 WHERE remediation_id=? AND (status IN ('awaiting_review','awaiting_push','push_failed') OR (status='succeeded' AND workspace_path=?))`), now, now, saved.ID, saved.WorkspacePath); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	saved.State, saved.WorkspacePath, saved.PublishedSHA = models.RemediationFrozen, "", sha
	saved.PublishedAt, saved.UpdatedAt = publishedAt, now
	*rem = saved
	return nil
}

// Only pending child scans need admission to the live shared workspace;
// terminal historical records can still be imported after handoff.
func beginRemediationScan(ctx context.Context, database *sqlx.DB, scan *models.Scan) (*sqlx.Tx, error) {
	if scan.RemediationID == "" || scan.PreparedWorkspace == "" || scan.Status != models.ScanStatusPending {
		return nil, nil
	}
	tx, _, err := beginFixWorkspaceChange(ctx, database, scan.RepoID)
	if err != nil {
		return nil, err
	}
	var rem models.Remediation
	err = tx.GetContext(ctx, &rem, tx.Rebind("SELECT * FROM remediations WHERE id=?"), scan.RemediationID)
	if err == nil && (rem.State != models.RemediationOpen || rem.WorkspacePath != scan.PreparedWorkspace) {
		err = ErrFixFrozen
	}
	if err == nil && (rem.RepoID != scan.RepoID || rem.UserID != scan.UserID) {
		err = ErrFixScope
	}
	if err == nil {
		var busy int
		err = tx.GetContext(ctx, &busy, tx.Rebind(`SELECT COUNT(*) FROM fix_jobs WHERE remediation_id=? AND
   (status IN ('queued','claimed','running') OR (status='cancelled' AND pause_reason='cancellation_cleanup'))`), rem.ID)
		if err == nil && busy != 0 {
			err = ErrFixBusy
		}
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}
