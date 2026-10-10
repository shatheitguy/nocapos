-- Cloud imports: cloud accounts (rclone remotes; their settings, tokens and
-- passwords are sealed together as JSON) and imports that copy a folder from
-- an account into a storage location, once or on a schedule. Copies never
-- delete anything on either side.
CREATE TABLE cloud_accounts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL,            -- drive, dropbox, onedrive, s3, webdav, sftp
    config     BLOB NOT NULL,            -- sealed JSON of rclone settings
    created_at INTEGER NOT NULL
);

CREATE TABLE cloud_imports (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id  INTEGER NOT NULL REFERENCES cloud_accounts(id),
    name        TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '', -- folder in the account ("" = everything)
    dest_root   TEXT NOT NULL,
    dest_path   TEXT NOT NULL,
    schedule    TEXT NOT NULL DEFAULT '{}',
    last_run    INTEGER NOT NULL DEFAULT 0,
    last_status TEXT NOT NULL DEFAULT '',
    last_error  TEXT NOT NULL DEFAULT '',
    last_bytes  INTEGER NOT NULL DEFAULT 0,
    last_files  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);
