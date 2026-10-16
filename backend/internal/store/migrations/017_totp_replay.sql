-- The last authenticator time step accepted for each person, so a code
-- that was already used (seen over a shoulder, replayed from a log) can't
-- be used again.
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;
