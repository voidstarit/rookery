-- Unify VM and LightVM disk attachments under one owner table.
CREATE TABLE workload_disks (
    vm_id      TEXT REFERENCES vms(id) ON DELETE CASCADE,
    lightvm_id TEXT REFERENCES lightvms(id) ON DELETE CASCADE,
    disk_id    INTEGER NOT NULL REFERENCES disks(id) ON DELETE RESTRICT,
    position   INTEGER NOT NULL CHECK (position >= 0),
    CHECK ((vm_id IS NOT NULL) <> (lightvm_id IS NOT NULL)),
    UNIQUE (disk_id),
    UNIQUE (vm_id, position),
    UNIQUE (lightvm_id, position)
);

INSERT INTO workload_disks (vm_id, lightvm_id, disk_id, position)
SELECT vm_id, NULL, disk_id,
       ROW_NUMBER() OVER (PARTITION BY vm_id ORDER BY position, disk_id) - 1
FROM vm_disks;

INSERT INTO workload_disks (vm_id, lightvm_id, disk_id, position)
SELECT v.id, NULL, v.disk_id,
       COALESCE((SELECT MAX(position) + 1
                 FROM workload_disks wd WHERE wd.vm_id = v.id), 0)
FROM vms v
WHERE v.disk_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM disks d WHERE d.id = v.disk_id)
  AND NOT EXISTS (SELECT 1 FROM vm_disks vd WHERE vd.vm_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM workload_disks wd
                  WHERE wd.disk_id = v.disk_id);

ALTER TABLE lightvms ADD COLUMN lifecycle_generation INTEGER NOT NULL DEFAULT 0;

UPDATE vms SET disk_id = NULL;
DROP TABLE vm_disks;
