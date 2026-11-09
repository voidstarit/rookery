-- Reusable QEMU disk throttling classes. The raw read_bps/write_bps columns
-- remain until the pre-release drop migration so development phases stay
-- buildable.
CREATE TABLE disk_throttle_classes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    description TEXT    NOT NULL DEFAULT '',
    limits      TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

ALTER TABLE disks ADD COLUMN throttle_class_id INTEGER
    REFERENCES disk_throttle_classes(id) ON DELETE RESTRICT;

CREATE INDEX idx_disks_throttle_class_id ON disks(throttle_class_id);
