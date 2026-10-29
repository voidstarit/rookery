CREATE TABLE vpp_interface_route_tables (
  interface_name TEXT PRIMARY KEY,
  ipv4_table_id INTEGER NOT NULL DEFAULT 0,
  ipv6_table_id INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
