-- Failover pairs: files. Each server's index of the files it keeps in
-- sync (one row per file, the latest version; deleted = a tombstone), in
-- the same shape as the settings' change log (038): seq is this server's
-- own order (what the other one reads from), lamport orders versions
-- across both servers, origin is the server that wrote it ('' = this one).
CREATE TABLE file_clock (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    seq     INTEGER NOT NULL DEFAULT 0,
    lamport INTEGER NOT NULL DEFAULT 0
);
INSERT INTO file_clock (id) VALUES (1);

CREATE TABLE file_index (
    loc     TEXT NOT NULL,    -- the location's id (drive, home, appdata, …)
    path    TEXT NOT NULL,    -- slash-separated, inside the location
    size    INTEGER NOT NULL DEFAULT 0,
    mtime   INTEGER NOT NULL DEFAULT 0, -- unix nanoseconds
    mode    INTEGER NOT NULL DEFAULT 420,
    hash    TEXT NOT NULL DEFAULT '',   -- SHA-256, hex
    deleted INTEGER NOT NULL DEFAULT 0,
    seq     INTEGER NOT NULL,
    lamport INTEGER NOT NULL,
    origin  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (loc, path)
);
CREATE INDEX file_index_seq ON file_index (seq);
CREATE INDEX file_index_hash ON file_index (loc, hash);

-- How far each server of a pair has read the other's file index, and the
-- other has read its own (as their_cursor / acked do for settings).
ALTER TABLE failover_pairs ADD COLUMN file_cursor INTEGER NOT NULL DEFAULT 0;
ALTER TABLE failover_pairs ADD COLUMN file_acked INTEGER NOT NULL DEFAULT 0;
