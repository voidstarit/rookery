CREATE TABLE route_tables (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    table_id INTEGER NOT NULL,
    is_ipv6 INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (table_id, is_ipv6),
    UNIQUE (name, is_ipv6)
);

ALTER TABLE virtual_interfaces ADD COLUMN ipv4_table_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE virtual_interfaces ADD COLUMN ipv6_table_id INTEGER NOT NULL DEFAULT 0;
