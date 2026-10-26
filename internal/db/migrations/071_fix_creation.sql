-- Durable caller identity is immutable across job status updates.
ALTER TABLE fix_jobs ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';
ALTER TABLE fix_jobs ADD COLUMN request_digest TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_fix_jobs_user_idempotency ON fix_jobs(user_id, idempotency_key) WHERE idempotency_key <> '';
