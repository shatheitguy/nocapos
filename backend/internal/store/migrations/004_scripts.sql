-- Quick Script Launcher: saved shell scripts that admins run on the host.
CREATE TABLE scripts (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL,
    confirm     INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    last_run_at INTEGER,
    last_exit   INTEGER
);
