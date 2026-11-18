-- Sticky Notes, per person and private to them. body is a small structured
-- document (JSON, see internal/notes), text its plain form for search.
-- Times are unix milliseconds. trashed_at set = in the trash; desk set = stuck
-- on the web desktop at that spot ({x,y,w,h}).
CREATE TABLE notes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title      TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '[]',
    text       TEXT NOT NULL DEFAULT '',
    color      TEXT NOT NULL DEFAULT 'glass',
    pinned     INTEGER NOT NULL DEFAULT 0,
    archived   INTEGER NOT NULL DEFAULT 0,
    trashed_at INTEGER,
    remind_at  INTEGER,
    reminded   INTEGER NOT NULL DEFAULT 0,
    desk       TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    version    INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX notes_user ON notes(user_id, updated_at);
CREATE INDEX notes_remind ON notes(remind_at) WHERE remind_at IS NOT NULL AND reminded = 0;
