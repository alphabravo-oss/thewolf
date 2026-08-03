package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *PostgresStore) FindScanByIdempotencyKey(ctx context.Context, userID, key string) (*models.Scan, error) {
	var scan models.Scan
	if err := s.db.GetContext(ctx, &scan,
		"SELECT * FROM scans WHERE user_id = $1 AND idempotency_key = $2 LIMIT 1", userID, key); err != nil {
		return nil, err
	}
	return &scan, nil
}

func (s *PostgresStore) ClaimNextScan(ctx context.Context, workerID, backend string, leaseUntil time.Time) (*models.Scan, error) {
	now := time.Now().UTC()
	var scan models.Scan
	err := s.db.GetContext(ctx, &scan,
		`UPDATE scans
		 SET status = $1, phase = 'preparing', claimed_by = $2, lease_token = $3,
		     lease_expires_at = $4, heartbeat_at = $5, attempt = attempt + 1,
		     execution_backend = $6, failure_code = '', failure_message = '',
		     completed_at = NULL, started_at = COALESCE(started_at, $5), updated_at = $5
		 WHERE id = (
		   SELECT id FROM scans
		   WHERE status = $7 AND cancel_requested_at IS NULL
		   ORDER BY created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED
		 )
		 RETURNING *`,
		models.ScanStatusRunning, workerID, uuid.NewString(), leaseUntil, now, backend,
		models.ScanStatusPending)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scan, nil
}

func (s *PostgresStore) StartScanExecution(ctx context.Context, scanID, leaseToken string, startedAt time.Time) (bool, error) {
	var (
		result sql.Result
		err    error
	)
	if leaseToken == "" {
		result, err = s.db.ExecContext(ctx,
			`UPDATE scans SET status = $1, phase = 'scanning',
			     started_at = COALESCE(started_at, $2), updated_at = $2
			 WHERE id = $3 AND status = $4 AND cancel_requested_at IS NULL`,
			models.ScanStatusRunning, startedAt, scanID, models.ScanStatusPending)
	} else {
		result, err = s.db.ExecContext(ctx,
			`UPDATE scans SET phase = 'scanning',
			     started_at = COALESCE(started_at, $1), updated_at = $1
			 WHERE id = $2 AND status = $3 AND lease_token = $4 AND cancel_requested_at IS NULL`,
			startedAt, scanID, models.ScanStatusRunning, leaseToken)
	}
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}

func (s *PostgresStore) HeartbeatScanLease(ctx context.Context, scanID, leaseToken string, leaseUntil time.Time) (bool, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE scans SET heartbeat_at = $1, lease_expires_at = $2, updated_at = $1
		 WHERE id = $3 AND lease_token = $4 AND status = $5`,
		now, leaseUntil, scanID, leaseToken, models.ScanStatusRunning)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}

func (s *PostgresStore) FinalizeScan(ctx context.Context, scan *models.Scan, leaseToken string) (bool, error) {
	if leaseToken == "" {
		return false, nil
	}
	scan.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = $1, phase = $2, tools_selected = $3, tools_completed = $4,
		     tools_failed = $5, tools_errors = $6, finding_count = $7, coverage_summary = $8,
		     source_type = $9, remote_node_id = $10, source_path = $11, commit_sha = $12,
		     tree_digest = $13, dirty_state = $14, prepared_workspace = $15, failure_code = $16,
		     failure_message = $17, completed_at = $18, claimed_by = '', lease_token = '',
		     lease_expires_at = NULL, heartbeat_at = NULL, updated_at = $19
		 WHERE id = $20 AND lease_token = $21 AND status = $22 AND cancel_requested_at IS NULL`,
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

func (s *PostgresStore) ReclaimStaleScans(ctx context.Context, now time.Time) (int, error) {
	requeued, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = $1, phase = 'queued', claimed_by = '', lease_token = '',
		     lease_expires_at = NULL, heartbeat_at = NULL, failure_code = 'worker_lost',
		     failure_message = 'scan worker lease expired; retrying', updated_at = $2
		 WHERE status = $3 AND lease_expires_at IS NOT NULL AND lease_expires_at < $2
		   AND attempt < max_attempts`,
		models.ScanStatusPending, now, models.ScanStatusRunning)
	if err != nil {
		return 0, err
	}
	n1, _ := requeued.RowsAffected()
	failed, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = $1, phase = 'failed', claimed_by = '', lease_token = '',
		     lease_expires_at = NULL, failure_code = 'worker_lost',
		     failure_message = 'scan worker lease expired and retry budget was exhausted',
		     completed_at = $2, updated_at = $2
		 WHERE status = $3 AND lease_expires_at IS NOT NULL AND lease_expires_at < $2
		   AND attempt >= max_attempts`,
		models.ScanStatusFailed, now, models.ScanStatusRunning)
	if err != nil {
		return int(n1), err
	}
	n2, _ := failed.RowsAffected()
	return int(n1 + n2), nil
}

func (s *PostgresStore) RequestScanCancellation(ctx context.Context, scanID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scans SET status = $1, phase = 'cancelled', cancel_requested_at = $2,
		     completed_at = COALESCE(completed_at, $2), updated_at = $2
		 WHERE id = $3 AND status IN ($4, $5)`,
		models.ScanStatusCancelled, at, scanID, models.ScanStatusPending, models.ScanStatusRunning)
	return err
}

func (s *PostgresStore) RequestScannerRunCancellation(ctx context.Context, scanID, toolName string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scanner_run_records SET cancel_requested_at = $1, status = 'cancelled',
		     error_message = 'cancelled by user', finished_at = COALESCE(finished_at, $1), updated_at = $1
		 WHERE scan_id = $2 AND tool_name = $3 AND status IN ('pending','queued','running')`,
		at, scanID, toolName)
	return err
}

func (s *PostgresStore) DeleteFindingsByScanTool(ctx context.Context, scanID, toolName string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM findings WHERE scan_id = $1 AND tool_name = $2", scanID, toolName)
	return err
}

func (s *PostgresStore) CreateFindingsForScanLease(ctx context.Context, findings []models.Finding, scanID, leaseToken string) (bool, error) {
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
		 WHERE id = $1 AND status = $2 AND lease_token = $3 AND cancel_requested_at IS NULL`,
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

func (s *PostgresStore) AppendScanEvent(ctx context.Context, event *models.ScanEvent) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize sequence allocation per scan without a separate sequence table.
	if event.LeaseToken == "" {
		if _, err := tx.ExecContext(ctx, "SELECT id FROM scans WHERE id = $1 FOR UPDATE", event.ScanID); err != nil {
			return err
		}
	} else {
		result, err := tx.ExecContext(ctx,
			`UPDATE scans SET updated_at = updated_at
			 WHERE id = $1 AND status = $2 AND lease_token = $3 AND cancel_requested_at IS NULL`,
			event.ScanID, models.ScanStatusRunning, event.LeaseToken)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return ErrStaleScanLease
		}
	}
	if err := enforceDurableScanEventPolicy(ctx, tx, event); err != nil {
		return err
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if err := tx.GetContext(ctx, &event.Sequence,
		"SELECT COALESCE(MAX(sequence), 0) + 1 FROM scan_events WHERE scan_id = $1", event.ScanID); err != nil {
		return err
	}
	if _, err := tx.NamedExecContext(ctx,
		`INSERT INTO scan_events (id, scan_id, sequence, event_type, data_json, created_at)
		 VALUES (:id, :scan_id, :sequence, :event_type, :data_json, :created_at)`, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ListScanEvents(ctx context.Context, scanID string, afterSequence int64, limit int) ([]models.ScanEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var events []models.ScanEvent
	err := s.db.SelectContext(ctx, &events,
		`SELECT * FROM scan_events WHERE scan_id = $1 AND sequence > $2
		 ORDER BY sequence ASC LIMIT $3`, scanID, afterSequence, limit)
	return events, err
}

func (s *PostgresStore) UpsertScanWorker(ctx context.Context, worker *models.ScanWorker) error {
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

func (s *PostgresStore) ListScanWorkers(ctx context.Context, activeAfter time.Time) ([]models.ScanWorker, error) {
	var workers []models.ScanWorker
	err := s.db.SelectContext(ctx, &workers,
		`SELECT * FROM scan_workers
		 WHERE heartbeat_at >= $1 AND status IN ('ready', 'busy')
		 ORDER BY id`, activeAfter)
	return workers, err
}
