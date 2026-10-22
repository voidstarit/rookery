CREATE TABLE IF NOT EXISTS network_host_taps (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    network_id   INTEGER NOT NULL REFERENCES networks(id) ON DELETE RESTRICT,
    name         TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    sw_if_index  INTEGER NOT NULL DEFAULT 0 CHECK (sw_if_index >= 0),
    ipv4_address TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_network_host_taps_network ON network_host_taps(network_id);
