-- 022_user_roles.sql
-- RBAC: every user has a role (admin | user). Admins manage settings, users,
-- nodes, and scanner images, and can modify any resource; users can only
-- modify what they created. New users default to 'user'.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user';

-- Existing installs: make sure there's at least one admin so the system stays
-- manageable. Promote the earliest-created user (the de-facto owner of a
-- single-user install / the original operator).
UPDATE users SET role = 'admin'
WHERE id = (SELECT id FROM users ORDER BY created_at ASC LIMIT 1);
