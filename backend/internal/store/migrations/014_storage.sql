-- Pools and disks set up in the Storage app that are offered as Files
-- locations (id "pool:<name>" or "disk:<label>"), restored at startup.
CREATE TABLE storage_locations (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('pool', 'disk')),
    target     TEXT NOT NULL,
    name       TEXT NOT NULL,
    path       TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
