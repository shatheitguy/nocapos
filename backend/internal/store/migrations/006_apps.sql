-- App Store: apps installed from the catalog (their containers carry the
-- label nocapos.app = id). secrets holds generated passwords/keys as JSON.
CREATE TABLE apps (
    id           TEXT PRIMARY KEY,
    version      TEXT NOT NULL,
    web_port     INTEGER NOT NULL DEFAULT 0,
    secrets      TEXT NOT NULL DEFAULT '{}',
    installed_at INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
