CREATE TABLE IF NOT EXISTS remediation_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    scan_id TEXT NOT NULL,
    loop_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    plan_gate_enabled INTEGER NOT NULL DEFAULT 1,
    patch_gate_enabled INTEGER NOT NULL DEFAULT 1,
    max_turns INTEGER NOT NULL DEFAULT 20,
    turns_used_plan INTEGER NOT NULL DEFAULT 0,
    turns_used_execute INTEGER NOT NULL DEFAULT 0,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    cost_used REAL NOT NULL DEFAULT 0,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    pr_url TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP,
    completed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_remediation_sessions_user ON remediation_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_remediation_sessions_scan ON remediation_sessions(scan_id);
CREATE INDEX IF NOT EXISTS idx_remediation_sessions_status ON remediation_sessions(status);

CREATE TABLE IF NOT EXISTS remediation_plans (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    plan_json TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    approved_by TEXT NOT NULL DEFAULT '',
    approved_at TIMESTAMP,
    rejected_reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_remediation_plans_session ON remediation_plans(session_id);

CREATE TABLE IF NOT EXISTS remediation_patches (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    files_changed TEXT NOT NULL DEFAULT '',
    finding_ids TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    approved_by TEXT NOT NULL DEFAULT '',
    approved_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_remediation_patches_session ON remediation_patches(session_id);

CREATE TABLE IF NOT EXISTS remediation_events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    type TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_remediation_events_session_seq ON remediation_events(session_id, seq);
