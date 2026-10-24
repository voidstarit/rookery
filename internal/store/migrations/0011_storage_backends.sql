CREATE TABLE storage_backends (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    driver TEXT NOT NULL,
    config TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

ALTER TABLE disks ADD COLUMN storage_backend_id INTEGER REFERENCES storage_backends(id) ON DELETE RESTRICT;
