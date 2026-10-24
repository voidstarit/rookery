ALTER TABLE disks ADD COLUMN is_template INTEGER NOT NULL DEFAULT 0;
ALTER TABLE disks ADD COLUMN template_disk_id INTEGER REFERENCES disks(id) ON DELETE SET NULL;
ALTER TABLE disks ADD COLUMN prepared_by_script INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_disks_template_disk_id ON disks(template_disk_id);
