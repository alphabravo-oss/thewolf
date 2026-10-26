ALTER TABLE findings ADD COLUMN cluster_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_findings_cluster ON findings(cluster_id);
