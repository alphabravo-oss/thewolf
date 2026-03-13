CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
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
    ON ai_prompt_templates(scope, scope_id, prompt_type, section);

INSERT OR IGNORE INTO settings (key, value) VALUES ('ai_enabled', 'true');
