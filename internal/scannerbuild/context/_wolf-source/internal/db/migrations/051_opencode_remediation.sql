CREATE TABLE IF NOT EXISTS remediation_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    scan_id TEXT NOT NULL,
    loop_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    -- BOOLEAN, not INTEGER: lib/pq encodes a Go bool parameter as the literal
    -- text "true"/"false" regardless of the target column's OID (it only
    -- consults the OID for []byte and time.Time), and Postgres infers an
    -- INTEGER column's placeholder type as integer, so writes would fail
    -- with "invalid input syntax for type integer". SQLite accepts BOOLEAN
    -- too (NUMERIC affinity, stores 0/1), so one spelling serves both.
    plan_gate_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    patch_gate_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    max_turns INTEGER NOT NULL DEFAULT 20,
    turns_used_plan INTEGER NOT NULL DEFAULT 0,
    turns_used_execute INTEGER NOT NULL DEFAULT 0,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    cost_used REAL NOT NULL DEFAULT 0,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    -- Scratch clone worktree_path was worktreed off of, for a local-source
    -- session. Not derivable from worktree_path (independent temp roots) —
    -- this is the handle needed to actually clean the clone up later.
    clone_root TEXT NOT NULL DEFAULT '',
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

-- clone_root deliberately appears twice: once above in CREATE TABLE (for a
-- database created fresh, after this edit), and again here as a standalone
-- ADD COLUMN (for a database that already ran an earlier version of this
-- file). execAdditiveMigration (internal/db/sqlite.go) has no migration
-- ledger — it re-runs every statement on every startup and swallows
-- "duplicate column"/"already exists"/"duplicate key" errors. On an
-- existing table, CREATE TABLE IF NOT EXISTS is a silent NO-OP, not an
-- error the runner can swallow, so a table created before clone_root
-- existed would never gain the column without this ALTER. On a fresh
-- database this statement itself hits "duplicate column" (already created
-- above) and is swallowed the normal way. Do not remove this as apparent
-- redundancy — the CREATE TABLE copy alone does not reach a stale database.
ALTER TABLE remediation_sessions ADD COLUMN clone_root TEXT NOT NULL DEFAULT '';

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

-- UNIQUE, not a plain index. seq is the sole ordering key for SSE replay and
-- for the audit trail, so two events sharing a session's seq make the stream
-- unorderable and let a replay silently drop or reorder turns. The constraint
-- turns an emitter that restarts its sequence per phase into a loud write
-- failure instead of quiet corruption. Free to add now because this migration
-- has never run in production, where it would need a data migration instead.
-- Keep this comment free of semicolons: the migration runner splits on them.
CREATE UNIQUE INDEX IF NOT EXISTS idx_remediation_events_session_seq ON remediation_events(session_id, seq);
