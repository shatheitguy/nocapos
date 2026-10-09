-- Network drives (SMB/NFS shares NoCapOS mounts) and file sharing (folders
-- NoCapOS shares over SMB and WebDAV), plus a small key/value table for
-- server-wide settings such as the sharing password hash.
CREATE TABLE net_mounts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('smb', 'nfs')),
    host       TEXT NOT NULL,
    share      TEXT NOT NULL,           -- SMB share name, or NFS export path
    username   TEXT NOT NULL DEFAULT '',
    password   BLOB,                    -- sealed
    auto       INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL
);

CREATE TABLE net_shares (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    root       TEXT NOT NULL,
    path       TEXT NOT NULL,
    read_only  INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE TABLE server_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
