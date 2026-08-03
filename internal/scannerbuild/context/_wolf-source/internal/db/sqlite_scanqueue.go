package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) FindScanByIdempotencyKey(ctx context.Context, userID, key string) (*models.Scan, error) {
	var scan models.Scan
	if err := s.db.GetContext(ctx, &scan,
		"SELECT * FROM scans WHERE user_id = ? AND idempotency_key = ? LIMIT 1", userID, key); err != nil {
		return nil, err
	}
	return &scan, nil
}

func (s *SQLiteStore) ClaimNextScan(ctx context.Context, workerID, backend string, leaseUntil time.Time) (*models.Scan, error) {
	now := time.Now().UTC()
	leaseToken := uuid.NewString()
	var scan models.Scan
	err := s.db.GetContext(ctx, &scan,
		`UPDATE scans
		 SET status = ?, phase = 'preparing', claimed_by = ?, lease_token = ?,
		     lease_expires_at = ?, heartbeat_at = ?, attempt = attempt + 1,
		     execution_backend = ?, failure_code = '', failure_message = '',
		     completed_at = NULL, started_at = COALESCE(started_at, ?), updated_at = ?
		 WHERE id = (
		   SELECT id FROM scans
		   WHERE status = ? AND cancel_requested_at IS NULL
		   ORDER BY created_at ASC LIMIT 1
		 )
		 RETURNING *`,
		models.ScanStatusRunning, workerID, leaseToken, leaseUntil, now, backend, now, now,
		models.ScanStatusPending)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scan, nil
}

func (s *SQLiteStore) StartScanExecution(ctx context.Context, scanID, leaseToken string, startedAt time.Time) (bool, error) {
	var (
		result sql.Result
		err    error
	)
	if leaseToken == "" {
		result, err = s.db.ExecContext(ctx,
			`UPDATE scans SET status = ?, phase = 'scanning',
			     started_at = COALESCE(started_at, ?), updated_at = ?
			 WHERE id = ? AND status = ? AND cancel_requested_at IS NULL`,
			models.ScanStatusRunning, startedAt, startedAt, scanID, models.ScanStatusPending)
	} else {
		result, err = s.db.ExecContext(ctx,
			`UPDATE scans SET phase = 'scanning',
			     started_at = COALESCE(started_at, ?), updated_at = ?
			 WHERE id = ? AND status = ? AND lease_token = ? AND cancel_requested_at IS NULL`,
			startedAt, startedAt, scanID, models.ScanStatusRunning, leaseToken)
	}
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}

func (s *SQLiteStore) HeartbeatScanLease(ctx context.Context, scanID, leaseToken string, leaseUntil time.Time) (bool, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE scans SET heartbeat_at = ?, lease_expires_at = ?, updated_at = ?
		 WHERE id = ? AND lease_token = ? AND status = ?`,
		now, leaseUntil, now, scanID, leaseToken, models.ScanStatusRunning)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}

func (s *SQLiteStore) FinalizeScan(ctx context.Context, scan *models.Scan, leaseToken string) (bool, error) {
	if leaseToken == "" {
		return false, nil
	}
	scan.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = ?, phase = ?, tools_selected = ?, tools_completed = ?,
		     tools_failed = ?, tools_errors = ?, finding_count = ?, coverage_summary = ?,
		     source_type = ?, remote_node_id = ?, source_path = ?, commit_sha = ?,
		     tree_digest = ?, dirty_state = ?, prepared_workspace = ?, failure_code = ?,
		     failure_message = ?, completed_at = ?, claimed_by = '', lease_token = '',
		     lease_expires_at = NULL, heartbeat_at = NULL, updated_at = ?
		 WHERE id = ? AND lease_token = ? AND status = ? AND cancel_requested_at IS NULL`,
		scan.Status, scan.Phase, scan.ToolsSelected, scan.ToolsCompleted,
		scan.ToolsFailed, scan.ToolsErrors, scan.FindingCount, scan.CoverageSummary,
		scan.SourceType, scan.RemoteNodeID, scan.SourcePath, scan.CommitSHA,
		scan.TreeDigest, scan.DirtyState, scan.PreparedWorkspace, scan.FailureCode, scan.FailureMessage,
		scan.CompletedAt, scan.UpdatedAt,
		scan.ID, leaseToken, models.ScanStatusRunning)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}

func (s *SQLiteStore) ReclaimStaleScans(ctx context.Context, now time.Time) (int, error) {
	requeued, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = ?, phase = 'queued', claimed_by = '', lease_token = '',
		     lease_expires_at = NULL, heartbeat_at = NULL, failure_code = 'worker_lost',
		     failure_message = 'scan worker lease expired; retrying', updated_at = ?
		 WHERE status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < ?
		   AND attempt < max_attempts`,
		models.ScanStatusPending, now, models.ScanStatusRunning, now)
	if err != nil {
		return 0, err
	}
	n1, _ := requeued.RowsAffected()
	failed, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = ?, phase = 'failed', claimed_by = '', lease_token = '',
		     lease_expires_at = NULL, failure_code = 'worker_lost',
		     failure_message = 'scan worker lease expired and retry budget was exhausted',
		     completed_at = ?, updated_at = ?
		 WHERE status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < ?
		   AND attempt >= max_attempts`,
		models.ScanStatusFailed, now, now, models.ScanStatusRunning, now)
	if err != nil {
		return int(n1), err
	}
	n2, _ := failed.RowsAffected()
	return int(n1 + n2), nil
}

func (s *SQLiteStore) RequestScanCancellation(ctx context.Context, scanID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = ?, phase = 'cancelled', cancel_requested_at = ?,
		     completed_at = COALESCE(completed_at, ?), updated_at = ?
		 WHERE id = ? AND status IN (?, ?)`,
		models.ScanStatusCancelled, at, at, at, scanID,
		models.ScanStatusPending, models.ScanStatusRunning)
	return err
}

func (s *SQLiteStore) RequestScannerRunCancellation(ctx context.Context, scanID, toolName string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scanner_run_records SET cancel_requested_at = ?, status = 'cancelled',
		     error_message = 'cancelled by user', finished_at = COALESCE(finished_at, ?), updated_at = ?
		 WHERE scan_id = ? AND tool_name = ? AND status IN ('pending','queued','running')`,
		at, at, at, scanID, toolName)
	return err
}

func (s *SQLiteStore) DeleteFindingsByScanTool(ctx context.Context, scanID, toolName string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM findings WHERE scan_id = ? AND tool_name = ?", scanID, toolName)
	return err
}

func (s *SQLiteStore) CreateFindingsForScanLease(ctx context.Context, findings []models.Finding, scanID, leaseToken string) (bool, error) {
	if leaseToken == "" {
		return false, nil
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx,
		`UPDATE scans SET updated_at = updated_at
		 WHERE id = ? AND status = ? AND lease_token = ? AND cancel_requested_at IS NULL`,
		scanID, models.ScanStatusRunning, leaseToken)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return false, nil
	}
	if err := insertFindingsTx(ctx, tx, findings); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) AppendScanEvent(ctx context.Context, event *models.ScanEvent) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	// Acquire SQLite's single-writer lock before allocating MAX+1 so an API
	// cancellation event cannot race a worker progress event to the same
	// sequence number.
	query := "UPDATE scans SET updated_at = updated_at WHERE id = ?"
	args := []interface{}{event.ScanID}
	if event.LeaseToken != "" {
		query += " AND status = ? AND lease_token = ? AND cancel_requested_at IS NULL"
		args = append(args, models.ScanStatusRunning, event.LeaseToken)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		if event.LeaseToken != "" {
			return ErrStaleScanLease
		}
		return sql.ErrNoRows
	}
	if err := enforceDurableScanEventPolicy(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.GetContext(ctx, &event.Sequence,
		"SELECT COALESCE(MAX(sequence), 0) + 1 FROM scan_events WHERE scan_id = ?", event.ScanID); err != nil {
		return err
	}
	if _, err := tx.NamedExecContext(ctx,
		`INSERT INTO scan_events (id, scan_id, sequence, event_type, data_json, created_at)
		 VALUES (:id, :scan_id, :sequence, :event_type, :data_json, :created_at)`, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListScanEvents(ctx context.Context, scanID string, afterSequence int64, limit int) ([]models.ScanEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var events []models.ScanEvent
	err := s.db.SelectContext(ctx, &events,
		`SELECT * FROM scan_events WHERE scan_id = ? AND sequence > ?
		 ORDER BY sequence ASC LIMIT ?`, scanID, afterSequence, limit)
	return events, err
}

func (s *SQLiteStore) UpsertScanWorker(ctx context.Context, worker *models.ScanWorker) error {
	now := time.Now().UTC()
	if worker.StartedAt.IsZero() {
		worker.StartedAt = now
	}
	worker.HeartbeatAt = now
	worker.UpdatedAt = now
	if worker.Status == "" {
		worker.Status = "ready"
	}
	if worker.Capacity <= 0 {
		worker.Capacity = 1
	}
	if worker.CapabilitiesJSON == "" {
		worker.CapabilitiesJSON = "{}"
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO scan_workers
		 (id, backend, status, capacity, active_scans, version, capabilities_json,
		  heartbeat_at, started_at, updated_at)
		 VALUES (:id, :backend, :status, :capacity, :active_scans, :version, :capabilities_json,
		  :heartbeat_at, :started_at, :updated_at)
		 ON CONFLICT(id) DO UPDATE SET backend=excluded.backend, status=excluded.status,
		  capacity=excluded.capacity, active_scans=excluded.active_scans, version=excluded.version,
		  capabilities_json=excluded.capabilities_json, heartbeat_at=excluded.heartbeat_at,
		  updated_at=excluded.updated_at`, worker)
	return err
}

func (s *SQLiteStore) ListScanWorkers(ctx context.Context, activeAfter time.Time) ([]models.ScanWorker, error) {
	var workers []models.ScanWorker
	err := s.db.SelectContext(ctx, &workers,
		`SELECT * FROM scan_workers
		 WHERE heartbeat_at >= ? AND status IN ('ready', 'busy')
		 ORDER BY id`, activeAfter)
	return workers, err
}
