ALTER TABLE networks ADD COLUMN vnf_transport TEXT NOT NULL DEFAULT 'shared';
ALTER TABLE networks ADD COLUMN vnf_memif_socket TEXT NOT NULL DEFAULT '';
ALTER TABLE networks ADD COLUMN vnf_memif_sw_if_index INTEGER NOT NULL DEFAULT 0;
ALTER TABLE networks ADD COLUMN vnf_memif_host_name TEXT NOT NULL DEFAULT '';
