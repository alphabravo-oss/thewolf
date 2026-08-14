-- 054_scan_remediation.sql — origin-scan lineage: one shared wolf-fix
-- branch/workspace until publish. Additive only.

CREATE TABLE IF NOT EXISTS remediations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL DEFAULT '',
    repo_id TEXT NOT NULL DEFAULT '',
    origin_scan_id TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    workspace_path TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'open',
    published_sha TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_remediations_origin ON remediations (origin_scan_id, state);
CREATE INDEX IF NOT EXISTS idx_remediations_repo ON remediations (repo_id);

ALTER TABLE scans ADD COLUMN origin_scan_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN previous_scan_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN remediation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN fix_job_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_scans_origin ON scans (origin_scan_id);
CREATE INDEX IF NOT EXISTS idx_scans_remediation ON scans (remediation_id);

ALTER TABLE fix_jobs ADD COLUMN remediation_id TEXT NOT NULL DEFAULT '';
