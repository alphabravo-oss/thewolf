-- 049_user_scanner_supply_chain_access.sql
-- Durable, server-validated human scanner personas. The value is a JSON array
-- of predefined persona IDs. An empty legacy value resolves to Viewer.
ALTER TABLE users ADD COLUMN scanner_supply_chain_personas TEXT NOT NULL DEFAULT '';
