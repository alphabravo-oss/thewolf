-- 020_fleet_mode.sql: seed the fleet_mode setting (default OFF).
--
-- When fleet_mode=true, list endpoints for Repos / Scans / Findings /
-- Collections drop the user_id filter and return the entire org's data
-- to any authenticated user with the right scope. Recommended for
-- installs with multiple users sharing a fleet of >20 repos. Default
-- off preserves single-user privacy.
INSERT OR IGNORE INTO settings (key, value) VALUES ('fleet_mode', 'false');
