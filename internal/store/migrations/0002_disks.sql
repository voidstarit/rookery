CREATE TABLE disks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    size_gb    INTEGER NOT NULL CHECK (size_gb > 0),
    vg         TEXT    NOT NULL,
    thin_pool  TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

ALTER TABLE vms ADD COLUMN disk_id INTEGER REFERENCES disks(id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX idx_vms_disk_id_unique ON vms(disk_id) WHERE disk_id IS NOT NULL;
