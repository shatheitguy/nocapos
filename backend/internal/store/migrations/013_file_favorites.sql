-- Files sidebar favorites, per user, as a JSON list of {root, path}.
-- No row means the user never changed them (the defaults are shown).
CREATE TABLE file_favorites (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    favorites  TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
