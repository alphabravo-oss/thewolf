-- 052_fixer_model_effort.sql — per-job model / effort / variant dials
-- and token usage on each attempt.

ALTER TABLE fix_jobs ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE fix_jobs ADD COLUMN effort TEXT NOT NULL DEFAULT '';
ALTER TABLE fix_jobs ADD COLUMN variant TEXT NOT NULL DEFAULT '';

ALTER TABLE fix_attempts ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE fix_attempts ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
