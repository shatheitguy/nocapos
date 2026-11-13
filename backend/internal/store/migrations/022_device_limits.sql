-- What a linked device tells us about itself: its model, its OS version and
-- which NoCap Link features it has on (comma-separated, e.g.
-- "notifications,clipboard,files").
ALTER TABLE devices ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN os_version TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN caps TEXT NOT NULL DEFAULT '';

-- A per-person limit on linked devices, set by an administrator. People
-- without a row get the server-wide default (server_settings).
CREATE TABLE device_limits (
    user_id     TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    max_devices INTEGER NOT NULL
);
