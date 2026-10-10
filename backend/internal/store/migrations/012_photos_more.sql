-- Photos: video length, album covers and a "Recently deleted" list.
ALTER TABLE photo_meta ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
-- Videos are read again once to learn their length.
DELETE FROM photo_meta WHERE lower(path) LIKE '%.mp4' OR lower(path) LIKE '%.mov' OR lower(path) LIKE '%.m4v';

ALTER TABLE photo_albums ADD COLUMN cover_root TEXT NOT NULL DEFAULT '';
ALTER TABLE photo_albums ADD COLUMN cover_path TEXT NOT NULL DEFAULT '';

-- Photos deleted from the Photos app sit in the recycle bin of their drive;
-- this remembers where they came from so they can be put back.
CREATE TABLE photo_trash (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    root        TEXT NOT NULL,
    trash_path  TEXT NOT NULL, -- "/.recycle/20261010-120000_a.jpg"
    orig_path   TEXT NOT NULL, -- "/Photos/2026/a.jpg"
    size        INTEGER NOT NULL DEFAULT 0,
    taken       INTEGER NOT NULL DEFAULT 0,
    width       INTEGER NOT NULL DEFAULT 0,
    height      INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    deleted_at  INTEGER NOT NULL,
    UNIQUE (root, trash_path)
);
