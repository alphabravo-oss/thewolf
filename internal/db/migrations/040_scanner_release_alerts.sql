-- Durable scanner-release operational alerts. Fingerprints are stable across
-- evaluator replicas and policy revisions so conditions resolve and reopen
-- without creating duplicate current alerts.

CREATE TABLE IF NOT EXISTS scanner_release_alerts (
    id TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN (
        'missed_discovery',
        'stale_stable_release',
        'queue_backlog',
        'lease_churn',
        'repeated_gate_failure',
        'mirror_drift',
        'rollout_failure',
        'signature_health'
    )),
    severity TEXT NOT NULL CHECK (severity IN ('warning', 'critical')),
    state TEXT NOT NULL CHECK (state IN ('open', 'resolved')),
    scope_type TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    summary TEXT NOT NULL,
    evidence_json TEXT NOT NULL DEFAULT '{}',
    policy_id TEXT NOT NULL DEFAULT '',
    policy_scope TEXT NOT NULL DEFAULT 'global',
    policy_revision BIGINT NOT NULL DEFAULT 0,
    trigger_count INTEGER NOT NULL DEFAULT 1 CHECK (trigger_count > 0),
    generation INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0),
    version BIGINT NOT NULL DEFAULT 1,
    first_triggered_at TIMESTAMP NOT NULL,
    last_triggered_at TIMESTAMP NOT NULL,
    resolved_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scanner_release_alert_state
    ON scanner_release_alerts (state, severity, updated_at);
CREATE INDEX IF NOT EXISTS idx_scanner_release_alert_kind
    ON scanner_release_alerts (kind, state, updated_at);
CREATE INDEX IF NOT EXISTS idx_scanner_release_alert_scope
    ON scanner_release_alerts (policy_scope, state, kind, updated_at);
