CREATE TABLE entity_labels (
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    label TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (entity_type, entity_id, label)
);

CREATE INDEX entity_labels_type_idx ON entity_labels (entity_type);
CREATE INDEX entity_labels_label_idx ON entity_labels (label);

-- Remove labels when their entity is deleted. Entity ids are stored as text;
-- SQLite applies TEXT affinity in these comparisons.
CREATE TRIGGER entity_labels_vm_delete AFTER DELETE ON vms BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'vm' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_disk_delete AFTER DELETE ON disks BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'disk' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_media_delete AFTER DELETE ON media BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'media' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_network_delete AFTER DELETE ON networks BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'network' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_interface_delete AFTER DELETE ON virtual_interfaces BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'interface' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_vnf_delete AFTER DELETE ON vnfs BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'vnf' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_acl_delete AFTER DELETE ON acls BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'acl' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_route_table_delete AFTER DELETE ON route_tables BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'route_table' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_route_delete AFTER DELETE ON routes BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'route' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_network_pipe_delete AFTER DELETE ON network_pipes BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'network_pipe' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_storage_backend_delete AFTER DELETE ON storage_backends BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'storage_backend' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_host_tap_delete AFTER DELETE ON network_host_taps BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'host_tap' AND entity_id = OLD.id;
END;
CREATE TRIGGER entity_labels_host_interface_delete AFTER DELETE ON host_interfaces BEGIN
    DELETE FROM entity_labels WHERE entity_type = 'host_interface' AND entity_id = OLD.nic;
END;
