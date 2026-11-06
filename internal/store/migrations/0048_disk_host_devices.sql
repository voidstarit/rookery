ALTER TABLE disks ADD COLUMN source TEXT NOT NULL DEFAULT 'lvm-thin';
ALTER TABLE disks ADD COLUMN device TEXT NOT NULL DEFAULT '';
ALTER TABLE disks ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_disks_host_device
    ON disks(device) WHERE source = 'host-device' AND device <> '';
