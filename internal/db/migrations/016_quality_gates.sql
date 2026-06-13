-- 016_quality_gates.sql: quality gate policies and per-scan gate results.

CREATE TABLE IF NOT EXISTS quality_policies (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'global',
    scope_id TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'warn',
    rules_json TEXT NOT NULL DEFAULT '[]',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (scope, scope_id, name)
);

CREATE INDEX IF NOT EXISTS idx_quality_policies_scope ON quality_policies (
    scope,
    scope_id,
    enabled
);

CREATE TABLE IF NOT EXISTS quality_gate_results (
    id TEXT PRIMARY KEY,
    scan_id TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    policy_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    summary_json TEXT NOT NULL DEFAULT '{}',
    matched_rules_json TEXT NOT NULL DEFAULT '[]',
    evaluated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (scan_id, policy_id)
);

CREATE INDEX IF NOT EXISTS idx_quality_gate_results_scan_id ON quality_gate_results (
    scan_id
);
CREATE INDEX IF NOT EXISTS idx_quality_gate_results_status ON quality_gate_results (status);
