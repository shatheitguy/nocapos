-- Backups: restic repositories (where backups go) and backup plans (what,
-- when, how long to keep). Repository passwords and cloud secrets are sealed
-- with the server's secret box; config holds the non-secret settings as JSON.
CREATE TABLE backup_repos (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('local', 'sftp', 's3')),
    config      TEXT NOT NULL DEFAULT '{}',
    password    BLOB NOT NULL,          -- sealed restic repository password
    secret      BLOB,                   -- sealed cloud secret key (s3)
    last_prune  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);

CREATE TABLE backup_plans (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id     INTEGER NOT NULL REFERENCES backup_repos(id),
    name        TEXT NOT NULL,
    sources     TEXT NOT NULL DEFAULT '[]', -- [{"root":"drive","path":"/Photos"}, {"special":"nocapos"}]
    schedule    TEXT NOT NULL DEFAULT '{}', -- {"every":"daily","at":"02:00","weekday":0}
    keep        TEXT NOT NULL DEFAULT '{}', -- {"hourly":24,"daily":7,"weekly":4,"monthly":12}
    enabled     INTEGER NOT NULL DEFAULT 1,
    last_run    INTEGER NOT NULL DEFAULT 0,
    last_status TEXT NOT NULL DEFAULT '',  -- "", ok, failed, canceled
    last_error  TEXT NOT NULL DEFAULT '',
    last_snapshot TEXT NOT NULL DEFAULT '',
    last_bytes  INTEGER NOT NULL DEFAULT 0, -- data added by the last run
    created_at  INTEGER NOT NULL
);
