-- Enrichment columns on findings
ALTER TABLE findings ADD COLUMN module_name TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN function_name TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN symbol_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN file_purpose TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN dependents_json TEXT NOT NULL DEFAULT '[]';

-- Per-tool summaries (one per tool per scan)
CREATE TABLE IF NOT EXISTS tool_summaries (
    id TEXT PRIMARY KEY,
    scan_id TEXT NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
    tool_name TEXT NOT NULL,
    summary_text TEXT NOT NULL DEFAULT '',
    finding_count INTEGER NOT NULL DEFAULT 0,
    severity_counts TEXT NOT NULL DEFAULT '{}',
    critical_issues TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(scan_id, tool_name)
);
CREATE INDEX IF NOT EXISTS idx_tool_summaries_scan_id ON tool_summaries(scan_id);

-- Structured scan-level recommendations
CREATE TABLE IF NOT EXISTS scan_recommendations (
    id TEXT PRIMARY KEY,
    scan_id TEXT NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
    priority INTEGER NOT NULL DEFAULT 3,
    category TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    affected_tools TEXT NOT NULL DEFAULT '[]',
    effort_estimate TEXT NOT NULL DEFAULT 'medium',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_scan_recommendations_scan_id ON scan_recommendations(scan_id);
