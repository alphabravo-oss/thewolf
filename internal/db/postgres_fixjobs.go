package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// --- Fix jobs (autonomous fix engine) ---
// encodeStrings/decodeStrings live in sqlite_fixjobs.go (same package).

func (s *PostgresStore) EnqueueFixJob(ctx context.Context, j *models.FixJob) error {
	now := time.Now().UTC()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	if j.Status == "" {
		j.Status = models.FixJobQueued
	}
	j.FindingIDs = encodeStrings(j.FindingIDList)
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO fix_jobs
		 (id, user_id, type, repo_id, scan_id, finding_ids, target_branch, engine, mode,
		  severity_floor, max_attempts, status, claimed_by, result_branch, diff_artifact_id,
		  summary, error, claimed_at, started_at, finished_at, heartbeat_at, created_at, updated_at)
		 VALUES
		 (:id, :user_id, :type, :repo_id, :scan_id, :finding_ids, :target_branch, :engine, :mode,
		  :severity_floor, :max_attempts, :status, :claimed_by, :result_branch, :diff_artifact_id,
		  :summary, :error, :claimed_at, :started_at, :finished_at, :heartbeat_at, :created_at, :updated_at)`, j)
	return err
}

func (s *PostgresStore) GetFixJobByID(ctx context.Context, id string) (*models.FixJob, error) {
	var j models.FixJob
	if err := s.db.GetContext(ctx, &j, "SELECT * FROM fix_jobs WHERE id = $1", id); err != nil {
		return nil, err
	}
	j.FindingIDList = decodeStrings(j.FindingIDs)
	return &j, nil
}

func (s *PostgresStore) ListFixJobs(ctx context.Context, repoID string) ([]models.FixJob, error) {
	var jobs []models.FixJob
	var err error
	if repoID == "" {
		err = s.db.SelectContext(ctx, &jobs, "SELECT * FROM fix_jobs ORDER BY created_at DESC")
	} else {
		err = s.db.SelectContext(ctx, &jobs, "SELECT * FROM fix_jobs WHERE repo_id = $1 ORDER BY created_at DESC", repoID)
	}
	for i := range jobs {
		jobs[i].FindingIDList = decodeStrings(jobs[i].FindingIDs)
	}
	return jobs, err
}

// ListFixJobsByUser returns only jobs owned by userID, optionally narrowed to
// one repo. The tenant-scoped counterpart to ListFixJobs — the API uses this so
// one user can never enumerate another's fix jobs (and their diffs/logs).
func (s *PostgresStore) ListFixJobsByUser(ctx context.Context, userID, repoID string) ([]models.FixJob, error) {
	var jobs []models.FixJob
	var err error
	if repoID == "" {
		err = s.db.SelectContext(ctx, &jobs,
			"SELECT * FROM fix_jobs WHERE user_id = $1 ORDER BY created_at DESC", userID)
	} else {
		err = s.db.SelectContext(ctx, &jobs,
			"SELECT * FROM fix_jobs WHERE user_id = $1 AND repo_id = $2 ORDER BY created_at DESC", userID, repoID)
	}
	for i := range jobs {
		jobs[i].FindingIDList = decodeStrings(jobs[i].FindingIDs)
	}
	return jobs, err
}

// ClaimNextFixJob uses FOR UPDATE SKIP LOCKED — the canonical concurrent
// queue claim. Multiple workers each grab a different job without blocking,
// and a job is never double-claimed. Returns (nil, nil) when empty.
func (s *PostgresStore) ClaimNextFixJob(ctx context.Context, workerID string) (*models.FixJob, error) {
	var j models.FixJob
	now := time.Now().UTC()
	err := s.db.GetContext(ctx, &j,
		`UPDATE fix_jobs SET status = $1, claimed_by = $2, claimed_at = $3, heartbeat_at = $3, updated_at = $3
		 WHERE id = (
		   SELECT id FROM fix_jobs WHERE status = $4 ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
		 )
		 RETURNING *`,
		models.FixJobClaimed, workerID, now, models.FixJobQueued)
	if err == sql.ErrNoRows {
		return nil, nil // empty queue
	}
	if err != nil {
		return nil, err
	}
	j.FindingIDList = decodeStrings(j.FindingIDs)
	return &j, nil
}

func (s *PostgresStore) UpdateFixJob(ctx context.Context, j *models.FixJob) error {
	j.UpdatedAt = time.Now().UTC()
	j.FindingIDs = encodeStrings(j.FindingIDList)
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE fix_jobs SET
		   type=:type, repo_id=:repo_id, scan_id=:scan_id, finding_ids=:finding_ids,
		   target_branch=:target_branch, engine=:engine, mode=:mode, severity_floor=:severity_floor,
		   max_attempts=:max_attempts, status=:status, claimed_by=:claimed_by, result_branch=:result_branch,
		   diff_artifact_id=:diff_artifact_id, summary=:summary, error=:error,
		   claimed_at=:claimed_at, started_at=:started_at, finished_at=:finished_at,
		   heartbeat_at=:heartbeat_at, updated_at=:updated_at
		 WHERE id=:id`, j)
	return err
}

func (s *PostgresStore) ReclaimStaleJobs(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE fix_jobs SET status = $1, claimed_by = '', updated_at = $2
		 WHERE status IN ($3, $4) AND (heartbeat_at IS NULL OR heartbeat_at < $5)`,
		models.FixJobQueued, time.Now().UTC(), models.FixJobClaimed, models.FixJobRunning, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *PostgresStore) CreateFixAttempt(ctx context.Context, a *models.FixAttempt) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO fix_attempts
		 (id, job_id, finding_id, attempt_no, engine_used, model, built, finding_cleared,
		  new_findings, outcome, files_changed, diff_excerpt, duration_ms, cost_usd, created_at)
		 VALUES
		 (:id, :job_id, :finding_id, :attempt_no, :engine_used, :model, :built, :finding_cleared,
		  :new_findings, :outcome, :files_changed, :diff_excerpt, :duration_ms, :cost_usd, :created_at)`, a)
	return err
}

func (s *PostgresStore) ListFixAttempts(ctx context.Context, jobID string) ([]models.FixAttempt, error) {
	var attempts []models.FixAttempt
	err := s.db.SelectContext(ctx, &attempts,
		"SELECT * FROM fix_attempts WHERE job_id = $1 ORDER BY created_at ASC", jobID)
	return attempts, err
}
