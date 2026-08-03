ALTER TABLE scan_artifacts ADD COLUMN storage_key TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_scan_artifacts_storage_key
    ON scan_artifacts (storage_key);
