package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

var (
	ErrFixIdempotencyConflict  = errors.New("Idempotency-Key was already used for a different fix request")
	ErrFixScope                = errors.New("fix repository, scan or finding is unavailable")
	ErrFixBusy                 = errors.New("this fix branch is busy or awaiting cancellation cleanup")
	ErrFixFrozen               = errors.New("this scan's fix branch was handed off; scan it again to continue")
	ErrFixBranchConflict       = errors.New("target branch differs from this scan's existing fix branch")
	ErrFixSourceBranchConflict = errors.New("findings span different source branches; submit a separate fix for each branch")
)

func (s *PostgresStore) FindFixJobByIdempotencyKey(ctx context.Context, user, key string) (*models.FixJob, error) {
	return findFixJobKey(ctx, s.db, user, key)
}
func findFixJobKey(ctx context.Context, q sqlx.ExtContext, user, key string) (*models.FixJob, error) {
	var job models.FixJob
	query := sqlx.Rebind(sqlx.BindType(q.DriverName()), "SELECT * FROM fix_jobs WHERE user_id=? AND idempotency_key=?")
	if err := sqlx.GetContext(ctx, q, &job, query, user, key); err != nil {
		return nil, err
	}
	job.FindingIDList = decodeStrings(job.FindingIDs)
	return &job, nil
}

func (s *PostgresStore) CreateFixJob(ctx context.Context, job *models.FixJob, allFindings bool) (*models.FixJob, bool, error) {
	return createFixJob(ctx, s.db, job, allFindings)
}

// Create the branch session, supersede old review jobs, and enqueue atomically.
func createFixJob(ctx context.Context, database *sqlx.DB, job *models.FixJob, allFindings bool) (*models.FixJob, bool, error) {
	if job == nil || job.UserID == "" || job.RepoID == "" || len(job.IdempotencyKey) > 255 || (job.IdempotencyKey != "" && job.RequestDigest == "") {
		return nil, false, ErrFixScope
	}
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// ponytail: creation is serialized per owner and repository for short transactions.
	// Separate command-key locks can replace this if submission throughput warrants it.
	suffix := ""
	if database.DriverName() == "postgres" {
		suffix = " FOR UPDATE"
	}
	var actor string
	if err := tx.GetContext(ctx, &actor, tx.Rebind("SELECT id FROM users WHERE id=?"+suffix), job.UserID); err != nil {
		return nil, false, err
	}
	if job.IdempotencyKey != "" {
		existing, err := findFixJobKey(ctx, tx, job.UserID, job.IdempotencyKey)
		if err == nil {
			if existing.RequestDigest != job.RequestDigest {
				return nil, false, ErrFixIdempotencyConflict
			}
			return existing, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
	}
	var repo models.Repo
	if err := tx.GetContext(ctx, &repo, tx.Rebind("SELECT * FROM repos WHERE id=?"+suffix), job.RepoID); err != nil {
		return nil, false, ErrFixScope
	}
	if repo.UserID != "" && repo.UserID != job.UserID {
		return nil, false, ErrFixScope
	}
	// A pending cancellation owns its checkout until cleanup is acknowledged.
	var cancelling int
	if err := tx.GetContext(ctx, &cancelling, tx.Rebind("SELECT COUNT(*) FROM fix_jobs WHERE repo_id=? AND status='cancelled' AND pause_reason='cancellation_cleanup'"), job.RepoID); err != nil {
		return nil, false, err
	}
	if cancelling > 0 {
		return nil, false, ErrFixBusy
	}
	if job.ScanID != "" {
		var scan models.Scan
		if err := tx.GetContext(ctx, &scan, tx.Rebind("SELECT * FROM scans WHERE id=?"), job.ScanID); err != nil {
			return nil, false, ErrFixScope
		}
		if scan.RepoID != job.RepoID || (scan.UserID != "" && scan.UserID != job.UserID) {
			return nil, false, ErrFixScope
		}
		if job.RunIndex > 1 {
			var previous models.FixJob
			if err := tx.GetContext(ctx, &previous, tx.Rebind("SELECT * FROM fix_jobs WHERE id=?"), scan.FixJobID); err != nil {
				return nil, false, ErrFixJobConflict
			}
			if scan.Status != models.ScanStatusCompleted || previous.RepoID != job.RepoID || previous.UserID != job.UserID ||
				(previous.Status != models.FixJobAwaitingPush && previous.Status != models.FixJobSucceeded) ||
				previous.RunIndex+1 != job.RunIndex || previous.PlannedRuns != job.PlannedRuns || job.RunIndex > job.PlannedRuns {
				return nil, false, ErrFixJobConflict
			}
		}
		origin := scan.OriginScanID
		if origin == "" {
			origin = scan.ID
		}
		var originScan models.Scan
		if err := tx.GetContext(ctx, &originScan, tx.Rebind("SELECT * FROM scans WHERE id=?"), origin); err != nil {
			return nil, false, ErrFixScope
		}
		if originScan.RepoID != job.RepoID || (originScan.UserID != "" && originScan.UserID != job.UserID) {
			return nil, false, ErrFixScope
		}
		var rem models.Remediation
		err := tx.GetContext(ctx, &rem, tx.Rebind("SELECT * FROM remediations WHERE origin_scan_id=? ORDER BY created_at DESC,id DESC LIMIT 1"), origin)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
		if err == nil && (rem.RepoID != job.RepoID || (rem.UserID != "" && rem.UserID != job.UserID)) {
			return nil, false, ErrFixScope
		}
		if err == nil && rem.State == models.RemediationFrozen {
			return nil, false, ErrFixFrozen
		}
		if err == nil && rem.State == models.RemediationOpen {
			if err := checkFixWorkspaceScans(ctx, tx, job.RepoID, rem.ID, rem.WorkspacePath); err != nil {
				return nil, false, err
			}
			var busy int
			if err := tx.GetContext(ctx, &busy, tx.Rebind("SELECT COUNT(*) FROM fix_jobs WHERE remediation_id=? AND status IN ('queued','claimed','running')"), rem.ID); err != nil {
				return nil, false, err
			}
			if busy > 0 {
				return nil, false, ErrFixBusy
			}
			if job.TargetBranch != "" && job.TargetBranch != rem.Branch {
				return nil, false, ErrFixBranchConflict
			}
			if allFindings && scan.ID == origin {
				var child string
				err := tx.GetContext(ctx, &child, tx.Rebind("SELECT id FROM scans WHERE origin_scan_id=? AND repo_id=? AND user_id=? AND status='completed' ORDER BY created_at DESC,id DESC LIMIT 1"), origin, job.RepoID, job.UserID)
				if err == nil {
					job.ScanID = child
				} else if !errors.Is(err, sql.ErrNoRows) {
					return nil, false, err
				}
			}
		} else {
			branch := job.TargetBranch
			if branch == "" {
				branch = "wolf-fix/" + strings.TrimSpace(origin)
			}
			now := time.Now().UTC()
			rem = models.Remediation{ID: uuid.NewString(), UserID: job.UserID, RepoID: job.RepoID, OriginScanID: origin, Branch: branch, State: models.RemediationOpen, CreatedAt: now, UpdatedAt: now}
			if _, err := tx.NamedExecContext(ctx, `INSERT INTO remediations(id,user_id,repo_id,origin_scan_id,branch,state,created_at,updated_at) VALUES(:id,:user_id,:repo_id,:origin_scan_id,:branch,:state,:created_at,:updated_at)`, &rem); err != nil {
				return nil, false, err
			}
		}
		job.RemediationID, job.TargetBranch, job.WorkspacePath = rem.ID, rem.Branch, rem.WorkspacePath
		job.BaseBranch = originScan.Branch
		if allFindings {
			if err := tx.SelectContext(ctx, &job.FindingIDList, tx.Rebind("SELECT id FROM findings WHERE scan_id=? AND (cluster_id='' OR cluster_id=id) ORDER BY id"), job.ScanID); err != nil {
				return nil, false, err
			}
		}
	}
	if !allFindings && len(job.FindingIDList) > 0 {
		var scopes []struct {
			RepoID string `db:"repo_id"`
			ScanID string `db:"scan_id"`
			UserID string `db:"user_id"`
			Branch string `db:"branch"`
		}
		query, args, err := sqlx.In("SELECT s.repo_id,f.scan_id,s.user_id,s.branch FROM findings f JOIN scans s ON s.id=f.scan_id WHERE f.id IN (?)", job.FindingIDList)
		if err != nil {
			return nil, false, err
		}
		if err := tx.SelectContext(ctx, &scopes, tx.Rebind(query), args...); err != nil {
			return nil, false, err
		}
		if len(scopes) != len(job.FindingIDList) {
			return nil, false, ErrFixScope
		}
		for i, scope := range scopes {
			if scope.RepoID != job.RepoID || (scope.UserID != "" && scope.UserID != job.UserID) || (job.ScanID != "" && scope.ScanID != job.ScanID) {
				return nil, false, ErrFixScope
			}
			if job.ScanID == "" {
				branch := scope.Branch
				if branch == "" {
					branch = repo.DefaultBranch
				}
				if i == 0 {
					job.BaseBranch = branch
				} else if job.BaseBranch != branch {
					return nil, false, ErrFixSourceBranchConflict
				}
			}
		}
	}
	if job.BaseBranch == "" {
		job.BaseBranch = repo.DefaultBranch
	}

	if err := enqueueFixJob(ctx, tx, job); err != nil {
		return nil, false, err
	}
	if job.RemediationID != "" {
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, tx.Rebind("UPDATE fix_jobs SET revision=revision+1,status='superseded',pause_reason='superseded by a later run on this branch',finished_at=?,updated_at=? WHERE remediation_id=? AND id<>? AND status IN ('awaiting_review','awaiting_push','push_failed')"), now, now, job.RemediationID, job.ID); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return job, false, nil
}
