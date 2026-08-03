-- 026_remote_scan_workers.sql
-- Durable scan queue, replayable progress events, worker heartbeats, and
-- additive source/profile metadata. All public scan status values remain
-- unchanged. Phase is the internal execution detail.

CREATE TABLE IF NOT EXISTS scan_events (
    id TEXT PRIMARY KEY,
    scan_id TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    data_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (scan_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_scan_events_scan_sequence
    ON scan_events (scan_id, sequence);

CREATE TABLE IF NOT EXISTS scan_workers (
    id TEXT PRIMARY KEY,
    backend TEXT NOT NULL DEFAULT 'docker',
    status TEXT NOT NULL DEFAULT 'ready',
    capacity INTEGER NOT NULL DEFAULT 1,
    active_scans INTEGER NOT NULL DEFAULT 0,
    version TEXT NOT NULL DEFAULT '',
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    heartbeat_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_scan_workers_heartbeat
    ON scan_workers (heartbeat_at);

ALTER TABLE scans ADD COLUMN request_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE scans ADD COLUMN request_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN client_reference TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN phase TEXT NOT NULL DEFAULT 'queued';
ALTER TABLE scans ADD COLUMN claimed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN lease_expires_at TIMESTAMP;
ALTER TABLE scans ADD COLUMN heartbeat_at TIMESTAMP;
ALTER TABLE scans ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scans ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 2;
ALTER TABLE scans ADD COLUMN cancel_requested_at TIMESTAMP;
ALTER TABLE scans ADD COLUMN failure_code TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN failure_message TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN execution_backend TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN source_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN profile TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN categories TEXT NOT NULL DEFAULT '[]';
ALTER TABLE scans ADD COLUMN include_paths TEXT NOT NULL DEFAULT '[]';
ALTER TABLE scans ADD COLUMN exclude_paths TEXT NOT NULL DEFAULT '[]';

CREATE UNIQUE INDEX IF NOT EXISTS idx_scans_user_idempotency
    ON scans (user_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_scans_queue
    ON scans (status, lease_expires_at, created_at);

ALTER TABLE scanner_run_records ADD COLUMN runtime_backend TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_run_records ADD COLUMN runtime_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_run_records ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_run_records ADD COLUMN cancel_requested_at TIMESTAMP;
ALTER TABLE scanner_run_records ADD COLUMN requested_scope TEXT NOT NULL DEFAULT '{}';
ALTER TABLE scanner_run_records ADD COLUMN effective_scope TEXT NOT NULL DEFAULT '{}';
ALTER TABLE scanner_run_records ADD COLUMN scope_message TEXT NOT NULL DEFAULT '';

ALTER TABLE repos ADD COLUMN source_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN credential_secret_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_user_source_fingerprint
    ON repos (user_id, source_fingerprint)
    WHERE source_fingerprint <> '';
