-- Monotonic credentials bind password-step challenges to the enrollment and
-- password that earned them. MFA revision also rejects stale equal-state writes.
ALTER TABLE users ADD COLUMN credential_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN mfa_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_last_step BIGINT NOT NULL DEFAULT -1;
