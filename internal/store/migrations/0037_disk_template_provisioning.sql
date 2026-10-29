ALTER TABLE disks ADD COLUMN provisioning_script TEXT NOT NULL DEFAULT '';
ALTER TABLE disks ADD COLUMN provisioning_timeout TEXT NOT NULL DEFAULT '';
