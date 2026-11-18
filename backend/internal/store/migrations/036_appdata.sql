-- App data folder: App Store apps keep their files in folders on the data
-- disk (<App data>/<app>/<volume>) instead of Docker volumes. Apps installed
-- before keep their volumes ('volume') until an admin moves them.
ALTER TABLE apps ADD COLUMN storage TEXT NOT NULL DEFAULT 'volume';

-- Docker volumes whose files were moved into the App data folder. They are
-- kept (as a way back) until an admin cleans them up.
CREATE TABLE appdata_old_volumes (
    volume    TEXT PRIMARY KEY,
    app       TEXT NOT NULL,
    dir       TEXT NOT NULL,
    moved_at  INTEGER NOT NULL
);
