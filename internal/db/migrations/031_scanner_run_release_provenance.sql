-- 031_scanner_run_release_provenance.sql
-- A scanner run retains the immutable release snapshot used by that tool.
-- Empty values are intentionally valid during the legacy compatibility
-- period, so these columns do not carry a foreign key.

ALTER TABLE scanner_run_records
    ADD COLUMN scanner_release_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_run_records
    ADD COLUMN release_manifest_digest TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_scanner_run_records_release
    ON scanner_run_records (scanner_release_id, scan_id);
CREATE INDEX IF NOT EXISTS idx_scanner_run_records_manifest_digest
    ON scanner_run_records (release_manifest_digest);
