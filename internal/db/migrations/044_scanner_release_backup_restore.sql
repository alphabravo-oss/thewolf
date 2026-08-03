-- Recovery infrastructure is deliberately outside backup payloads. It
-- records export/restore evidence and coordinates an exclusive restore lease.

CREATE TABLE IF NOT EXISTS scanner_release_maintenance (
    id TEXT PRIMARY KEY CHECK (id = 'scanner-release'),
    mode TEXT NOT NULL CHECK (mode IN ('normal', 'restore')),
    owner TEXT NOT NULL DEFAULT '',
    lease_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMP,
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMP NOT NULL
);

INSERT INTO scanner_release_maintenance
    (id, mode, owner, lease_token, lease_expires_at, version, updated_at)
VALUES
    ('scanner-release', 'normal', '', '', NULL, 1, CURRENT_TIMESTAMP)
ON CONFLICT(id) DO NOTHING;

CREATE TABLE IF NOT EXISTS scanner_release_backup_operations (
    id TEXT PRIMARY KEY,
    operation_type TEXT NOT NULL CHECK (operation_type IN ('export', 'restore')),
    state TEXT NOT NULL CHECK (state IN ('completed', 'failed')),
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload_digest TEXT NOT NULL,
    format_version INTEGER NOT NULL,
    table_counts_json TEXT NOT NULL DEFAULT '{}',
    error_detail TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP,
    UNIQUE (operation_type, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_scanner_release_backup_operations_time
    ON scanner_release_backup_operations (started_at, id);
