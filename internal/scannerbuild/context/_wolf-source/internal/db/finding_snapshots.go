package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/suppress"
	"github.com/jmoiron/sqlx"
)

var ErrStaleFindings = errors.New("scan or findings changed during evaluation; retry with a fresh snapshot")

// Finding persistence, triage, finalization, recovery and manual regrouping
// advance this counter in their own transactions. Progress/heartbeat writes
// never overwrite it. Orphan maintenance is a separate cleanup path.
func advanceFindingsRevision(ctx context.Context, tx *sqlx.Tx, scanID string) error {
	result, err := tx.ExecContext(ctx, tx.Rebind("UPDATE scans SET findings_revision = findings_revision + 1 WHERE id = ?"), scanID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func updateFindingWithRevision(ctx context.Context, database *sqlx.DB, f *models.Finding) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var scanID string
	if err := tx.GetContext(ctx, &scanID, tx.Rebind("SELECT scan_id FROM findings WHERE id = ?"), f.ID); err != nil {
		return err
	}
	if err := advanceFindingsRevision(ctx, tx, scanID); err != nil {
		return err
	}
	projected := []models.Finding{*f}
	if err := applyFindingSuppressionsTx(ctx, tx, projected); err != nil {
		return err
	}
	*f = projected[0]
	if err := updateFinding(ctx, tx, f); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetScanFindingSnapshot(ctx context.Context, scanID string) (*models.Scan, []models.Finding, error) {
	return getScanFindingSnapshot(ctx, s.readDatabase(), scanID)
}
func (s *PostgresStore) GetScanFindingSnapshot(ctx context.Context, scanID string) (*models.Scan, []models.Finding, error) {
	return getScanFindingSnapshot(ctx, s.db, scanID)
}
func getScanFindingSnapshot(ctx context.Context, database *sqlx.DB, scanID string) (*models.Scan, []models.Finding, error) {
	// PostgreSQL requires repeatable read for the two SELECTs. SQLite holds its
	// existing transaction snapshot; no application mutation occurs here.
	tx, err := database.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var scan models.Scan
	if err := tx.GetContext(ctx, &scan, tx.Rebind("SELECT * FROM scans WHERE id = ?"), scanID); err != nil {
		return nil, nil, err
	}
	var findings []models.Finding
	if err := tx.SelectContext(ctx, &findings, tx.Rebind("SELECT * FROM findings WHERE scan_id = ? AND (cluster_id = '' OR cluster_id = id) ORDER BY composite_score DESC, id"), scanID); err != nil {
		return nil, nil, err
	}
	hydrateFindingsAfterRead(findings)
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return &scan, findings, nil
}

func refreshScanFindingCount(ctx context.Context, tx *sqlx.Tx, scanID string) error {
	var scan models.Scan
	if err := tx.GetContext(ctx, &scan, tx.Rebind("SELECT * FROM scans WHERE id=?"), scanID); err != nil {
		return err
	}
	var path string
	if err := tx.GetContext(ctx, &path, tx.Rebind("SELECT r.source_path FROM repos r JOIN scans s ON s.repo_id = r.id WHERE s.id = ?"), scanID); err != nil {
		return err
	}
	var findings []models.Finding
	if err := tx.SelectContext(ctx, &findings, tx.Rebind("SELECT * FROM findings WHERE scan_id = ? AND (cluster_id = '' OR cluster_id = id)"), scanID); err != nil {
		return err
	}
	// Use the same captured policy as lists/gates; completed scans need no checkout.
	if _, _, err := suppress.ApplyScan(ctx, findings, &scan, path); err != nil {
		return err
	}
	count := 0
	for _, f := range findings {
		if !f.Suppressed {
			count++
		}
	}
	_, err := tx.ExecContext(ctx, tx.Rebind("UPDATE scans SET finding_count = ?, updated_at = ? WHERE id = ?"), count, time.Now().UTC(), scanID)
	return err
}

func upsertQualityGateResult(ctx context.Context, database *sqlx.DB, result *models.QualityGateResult) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize the revision check with finding writes. Checking and writing in
	// separate statements without this lock would let an old gate win a race.
	locked, err := tx.ExecContext(ctx, tx.Rebind("UPDATE scans SET updated_at = updated_at WHERE id = ? AND findings_revision = ? AND status = ?"), result.ScanID, result.FindingsRevision, result.ScanStatus)
	if err != nil {
		return err
	}
	n, err := locked.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleFindings
	}
	if err := upsertQualityGateResultTx(ctx, tx, result); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertQualityGateResultTx(ctx context.Context, tx *sqlx.Tx, result *models.QualityGateResult) error {
	now := time.Now().UTC()
	if result.CreatedAt.IsZero() {
		result.CreatedAt = now
	}
	if result.EvaluatedAt.IsZero() {
		result.EvaluatedAt = now
	}
	result.UpdatedAt = now
	_, err := tx.NamedExecContext(ctx, `INSERT INTO quality_gate_results (id,scan_id,policy_id,status,summary_json,matched_rules_json,evaluated_at,created_at,updated_at,findings_revision,scan_status)
 VALUES (:id,:scan_id,:policy_id,:status,:summary_json,:matched_rules_json,:evaluated_at,:created_at,:updated_at,:findings_revision,:scan_status)
 ON CONFLICT (scan_id,policy_id) DO UPDATE SET id=excluded.id,created_at=excluded.created_at,status=excluded.status,summary_json=excluded.summary_json,matched_rules_json=excluded.matched_rules_json,evaluated_at=excluded.evaluated_at,updated_at=excluded.updated_at,findings_revision=excluded.findings_revision,scan_status=excluded.scan_status`, result)
	if err != nil {
		return err
	}
	return nil
}

// A late publication failure must not rewrite a stale scan struct over manual
// counts or a concurrent cancellation. Preserve every unrelated scan field.
func (s *SQLiteStore) FailCompletedScan(ctx context.Context, scanID, code, message string) error {
	return failCompletedScan(ctx, s.db, scanID, code, message)
}
func (s *PostgresStore) FailCompletedScan(ctx context.Context, scanID, code, message string) error {
	return failCompletedScan(ctx, s.db, scanID, code, message)
}
func failCompletedScan(ctx context.Context, database sqlx.ExtContext, scanID, code, message string) error {
	_, err := database.ExecContext(ctx, database.Rebind("UPDATE scans SET status = ?, phase = 'failed', failure_code = ?, failure_message = ?, updated_at = ? WHERE id = ? AND status = ?"), models.ScanStatusFailed, code, message, time.Now().UTC(), scanID, models.ScanStatusCompleted)
	return err
}
