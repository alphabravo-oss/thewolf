-- Customer signer configuration stores opaque references and identity policy,
-- never private signing material.
CREATE TABLE IF NOT EXISTS scanner_signer_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (provider IN (
        'aws_kms', 'gcp_kms', 'azure_key_vault', 'pkcs11',
        'keyless', 'offline', 'managed_keyless'
    )),
    algorithm TEXT NOT NULL CHECK (algorithm IN (
        'ed25519', 'ecdsa-p256-sha256', 'rsa-pss-sha256', 'cosign-keyless'
    )),
    key_reference TEXT NOT NULL,
    secret_reference TEXT NOT NULL DEFAULT '',
    workload_identity BOOLEAN NOT NULL DEFAULT FALSE,
    identity TEXT NOT NULL,
    issuer TEXT NOT NULL,
    subject TEXT NOT NULL,
    trust_root_reference TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'disabled', 'revoked')),
    revision BIGINT NOT NULL,
    rotated_from_id TEXT NOT NULL DEFAULT '',
    revocation_reason TEXT NOT NULL DEFAULT '',
    revoked_by TEXT NOT NULL DEFAULT '',
    revoked_at TIMESTAMP NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE(name, revision)
);

CREATE INDEX IF NOT EXISTS idx_scanner_signer_profiles_state
    ON scanner_signer_profiles (state, name, revision);
CREATE INDEX IF NOT EXISTS idx_scanner_signer_profiles_rotation
    ON scanner_signer_profiles (rotated_from_id);
