CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'user')),
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- One row per issued refresh token. Rotated tokens stay (revoked) until expiry
-- so that replay of an old token can be detected and the family killed.
CREATE TABLE refresh_tokens (
    token_hash        TEXT PRIMARY KEY,
    family_id         TEXT NOT NULL,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at        INTEGER NOT NULL,
    expires_at        INTEGER NOT NULL,
    family_expires_at INTEGER NOT NULL,
    revoked_at        INTEGER,
    user_agent        TEXT NOT NULL DEFAULT '',
    ip                TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_refresh_family ON refresh_tokens(family_id);
CREATE INDEX idx_refresh_expires ON refresh_tokens(expires_at);

CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    ts      INTEGER NOT NULL,
    user_id TEXT,
    action  TEXT NOT NULL,
    target  TEXT NOT NULL DEFAULT '',
    ip      TEXT NOT NULL DEFAULT '',
    success INTEGER NOT NULL,
    detail  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_audit_ts ON audit_log(ts);
