CREATE TABLE networks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    bridge_domain_id INTEGER NOT NULL UNIQUE,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE TABLE virtual_interfaces (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    network_id     INTEGER NOT NULL REFERENCES networks(id) ON DELETE RESTRICT,
    socket_path    TEXT    NOT NULL UNIQUE,
    mac            TEXT    NOT NULL,
    sw_if_index    INTEGER NOT NULL CHECK (sw_if_index >= 0),
    attached_vm_id TEXT    REFERENCES vms(id) ON DELETE RESTRICT,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE INDEX idx_virtual_interfaces_network ON virtual_interfaces(network_id);
CREATE INDEX idx_virtual_interfaces_vm ON virtual_interfaces(attached_vm_id);
