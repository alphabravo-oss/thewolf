ALTER TABLE scans ADD COLUMN baseline_scan_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN baseline_resolved_at TIMESTAMP;
ALTER TABLE findings ADD COLUMN first_seen_at TIMESTAMP;
ALTER TABLE findings ADD COLUMN last_seen_at TIMESTAMP;
