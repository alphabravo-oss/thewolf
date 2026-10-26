package db

import (
	"context"
	"encoding/json"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/suppress"
	"github.com/jmoiron/sqlx"
)

func (s *SQLiteStore) CaptureScanIgnorePolicy(ctx context.Context, scanID, leaseToken, repoPath string) error {
	return captureScanIgnorePolicy(ctx, s.db, scanID, leaseToken, repoPath)
}
func (s *PostgresStore) CaptureScanIgnorePolicy(ctx context.Context, scanID, leaseToken, repoPath string) error {
	return captureScanIgnorePolicy(ctx, s.db, scanID, leaseToken, repoPath)
}

// Capture once after tool persistence, before final grouping/baseline/reporting.
// The scan lock makes the observed paths, policy and revision one snapshot; a
// lost lease or failed read/write cannot publish a partial policy.
func captureScanIgnorePolicy(ctx context.Context, database *sqlx.DB, scanID, leaseToken, repoPath string) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE scans SET updated_at=updated_at
 WHERE id=? AND status=? AND lease_token=? AND cancel_requested_at IS NULL`), scanID, models.ScanStatusRunning, leaseToken)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleScanLease
	}
	var scan models.Scan
	if err := tx.GetContext(ctx, &scan, tx.Rebind("SELECT * FROM scans WHERE id=?"), scanID); err != nil {
		return err
	}
	if scan.IgnorePolicyJSON != "" {
		if _, _, err := suppress.ApplyScan(ctx, nil, &scan, ""); err != nil {
			return err
		}
		return tx.Commit()
	}
	var findings []models.Finding
	if err := tx.SelectContext(ctx, &findings, tx.Rebind("SELECT * FROM findings WHERE scan_id=? ORDER BY id"), scanID); err != nil {
		return err
	}
	snapshot, err := suppress.Capture(ctx, findings, repoPath)
	if err != nil {
		return err
	}
	snapshot.SourceKey = suppress.ScanSourceKey(&scan)
	if _, _, err := snapshot.Apply(findings); err != nil {
		return err
	}
	for _, f := range findings {
		if _, err := tx.ExecContext(ctx, tx.Rebind("UPDATE findings SET suppressed=?, suppressed_reason=? WHERE id=?"), f.Suppressed, f.SuppressedReason, f.ID); err != nil {
			return err
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, tx.Rebind("UPDATE scans SET ignore_policy_json=?, findings_revision=findings_revision+1 WHERE id=?"), string(data), scanID); err != nil {
		return err
	}
	return tx.Commit()
}
