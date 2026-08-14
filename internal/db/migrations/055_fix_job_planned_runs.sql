-- Sequential agent runs on one remediation.
-- planned_runs is how many jobs the operator asked for.
-- run_index is 1-based within that batch.
ALTER TABLE fix_jobs ADD COLUMN planned_runs INTEGER NOT NULL DEFAULT 1;
ALTER TABLE fix_jobs ADD COLUMN run_index INTEGER NOT NULL DEFAULT 1;
