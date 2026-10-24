CREATE TABLE IF NOT EXISTS host_interfaces (
    nic TEXT PRIMARY KEY,
    vpp_name TEXT NOT NULL,
    mac TEXT NOT NULL DEFAULT '',
    sw_if_index INTEGER NOT NULL DEFAULT 0,
    purpose TEXT NOT NULL DEFAULT 'manual',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
