CREATE TABLE network_pipes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  network_a_id INTEGER NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
  network_b_id INTEGER NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
  instance INTEGER NOT NULL,
  parent_sw_if_index INTEGER NOT NULL DEFAULT 0,
  endpoint_a_sw_if_index INTEGER NOT NULL DEFAULT 0,
  endpoint_b_sw_if_index INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (network_a_id, network_b_id)
);
