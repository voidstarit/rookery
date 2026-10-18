CREATE TABLE vm_disks (
    vm_id TEXT NOT NULL REFERENCES vms(id) ON DELETE CASCADE,
    disk_id INTEGER NOT NULL REFERENCES disks(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (vm_id, disk_id),
    UNIQUE (disk_id)
);

INSERT INTO vm_disks (vm_id, disk_id, position)
SELECT id, disk_id, 0 FROM vms WHERE disk_id IS NOT NULL;
