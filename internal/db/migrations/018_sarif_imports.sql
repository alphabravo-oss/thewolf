-- 018_sarif_imports.sql: imported SARIF metadata.

CREATE TABLE IF NOT EXISTS sarif_imports (
    id TEXT PRIMARY KEY,
    repo_id TEXT NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    scan_id TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    source TEXT NOT NULL DEFAULT '',
    checksum_sha256 TEXT NOT NULL DEFAULT '',
    result_count INTEGER NOT NULL DEFAULT 0,
    imported_count INTEGER NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sarif_imports_repo_id ON sarif_imports (repo_id);
CREATE INDEX IF NOT EXISTS idx_sarif_imports_scan_id ON sarif_imports (scan_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sarif_imports_checksum_repo ON sarif_imports (
    repo_id,
    checksum_sha256
);
