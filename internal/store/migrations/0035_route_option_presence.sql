ALTER TABLE routes ADD COLUMN preference_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE routes ADD COLUMN route_count_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE routes ADD COLUMN next_hop_table_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE routes ADD COLUMN weight_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE routes ADD COLUMN udp_encap_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE routes ADD COLUMN ipv4_lookup_table_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE routes ADD COLUMN ipv6_lookup_table_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE routes ADD COLUMN mpls_lookup_table_set INTEGER NOT NULL DEFAULT 0;

UPDATE routes SET preference_set = CASE WHEN preference != 0 THEN 1 ELSE 0 END;
UPDATE routes SET route_count_set = CASE WHEN route_count != 1 THEN 1 ELSE 0 END;
UPDATE routes SET next_hop_table_set = CASE WHEN next_hop_table != 0 THEN 1 ELSE 0 END;
UPDATE routes SET weight_set = CASE WHEN weight != 0 THEN 1 ELSE 0 END;
UPDATE routes SET udp_encap_set = CASE WHEN udp_encap != 0 THEN 1 ELSE 0 END;
UPDATE routes SET ipv4_lookup_table_set = CASE WHEN ipv4_lookup_table != 0 THEN 1 ELSE 0 END;
UPDATE routes SET ipv6_lookup_table_set = CASE WHEN ipv6_lookup_table != 0 THEN 1 ELSE 0 END;
UPDATE routes SET mpls_lookup_table_set = CASE WHEN mpls_lookup_table != 0 THEN 1 ELSE 0 END;
