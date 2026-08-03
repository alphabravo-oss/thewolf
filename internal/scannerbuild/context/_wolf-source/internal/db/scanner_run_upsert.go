package db

import "errors"

// ErrStaleScanLease indicates that a worker-owned mutation arrived after its
// scan lease was cancelled, expired, or transferred to another worker.
var ErrStaleScanLease = errors.New("scan lease is no longer current")

// ErrScanEventDropped indicates that the durable per-scan tool-output budget
// has already been exhausted. The first overflow is represented by a durable
// tool_output_dropped marker; later output is intentionally discarded.
var ErrScanEventDropped = errors.New("durable scan event output limit reached")

const scannerRunRecordUpsertQuery = `INSERT INTO scanner_run_records (
 id, scan_id, tool_name, status, category, image, image_digest, scanner_release_id,
 release_manifest_digest, version, command_json,
 exit_code, duration_ms, finding_count, error_message, parser_status, parser_message,
 runtime_backend, runtime_ref, attempt, cancel_requested_at, requested_scope,
 effective_scope, scope_message,
 started_at, finished_at, created_at, updated_at)
 VALUES (
 :id, :scan_id, :tool_name, :status, :category, :image, :image_digest, :scanner_release_id,
 :release_manifest_digest, :version, :command_json,
 :exit_code, :duration_ms, :finding_count, :error_message, :parser_status, :parser_message,
 :runtime_backend, :runtime_ref, :attempt, :cancel_requested_at, :requested_scope,
 :effective_scope, :scope_message,
 :started_at, :finished_at, :created_at, :updated_at)
 ON CONFLICT (scan_id, tool_name) DO UPDATE SET
   status=CASE
     WHEN scanner_run_records.cancel_requested_at IS NOT NULL THEN 'cancelled'
     ELSE excluded.status
   END,
   category=excluded.category,
   image=excluded.image,
   image_digest=excluded.image_digest,
   scanner_release_id=excluded.scanner_release_id,
   release_manifest_digest=excluded.release_manifest_digest,
   version=excluded.version,
   command_json=excluded.command_json,
   exit_code=excluded.exit_code,
   duration_ms=excluded.duration_ms,
   finding_count=excluded.finding_count,
   error_message=CASE
     WHEN scanner_run_records.cancel_requested_at IS NOT NULL
       THEN scanner_run_records.error_message
     ELSE excluded.error_message
   END,
   parser_status=excluded.parser_status,
   parser_message=excluded.parser_message,
   runtime_backend=excluded.runtime_backend,
   runtime_ref=excluded.runtime_ref,
   attempt=excluded.attempt,
   cancel_requested_at=COALESCE(
     scanner_run_records.cancel_requested_at,
     excluded.cancel_requested_at
   ),
   requested_scope=excluded.requested_scope,
   effective_scope=excluded.effective_scope,
   scope_message=excluded.scope_message,
   started_at=COALESCE(excluded.started_at, scanner_run_records.started_at),
   finished_at=CASE
     WHEN scanner_run_records.cancel_requested_at IS NOT NULL
       THEN scanner_run_records.finished_at
     ELSE excluded.finished_at
   END,
   updated_at=excluded.updated_at`
