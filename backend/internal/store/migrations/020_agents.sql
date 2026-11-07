-- NoCap MCP: access tokens for AI agents (only the SHA-256 of each token is
-- kept; revoking sets revoked_at) and what each agent did.
CREATE TABLE agent_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    scope        TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    tools        TEXT NOT NULL DEFAULT '',
    always_allow TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL DEFAULT 0,
    revoked_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX agent_tokens_user_id ON agent_tokens (user_id);

CREATE TABLE agent_activity (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    token_id   TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    agent      TEXT NOT NULL,
    tool       TEXT NOT NULL,
    summary    TEXT NOT NULL,
    outcome    TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX agent_activity_user_id ON agent_activity (user_id, id);
