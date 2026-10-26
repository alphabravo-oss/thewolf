package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// --- Fix jobs (autonomous fix engine) ---
// encodeStrings/decodeStrings live in sqlite_fixjobs.go (same package).

func (s *PostgresStore) EnqueueFixJob(ctx context.Context, j *models.FixJob) error {
	return enqueueFixJob(ctx, s.db, j)
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

func (s *PostgresStore) ClaimNextFixJob(ctx context.Context, workerID string) (*models.FixJob, error) {
	return claimNextFixJob(ctx, s.db, workerID)
}

func (s *PostgresStore) UpdateFixJob(ctx context.Context, j *models.FixJob) error {
	return updateFixJob(ctx, s.db, j)
}

func (s *PostgresStore) HeartbeatFixJob(ctx context.Context, id, worker string, claimedAt time.Time) error {
	return heartbeatFixJob(ctx, s.db, id, worker, claimedAt)
}

func (s *PostgresStore) ReclaimStaleJobs(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE fix_jobs SET revision = revision + 1, status = $1, claimed_by = '', updated_at = $2
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
		  new_findings, outcome, files_changed, diff_excerpt, duration_ms, cost_usd,
		  input_tokens, output_tokens, created_at)
		 VALUES
		 (:id, :job_id, :finding_id, :attempt_no, :engine_used, :model, CAST(CAST(:built AS BOOLEAN) AS INTEGER), CAST(CAST(:finding_cleared AS BOOLEAN) AS INTEGER),
		  :new_findings, :outcome, :files_changed, :diff_excerpt, :duration_ms, :cost_usd,
		  :input_tokens, :output_tokens, :created_at)`, a)
	return err
}

func (s *PostgresStore) ListFixAttempts(ctx context.Context, jobID string) ([]models.FixAttempt, error) {
	var attempts []models.FixAttempt
	err := s.db.SelectContext(ctx, &attempts,
		"SELECT * FROM fix_attempts WHERE job_id = $1 ORDER BY created_at ASC", jobID)
	return attempts, err
}
