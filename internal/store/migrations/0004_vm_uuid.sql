CREATE TABLE vms_new (
    id            TEXT    PRIMARY KEY,
    name          TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    description   TEXT    NOT NULL DEFAULT '',
    status        TEXT    NOT NULL DEFAULT 'stopped',
    vcpus         INTEGER NOT NULL DEFAULT 1 CHECK (vcpus > 0),
    memory_mb     INTEGER NOT NULL DEFAULT 512 CHECK (memory_mb > 0),
    disk_id       INTEGER UNIQUE REFERENCES disks(id) ON DELETE RESTRICT,
    boot_media_id INTEGER REFERENCES media(id) ON DELETE RESTRICT,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

INSERT INTO vms_new (id, name, description, status, vcpus, memory_mb, disk_id, boot_media_id, created_at, updated_at)
SELECT
    lower(hex(randomblob(4))) || '-' ||
    lower(hex(randomblob(2))) || '-4' ||
    substr(lower(hex(randomblob(2))), 2) || '-' ||
    substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' ||
    lower(hex(randomblob(6))),
    name, description, status, vcpus, memory_mb, disk_id, boot_media_id, created_at, updated_at
FROM vms;

DROP TABLE vms;
ALTER TABLE vms_new RENAME TO vms;
