-- Read-only disks may be attached to multiple workloads. The attachment keeps
-- a snapshot of the disk flag so the single-writer rule can be enforced with a
-- partial unique index instead of UNIQUE (disk_id).
ALTER TABLE disks ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0;

CREATE TABLE workload_disks_new (
    vm_id      TEXT REFERENCES vms(id) ON DELETE CASCADE,
    lightvm_id TEXT REFERENCES lightvms(id) ON DELETE CASCADE,
    disk_id    INTEGER NOT NULL REFERENCES disks(id) ON DELETE RESTRICT,
    position   INTEGER NOT NULL CHECK (position >= 0),
    serial     TEXT NOT NULL DEFAULT '',
    read_only  INTEGER NOT NULL DEFAULT 0,
    CHECK ((vm_id IS NOT NULL) <> (lightvm_id IS NOT NULL)),
    UNIQUE (vm_id, position),
    UNIQUE (lightvm_id, position)
);

INSERT INTO workload_disks_new (vm_id, lightvm_id, disk_id, position, serial, read_only)
SELECT vm_id, lightvm_id, disk_id, position, serial, 0 FROM workload_disks;

DROP TABLE workload_disks;
ALTER TABLE workload_disks_new RENAME TO workload_disks;

-- Only read-write attachments compete for a disk. Read-only attachments may
-- coexist with any number of owners.
CREATE UNIQUE INDEX idx_workload_disks_single_writer
    ON workload_disks(disk_id) WHERE read_only = 0;
CREATE INDEX idx_workload_disks_disk ON workload_disks(disk_id);

-- The attachment snapshot must match the disk flag. Application inserts read
-- the flag from disks in the same statement; this trigger protects databases
-- written by other tools.
CREATE TRIGGER workload_disks_read_only_matches_disk
BEFORE INSERT ON workload_disks
WHEN NEW.read_only <> (SELECT read_only FROM disks WHERE id = NEW.disk_id)
BEGIN
    SELECT RAISE(ABORT, 'workload disk read_only does not match disk');
END;

-- Backstop for a demotion that bypasses the store: never allow a disk with
-- multiple owners to become read-write.
CREATE TRIGGER disks_read_only_single_writer
BEFORE UPDATE OF read_only ON disks
WHEN NEW.read_only = 0 AND OLD.read_only = 1
  AND (SELECT COUNT(*) FROM workload_disks WHERE disk_id = NEW.id) > 1
BEGIN
    SELECT RAISE(ABORT, 'disk is attached to multiple workloads');
END;
