package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
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

func enqueueFixJob(ctx context.Context, executor sqlx.ExtContext, j *models.FixJob) error {
	now := time.Now().UTC()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	j.Revision = 0
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
	_, err := sqlx.NamedExecContext(ctx, executor,
		`INSERT INTO fix_jobs
		 (id, user_id, idempotency_key, request_digest, type, repo_id, scan_id, finding_ids, target_branch, engine, mode,
		  severity_floor, max_attempts, max_loops, current_loop, human_in_the_loop,
		  workspace_path, base_branch, pushed, push_sha, pause_reason, resume_action,
		  model, effort, variant, remediation_id, planned_runs, run_index,
		  status, claimed_by, result_branch, diff_artifact_id,
		  summary, error, claimed_at, started_at, finished_at, heartbeat_at, created_at, updated_at)
		 VALUES
		 (:id, :user_id, :idempotency_key, :request_digest, :type, :repo_id, :scan_id, :finding_ids, :target_branch, :engine, :mode,
		  :severity_floor, :max_attempts, :max_loops, :current_loop, CAST(CAST(:human_in_the_loop AS BOOLEAN) AS INTEGER),
		  :workspace_path, :base_branch, CAST(CAST(:pushed AS BOOLEAN) AS INTEGER), :push_sha, :pause_reason, :resume_action,
		  :model, :effort, :variant, :remediation_id, :planned_runs, :run_index,
		  :status, :claimed_by, :result_branch, :diff_artifact_id,
		  :summary, :error, :claimed_at, :started_at, :finished_at, :heartbeat_at, :created_at, :updated_at)`, j)
	return err
}

func claimNextFixJob(ctx context.Context, database *sqlx.DB, workerID string) (*models.FixJob, error) {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// ponytail: serialize only the short claim transaction, never engine work.
	// Per-resource locks can replace this if claim throughput becomes material.
	if database.DriverName() == "postgres" {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('wolf.fix-job-claim', 0))"); err != nil {
			return nil, err
		}
	}
	var id string
	row := tx.QueryRowxContext(ctx,
		`SELECT q.id FROM fix_jobs q WHERE q.status='queued' AND NOT EXISTS (
		 SELECT 1 FROM fix_jobs active WHERE (active.status IN ('claimed','running') OR (active.status='cancelled' AND active.pause_reason='cancellation_cleanup')) AND (
		  (q.workspace_path<>'' AND active.workspace_path=q.workspace_path)
		  OR (q.remediation_id<>'' AND active.remediation_id=q.remediation_id)
		  OR (active.repo_id=q.repo_id AND
		   COALESCE(NULLIF(active.result_branch,''),NULLIF(active.target_branch,''),'wolf-fix/'||COALESCE(NULLIF(active.scan_id,''),active.id)) =
		   COALESCE(NULLIF(q.result_branch,''),NULLIF(q.target_branch,''),'wolf-fix/'||COALESCE(NULLIF(q.scan_id,''),q.id)))))
		 AND NOT EXISTS (SELECT 1 FROM scans s WHERE s.status IN ('pending','running') AND s.repo_id=q.repo_id
		 AND ((q.remediation_id<>'' AND s.remediation_id=q.remediation_id) OR (q.workspace_path<>'' AND s.prepared_workspace=q.workspace_path)))
		 ORDER BY q.created_at,q.id LIMIT 1`)
	if err := row.Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx,
		tx.Rebind(`UPDATE fix_jobs SET revision = revision + 1, status = ?, claimed_by = ?, claimed_at = ?, heartbeat_at = ?, updated_at = ?
		 WHERE id = ? AND status = ?`),
		models.FixJobClaimed, workerID, now, now, now, id, models.FixJobQueued)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, nil
	} // cancelled after selection
	var job models.FixJob
	if err := tx.GetContext(ctx, &job, tx.Rebind("SELECT * FROM fix_jobs WHERE id=?"), id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	job.FindingIDList = decodeStrings(job.FindingIDs)
	return &job, nil
}

// ErrFixJobConflict means a newer transition or claim owns this job. Callers must
// not reload and retry a stale worker's result against the newer revision.
var ErrFixJobConflict = errors.New("fix job changed; reload its current state")

func updateFixJob(ctx context.Context, database *sqlx.DB, j *models.FixJob) error {
	var executor sqlx.ExtContext = database
	var tx *sqlx.Tx
	if j.Status == models.FixJobQueued || j.Status == models.FixJobCancelled {
		var err error
		tx, err = database.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		executor = tx
		// Serialize resume against CreateFixJob's repository lock. Otherwise a
		// creation could inspect a paused job before a concurrent resume queues it.
		suffix := ""
		if database.DriverName() == "postgres" {
			suffix = " FOR UPDATE"
		}
		var repo string
		if err := tx.GetContext(ctx, &repo, tx.Rebind("SELECT id FROM repos WHERE id=?"+suffix), j.RepoID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if j.Status == models.FixJobQueued {
			if err := checkFixWorkspaceScans(ctx, tx, j.RepoID, j.RemediationID, j.WorkspacePath); err != nil {
				return err
			}
			branch := j.ResultBranch
			if branch == "" {
				branch = j.TargetBranch
			}
			if branch == "" {
				origin := j.ScanID
				if origin == "" {
					origin = j.ID
				}
				branch = "wolf-fix/" + origin
			}
			var busy int
			if err := tx.GetContext(ctx, &busy, tx.Rebind(`SELECT COUNT(*) FROM fix_jobs
			WHERE repo_id=? AND id<>? AND (status IN ('queued','claimed','running') OR (status='cancelled' AND pause_reason='cancellation_cleanup'))
			AND ((?<>'' AND remediation_id=?) OR
			COALESCE(NULLIF(result_branch,''),NULLIF(target_branch,''),'wolf-fix/'||COALESCE(NULLIF(scan_id,''),id))=?
			OR (?<>'' AND workspace_path=?))`), j.RepoID, j.ID, j.RemediationID, j.RemediationID,
				branch, j.WorkspacePath, j.WorkspacePath); err != nil {
				return err
			}
			if busy > 0 {
				return ErrFixBusy
			}
		}
	}
	j.UpdatedAt = time.Now().UTC()
	j.FindingIDs = encodeStrings(j.FindingIDList)
	result, err := sqlx.NamedExecContext(ctx, executor,
		`UPDATE fix_jobs SET revision=revision+1,
		   type=:type, repo_id=:repo_id, scan_id=:scan_id, finding_ids=:finding_ids,
		   target_branch=:target_branch, engine=:engine, mode=:mode, severity_floor=:severity_floor,
		   max_attempts=:max_attempts, max_loops=:max_loops, current_loop=:current_loop,
		   human_in_the_loop=CAST(CAST(:human_in_the_loop AS BOOLEAN) AS INTEGER), workspace_path=:workspace_path,
		   base_branch=:base_branch, pushed=CAST(CAST(:pushed AS BOOLEAN) AS INTEGER), push_sha=:push_sha,
		   pause_reason=:pause_reason, resume_action=:resume_action,
		   model=:model, effort=:effort, variant=:variant, remediation_id=:remediation_id,
		   planned_runs=:planned_runs, run_index=:run_index,
		   status=:status, claimed_by=:claimed_by, result_branch=:result_branch,
		   diff_artifact_id=:diff_artifact_id, summary=:summary, error=:error,
		   claimed_at=:claimed_at, started_at=:started_at, finished_at=:finished_at,
		   updated_at=:updated_at
		 WHERE id=:id AND revision=:revision
		 AND (status NOT IN ('cancelled','superseded','succeeded','failed') OR status=:status)`, j)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrFixJobConflict
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	j.Revision++
	return nil
}

func heartbeatFixJob(ctx context.Context, database *sqlx.DB, id, worker string, claimedAt time.Time) error {
	result, err := database.ExecContext(ctx, database.Rebind(`UPDATE fix_jobs SET heartbeat_at=?
		WHERE id=? AND claimed_by=? AND claimed_at=? AND status IN ('claimed','running')`),
		time.Now().UTC(), id, worker, claimedAt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrFixJobConflict
	}
	return nil
}
