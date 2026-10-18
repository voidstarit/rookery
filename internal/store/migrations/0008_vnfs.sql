CREATE TABLE vnfs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    network_id INTEGER NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 0,
    config TEXT NOT NULL DEFAULT '{}',
    tap_sw_if_index INTEGER NOT NULL DEFAULT 0,
    tap_host_name TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX vnfs_network_id_idx ON vnfs(network_id);

-- Preserve existing per-network DHCP configuration as a DHCPv4 VNF. The old
-- tap fields are deliberately not copied: the VNF owns a new tap and the
-- legacy ndn<network-id> tap is removed when the VNF is restarted.
INSERT INTO vnfs
    (name, network_id, type, enabled, config, tap_sw_if_index, tap_host_name, created_at, updated_at)
SELECT
    'dhcpv4-' || id,
    id,
    'dhcpv4',
    dhcp_enabled,
    json_object(
        'gateway', dhcp_gateway,
        'range_start', dhcp_range_start,
        'range_end', dhcp_range_end,
        'dns', dhcp_dns,
        'lease_time', '12h'
    ),
    0,
    '',
    created_at,
    updated_at
FROM networks
WHERE dhcp_gateway <> '';
