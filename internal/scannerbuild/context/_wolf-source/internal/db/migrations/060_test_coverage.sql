-- Measured test coverage is independent of scan-worker status updates.
CREATE TABLE IF NOT EXISTS scan_test_coverage (
    scan_id TEXT PRIMARY KEY REFERENCES scans (id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL,
    report_json TEXT NOT NULL,
    imported_by TEXT NOT NULL,
    imported_at TIMESTAMP NOT NULL
);
