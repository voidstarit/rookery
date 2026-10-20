CREATE TABLE routes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    table_id INTEGER NOT NULL,
    is_ipv6 INTEGER NOT NULL DEFAULT 0,
    prefix TEXT NOT NULL,
    next_hop TEXT NOT NULL,
    interface_id INTEGER REFERENCES virtual_interfaces(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (table_id, is_ipv6, prefix)
);

CREATE INDEX IF NOT EXISTS idx_routes_table ON routes(table_id, is_ipv6);
