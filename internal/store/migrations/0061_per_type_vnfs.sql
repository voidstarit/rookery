-- Dedicated per-type VNF storage. The vnfs table keeps the shared identity
-- (name, network, type, enabled flag, tap/memif metadata, timestamps, labels);
-- every type-specific field moves to a typed parent table and child rows get
-- their own tables with database-enforced constraints and unique keys.
--
-- The move and backfill are a single SQL transaction: the migration wrapper
-- commits the schema, the data, and PRAGMA user_version together. store.Open
-- runs a read-only preflight before this file is applied and refuses to migrate
-- data that would violate the new constraints.

CREATE TABLE dns_vnfs (
    vnf_id  INTEGER PRIMARY KEY REFERENCES vnfs(id) ON DELETE CASCADE,
    forward INTEGER NOT NULL DEFAULT 0 CHECK (forward IN (0, 1))
);

CREATE TABLE dns_records (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    vnf_id INTEGER NOT NULL REFERENCES dns_vnfs(vnf_id) ON DELETE CASCADE,
    name   TEXT NOT NULL,
    type   TEXT NOT NULL CHECK (type IN ('A', 'AAAA', 'CNAME', 'TXT', 'PTR')),
    value  TEXT NOT NULL,
    ttl    INTEGER NOT NULL DEFAULT 300 CHECK (ttl BETWEEN 1 AND 86400),
    UNIQUE (vnf_id, name, type, value)
);

CREATE TABLE dns_forwarders (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    vnf_id   INTEGER NOT NULL REFERENCES dns_vnfs(vnf_id) ON DELETE CASCADE,
    address  TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    UNIQUE (vnf_id, address)
);

CREATE TABLE dhcpv4_vnfs (
    vnf_id      INTEGER PRIMARY KEY REFERENCES vnfs(id) ON DELETE CASCADE,
    gateway     TEXT NOT NULL,
    range_start TEXT NOT NULL,
    range_end   TEXT NOT NULL,
    lease_time  TEXT NOT NULL DEFAULT '12h',
    dns         TEXT NOT NULL DEFAULT '',
    bind_ip     TEXT NOT NULL DEFAULT '',
    transport   TEXT NOT NULL DEFAULT '' CHECK (transport IN ('', 'shared', 'dedicated', 'tap')),
    dns_vnf_id  INTEGER REFERENCES dns_vnfs(vnf_id) ON DELETE SET NULL,
    dns_domain  TEXT NOT NULL DEFAULT '',
    dns_ttl     INTEGER NOT NULL DEFAULT 300 CHECK (dns_ttl BETWEEN 0 AND 86400)
);

CREATE TABLE dhcpv4_static_assignments (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    vnf_id INTEGER NOT NULL REFERENCES dhcpv4_vnfs(vnf_id) ON DELETE CASCADE,
    mac    TEXT NOT NULL COLLATE NOCASE,
    ip     TEXT NOT NULL,
    UNIQUE (vnf_id, mac),
    UNIQUE (vnf_id, ip)
);

CREATE INDEX dhcpv4_static_assignments_vnf_id_idx ON dhcpv4_static_assignments(vnf_id);

CREATE TABLE dhcpv4_options (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    vnf_id INTEGER NOT NULL REFERENCES dhcpv4_vnfs(vnf_id) ON DELETE CASCADE,
    code   INTEGER NOT NULL CHECK (code BETWEEN 1 AND 254),
    format TEXT NOT NULL DEFAULT 'string',
    value  TEXT NOT NULL DEFAULT '',
    UNIQUE (vnf_id, code)
);

CREATE TABLE dhcpv6_vnfs (
    vnf_id      INTEGER PRIMARY KEY REFERENCES vnfs(id) ON DELETE CASCADE,
    gateway     TEXT NOT NULL,
    range_start TEXT NOT NULL,
    range_end   TEXT NOT NULL,
    lease_time  TEXT NOT NULL DEFAULT '12h',
    dns         TEXT NOT NULL DEFAULT '',
    bind_ip     TEXT NOT NULL DEFAULT '',
    transport   TEXT NOT NULL DEFAULT 'tap' CHECK (transport IN ('', 'tap')),
    dns_vnf_id  INTEGER REFERENCES dns_vnfs(vnf_id) ON DELETE SET NULL,
    dns_domain  TEXT NOT NULL DEFAULT '',
    dns_ttl     INTEGER NOT NULL DEFAULT 300 CHECK (dns_ttl BETWEEN 0 AND 86400)
);

CREATE TABLE dhcpv6_options (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    vnf_id INTEGER NOT NULL REFERENCES dhcpv6_vnfs(vnf_id) ON DELETE CASCADE,
    code   INTEGER NOT NULL CHECK (code BETWEEN 1 AND 65535),
    format TEXT NOT NULL DEFAULT 'string',
    value  TEXT NOT NULL DEFAULT '',
    UNIQUE (vnf_id, code)
);

CREATE TABLE nat44_vnfs (
    vnf_id            INTEGER PRIMARY KEY REFERENCES vnfs(id) ON DELETE CASCADE,
    gateway           TEXT NOT NULL,
    outside_interface TEXT NOT NULL,
    outside_address   TEXT NOT NULL DEFAULT '',
    outside_gateway   TEXT NOT NULL DEFAULT '',
    nat44_pool        TEXT NOT NULL DEFAULT ''
);

CREATE TABLE nat44_dnat_rules (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    vnf_id        INTEGER NOT NULL REFERENCES nat44_vnfs(vnf_id) ON DELETE CASCADE,
    protocol      TEXT NOT NULL CHECK (protocol IN ('tcp', 'udp')),
    external_port INTEGER NOT NULL CHECK (external_port BETWEEN 1 AND 65535),
    local_ip      TEXT NOT NULL,
    local_port    INTEGER NOT NULL CHECK (local_port BETWEEN 1 AND 65535),
    UNIQUE (vnf_id, protocol, external_port)
);

CREATE TABLE bgp_vnfs (
    vnf_id          INTEGER PRIMARY KEY REFERENCES vnfs(id) ON DELETE CASCADE,
    transport       TEXT NOT NULL DEFAULT 'memif' CHECK (transport IN ('memif', 'tap')),
    local_as        INTEGER NOT NULL CHECK (local_as BETWEEN 1 AND 4294967295),
    router_id       TEXT NOT NULL,
    peer_as         INTEGER NOT NULL CHECK (peer_as BETWEEN 1 AND 4294967295),
    peer_ip         TEXT NOT NULL,
    local_ip        TEXT NOT NULL,
    peer_gateway    TEXT NOT NULL DEFAULT '',
    multihop_ttl    INTEGER NOT NULL DEFAULT 64 CHECK (multihop_ttl BETWEEN 1 AND 255),
    password        TEXT NOT NULL DEFAULT '',
    passive         INTEGER NOT NULL DEFAULT 0 CHECK (passive IN (0, 1)),
    target_table_id INTEGER NOT NULL DEFAULT 0,
    next_hop        TEXT NOT NULL DEFAULT ''
);

CREATE TABLE bgp_advertised_prefixes (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    vnf_id INTEGER NOT NULL REFERENCES bgp_vnfs(vnf_id) ON DELETE CASCADE,
    prefix TEXT NOT NULL,
    UNIQUE (vnf_id, prefix)
);

-- One VNF of each type per network is a storage rule now, not only an API
-- check. The preflight rejects an existing duplicate before this index is
-- created.
CREATE UNIQUE INDEX vnfs_network_type_idx ON vnfs(network_id, type);

-- A parent row may only exist under an identity of its own type. The app never
-- changes a VNF type; this keeps a direct SQL write from creating a DNS row for
-- a DHCP VNF or changing a type after the parent row exists.
CREATE TRIGGER vnfs_type_insert
BEFORE INSERT ON vnfs
FOR EACH ROW
WHEN NEW.type NOT IN ('dhcpv4', 'dhcpv6', 'dns', 'nat44', 'bgp')
BEGIN
    SELECT RAISE(ABORT, 'unsupported vnf type');
END;

CREATE TRIGGER vnfs_type_update
BEFORE UPDATE OF type ON vnfs
FOR EACH ROW
WHEN NEW.type IS NOT OLD.type
BEGIN
    SELECT RAISE(ABORT, 'vnf type cannot change');
END;

CREATE TRIGGER dns_vnfs_type_insert
BEFORE INSERT ON dns_vnfs
FOR EACH ROW
WHEN (SELECT type FROM vnfs WHERE id = NEW.vnf_id) IS NOT 'dns'
BEGIN
    SELECT RAISE(ABORT, 'dns_vnfs.vnf_id must reference a dns VNF');
END;

CREATE TRIGGER dhcpv4_vnfs_type_insert
BEFORE INSERT ON dhcpv4_vnfs
FOR EACH ROW
WHEN (SELECT type FROM vnfs WHERE id = NEW.vnf_id) IS NOT 'dhcpv4'
BEGIN
    SELECT RAISE(ABORT, 'dhcpv4_vnfs.vnf_id must reference a dhcpv4 VNF');
END;

CREATE TRIGGER dhcpv6_vnfs_type_insert
BEFORE INSERT ON dhcpv6_vnfs
FOR EACH ROW
WHEN (SELECT type FROM vnfs WHERE id = NEW.vnf_id) IS NOT 'dhcpv6'
BEGIN
    SELECT RAISE(ABORT, 'dhcpv6_vnfs.vnf_id must reference a dhcpv6 VNF');
END;

CREATE TRIGGER nat44_vnfs_type_insert
BEFORE INSERT ON nat44_vnfs
FOR EACH ROW
WHEN (SELECT type FROM vnfs WHERE id = NEW.vnf_id) IS NOT 'nat44'
BEGIN
    SELECT RAISE(ABORT, 'nat44_vnfs.vnf_id must reference a nat44 VNF');
END;

CREATE TRIGGER bgp_vnfs_type_insert
BEFORE INSERT ON bgp_vnfs
FOR EACH ROW
WHEN (SELECT type FROM vnfs WHERE id = NEW.vnf_id) IS NOT 'bgp'
BEGIN
    SELECT RAISE(ABORT, 'bgp_vnfs.vnf_id must reference a bgp VNF');
END;

-- Backfill parent scalar configuration. DNS parents are inserted first so the
-- DHCP dns_vnf_id foreign keys can resolve. A zero dns_vnf_id becomes NULL.
INSERT INTO dns_vnfs (vnf_id, forward)
SELECT f.id,
       COALESCE(json_extract(f.config, '$.dns_forward'), 0)
FROM vnfs AS f
WHERE f.type = 'dns';

INSERT INTO dhcpv4_vnfs
    (vnf_id, gateway, range_start, range_end, lease_time, dns, bind_ip,
     transport, dns_vnf_id, dns_domain, dns_ttl)
SELECT f.id,
       COALESCE(json_extract(f.config, '$.gateway'), ''),
       COALESCE(json_extract(f.config, '$.range_start'), ''),
       COALESCE(json_extract(f.config, '$.range_end'), ''),
       COALESCE(NULLIF(json_extract(f.config, '$.lease_time'), ''), '12h'),
       COALESCE(json_extract(f.config, '$.dns'), ''),
       COALESCE(json_extract(f.config, '$.bind_ip'), ''),
       CASE trim(json_extract(f.config, '$.transport'))
           WHEN 'memif' THEN 'dedicated'
           WHEN 'shared' THEN 'shared'
           WHEN 'dedicated' THEN 'dedicated'
           WHEN 'tap' THEN 'tap'
           ELSE ''
       END,
       NULLIF(json_extract(f.config, '$.dns_vnf_id'), 0),
       COALESCE(json_extract(f.config, '$.dns_domain'), ''),
       COALESCE(json_extract(f.config, '$.dns_ttl'), 300)
FROM vnfs AS f
WHERE f.type = 'dhcpv4';

INSERT INTO dhcpv6_vnfs
    (vnf_id, gateway, range_start, range_end, lease_time, dns, bind_ip,
     transport, dns_vnf_id, dns_domain, dns_ttl)
SELECT f.id,
       COALESCE(json_extract(f.config, '$.gateway'), ''),
       COALESCE(json_extract(f.config, '$.range_start'), ''),
       COALESCE(json_extract(f.config, '$.range_end'), ''),
       COALESCE(NULLIF(json_extract(f.config, '$.lease_time'), ''), '12h'),
       COALESCE(json_extract(f.config, '$.dns'), ''),
       COALESCE(json_extract(f.config, '$.bind_ip'), ''),
       'tap',
       NULLIF(json_extract(f.config, '$.dns_vnf_id'), 0),
       COALESCE(json_extract(f.config, '$.dns_domain'), ''),
       COALESCE(json_extract(f.config, '$.dns_ttl'), 300)
FROM vnfs AS f
WHERE f.type = 'dhcpv6';

INSERT INTO nat44_vnfs
    (vnf_id, gateway, outside_interface, outside_address, outside_gateway, nat44_pool)
SELECT f.id,
       COALESCE(json_extract(f.config, '$.gateway'), ''),
       COALESCE(json_extract(f.config, '$.outside_interface'), ''),
       COALESCE(json_extract(f.config, '$.outside_address'), ''),
       COALESCE(json_extract(f.config, '$.outside_gateway'), ''),
       COALESCE(json_extract(f.config, '$.nat44_pool'), '')
FROM vnfs AS f
WHERE f.type = 'nat44';

INSERT INTO bgp_vnfs
    (vnf_id, transport, local_as, router_id, peer_as, peer_ip, local_ip,
     peer_gateway, multihop_ttl, password, passive, target_table_id, next_hop)
SELECT f.id,
       COALESCE(NULLIF(trim(json_extract(f.config, '$.transport')), ''), 'memif'),
       COALESCE(json_extract(f.config, '$.local_as'), 0),
       COALESCE(json_extract(f.config, '$.router_id'), ''),
       COALESCE(json_extract(f.config, '$.peer_as'), 0),
       COALESCE(json_extract(f.config, '$.peer_ip'), ''),
       COALESCE(json_extract(f.config, '$.local_ip'), ''),
       COALESCE(json_extract(f.config, '$.peer_gateway'), ''),
       COALESCE(NULLIF(json_extract(f.config, '$.multihop_ttl'), 0), 64),
       COALESCE(json_extract(f.config, '$.password'), ''),
       COALESCE(json_extract(f.config, '$.passive'), 0),
       COALESCE(json_extract(f.config, '$.target_table_id'), 0),
       COALESCE(json_extract(f.config, '$.next_hop'), '')
FROM vnfs AS f
WHERE f.type = 'bgp';

-- Backfill child rows. json_each yields no rows when the array is absent.
INSERT INTO dhcpv4_static_assignments (vnf_id, mac, ip)
SELECT f.id,
       trim(json_extract(j.value, '$.mac')),
       trim(json_extract(j.value, '$.ip'))
FROM vnfs AS f,
     json_each(CASE WHEN json_valid(f.config) THEN f.config ELSE '{}' END, '$.static_assignments') AS j
WHERE f.type = 'dhcpv4'
  AND j.type = 'object';

INSERT INTO dhcpv4_options (vnf_id, code, format, value)
SELECT f.id,
       CAST(json_extract(j.value, '$.code') AS INTEGER),
       COALESCE(NULLIF(trim(json_extract(j.value, '$.format')), ''), 'string'),
       COALESCE(json_extract(j.value, '$.value'), '')
FROM vnfs AS f,
     json_each(CASE WHEN json_valid(f.config) THEN f.config ELSE '{}' END, '$.dhcp_options') AS j
WHERE f.type = 'dhcpv4'
  AND j.type = 'object';

INSERT INTO dhcpv6_options (vnf_id, code, format, value)
SELECT f.id,
       CAST(json_extract(j.value, '$.code') AS INTEGER),
       COALESCE(NULLIF(trim(json_extract(j.value, '$.format')), ''), 'string'),
       COALESCE(json_extract(j.value, '$.value'), '')
FROM vnfs AS f,
     json_each(CASE WHEN json_valid(f.config) THEN f.config ELSE '{}' END, '$.dhcp_options') AS j
WHERE f.type = 'dhcpv6'
  AND j.type = 'object';

INSERT INTO dns_records (vnf_id, name, type, value, ttl)
SELECT f.id,
       json_extract(j.value, '$.name'),
       upper(trim(json_extract(j.value, '$.type'))),
       json_extract(j.value, '$.value'),
       CASE
           WHEN COALESCE(json_extract(j.value, '$.ttl'), 0) = 0 THEN 300
           ELSE json_extract(j.value, '$.ttl')
       END
FROM vnfs AS f,
     json_each(CASE WHEN json_valid(f.config) THEN f.config ELSE '{}' END, '$.dns_records') AS j
WHERE f.type = 'dns'
  AND j.type = 'object';

INSERT INTO dns_forwarders (vnf_id, address, position)
SELECT f.id,
       trim(j.value),
       CAST(j.key AS INTEGER)
FROM vnfs AS f,
     json_each(CASE WHEN json_valid(f.config) THEN f.config ELSE '{}' END, '$.dns_forwarders') AS j
WHERE f.type = 'dns'
  AND j.type = 'text'
  AND trim(j.value) <> '';

INSERT INTO nat44_dnat_rules (vnf_id, protocol, external_port, local_ip, local_port)
SELECT f.id,
       lower(trim(json_extract(j.value, '$.protocol'))),
       CAST(json_extract(j.value, '$.external_port') AS INTEGER),
       trim(json_extract(j.value, '$.local_ip')),
       CAST(json_extract(j.value, '$.local_port') AS INTEGER)
FROM vnfs AS f,
     json_each(CASE WHEN json_valid(f.config) THEN f.config ELSE '{}' END, '$.dnat_rules') AS j
WHERE f.type = 'nat44'
  AND j.type = 'object';

INSERT INTO bgp_advertised_prefixes (vnf_id, prefix)
SELECT f.id, trim(j.value)
FROM vnfs AS f,
     json_each(CASE WHEN json_valid(f.config) THEN f.config ELSE '{}' END, '$.advertise_prefixes') AS j
WHERE f.type = 'bgp'
  AND j.type = 'text'
  AND trim(j.value) <> '';

-- The union column is gone: typed tables are the only configuration storage.
ALTER TABLE vnfs DROP COLUMN config;
