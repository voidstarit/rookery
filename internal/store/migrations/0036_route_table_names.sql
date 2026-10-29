ALTER TABLE routes ADD COLUMN next_hop_table_name TEXT;
ALTER TABLE routes ADD COLUMN ipv4_lookup_table_name TEXT;
ALTER TABLE routes ADD COLUMN ipv6_lookup_table_name TEXT;

UPDATE routes
SET next_hop_table_name = 'default'
WHERE next_hop_table_set != 0 AND next_hop_table = 0;

UPDATE routes
SET next_hop_table_name = (
  SELECT name FROM route_tables
  WHERE route_tables.table_id = routes.next_hop_table
    AND route_tables.is_ipv6 = routes.is_ipv6
  LIMIT 1
)
WHERE next_hop_table_set != 0 AND next_hop_table != 0;

UPDATE routes
SET ipv4_lookup_table_name = (
  SELECT name FROM route_tables
  WHERE route_tables.table_id = routes.ipv4_lookup_table
    AND route_tables.is_ipv6 = 0
  LIMIT 1
)
WHERE ipv4_lookup_table_set != 0 AND ipv4_lookup_table != 0;

UPDATE routes
SET ipv6_lookup_table_name = (
  SELECT name FROM route_tables
  WHERE route_tables.table_id = routes.ipv6_lookup_table
    AND route_tables.is_ipv6 = 1
  LIMIT 1
)
WHERE ipv6_lookup_table_set != 0 AND ipv6_lookup_table != 0;

UPDATE virtual_interfaces
SET ipv4_table_id = (
      SELECT route_table_id FROM networks
      WHERE networks.id = virtual_interfaces.network_id AND route_table_id != 0
    ),
    ipv6_table_id = (
      SELECT route_table_id FROM networks
      WHERE networks.id = virtual_interfaces.network_id AND route_table_id != 0
    )
WHERE ipv4_table_id = 0 AND ipv6_table_id = 0
  AND EXISTS (
    SELECT 1 FROM networks
    WHERE networks.id = virtual_interfaces.network_id AND route_table_id != 0
  );
