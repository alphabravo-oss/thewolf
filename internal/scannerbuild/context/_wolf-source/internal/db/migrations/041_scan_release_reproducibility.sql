-- Phase 9: distinguish an operator-requested scan under a different immutable
-- scanner release from transparent worker retries of the same scan row.
--
-- Empty values preserve every existing UI/API scan response and workflow.
ALTER TABLE scans
    ADD COLUMN rescan_of_scan_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scans
    ADD COLUMN release_selection_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_scans_rescan_lineage
    ON scans (rescan_of_scan_id, created_at);
