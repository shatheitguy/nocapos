-- Photos: facts read from each picture (cached until the file changes),
-- plus each user's favorites and albums. Photos are addressed the same way
-- as in Files: storage location id + slash path ("/Photos/2026/a.jpg").
CREATE TABLE photo_meta (
    root     TEXT NOT NULL,
    path     TEXT NOT NULL,
    size     INTEGER NOT NULL,
    mtime    INTEGER NOT NULL,
    taken    INTEGER NOT NULL, -- unix seconds; the file time when the photo has no date
    width    INTEGER NOT NULL DEFAULT 0,
    height   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (root, path)
);

CREATE TABLE photo_favorites (
    user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    root     TEXT NOT NULL,
    path     TEXT NOT NULL,
    PRIMARY KEY (user_id, root, path)
);

CREATE TABLE photo_albums (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE photo_album_items (
    album_id INTEGER NOT NULL REFERENCES photo_albums(id) ON DELETE CASCADE,
    root     TEXT NOT NULL,
    path     TEXT NOT NULL,
    added_at INTEGER NOT NULL,
    PRIMARY KEY (album_id, root, path)
);
