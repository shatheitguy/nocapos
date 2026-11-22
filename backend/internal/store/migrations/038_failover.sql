-- Failover pairs: two linked NoCap servers kept in sync (Settings → Your
-- Squad → a linked server → Make this my failover).
--
-- The change log: triggers (created at start-up, see replicate.go) note
-- every insert, update and delete in the tables both servers share, one
-- row per record in sync_rows with the latest version only. seq is this
-- server's own order (what the other one reads from), lamport the logical
-- clock that orders writes across both servers (last writer wins, ties go
-- to the higher NoCap ID), origin the server that wrote it ('' = this one).
-- sync_ctx says who is writing right now: '' for this server, or the other
-- server's NoCap ID and its clock while its changes are applied.
CREATE TABLE sync_clock (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    seq     INTEGER NOT NULL DEFAULT 0,
    lamport INTEGER NOT NULL DEFAULT 0
);
INSERT INTO sync_clock (id) VALUES (1);

CREATE TABLE sync_ctx (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    origin  TEXT NOT NULL DEFAULT '',
    lamport INTEGER NOT NULL DEFAULT 0
);
INSERT INTO sync_ctx (id) VALUES (1);

CREATE TABLE sync_rows (
    tbl     TEXT NOT NULL,
    pk      TEXT NOT NULL, -- JSON array of the primary key's values
    seq     INTEGER NOT NULL,
    lamport INTEGER NOT NULL,
    origin  TEXT NOT NULL DEFAULT '',
    at      INTEGER NOT NULL,
    PRIMARY KEY (tbl, pk)
);
CREATE INDEX sync_rows_seq ON sync_rows (seq);

-- Both servers changed the same record before they synced. Last writer
-- wins at once (winner); the administrator can keep the other version.
-- mine/theirs are the record's values as JSON (NULL = deleted), with
-- secrets sealed by this server's own key.
CREATE TABLE sync_conflicts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tbl         TEXT NOT NULL,
    pk          TEXT NOT NULL,
    peer        TEXT NOT NULL,
    mine        TEXT,
    theirs      TEXT,
    winner      TEXT NOT NULL CHECK (winner IN ('mine', 'theirs')),
    note        TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    resolved_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX sync_conflicts_open ON sync_conflicts (resolved_at, id);

-- The failover pair (at most one per server). state: asked (this server
-- asked the other), asking (the other asked this one), starting (the
-- first sync runs: the server that isn't primary_id takes the primary's
-- data), active. their_cursor: how far this server has read the other's
-- change log; acked: how far the other has read this one's.
CREATE TABLE failover_pairs (
    nocap_id     TEXT PRIMARY KEY REFERENCES server_links(nocap_id) ON DELETE CASCADE,
    state        TEXT NOT NULL CHECK (state IN ('asked', 'asking', 'starting', 'active')),
    primary_id   TEXT NOT NULL,
    their_cursor INTEGER NOT NULL DEFAULT 0,
    acked        INTEGER NOT NULL DEFAULT 0,
    paused       INTEGER NOT NULL DEFAULT 0,
    asked_by     TEXT NOT NULL DEFAULT '',
    options      TEXT NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL,
    active_at    INTEGER NOT NULL DEFAULT 0,
    last_pull    INTEGER NOT NULL DEFAULT 0,
    last_ok      INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT NOT NULL DEFAULT '',
    failures     INTEGER NOT NULL DEFAULT 0
);
