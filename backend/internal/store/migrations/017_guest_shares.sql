-- Guest access to a shared folder: anyone on the network, no password.
-- '' = off, 'ro' = read only, 'rw' = read and write.
ALTER TABLE shared_folders ADD COLUMN guest TEXT NOT NULL DEFAULT '' CHECK (guest IN ('', 'ro', 'rw'));
