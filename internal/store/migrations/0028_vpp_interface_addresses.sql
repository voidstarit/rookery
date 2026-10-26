CREATE TABLE vpp_interface_addresses (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  interface_name TEXT NOT NULL,
  address TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE (interface_name, address)
);

CREATE INDEX idx_vpp_interface_addresses_name
  ON vpp_interface_addresses (interface_name);

CREATE TABLE IF NOT EXISTS vpp_interface_dhcp_clients (
  interface_name TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
);
