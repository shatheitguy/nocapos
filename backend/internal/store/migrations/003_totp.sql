-- TOTP (authenticator app) 2FA, also used for the forgot-password reset.
-- totp_secret holds the AES-GCM sealed base32 secret and is empty when not enrolled.
ALTER TABLE users ADD COLUMN totp_secret BLOB;
ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;
