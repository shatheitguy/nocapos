-- Phones and other devices linked to a person with a pairing code. Only the
-- SHA-256 of each device's token is kept. Unlinking sets revoked_at.
CREATE TABLE devices (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    platform     TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    app_version  TEXT NOT NULL DEFAULT '',
    battery      INTEGER NOT NULL DEFAULT -1,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL DEFAULT 0,
    revoked_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX devices_user_id ON devices (user_id);
