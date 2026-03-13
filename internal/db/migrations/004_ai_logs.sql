CREATE TABLE IF NOT EXISTS ai_logs (
    id TEXT PRIMARY KEY,
    scan_id TEXT NOT NULL REFERENCES scans(id),
    provider TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    phase TEXT NOT NULL,
    tool_name TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL,
    response TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    prompt_tokens INT NOT NULL DEFAULT 0,
    response_tokens INT NOT NULL DEFAULT 0,
    duration_ms INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ai_logs_scan_id ON ai_logs(scan_id);
