-- What shape a linked device is ("phone", "tablet", "computer"; "" until
-- it says), so its screen is shown in a matching frame.
ALTER TABLE devices ADD COLUMN form TEXT NOT NULL DEFAULT '';
