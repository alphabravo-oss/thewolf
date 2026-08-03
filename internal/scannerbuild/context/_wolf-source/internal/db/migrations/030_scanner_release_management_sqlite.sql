-- 030_scanner_release_management_sqlite.sql
-- Durable scanner-release operations. Scanner definitions and release locks
-- remain Git-owned; this schema stores operational state and immutable
-- publication evidence.

CREATE TABLE IF NOT EXISTS scanner_update_policies (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    schedule_json TEXT NOT NULL DEFAULT '{}',
    rules_json TEXT NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (scope, revision)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scanner_policy_active_scope
    ON scanner_update_policies (scope) WHERE enabled = 1;

CREATE TABLE IF NOT EXISTS scanner_registry_targets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    registry_type TEXT NOT NULL CHECK (registry_type IN ('managed', 'mirror', 'private', 'air_gap')),
    host TEXT NOT NULL,
    namespace TEXT NOT NULL,
    secret_reference TEXT NOT NULL DEFAULT '',
    trust_policy_reference TEXT NOT NULL DEFAULT '',
    platform_policy_json TEXT NOT NULL DEFAULT '{}',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (host, namespace)
);
CREATE INDEX IF NOT EXISTS idx_scanner_registry_enabled
    ON scanner_registry_targets (enabled, name);

CREATE TABLE IF NOT EXISTS scanner_discovery_runs (
    id TEXT PRIMARY KEY,
    trigger TEXT NOT NULL CHECK (trigger IN ('scheduled', 'on_demand', 'security')),
    schedule_period TEXT NOT NULL DEFAULT '',
    definition_commit TEXT NOT NULL,
    policy_id TEXT NOT NULL REFERENCES scanner_update_policies (id) ON DELETE RESTRICT,
    policy_revision INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'resolving', 'comparing', 'proposing', 'completed', 'failed', 'cancelled')),
    available_count INTEGER NOT NULL DEFAULT 0 CHECK (available_count >= 0),
    selected_count INTEGER NOT NULL DEFAULT 0 CHECK (selected_count >= 0),
    error_class TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    actor TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scanner_discovery_idempotency
    ON scanner_discovery_runs (idempotency_key);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scanner_discovery_schedule_period
    ON scanner_discovery_runs (trigger, schedule_period, definition_commit)
    WHERE schedule_period <> '';
CREATE INDEX IF NOT EXISTS idx_scanner_discovery_queue
    ON scanner_discovery_runs (state, created_at);

CREATE TABLE IF NOT EXISTS scanner_update_items (
    id TEXT PRIMARY KEY,
    discovery_run_id TEXT NOT NULL REFERENCES scanner_discovery_runs (id) ON DELETE CASCADE,
    component_type TEXT NOT NULL CHECK (component_type IN ('tool', 'upstream_image', 'base_image', 'toolchain', 'package')),
    component_name TEXT NOT NULL,
    current_value TEXT NOT NULL,
    available_value TEXT NOT NULL,
    source_evidence_json TEXT NOT NULL DEFAULT '{}',
    risk_class TEXT NOT NULL CHECK (risk_class IN ('none', 'low', 'medium', 'high', 'critical')),
    compatibility_json TEXT NOT NULL DEFAULT '{}',
    selection_state TEXT NOT NULL DEFAULT 'unselected' CHECK (selection_state IN ('unselected', 'selected', 'held', 'rejected')),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (discovery_run_id, component_type, component_name)
);
CREATE INDEX IF NOT EXISTS idx_scanner_update_items_run_risk
    ON scanner_update_items (discovery_run_id, risk_class, component_name);

CREATE TABLE IF NOT EXISTS scanner_release_candidates (
    id TEXT PRIMARY KEY,
    discovery_run_id TEXT REFERENCES scanner_discovery_runs (id) ON DELETE SET NULL,
    definition_commit TEXT NOT NULL,
    proposed_commit TEXT NOT NULL DEFAULT '',
    proposal_url TEXT NOT NULL DEFAULT '',
    lock_digest TEXT NOT NULL DEFAULT '',
    lock_uri TEXT NOT NULL DEFAULT '',
    risk_summary_json TEXT NOT NULL DEFAULT '{}',
    state TEXT NOT NULL CHECK (state IN ('draft', 'awaiting_definition', 'queued', 'building', 'testing', 'security_review', 'awaiting_approval', 'approved', 'publishing', 'published', 'blocked', 'rejected', 'failed')),
    required_gates_json TEXT NOT NULL DEFAULT '[]',
    policy_decision TEXT NOT NULL DEFAULT '',
    policy_id TEXT NOT NULL REFERENCES scanner_update_policies (id) ON DELETE RESTRICT,
    policy_revision INTEGER NOT NULL,
    actor TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    error_class TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scanner_candidate_idempotency
    ON scanner_release_candidates (idempotency_key);
CREATE INDEX IF NOT EXISTS idx_scanner_candidate_pending
    ON scanner_release_candidates (state, created_at);
CREATE INDEX IF NOT EXISTS idx_scanner_candidate_discovery
    ON scanner_release_candidates (discovery_run_id);

CREATE TABLE IF NOT EXISTS scanner_build_runs (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL REFERENCES scanner_release_candidates (id) ON DELETE RESTRICT,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    worker_id TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('queued', 'claimed', 'running', 'completed', 'failed', 'cancelled')),
    platforms_json TEXT NOT NULL DEFAULT '[]',
    lease_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMP,
    heartbeat_at TIMESTAMP,
    cancel_requested_at TIMESTAMP,
    error_class TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (candidate_id, attempt)
);
CREATE INDEX IF NOT EXISTS idx_scanner_build_claim
    ON scanner_build_runs (state, lease_expires_at, created_at);

CREATE TABLE IF NOT EXISTS scanner_build_steps (
    id TEXT PRIMARY KEY,
    build_run_id TEXT NOT NULL REFERENCES scanner_build_runs (id) ON DELETE CASCADE,
    step_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'claimed', 'running', 'completed', 'failed', 'cancelled')),
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    output_uri TEXT NOT NULL DEFAULT '',
    output_digest TEXT NOT NULL DEFAULT '',
    summary_json TEXT NOT NULL DEFAULT '{}',
    retention_class TEXT NOT NULL DEFAULT 'transient',
    retain_until TIMESTAMP,
    protected INTEGER NOT NULL DEFAULT 0 CHECK (protected IN (0, 1)),
    error_class TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (build_run_id, step_key, attempt)
);
CREATE INDEX IF NOT EXISTS idx_scanner_build_step_queue
    ON scanner_build_steps (state, created_at);
CREATE INDEX IF NOT EXISTS idx_scanner_build_step_retention
    ON scanner_build_steps (protected, retain_until);

CREATE TABLE IF NOT EXISTS scanner_releases (
    id TEXT PRIMARY KEY,
    release_name TEXT NOT NULL UNIQUE,
    candidate_id TEXT NOT NULL UNIQUE REFERENCES scanner_release_candidates (id) ON DELETE RESTRICT,
    lock_digest TEXT NOT NULL,
    manifest_digest TEXT NOT NULL UNIQUE,
    manifest_uri TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('published', 'candidate_channel', 'canary', 'stable', 'deprecated', 'revoked')),
    signer_identity TEXT NOT NULL,
    policy_id TEXT NOT NULL REFERENCES scanner_update_policies (id) ON DELETE RESTRICT,
    policy_revision INTEGER NOT NULL,
    definition_commit TEXT NOT NULL,
    imported INTEGER NOT NULL DEFAULT 0 CHECK (imported IN (0, 1)),
    legacy INTEGER NOT NULL DEFAULT 0 CHECK (legacy IN (0, 1)),
    protected INTEGER NOT NULL DEFAULT 1 CHECK (protected IN (0, 1)),
    rollback_eligible INTEGER NOT NULL DEFAULT 1 CHECK (rollback_eligible IN (0, 1)),
    retention_class TEXT NOT NULL DEFAULT 'release',
    retain_until TIMESTAMP,
    version INTEGER NOT NULL DEFAULT 1,
    published_at TIMESTAMP NOT NULL,
    deprecated_at TIMESTAMP,
    revoked_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scanner_release_state
    ON scanner_releases (state, published_at);
CREATE INDEX IF NOT EXISTS idx_scanner_release_retention
    ON scanner_releases (protected, rollback_eligible, retain_until);

CREATE TABLE IF NOT EXISTS scanner_release_tools (
    id TEXT PRIMARY KEY,
    release_id TEXT NOT NULL REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    tool_key TEXT NOT NULL,
    tool_version TEXT NOT NULL,
    source_reference TEXT NOT NULL,
    source_digest TEXT NOT NULL DEFAULT '',
    checksum TEXT NOT NULL DEFAULT '',
    parser_compatibility TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    UNIQUE (release_id, tool_key)
);

CREATE TABLE IF NOT EXISTS scanner_release_images (
    id TEXT PRIMARY KEY,
    release_id TEXT NOT NULL REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    image_key TEXT NOT NULL,
    registry_target_id TEXT NOT NULL REFERENCES scanner_registry_targets (id) ON DELETE RESTRICT,
    repository TEXT NOT NULL,
    digest TEXT NOT NULL,
    platform_digests_json TEXT NOT NULL DEFAULT '{}',
    size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    signature_status TEXT NOT NULL,
    provenance_digest TEXT NOT NULL,
    sbom_digest TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (release_id, image_key, registry_target_id)
);
CREATE INDEX IF NOT EXISTS idx_scanner_release_image_digest
    ON scanner_release_images (digest);

CREATE TABLE IF NOT EXISTS scanner_release_artifacts (
    id TEXT PRIMARY KEY,
    release_id TEXT REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    candidate_id TEXT REFERENCES scanner_release_candidates (id) ON DELETE RESTRICT,
    artifact_type TEXT NOT NULL,
    media_type TEXT NOT NULL,
    uri TEXT NOT NULL,
    digest TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    retention_class TEXT NOT NULL DEFAULT 'evidence',
    retain_until TIMESTAMP,
    protected INTEGER NOT NULL DEFAULT 0 CHECK (protected IN (0, 1)),
    created_at TIMESTAMP NOT NULL,
    CHECK (release_id IS NOT NULL OR candidate_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_scanner_artifact_release
    ON scanner_release_artifacts (release_id, artifact_type);
CREATE INDEX IF NOT EXISTS idx_scanner_artifact_candidate
    ON scanner_release_artifacts (candidate_id, artifact_type);
CREATE INDEX IF NOT EXISTS idx_scanner_artifact_retention
    ON scanner_release_artifacts (protected, retain_until);

CREATE TABLE IF NOT EXISTS scanner_release_approvals (
    id TEXT PRIMARY KEY,
    candidate_id TEXT REFERENCES scanner_release_candidates (id) ON DELETE RESTRICT,
    release_id TEXT REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    actor TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('approve', 'reject', 'exception', 'emergency_override')),
    reason TEXT NOT NULL,
    evidence_digest TEXT NOT NULL,
    policy_decision TEXT NOT NULL,
    expires_at TIMESTAMP,
    idempotency_key TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    CHECK (candidate_id IS NOT NULL OR release_id IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scanner_approval_idempotency
    ON scanner_release_approvals (idempotency_key);
CREATE INDEX IF NOT EXISTS idx_scanner_approval_candidate
    ON scanner_release_approvals (candidate_id, created_at);
CREATE INDEX IF NOT EXISTS idx_scanner_approval_release
    ON scanner_release_approvals (release_id, created_at);

CREATE TABLE IF NOT EXISTS scanner_rollouts (
    id TEXT PRIMARY KEY,
    target TEXT NOT NULL,
    from_release_id TEXT REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    to_release_id TEXT NOT NULL REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    strategy TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'preparing', 'canary', 'verifying', 'rolling_out', 'completed', 'failed', 'paused', 'rolling_back', 'rolled_back')),
    policy_snapshot_json TEXT NOT NULL DEFAULT '{}',
    actor TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    rollback_of_rollout_id TEXT REFERENCES scanner_rollouts (id) ON DELETE RESTRICT,
    error_class TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scanner_rollout_idempotency
    ON scanner_rollouts (idempotency_key);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scanner_rollout_active_target
    ON scanner_rollouts (target)
    WHERE state IN ('pending', 'preparing', 'canary', 'verifying', 'rolling_out', 'paused', 'rolling_back');
CREATE INDEX IF NOT EXISTS idx_scanner_rollout_state
    ON scanner_rollouts (state, created_at);

CREATE TABLE IF NOT EXISTS scanner_rollout_cohorts (
    id TEXT PRIMARY KEY,
    rollout_id TEXT NOT NULL REFERENCES scanner_rollouts (id) ON DELETE CASCADE,
    cohort_name TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    desired_release_id TEXT NOT NULL REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    observed_release_id TEXT REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    state TEXT NOT NULL,
    total_workers INTEGER NOT NULL DEFAULT 0 CHECK (total_workers >= 0),
    ready_workers INTEGER NOT NULL DEFAULT 0 CHECK (ready_workers >= 0),
    failed_workers INTEGER NOT NULL DEFAULT 0 CHECK (failed_workers >= 0),
    health_summary_json TEXT NOT NULL DEFAULT '{}',
    deadline TIMESTAMP,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (rollout_id, cohort_name),
    UNIQUE (rollout_id, ordinal)
);

CREATE TABLE IF NOT EXISTS scanner_worker_release_status (
    worker_id TEXT PRIMARY KEY,
    cohort TEXT NOT NULL,
    desired_release_id TEXT REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    observed_release_id TEXT REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    cached_digests_json TEXT NOT NULL DEFAULT '[]',
    verification_state TEXT NOT NULL,
    verification_error TEXT NOT NULL DEFAULT '',
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    version INTEGER NOT NULL DEFAULT 1,
    last_heartbeat TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scanner_worker_release_heartbeat
    ON scanner_worker_release_status (cohort, last_heartbeat);

CREATE TABLE IF NOT EXISTS scanner_release_events (
    id TEXT PRIMARY KEY,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    event_type TEXT NOT NULL,
    prior_state TEXT NOT NULL DEFAULT '',
    new_state TEXT NOT NULL DEFAULT '',
    actor TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    policy_revision INTEGER NOT NULL DEFAULT 0,
    idempotency_key TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    UNIQUE (aggregate_type, aggregate_id, sequence),
    UNIQUE (aggregate_type, aggregate_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_scanner_release_events_stream
    ON scanner_release_events (aggregate_type, aggregate_id, sequence);

CREATE TABLE IF NOT EXISTS scanner_schedule_leases (
    schedule_key TEXT NOT NULL,
    period_key TEXT NOT NULL,
    owner TEXT NOT NULL,
    lease_token TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'completed', 'failed')),
    lease_expires_at TIMESTAMP NOT NULL,
    heartbeat_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP,
    result_ref TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (schedule_key, period_key)
);
CREATE INDEX IF NOT EXISTS idx_scanner_schedule_lease_active
    ON scanner_schedule_leases (state, lease_expires_at);

-- Release identity/evidence and approvals are append-only. Release state and
-- retention flags remain mutable, but publication identity cannot change.
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_identity_immutable
BEFORE UPDATE ON scanner_releases
WHEN NEW.id <> OLD.id
  OR NEW.release_name <> OLD.release_name
  OR NEW.candidate_id <> OLD.candidate_id
  OR NEW.lock_digest <> OLD.lock_digest
  OR NEW.manifest_digest <> OLD.manifest_digest
  OR NEW.manifest_uri <> OLD.manifest_uri
  OR NEW.signer_identity <> OLD.signer_identity
  OR NEW.policy_id <> OLD.policy_id
  OR NEW.policy_revision <> OLD.policy_revision
  OR NEW.definition_commit <> OLD.definition_commit
  OR NEW.published_at <> OLD.published_at
BEGIN
    SELECT RAISE(ABORT, 'published scanner release identity is immutable');
END;

CREATE TRIGGER IF NOT EXISTS trg_scanner_release_no_delete
BEFORE DELETE ON scanner_releases
BEGIN
    SELECT RAISE(ABORT, 'published scanner releases are append-only');
END;

CREATE TRIGGER IF NOT EXISTS trg_scanner_release_tools_no_update
BEFORE UPDATE ON scanner_release_tools
BEGIN
    SELECT RAISE(ABORT, 'scanner release tool evidence is append-only');
END;
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_tools_no_delete
BEFORE DELETE ON scanner_release_tools
BEGIN
    SELECT RAISE(ABORT, 'scanner release tool evidence is append-only');
END;
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_images_no_update
BEFORE UPDATE ON scanner_release_images
BEGIN
    SELECT RAISE(ABORT, 'scanner release image evidence is append-only');
END;
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_images_no_delete
BEFORE DELETE ON scanner_release_images
BEGIN
    SELECT RAISE(ABORT, 'scanner release image evidence is append-only');
END;
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_artifacts_no_update
BEFORE UPDATE ON scanner_release_artifacts
BEGIN
    SELECT RAISE(ABORT, 'scanner release artifacts are append-only');
END;
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_artifacts_no_delete
BEFORE DELETE ON scanner_release_artifacts
BEGIN
    SELECT RAISE(ABORT, 'scanner release artifacts are append-only');
END;
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_approvals_no_update
BEFORE UPDATE ON scanner_release_approvals
BEGIN
    SELECT RAISE(ABORT, 'scanner release approvals are append-only');
END;
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_approvals_no_delete
BEFORE DELETE ON scanner_release_approvals
BEGIN
    SELECT RAISE(ABORT, 'scanner release approvals are append-only');
END;
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_events_no_update
BEFORE UPDATE ON scanner_release_events
BEGIN
    SELECT RAISE(ABORT, 'scanner release events are append-only');
END;
CREATE TRIGGER IF NOT EXISTS trg_scanner_release_events_no_delete
BEFORE DELETE ON scanner_release_events
BEGIN
    SELECT RAISE(ABORT, 'scanner release events are append-only');
END;
