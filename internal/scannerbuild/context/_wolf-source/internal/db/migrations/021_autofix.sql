-- 021_autofix.sql — autonomous fix engine: durable job queue + the master
-- feature flag. Everything in the fix engine stays dark until an admin flips
-- autofix_enabled to 'true' (Settings → General, or
-- `wolf settings set --set autofix_enabled=true`). Mirrors fleet_mode /
-- ai_enabled. Default OFF.
INSERT OR IGNORE INTO settings (key, value) VALUES ('autofix_enabled', 'false');

-- A fix job: one queued unit of remediation work a worker claims and runs.
-- v1 mode is always 'dry_run' (produce a branch + diff, no push/PR).
CREATE TABLE IF NOT EXISTS fix_jobs (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'fix',          -- fix | loop
    repo_id TEXT NOT NULL,
    scan_id TEXT NOT NULL DEFAULT '',
    finding_ids TEXT NOT NULL DEFAULT '[]',     -- JSON array of finding ids
    target_branch TEXT NOT NULL DEFAULT 'main',
    engine TEXT NOT NULL DEFAULT 'auto',        -- auto | claude-code | codex | api | custom
    mode TEXT NOT NULL DEFAULT 'dry_run',       -- dry_run | (branch/pr in v1.1)
    severity_floor TEXT NOT NULL DEFAULT 'low',
    max_attempts INTEGER NOT NULL DEFAULT 2,
    status TEXT NOT NULL DEFAULT 'queued',      -- queued|claimed|running|succeeded|failed|cancelled
    claimed_by TEXT NOT NULL DEFAULT '',        -- worker id
    result_branch TEXT NOT NULL DEFAULT '',
    diff_artifact_id TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '{}',         -- JSON summary
    error TEXT NOT NULL DEFAULT '',
    claimed_at TIMESTAMP,
    started_at TIMESTAMP,
    finished_at TIMESTAMP,
    heartbeat_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fix_jobs_status ON fix_jobs (status, created_at);
CREATE INDEX IF NOT EXISTS idx_fix_jobs_repo ON fix_jobs (repo_id);

-- One row per finding-attempt — the audit trail of what each engine tried
-- and how verification judged it.
CREATE TABLE IF NOT EXISTS fix_attempts (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    finding_id TEXT NOT NULL,
    attempt_no INTEGER NOT NULL DEFAULT 1,
    engine_used TEXT NOT NULL DEFAULT '',       -- cli:claude-code | cli:codex | api | custom
    model TEXT NOT NULL DEFAULT '',
    built INTEGER NOT NULL DEFAULT 0,           -- bool: still builds after the change
    finding_cleared INTEGER NOT NULL DEFAULT 0, -- bool: targeted rescan no longer reports it
    new_findings INTEGER NOT NULL DEFAULT 0,    -- count of regressions introduced
    outcome TEXT NOT NULL DEFAULT '',           -- kept | rolled_back | unfixable
    files_changed TEXT NOT NULL DEFAULT '[]',   -- JSON array
    diff_excerpt TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    cost_usd REAL NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fix_attempts_job ON fix_attempts (job_id);
