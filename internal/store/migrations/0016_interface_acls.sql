CREATE TABLE interface_acls (
    interface_id INTEGER NOT NULL REFERENCES virtual_interfaces(id) ON DELETE CASCADE,
    direction TEXT NOT NULL CHECK (direction IN ('ingress', 'egress')),
    acl_id INTEGER NOT NULL REFERENCES acls(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    PRIMARY KEY (interface_id, direction, position)
);

CREATE INDEX IF NOT EXISTS idx_interface_acls_acl_id ON interface_acls(acl_id);

INSERT INTO interface_acls (interface_id, direction, acl_id, position)
SELECT id, 'ingress', ingress_acl_id, 0
FROM virtual_interfaces
WHERE ingress_acl_id IS NOT NULL;

INSERT INTO interface_acls (interface_id, direction, acl_id, position)
SELECT id, 'egress', egress_acl_id, 0
FROM virtual_interfaces
WHERE egress_acl_id IS NOT NULL;
