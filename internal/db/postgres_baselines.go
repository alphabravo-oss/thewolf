package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *PostgresStore) CreateScanBaseline(ctx context.Context, baseline *models.ScanBaseline) error {
	now := time.Now().UTC()
	baseline.CreatedAt = now
	baseline.UpdatedAt = now
	if baseline.Strategy == "" {
		baseline.Strategy = "named"
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO scan_baselines (id, repo_id, branch, name, scan_id, strategy, created_by, created_at, updated_at)
		 VALUES (:id, :repo_id, :branch, :name, :scan_id, :strategy, :created_by, :created_at, :updated_at)`,
		baseline)
	return err
}

func (s *PostgresStore) ListScanBaselines(ctx context.Context, repoID, branch string) ([]models.ScanBaseline, error) {
	var baselines []models.ScanBaseline
	var err error
	if branch == "" {
		err = s.db.SelectContext(ctx, &baselines,
			"SELECT * FROM scan_baselines WHERE repo_id = $1 ORDER BY branch, name", repoID)
	} else {
		err = s.db.SelectContext(ctx, &baselines,
			"SELECT * FROM scan_baselines WHERE repo_id = $1 AND branch = $2 ORDER BY name", repoID, branch)
	}
	return baselines, err
}

func (s *PostgresStore) GetScanBaselineByName(ctx context.Context, repoID, branch, name string) (*models.ScanBaseline, error) {
	var baseline models.ScanBaseline
	err := s.db.GetContext(ctx, &baseline,
		"SELECT * FROM scan_baselines WHERE repo_id = $1 AND branch = $2 AND name = $3", repoID, branch, name)
	if err != nil {
		return nil, err
	}
	return &baseline, nil
}

func (s *PostgresStore) UpsertScanComparison(ctx context.Context, comparison *models.ScanComparison) error {
	now := time.Now().UTC()
	if comparison.CreatedAt.IsZero() {
		comparison.CreatedAt = now
	}
	comparison.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO scan_comparisons (id, repo_id, baseline_scan_id, current_scan_id, summary_json, created_at, updated_at)
		 VALUES (:id, :repo_id, :baseline_scan_id, :current_scan_id, :summary_json, :created_at, :updated_at)
		 ON CONFLICT (baseline_scan_id, current_scan_id) DO UPDATE SET
		   summary_json = excluded.summary_json,
		   updated_at = excluded.updated_at`,
		comparison)
	return err
}

func (s *PostgresStore) GetScanComparison(ctx context.Context, baselineScanID, currentScanID string) (*models.ScanComparison, error) {
	var comparison models.ScanComparison
	err := s.db.GetContext(ctx, &comparison,
		"SELECT * FROM scan_comparisons WHERE baseline_scan_id = $1 AND current_scan_id = $2", baselineScanID, currentScanID)
	if err != nil {
		return nil, err
	}
	return &comparison, nil
}
