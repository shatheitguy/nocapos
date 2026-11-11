-- What each login session (refresh token family) is: how it signed in, from
-- which browser and address, and when it was last active. Rows outlive the
-- session itself for a while so a sign-in from a device that was used
-- before isn't reported as new.
CREATE TABLE session_info (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    device_id    TEXT NOT NULL DEFAULT '',
    device_key   TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    ip           TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
);
CREATE INDEX session_info_user ON session_info (user_id, device_key);

-- The address a linked device last called from.
ALTER TABLE devices ADD COLUMN last_ip TEXT NOT NULL DEFAULT '';
