CREATE TABLE IF NOT EXISTS repo_dast_targets (
    repo_id TEXT PRIMARY KEY REFERENCES repos(id) ON DELETE CASCADE,
    url TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'baseline' CHECK (mode IN ('baseline', 'full', 'api')),
    api_spec_url TEXT NOT NULL DEFAULT '',
    api_format TEXT NOT NULL DEFAULT 'openapi' CHECK (api_format IN ('openapi', 'graphql')),
    auth_secret_id TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    approved_by TEXT NOT NULL DEFAULT '',
    allow_private BOOLEAN NOT NULL DEFAULT FALSE,
    active_approved BOOLEAN NOT NULL DEFAULT FALSE
);
