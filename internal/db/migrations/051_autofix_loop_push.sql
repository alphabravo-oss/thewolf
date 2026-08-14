-- 051_autofix_loop_push.sql — multi-round fix loops, HITL pause, and
-- push-for-review. Additive only. Restartable via execAdditiveMigration.

ALTER TABLE fix_jobs ADD COLUMN max_loops INTEGER NOT NULL DEFAULT 2;
ALTER TABLE fix_jobs ADD COLUMN current_loop INTEGER NOT NULL DEFAULT 0;
ALTER TABLE fix_jobs ADD COLUMN human_in_the_loop INTEGER NOT NULL DEFAULT 0;
ALTER TABLE fix_jobs ADD COLUMN workspace_path TEXT NOT NULL DEFAULT '';
ALTER TABLE fix_jobs ADD COLUMN base_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE fix_jobs ADD COLUMN pushed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE fix_jobs ADD COLUMN push_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE fix_jobs ADD COLUMN pause_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE fix_jobs ADD COLUMN resume_action TEXT NOT NULL DEFAULT '';

ALTER TABLE loops ADD COLUMN fix_job_id TEXT NOT NULL DEFAULT '';
