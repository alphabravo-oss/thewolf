package db

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

var ErrInvalidBaseline = errors.New("baseline requires a completed scan owned by the repository owner on the same branch")

func createScanBaseline(ctx context.Context, database *sqlx.DB, baseline *models.ScanBaseline) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := createScanBaselineTx(ctx, tx, baseline); err != nil {
		return err
	}
	return tx.Commit()
}

func createScanBaselineTx(ctx context.Context, tx *sqlx.Tx, baseline *models.ScanBaseline) error {
	if err := lockVulnerabilityRepo(ctx, tx, baseline.RepoID); err != nil {
		return err
	}
	scan, err := lockClusterScan(ctx, tx, baseline.RepoID, baseline.ScanID)
	if err != nil {
		return err
	}
	var owner string
	if err := tx.GetContext(ctx, &owner, tx.Rebind("SELECT user_id FROM repos WHERE id = ?"), baseline.RepoID); err != nil {
		return err
	}
	if !validBaselineScan(*scan) || scan.Branch != baseline.Branch || scan.UserID != owner || (baseline.CreatedBy != "" && baseline.CreatedBy != owner) {
		return ErrInvalidBaseline
	}
	now := time.Now().UTC()
	baseline.CreatedAt, baseline.UpdatedAt = now, now
	if baseline.Strategy == "" {
		baseline.Strategy = "named"
	}
	_, err = tx.NamedExecContext(ctx, `INSERT INTO scan_baselines (id,repo_id,branch,name,scan_id,strategy,created_by,created_at,updated_at)
 VALUES (:id,:repo_id,:branch,:name,:scan_id,:strategy,:created_by,:created_at,:updated_at)`, baseline)
	if err != nil {
		return err
	}
	return nil
}

// Completed is insufficient when requested scanners failed or never finished.
func validBaselineScan(scan models.Scan) bool {
	if scan.Status != models.ScanStatusCompleted || scan.CompletedAt == nil || scan.CancelRequestedAt != nil || scan.FailureCode != "" {
		return false
	}
	var selected, completed, failed []string
	for _, field := range []struct {
		raw string
		out *[]string
	}{{scan.ToolsSelected, &selected}, {scan.ToolsCompleted, &completed}, {scan.ToolsFailed, &failed}} {
		if field.raw != "" {
			if err := json.Unmarshal([]byte(field.raw), field.out); err != nil {
				return false
			}
		}
	}
	if len(completed) == 0 || len(failed) > 0 {
		return false
	}
	for _, tool := range selected {
		if !slices.Contains(completed, tool) {
			return false
		}
	}
	return true
}
