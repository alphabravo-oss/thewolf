-- 015_finding_suppressions.sql: durable server-side finding suppressions.

CREATE TABLE IF NOT EXISTS finding_suppressions (
    id TEXT PRIMARY KEY,
    repo_id TEXT NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    created_by TEXT NOT NULL REFERENCES users (id),
    scope_type TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    branch TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL,
    expires_at TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_finding_suppressions_repo_status ON finding_suppressions (
    repo_id,
    status
);
CREATE INDEX IF NOT EXISTS idx_finding_suppressions_scope ON finding_suppressions (
    scope_type,
    scope_value
);

CREATE TABLE IF NOT EXISTS finding_suppression_audit (
    id TEXT PRIMARY KEY,
    suppression_id TEXT NOT NULL REFERENCES finding_suppressions (id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    actor_id TEXT NOT NULL DEFAULT '',
    details_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_finding_suppression_audit_suppression_id ON finding_suppression_audit (
    suppression_id
);
