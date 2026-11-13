-- Groups of people (Settings → Users & Groups): a name, a short note, a
-- color, and who is in it. Members are kept by user name so Linux accounts
-- that never signed in to NoCapOS can be members too.
CREATE TABLE user_groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL COLLATE NOCASE UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    color       TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);

CREATE TABLE user_group_members (
    group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    username TEXT NOT NULL COLLATE NOCASE,
    PRIMARY KEY (group_id, username)
);
CREATE INDEX user_group_members_username ON user_group_members (username);
