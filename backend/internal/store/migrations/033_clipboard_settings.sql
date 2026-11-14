-- NoCap Link clipboard, per person: keep a short history (off = only the
-- last item), and whether NoCap Desktop's clipboard joins in.
CREATE TABLE clipboard_settings (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    history INTEGER NOT NULL DEFAULT 0,
    desktop INTEGER NOT NULL DEFAULT 1
);
