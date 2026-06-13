-- 019_scanner_run_records.sql: durable per-tool scanner execution records.

CREATE TABLE IF NOT EXISTS scanner_run_records (
    id TEXT PRIMARY KEY,
    scan_id TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    tool_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    category TEXT NOT NULL DEFAULT '',
    image TEXT NOT NULL DEFAULT '',
    image_digest TEXT NOT NULL DEFAULT '',
    version TEXT NOT NULL DEFAULT '',
    command_json TEXT NOT NULL DEFAULT '{}',
    exit_code INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    finding_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    parser_status TEXT NOT NULL DEFAULT '',
    parser_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP,
    finished_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (scan_id, tool_name)
);

CREATE INDEX IF NOT EXISTS idx_scanner_run_records_scan_id ON scanner_run_records (scan_id);
CREATE INDEX IF NOT EXISTS idx_scanner_run_records_status ON scanner_run_records (status);
