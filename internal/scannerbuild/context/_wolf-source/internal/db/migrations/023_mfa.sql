-- 023_mfa.sql
-- Two-factor auth (TOTP). Per-user secret + activation flag + one-time recovery
-- codes. The secret is stored encrypted at rest (master key); recovery codes
-- are stored as SHA-256 hashes (JSON array), consumed as they are used.
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN totp_recovery_codes TEXT NOT NULL DEFAULT '';
