-- AI agents get real profiles: what kind of agent it is (local: this
-- machine only; remote: other devices; assistant: the built-in AI
-- Assistant), a scope per capability (JSON, '' = from the old read/act
-- scope), an IP allowlist, an expiry, a pause switch (Stop all agents),
-- and a one-time pairing code (only its hash) that turns into a token.
-- Tokens made before this stay usable from wherever they were (remote).
ALTER TABLE agent_tokens ADD COLUMN kind TEXT NOT NULL DEFAULT 'remote';
ALTER TABLE agent_tokens ADD COLUMN scopes TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tokens ADD COLUMN ip_allow TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tokens ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_tokens ADD COLUMN paused INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_tokens ADD COLUMN pair_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tokens ADD COLUMN pair_expires_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX agent_tokens_pair_hash ON agent_tokens (pair_hash) WHERE pair_hash != '';
CREATE INDEX agent_activity_token_id ON agent_activity (token_id, id);
