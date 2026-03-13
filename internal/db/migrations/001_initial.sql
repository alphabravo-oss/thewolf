-- 001_initial.sql: Initial schema for The Wolf

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS repos (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_path TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT 'main',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS collections (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS collection_repos (
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    PRIMARY KEY (collection_id, repo_id)
);

CREATE TABLE IF NOT EXISTS secrets (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    key_type TEXT NOT NULL,
    key_name TEXT NOT NULL,
    encrypted_value TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS repo_maps (
    id TEXT PRIMARY KEY,
    repo_id TEXT NOT NULL REFERENCES repos(id),
    branch TEXT NOT NULL,
    structural_data TEXT NOT NULL DEFAULT '{}',
    semantic_data TEXT NOT NULL DEFAULT '{}',
    file_hashes TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS scans (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    repo_id TEXT NOT NULL REFERENCES repos(id),
    collection_id TEXT REFERENCES collections(id),
    loop_id TEXT,
    iteration INTEGER,
    branch TEXT NOT NULL DEFAULT 'main',
    status TEXT NOT NULL DEFAULT 'pending',
    tools_selected TEXT NOT NULL DEFAULT '[]',
    tools_completed TEXT NOT NULL DEFAULT '[]',
    tools_failed TEXT NOT NULL DEFAULT '[]',
    finding_count INTEGER NOT NULL DEFAULT 0,
    coverage_summary TEXT NOT NULL DEFAULT '{}',
    ai_summary TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS findings (
    id TEXT PRIMARY KEY,
    scan_id TEXT NOT NULL REFERENCES scans(id),
    repo_id TEXT NOT NULL REFERENCES repos(id),
    fingerprint TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    category TEXT NOT NULL,
    severity TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    file_path TEXT NOT NULL,
    line_start INTEGER NOT NULL DEFAULT 0,
    line_end INTEGER NOT NULL DEFAULT 0,
    code_snippet TEXT NOT NULL DEFAULT '',
    cwe_id TEXT,
    rule_id TEXT,
    tool_severity_score REAL NOT NULL DEFAULT 0,
    location_weight REAL NOT NULL DEFAULT 1,
    ai_context_score REAL NOT NULL DEFAULT 0,
    composite_score REAL NOT NULL DEFAULT 0,
    ai_fix_suggestion TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    sarif_data TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS fixes (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    scan_id TEXT NOT NULL REFERENCES scans(id),
    loop_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    severity_filter TEXT NOT NULL DEFAULT '[]',
    branch_name TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    findings_attempted INTEGER NOT NULL DEFAULT 0,
    findings_fixed INTEGER NOT NULL DEFAULT 0,
    findings_failed INTEGER NOT NULL DEFAULT 0,
    pr_urls TEXT NOT NULL DEFAULT '[]',
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS fix_items (
    id TEXT PRIMARY KEY,
    fix_id TEXT NOT NULL REFERENCES fixes(id),
    finding_id TEXT NOT NULL REFERENCES findings(id),
    status TEXT NOT NULL DEFAULT 'pending',
    files_changed TEXT NOT NULL DEFAULT '[]',
    diff TEXT NOT NULL DEFAULT '',
    validation_result TEXT NOT NULL DEFAULT '',
    validation_output TEXT NOT NULL DEFAULT '',
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS loops (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    repo_id TEXT NOT NULL REFERENCES repos(id),
    collection_id TEXT REFERENCES collections(id),
    status TEXT NOT NULL DEFAULT 'running',
    max_iterations INTEGER NOT NULL DEFAULT 5,
    current_iteration INTEGER NOT NULL DEFAULT 0,
    severity_filter TEXT NOT NULL DEFAULT '[]',
    rescan_strategy TEXT NOT NULL DEFAULT 'full',
    total_findings_initial INTEGER NOT NULL DEFAULT 0,
    total_findings_fixed INTEGER NOT NULL DEFAULT 0,
    total_findings_new INTEGER NOT NULL DEFAULT 0,
    total_findings_remaining INTEGER NOT NULL DEFAULT 0,
    guardrail_warnings TEXT NOT NULL DEFAULT '[]',
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS scan_artifacts (
    id TEXT PRIMARY KEY,
    scan_id TEXT NOT NULL REFERENCES scans(id),
    artifact_type TEXT NOT NULL,
    file_path TEXT NOT NULL,
    file_size INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_repos_user_id ON repos(user_id);
CREATE INDEX IF NOT EXISTS idx_collections_user_id ON collections(user_id);
CREATE INDEX IF NOT EXISTS idx_scans_user_id ON scans(user_id);
CREATE INDEX IF NOT EXISTS idx_scans_repo_id ON scans(repo_id);
CREATE INDEX IF NOT EXISTS idx_scans_status ON scans(status);
CREATE INDEX IF NOT EXISTS idx_findings_scan_id ON findings(scan_id);
CREATE INDEX IF NOT EXISTS idx_findings_repo_id ON findings(repo_id);
CREATE INDEX IF NOT EXISTS idx_findings_fingerprint ON findings(fingerprint);
CREATE INDEX IF NOT EXISTS idx_findings_severity ON findings(severity);
CREATE INDEX IF NOT EXISTS idx_findings_status ON findings(status);
CREATE INDEX IF NOT EXISTS idx_fixes_scan_id ON fixes(scan_id);
CREATE INDEX IF NOT EXISTS idx_fix_items_fix_id ON fix_items(fix_id);
CREATE INDEX IF NOT EXISTS idx_loops_user_id ON loops(user_id);
CREATE INDEX IF NOT EXISTS idx_scan_artifacts_scan_id ON scan_artifacts(scan_id);
