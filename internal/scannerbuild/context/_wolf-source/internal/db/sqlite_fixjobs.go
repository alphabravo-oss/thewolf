package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// --- Fix jobs (autonomous fix engine) ---

func encodeStrings(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func decodeStrings(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func (s *SQLiteStore) EnqueueFixJob(ctx context.Context, j *models.FixJob) error {
	now := time.Now().UTC()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	if j.Status == "" {
		j.Status = models.FixJobQueued
	}
	j.FindingIDs = encodeStrings(j.FindingIDList)
	if j.PlannedRuns <= 0 {
		j.PlannedRuns = 1
	}
	if j.RunIndex <= 0 {
		j.RunIndex = 1
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO fix_jobs
		 (id, user_id, type, repo_id, scan_id, finding_ids, target_branch, engine, mode,
		  severity_floor, max_attempts, max_loops, current_loop, human_in_the_loop,
		  workspace_path, base_branch, pushed, push_sha, pause_reason, resume_action,
		  model, effort, variant, remediation_id, planned_runs, run_index,
		  status, claimed_by, result_branch, diff_artifact_id,
		  summary, error, claimed_at, started_at, finished_at, heartbeat_at, created_at, updated_at)
		 VALUES
		 (:id, :user_id, :type, :repo_id, :scan_id, :finding_ids, :target_branch, :engine, :mode,
		  :severity_floor, :max_attempts, :max_loops, :current_loop, :human_in_the_loop,
		  :workspace_path, :base_branch, :pushed, :push_sha, :pause_reason, :resume_action,
		  :model, :effort, :variant, :remediation_id, :planned_runs, :run_index,
		  :status, :claimed_by, :result_branch, :diff_artifact_id,
		  :summary, :error, :claimed_at, :started_at, :finished_at, :heartbeat_at, :created_at, :updated_at)`, j)
	return err
}

func (s *SQLiteStore) GetFixJobByID(ctx context.Context, id string) (*models.FixJob, error) {
	var j models.FixJob
	if err := s.db.GetContext(ctx, &j, "SELECT * FROM fix_jobs WHERE id = ?", id); err != nil {
		return nil, err
	}
	j.FindingIDList = decodeStrings(j.FindingIDs)
	return &j, nil
}

func (s *SQLiteStore) ListFixJobs(ctx context.Context, repoID string) ([]models.FixJob, error) {
	var jobs []models.FixJob
	var err error
	if repoID == "" {
		err = s.db.SelectContext(ctx, &jobs, "SELECT * FROM fix_jobs ORDER BY created_at DESC")
	} else {
		err = s.db.SelectContext(ctx, &jobs, "SELECT * FROM fix_jobs WHERE repo_id = ? ORDER BY created_at DESC", repoID)
	}
	for i := range jobs {
		jobs[i].FindingIDList = decodeStrings(jobs[i].FindingIDs)
	}
	return jobs, err
}

// ListFixJobsByUser returns only jobs owned by userID, optionally narrowed to
// one repo. The tenant-scoped counterpart to ListFixJobs — the API uses this so
// one user can never enumerate another's fix jobs (and their diffs/logs).
func (s *SQLiteStore) ListFixJobsByUser(ctx context.Context, userID, repoID string) ([]models.FixJob, error) {
	var jobs []models.FixJob
	var err error
	if repoID == "" {
		err = s.db.SelectContext(ctx, &jobs,
			"SELECT * FROM fix_jobs WHERE user_id = ? ORDER BY created_at DESC", userID)
	} else {
		err = s.db.SelectContext(ctx, &jobs,
			"SELECT * FROM fix_jobs WHERE user_id = ? AND repo_id = ? ORDER BY created_at DESC", userID, repoID)
	}
	for i := range jobs {
		jobs[i].FindingIDList = decodeStrings(jobs[i].FindingIDs)
	}
	return jobs, err
}

// ClaimNextFixJob atomically claims the oldest queued job. SQLite has no
// UPDATE … RETURNING for older drivers reliably, so we run it inside a
// transaction with the single-connection in-memory guarantee (and a real
// file DB serializes writes anyway). Returns (nil, nil) when empty.
func (s *SQLiteStore) ClaimNextFixJob(ctx context.Context, workerID string) (*models.FixJob, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var id string
	row := tx.QueryRowxContext(ctx,
		`SELECT id FROM fix_jobs WHERE status = ? ORDER BY created_at LIMIT 1`, models.FixJobQueued)
	if err := row.Scan(&id); err != nil {
		// No queued job (sql.ErrNoRows) → empty queue.
		return nil, nil //nolint:nilerr // empty queue is not an error
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`UPDATE fix_jobs SET status = ?, claimed_by = ?, claimed_at = ?, heartbeat_at = ?, updated_at = ?
		 WHERE id = ? AND status = ?`,
		models.FixJobClaimed, workerID, now, now, now, id, models.FixJobQueued); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetFixJobByID(ctx, id)
}

func (s *SQLiteStore) UpdateFixJob(ctx context.Context, j *models.FixJob) error {
	if j != nil && j.Status != models.FixJobCancelled {
		var current string
		if err := s.db.GetContext(ctx, &current, "SELECT status FROM fix_jobs WHERE id = ?", j.ID); err == nil && current == models.FixJobCancelled {
			return nil
		}
	}
	j.UpdatedAt = time.Now().UTC()
	j.FindingIDs = encodeStrings(j.FindingIDList)
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE fix_jobs SET
		   type=:type, repo_id=:repo_id, scan_id=:scan_id, finding_ids=:finding_ids,
		   target_branch=:target_branch, engine=:engine, mode=:mode, severity_floor=:severity_floor,
		   max_attempts=:max_attempts, max_loops=:max_loops, current_loop=:current_loop,
		   human_in_the_loop=:human_in_the_loop, workspace_path=:workspace_path,
		   base_branch=:base_branch, pushed=:pushed, push_sha=:push_sha,
		   pause_reason=:pause_reason, resume_action=:resume_action,
		   model=:model, effort=:effort, variant=:variant, remediation_id=:remediation_id,
		   planned_runs=:planned_runs, run_index=:run_index,
		   status=:status, claimed_by=:claimed_by, result_branch=:result_branch,
		   diff_artifact_id=:diff_artifact_id, summary=:summary, error=:error,
		   claimed_at=:claimed_at, started_at=:started_at, finished_at=:finished_at,
		   heartbeat_at=:heartbeat_at, updated_at=:updated_at
		 WHERE id=:id`, j)
	return err
}

func (s *SQLiteStore) ReclaimStaleJobs(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE fix_jobs SET status = ?, claimed_by = '', updated_at = ?
		 WHERE status IN (?, ?) AND (heartbeat_at IS NULL OR heartbeat_at < ?)`,
		models.FixJobQueued, time.Now().UTC(), models.FixJobClaimed, models.FixJobRunning, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) CreateFixAttempt(ctx context.Context, a *models.FixAttempt) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO fix_attempts
		 (id, job_id, finding_id, attempt_no, engine_used, model, built, finding_cleared,
		  new_findings, outcome, files_changed, diff_excerpt, duration_ms, cost_usd,
		  input_tokens, output_tokens, created_at)
		 VALUES
		 (:id, :job_id, :finding_id, :attempt_no, :engine_used, :model, :built, :finding_cleared,
		  :new_findings, :outcome, :files_changed, :diff_excerpt, :duration_ms, :cost_usd,
		  :input_tokens, :output_tokens, :created_at)`, a)
	return err
}

func (s *SQLiteStore) ListFixAttempts(ctx context.Context, jobID string) ([]models.FixAttempt, error) {
	var attempts []models.FixAttempt
	err := s.db.SelectContext(ctx, &attempts,
		"SELECT * FROM fix_attempts WHERE job_id = ? ORDER BY created_at ASC", jobID)
	return attempts, err
}
