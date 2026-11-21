-- Linked NoCap servers (Settings → Your Squad). Two servers link after an
-- administrator on one sends a link request and an administrator on the
-- other accepts it. Each side keeps the other's address, the certificate
-- it pinned when the request was made (SHA-256 of the leaf, hex), the token
-- it presents to the other (their_token, sealed with the server's secret
-- box when there is one: sealed = 1) and the hash of the token the other
-- presents to it. Times are unix seconds.
CREATE TABLE server_links (
    nocap_id       TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    url            TEXT NOT NULL,
    fingerprint    TEXT NOT NULL,
    their_token    BLOB NOT NULL,
    sealed         INTEGER NOT NULL DEFAULT 0,
    our_token_hash TEXT NOT NULL UNIQUE,
    version        TEXT NOT NULL DEFAULT '',
    linked_by      TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    last_seen      INTEGER NOT NULL DEFAULT 0
);
