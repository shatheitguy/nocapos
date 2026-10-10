-- Changes people made to App Store apps' containers (ports, storage,
-- networks, environment, limits) as a container spec in JSON, re-applied
-- whenever the App Store creates the container again (updates, repairs).
CREATE TABLE app_overrides (
    app_id     TEXT NOT NULL,
    service    TEXT NOT NULL,
    spec       TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (app_id, service)
);
