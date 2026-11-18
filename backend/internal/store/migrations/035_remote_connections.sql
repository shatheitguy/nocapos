-- Remote Desktop: each person's saved computers (RDP or VNC). settings is the
-- connection's options as JSON (display, local resources, experience,
-- security, gateway). password_enc / gateway_password_enc are sealed with the
-- server's secret box and set only when the person ticked "Remember password".
-- Times are unix milliseconds.
CREATE TABLE remote_connections (
    id                   TEXT PRIMARY KEY,
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL DEFAULT '',
    protocol             TEXT NOT NULL DEFAULT 'rdp',
    hostname             TEXT NOT NULL,
    port                 INTEGER NOT NULL DEFAULT 0,
    username             TEXT NOT NULL DEFAULT '',
    domain               TEXT NOT NULL DEFAULT '',
    settings             TEXT NOT NULL DEFAULT '{}',
    password_enc         BLOB,
    gateway_password_enc BLOB,
    last_used            INTEGER,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);
CREATE INDEX remote_connections_user ON remote_connections(user_id, name);
