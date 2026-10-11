-- Per-user spaces: who may sign in to the desktop, shared folders with
-- per-person permissions, which App Store apps each person sees, and
-- personal desktop preferences that follow people between browsers.

-- Access type per person (by user name, so it also works for Linux accounts
-- that have never signed in). No row = desktop.
CREATE TABLE user_access (
    username TEXT PRIMARY KEY COLLATE NOCASE,
    access   TEXT NOT NULL CHECK (access IN ('desktop', 'files'))
);

-- Shared folders replace the old network shares. Existing shares carry over
-- with the same ids, shown on the network, with the recycle bin on.
CREATE TABLE shared_folders (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
    root        TEXT NOT NULL,
    path        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    recycle     INTEGER NOT NULL DEFAULT 1,
    network     INTEGER NOT NULL DEFAULT 1,
    read_only   INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);
INSERT INTO shared_folders (id, name, root, path, description, recycle, network, read_only, created_at)
    SELECT id, name, root, path, '', 1, 1, read_only, created_at FROM net_shares;
DROP TABLE net_shares;

-- What each person may do in a shared folder. No row = no access.
CREATE TABLE share_permissions (
    folder_id INTEGER NOT NULL REFERENCES shared_folders(id) ON DELETE CASCADE,
    username  TEXT NOT NULL COLLATE NOCASE,
    perm      TEXT NOT NULL CHECK (perm IN ('rw', 'ro')),
    PRIMARY KEY (folder_id, username)
);
CREATE INDEX idx_share_permissions_user ON share_permissions(username);

-- Who can use an installed App Store app. No mode row = everyone.
CREATE TABLE app_access_mode (
    app_id TEXT PRIMARY KEY,
    mode   TEXT NOT NULL CHECK (mode IN ('everyone', 'selected'))
);
CREATE TABLE app_access (
    app_id   TEXT NOT NULL,
    username TEXT NOT NULL COLLATE NOCASE,
    PRIMARY KEY (app_id, username)
);
CREATE INDEX idx_app_access_user ON app_access(username);

-- Desktop preferences (theme, wallpaper, dock settings, ...) as JSON.
CREATE TABLE user_prefs (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    data       TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
