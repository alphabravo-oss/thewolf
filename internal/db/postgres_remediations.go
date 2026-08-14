package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *PostgresStore) CreateRemediation(ctx context.Context, rem *models.Remediation) error {
	now := time.Now().UTC()
	if rem.CreatedAt.IsZero() {
		rem.CreatedAt = now
	}
	rem.UpdatedAt = now
	if rem.State == "" {
		rem.State = models.RemediationOpen
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO remediations
		 (id, user_id, repo_id, origin_scan_id, branch, workspace_path, state,
		  published_sha, published_at, created_at, updated_at)
		 VALUES
		 (:id, :user_id, :repo_id, :origin_scan_id, :branch, :workspace_path, :state,
		  :published_sha, :published_at, :created_at, :updated_at)`, rem)
	return err
}

func (s *PostgresStore) GetRemediationByID(ctx context.Context, id string) (*models.Remediation, error) {
	var rem models.Remediation
	if err := s.db.GetContext(ctx, &rem, "SELECT * FROM remediations WHERE id = $1", id); err != nil {
		return nil, err
	}
	return &rem, nil
}

func (s *PostgresStore) GetOpenRemediationByOrigin(ctx context.Context, originScanID string) (*models.Remediation, error) {
	var rem models.Remediation
	err := s.db.GetContext(ctx, &rem,
		"SELECT * FROM remediations WHERE origin_scan_id = $1 AND state = $2 ORDER BY created_at DESC LIMIT 1",
		originScanID, models.RemediationOpen)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rem, nil
}

func (s *PostgresStore) GetLatestRemediationByOrigin(ctx context.Context, originScanID string) (*models.Remediation, error) {
	var rem models.Remediation
	err := s.db.GetContext(ctx, &rem,
		"SELECT * FROM remediations WHERE origin_scan_id = $1 ORDER BY created_at DESC LIMIT 1",
		originScanID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rem, nil
}

func (s *PostgresStore) UpdateRemediation(ctx context.Context, rem *models.Remediation) error {
	rem.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE remediations SET
		   repo_id=:repo_id, branch=:branch, workspace_path=:workspace_path, state=:state,
		   published_sha=:published_sha, published_at=:published_at, updated_at=:updated_at
		 WHERE id=:id`, rem)
	return err
}

func (s *PostgresStore) ListScansByOrigin(ctx context.Context, originScanID string) ([]models.Scan, error) {
	var scans []models.Scan
	err := s.db.SelectContext(ctx, &scans,
		`SELECT * FROM scans WHERE origin_scan_id = $1 OR id = $2 ORDER BY created_at ASC`,
		originScanID, originScanID)
	return scans, err
}

func (s *PostgresStore) ListFixJobsByRemediation(ctx context.Context, remediationID string) ([]models.FixJob, error) {
	var jobs []models.FixJob
	err := s.db.SelectContext(ctx, &jobs,
		"SELECT * FROM fix_jobs WHERE remediation_id = $1 ORDER BY created_at ASC", remediationID)
	for i := range jobs {
		jobs[i].FindingIDList = decodeStrings(jobs[i].FindingIDs)
	}
	return jobs, err
}
