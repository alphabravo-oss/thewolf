CREATE TABLE IF NOT EXISTS scanner_version_checks (
    tool_name TEXT PRIMARY KEY,
    pinned_version TEXT NOT NULL,
    latest_version TEXT,
    latest_reference TEXT,
    status TEXT NOT NULL,
    checked_at TIMESTAMP NOT NULL,
    error TEXT,
    source_type TEXT NOT NULL,
    source_url TEXT
);

CREATE INDEX IF NOT EXISTS idx_scanner_version_checks_status
ON scanner_version_checks (status);

CREATE INDEX IF NOT EXISTS idx_scanner_version_checks_checked_at
ON scanner_version_checks (checked_at);

