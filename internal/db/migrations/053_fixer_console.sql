-- 053_fixer_console.sql — worker-attached login / operator console.

CREATE TABLE IF NOT EXISTS fixer_consoles (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'login',
    engine TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued',
    claimed_by TEXT NOT NULL DEFAULT '',
    last_url TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    claimed_at TIMESTAMP,
    started_at TIMESTAMP,
    finished_at TIMESTAMP,
    heartbeat_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fixer_consoles_status ON fixer_consoles (status, created_at);

CREATE TABLE IF NOT EXISTS fixer_console_stdin (
    id TEXT PRIMARY KEY,
    console_id TEXT NOT NULL,
    data TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fixer_console_stdin ON fixer_console_stdin (console_id, created_at);
