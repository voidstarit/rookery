-- LightVM instances combine uploaded kernel/initramfs images with a container
-- image, environment, and executable command. Interfaces are attached through
-- lightvm_interfaces so an interface cannot belong to both a VM and a LightVM.
CREATE TABLE lightvms (
    id              TEXT    PRIMARY KEY,
    name            TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    description     TEXT    NOT NULL DEFAULT '',
    vcpus           INTEGER NOT NULL,
    memory_mb       INTEGER NOT NULL,
    console_enabled INTEGER NOT NULL DEFAULT 0,
    cgroup_class    TEXT    NOT NULL DEFAULT '',
    kernel_id       INTEGER NOT NULL REFERENCES lightvm_kernels(id) ON DELETE RESTRICT,
    initramfs_id    INTEGER NOT NULL REFERENCES lightvm_initramfs(id) ON DELETE RESTRICT,
    image           TEXT    NOT NULL,
    env             TEXT    NOT NULL DEFAULT '[]',
    executable_path TEXT    NOT NULL,
    executable_args TEXT    NOT NULL DEFAULT '[]',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

CREATE INDEX idx_lightvms_kernel ON lightvms(kernel_id);
CREATE INDEX idx_lightvms_initramfs ON lightvms(initramfs_id);

CREATE TABLE lightvm_interfaces (
    lightvm_id   TEXT    NOT NULL REFERENCES lightvms(id) ON DELETE CASCADE,
    interface_id INTEGER NOT NULL REFERENCES virtual_interfaces(id) ON DELETE RESTRICT,
    position     INTEGER NOT NULL,
    PRIMARY KEY (lightvm_id, interface_id)
);

CREATE UNIQUE INDEX idx_lightvm_interfaces_interface ON lightvm_interfaces(interface_id);
CREATE INDEX idx_lightvm_interfaces_lightvm ON lightvm_interfaces(lightvm_id, position);

CREATE TRIGGER entity_labels_lightvm_delete AFTER DELETE ON lightvms BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'lightvm' AND entity_id = OLD.id;
END;
