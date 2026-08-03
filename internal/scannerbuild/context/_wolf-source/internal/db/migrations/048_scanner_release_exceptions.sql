-- Preserve complete, attributable exception context on the append-only
-- approval ledger. Empty defaults retain compatibility with historical
-- approval and rejection rows.
ALTER TABLE scanner_release_approvals
    ADD COLUMN exception_scope TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_release_approvals
    ADD COLUMN exception_owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_release_approvals
    ADD COLUMN compensating_control TEXT NOT NULL DEFAULT '';
