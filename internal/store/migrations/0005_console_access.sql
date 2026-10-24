ALTER TABLE vms ADD COLUMN console_enabled INTEGER NOT NULL DEFAULT 0;

-- Preserve console access for VMs that existed before this setting was added.
UPDATE vms SET console_enabled = 1;
