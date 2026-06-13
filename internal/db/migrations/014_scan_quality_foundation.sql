-- 014_scan_quality_foundation.sql: durable finding identity and baseline/diff foundations.

ALTER TABLE findings ADD COLUMN stable_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN location_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN semantic_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN evidence_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN identity_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE findings ADD COLUMN fine_category TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN fix_strategy_id TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN confidence TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN corroborated_by_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE findings ADD COLUMN suppressed BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE findings ADD COLUMN suppression_id TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN suppressed_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN baseline_state TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN introduced_in_scan_id TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN resolved_in_scan_id TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN source_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN source_ref TEXT NOT NULL DEFAULT '';

UPDATE findings
SET stable_fingerprint = fingerprint
WHERE stable_fingerprint = '';

CREATE INDEX IF NOT EXISTS idx_findings_stable_fingerprint ON findings (stable_fingerprint);
CREATE INDEX IF NOT EXISTS idx_findings_baseline_state ON findings (baseline_state);
CREATE INDEX IF NOT EXISTS idx_findings_fine_category ON findings (fine_category);
CREATE INDEX IF NOT EXISTS idx_findings_suppressed ON findings (suppressed);

CREATE TABLE IF NOT EXISTS scan_baselines (
    id TEXT PRIMARY KEY,
    repo_id TEXT NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    branch TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    scan_id TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    strategy TEXT NOT NULL DEFAULT 'named',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (repo_id, branch, name)
);

CREATE INDEX IF NOT EXISTS idx_scan_baselines_repo_branch ON scan_baselines (
    repo_id,
    branch
);
CREATE INDEX IF NOT EXISTS idx_scan_baselines_scan_id ON scan_baselines (scan_id);

CREATE TABLE IF NOT EXISTS scan_comparisons (
    id TEXT PRIMARY KEY,
    repo_id TEXT NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    baseline_scan_id TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    current_scan_id TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    summary_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (baseline_scan_id, current_scan_id)
);

CREATE INDEX IF NOT EXISTS idx_scan_comparisons_repo_id ON scan_comparisons (repo_id);
CREATE INDEX IF NOT EXISTS idx_scan_comparisons_current_scan_id ON scan_comparisons (
    current_scan_id
);
