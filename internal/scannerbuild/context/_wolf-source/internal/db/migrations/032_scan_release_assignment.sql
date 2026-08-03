-- 032_scan_release_assignment.sql
-- Freeze scanner release identity at scan assignment so retries remain
-- reproducible even when the stable channel or deployment desired state moves.
-- Empty values preserve compatibility for legacy/unmanaged scanner images.

ALTER TABLE scans
    ADD COLUMN scanner_release_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scans
    ADD COLUMN release_manifest_digest TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_scans_scanner_release
    ON scans (scanner_release_id, status, created_at);
