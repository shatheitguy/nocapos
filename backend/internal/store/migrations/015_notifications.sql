-- System notifications for admins (Notification Center). key is a stable id
-- per event so the same update, crash or alert is never stored twice; deleted
-- ones are only hidden so a later check doesn't bring them back.
CREATE TABLE notifications (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    key         TEXT NOT NULL UNIQUE,
    kind        TEXT NOT NULL,
    level       TEXT NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    action_json TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    read_at     INTEGER,
    hidden      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX notifications_hidden ON notifications (hidden, id);
