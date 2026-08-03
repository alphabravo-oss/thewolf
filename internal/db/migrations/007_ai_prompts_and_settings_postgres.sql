-- PostgreSQL form of migration 007. The portable/SQLite migration ends with
-- INSERT OR IGNORE statements; PostgreSQL rejects that syntax and rolls back
-- the entire multi-statement batch, including the settings table creation.
-- Keep the backend-specific form atomic and idempotent so an installation that
-- previously skipped the table repairs itself on its next startup.
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ai_prompt_templates (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL DEFAULT 'global',
    scope_id TEXT NOT NULL DEFAULT '',
    prompt_type TEXT NOT NULL,
    section TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_prompt_templates_lookup
ON ai_prompt_templates (scope, scope_id, prompt_type, section);

INSERT INTO settings (key, value)
VALUES ('ai_enabled', 'false')
ON CONFLICT (key) DO NOTHING;

INSERT INTO settings (key, value)
VALUES ('registration_enabled', 'false')
ON CONFLICT (key) DO NOTHING;
