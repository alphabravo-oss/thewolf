-- A settings KV store. The column name `key` collides with a SQL
-- reserved keyword (sqlfluff RF04) but it's the natural name for a
-- KV-store column and is already bound in internal/db/{sqlite,postgres}.go
-- via `db:"key"` tags. Renaming would require a new migration plus a
-- coordinated Go change; suppressing the lint here is the right
-- trade-off for SQLite which accepts the bare identifier without
-- escaping.
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,  -- noqa: RF04
    value TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ai_prompt_templates (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL DEFAULT 'global',
    scope_id TEXT NOT NULL DEFAULT '',
    prompt_type TEXT NOT NULL,
    section TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_prompt_templates_lookup
ON ai_prompt_templates (scope, scope_id, prompt_type, section);

INSERT OR IGNORE INTO settings (key, value) VALUES ('ai_enabled', 'true');
INSERT OR IGNORE INTO settings (key, value) VALUES ('registration_enabled', 'true');
