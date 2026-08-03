-- Durable OCI registry reconciliation, repair, and quarantine cleanup.
--
-- Jobs are idempotent, leased work items. Image observations retain exact
-- source/destination and evidence readback so an operator can distinguish
-- drift from a partial copy. Quarantine rows are deliberately separate from
-- release inventory: an object referenced by immutable release inventory is
-- never eligible for deletion.

CREATE TABLE IF NOT EXISTS scanner_registry_jobs (
    id TEXT PRIMARY KEY,
    registry_target_id TEXT NOT NULL REFERENCES scanner_registry_targets (id) ON DELETE RESTRICT,
    source_registry_target_id TEXT REFERENCES scanner_registry_targets (id) ON DELETE RESTRICT,
    release_id TEXT REFERENCES scanner_releases (id) ON DELETE RESTRICT,
    job_kind TEXT NOT NULL CHECK (job_kind IN ('reconcile', 'repair', 'cleanup')),
    re_sign_policy TEXT NOT NULL DEFAULT 'preserve' CHECK (re_sign_policy IN ('preserve', 'required', 'forbidden')),
    state TEXT NOT NULL CHECK (state IN ('queued', 'claimed', 'retry', 'completed', 'dead_letter', 'cancelled')),
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    available_at TIMESTAMP NOT NULL,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMP,
    heartbeat_at TIMESTAMP,
    summary_json TEXT NOT NULL DEFAULT '{}',
    error_class TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    dead_lettered_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scanner_registry_job_queue
    ON scanner_registry_jobs (state, available_at, created_at);
CREATE INDEX IF NOT EXISTS idx_scanner_registry_job_lease
    ON scanner_registry_jobs (state, lease_expires_at);
CREATE INDEX IF NOT EXISTS idx_scanner_registry_job_target
    ON scanner_registry_jobs (registry_target_id, release_id, created_at);

CREATE TABLE IF NOT EXISTS scanner_registry_image_observations (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES scanner_registry_jobs (id) ON DELETE CASCADE,
    image_key TEXT NOT NULL,
    source_reference TEXT NOT NULL DEFAULT '',
    destination_reference TEXT NOT NULL,
    expected_digest TEXT NOT NULL,
    source_digest TEXT NOT NULL DEFAULT '',
    destination_digest TEXT NOT NULL DEFAULT '',
    expected_signature_digest TEXT NOT NULL DEFAULT '',
    destination_signature_digest TEXT NOT NULL DEFAULT '',
    expected_provenance_digest TEXT NOT NULL DEFAULT '',
    destination_provenance_digest TEXT NOT NULL DEFAULT '',
    expected_sbom_digest TEXT NOT NULL DEFAULT '',
    destination_sbom_digest TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('pending', 'matched', 'drifted', 'copying', 'repaired', 'failed', 'skipped')),
    detail_json TEXT NOT NULL DEFAULT '{}',
    checked_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (job_id, image_key)
);

CREATE INDEX IF NOT EXISTS idx_scanner_registry_image_observation_job
    ON scanner_registry_image_observations (job_id, state, image_key);

CREATE TABLE IF NOT EXISTS scanner_registry_quarantine_objects (
    id TEXT PRIMARY KEY,
    registry_target_id TEXT NOT NULL REFERENCES scanner_registry_targets (id) ON DELETE RESTRICT,
    candidate_id TEXT REFERENCES scanner_release_candidates (id) ON DELETE RESTRICT,
    repository TEXT NOT NULL,
    digest TEXT NOT NULL,
    object_kind TEXT NOT NULL CHECK (object_kind IN ('manifest', 'signature', 'provenance', 'sbom', 'blob')),
    state TEXT NOT NULL CHECK (state IN ('quarantined', 'promoted', 'orphaned', 'deleting', 'deleted', 'retained', 'delete_failed')),
    protected BOOLEAN NOT NULL DEFAULT FALSE,
    retention_class TEXT NOT NULL DEFAULT 'quarantine',
    retain_until TIMESTAMP,
    discovered_at TIMESTAMP NOT NULL,
    last_referenced_at TIMESTAMP,
    deletion_worker_id TEXT NOT NULL DEFAULT '',
    deletion_lease_token TEXT NOT NULL DEFAULT '',
    deletion_lease_expires_at TIMESTAMP,
    deletion_verified_at TIMESTAMP,
    error_detail TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (registry_target_id, repository, digest, object_kind)
);

CREATE INDEX IF NOT EXISTS idx_scanner_registry_quarantine_cleanup
    ON scanner_registry_quarantine_objects (state, protected, retain_until, discovered_at);
CREATE INDEX IF NOT EXISTS idx_scanner_registry_quarantine_digest
    ON scanner_registry_quarantine_objects (digest, state);
