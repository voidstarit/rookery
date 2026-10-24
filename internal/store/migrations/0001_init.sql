CREATE TABLE vms (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    description TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'stopped',
    vcpus       INTEGER NOT NULL DEFAULT 1 CHECK (vcpus > 0),
    memory_mb   INTEGER NOT NULL DEFAULT 512 CHECK (memory_mb > 0),
    disk_gb     INTEGER NOT NULL DEFAULT 10 CHECK (disk_gb > 0),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
