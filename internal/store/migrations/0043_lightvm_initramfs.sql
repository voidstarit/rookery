-- LightVM initramfs images are uploaded files managed by Rookery. The path
-- points at the stored copy; managed is false for records registered from an
-- existing host path, in which case deleting the record leaves the file alone.
CREATE TABLE lightvm_initramfs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    description TEXT    NOT NULL DEFAULT '',
    path        TEXT    NOT NULL,
    file_name   TEXT    NOT NULL DEFAULT '',
    size_bytes  INTEGER NOT NULL CHECK (size_bytes >= 0),
    managed     INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TRIGGER entity_labels_lightvm_initramfs_delete AFTER DELETE ON lightvm_initramfs BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'lightvm_initramfs' AND entity_id = OLD.id;
END;
