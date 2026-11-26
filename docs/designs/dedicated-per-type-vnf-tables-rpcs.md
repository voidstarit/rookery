# Dedicated Per-Type VNF Tables and RPCs

*   **Author:** Masoud Mazarei
*   **Status:** Draft
*   **Date:** 2026-11-26

--------------------------------------------------------------------------------

## Overview

Rookery manages five network functions: DHCPv4, DHCPv6, DNS, NAT44, and BGP. They
currently share one `vnfs` table, one `VNFConfig` message, one public
`VNFService`, and one internal `VNFDaemon.EnsureVNF` call. Type-specific settings
live in the `vnfs.config` JSON column, and a create request carries a `type`
string that decides which parts of the message are valid.

This design separates configuration storage and the RPC surface by type. DHCPv4,
DHCPv6, DNS, NAT44, and BGP each get typed tables. DHCPv4 and DHCPv6 share one
DHCP service; DNS, NAT44, and BGP each get their own public service. There is no
common `/api/vnfs` read or write endpoint: the UI and the Terraform provider call
each type's endpoints and merge the results. DHCP, DNS, and BGP get dedicated
daemon RPCs, including their type-specific reads (leases, DNS records, and BGP
status). NAT44 stays on the API side because it is VPP NAT44, not a daemon
runtime. One `vnfs` row remains the identity for every type: it holds the name,
network attachment, enabled flag, tap/memif metadata, and timestamps that labels,
network rules, and reconciliation already use. Runtime activation and
reconciliation stay shared. Child writes reload the running DHCP, DNS, or BGP
runtime and keep its lease file; a parent scalar, transport, or enabled change
still stops and starts it. The split is at the data and API boundaries, not a
second runtime.

The Terraform provider retires `rookery_vnf` and moves to typed per-type
resources. Child rows are first-class resources too: DHCP static assignments and
options, DNS records and forwarders, NAT44 DNAT rules, and BGP advertised
prefixes each get their own RPCs and Terraform resources. The old mixed write
path is removed in one release with no compatibility shim.

## The Problem

The shared shape creates concrete failures that a better API alone cannot fix:

*   **The database cannot enforce type rules.** `vnfs.config` is a JSON blob. It
    cannot make a DHCP static-assignment MAC unique, make a DNS record unique, or
    reject a BGP field on a NAT44 row. Every constraint is checked in Go after
    the row is read, and different callers can apply different checks.
*   **Every change rewrites the whole configuration.** Updating one lease range
    or one DNS record means restating every other field. A stale read can remove
    unrelated configuration, and two writers have no per-field or per-item
    boundary.
*   **One message carries five types.** `VNFConfig` contains DHCPv4, DHCPv6,
    DNS, NAT44, and BGP fields at once. Validation is a chain of type checks, and
    generated clients, OpenAPI, and the Terraform `config_json` field cannot tell
    which fields apply to the selected type.
*   **One service serves unrelated operations.** Lease inspection, NAT44
    sessions, BGP status, and DNS records hang off the same generic
    `/api/vnfs/{id}` path, even though each operation belongs to one type. A
    wrong-type request is a runtime error instead of an absent endpoint.
*   **A change to one type touches all types.** Adding a field changes the shared
    protobuf, store struct, JSON column, generated code, UI form, and provider
    schema. That coupling is the source of accidental cross-type behavior.

An earlier design split only DHCP and DNS and explicitly left NAT44 and BGP on
the generic API. That left the same shared model in place for the rest of the
system. This design covers all five types.

## Goals

*   **Typed storage for every type.** DHCPv4, DHCPv6, DNS, NAT44, and BGP each
    get typed tables and constraints for their own configuration and child rows.
    DHCPv4 and DHCPv6 keep separate storage tables even though they share one
    service. Type-specific settings stop living in `vnfs.config`; the column is
    removed from the write path (the final schema decision is recorded in the
    technical plan).
*   **A typed public service per type.** DHCPv4 and DHCPv6 share one DHCP service;
    DNS, NAT44, and BGP each get their own service. Create and update requests no
    longer carry a `type` field, and fields from another type cannot be expressed
    in the message shape.
*   **No common read endpoint.** Every type service exposes list and get. The UI
    and the provider call the services they need and merge the results.
    `/api/vnfs` disappears instead of remaining as a read-only aggregate.
*   **Dedicated daemon RPCs for runtime types.** DHCP, DNS, and BGP get dedicated
    runtime RPCs for ensure, remove, state, reload, and their type-specific
    reads: DHCP leases, DNS records, and BGP status. Daemon-wide operations such
    as runtime reset and legacy cleanup stay global.
*   **Reload-based child writes.** A child create, update, or delete reloads the
    running DHCP, DNS, or BGP runtime without removing its lease file or desired
    spec. NAT44 DNAT writes apply the new rule set to VPP without stopping NAT44.
    Parent scalar, transport, tap/memif, and enabled changes still stop and start
    the runtime; a derived DHCP gateway/`bind_ip` change reloads.
*   **One VNF identity.** Every VNF keeps one `vnfs` row with the same ID, name,
    network attachment, enabled flag, labels, and tap/memif metadata. The
    one-VNF-of-each-type-per-network rule and the single reconciliation loop
    continue to work across types.
*   **Data preservation.** The migration moves existing JSON configuration into
    the typed tables without changing VNF IDs, names, networks, labels, or
    runtime behavior. An upgraded database produces the same leases, records,
    routes, and NAT44 state for the same inputs.
*   **UI continuity.** The VNFs tab still lists all five types together. It
    fetches each type's list and merges them, and leases, BGP status, DNS records,
    and NAT44 sessions stay reachable from the rows that own them.
*   **Provider migration.** The provider retires the `rookery_vnf` resource and
    adds typed per-type resources and data sources. Each child row, including
    DHCP static assignments and options, DNS records and forwarders, NAT44 DNAT
    rules, and BGP advertised prefixes, gets its own Terraform resource.
*   **A hard cut.** The mixed create, update, and delete path is removed in the
    same release. There is no compatibility shim; existing Terraform state and API
    clients move to the new resources through a documented migration.

## Non-Goals

*   **Duplicating the runtime per type.** Tap and memif setup, shared-stack
    activation, lease files, DNS integration, VPP reconciliation, and the
    supervision loop stay shared. The split is storage and RPC boundaries, not
    five copies of the runtime.
*   **Changing VNF behavior.** This design does not add DHCP, DNS, BGP, or NAT44
    features, change address allocation, or alter how routes and NAT44 rules are
    programmed. Reload changes when the runtime picks up a child change, not what
    it serves. The migration preserves current behavior.
*   **Keeping the old provider resource.** `rookery_vnf` is retired rather than
    kept as a wrapper over the new services. Its state migration is documented,
    not automated into a second writable surface.
*   **Changing labels or network attachment semantics.** Labels stay on entity
    type `vnf` with the same VNF ID. Network ownership, cascade behavior, and the
    one-per-type rule keep their current meaning.
*   **A NAT44 daemon runtime.** NAT44 remains VPP NAT44 driven by the API. This
    design does not move it into `cmd/vnfs`.
*   **A compatibility window.** The old mixed write path is not preserved for a
    deprecation period. The server, provider, and UI move together.

## Open Questions

None. The owner decisions are recorded in this document:

*   There is no common read endpoint. The UI and provider call each type service
    and merge the results.
*   DHCPv4 and DHCPv6 are grouped as one DHCP service while keeping separate
    storage types.
*   Per-type daemon RPCs include ensure, remove, state, leases, DNS records, and
    BGP status. Runtime reset and legacy cleanup stay daemon-wide.
*   The Terraform provider retires `rookery_vnf` and uses typed per-type
    resources.
*   Child rows are per-item resources with their own RPCs and Terraform
    resources. Parent updates do not replace child rows.
*   Child writes reload the running DHCP, DNS, or BGP runtime and keep the lease
    file and desired spec. NAT44 DNAT writes update VPP mappings in place. Parent
    scalar, transport, and enabled changes still stop and start the runtime; a
    derived DHCP gateway/`bind_ip` change reloads.
*   The mixed write path is removed in one release with no compatibility shim.

## Critical User Journeys (CUJs)

### CUJ 1: Create a DHCPv4 VNF with a static assignment and an option

*   **Given** a network and a running API and VNF daemon.
*   **When** the user creates a DHCPv4 VNF, adds one static assignment and one
    DHCP option, and enables the VNF.
*   **Then** the VNF starts with both rows, a client with the reserved MAC gets
    the reserved address, the option is present in the reply, the parent
    `static_assignment_count` and `option_count` reflect the rows, and the lease
    file is kept across the child writes.

### CUJ 2: Change one DNS record while the server is running

*   **Given** an enabled DNS VNF with several static records and a running
    resolver.
*   **When** the user updates the value of one record.
*   **Then** the DNS runtime reloads in place, the resolver returns the new value
    for that name, the other records and forwarders are unchanged, the process
    and lease state are not removed, and `GET /api/dns-vnfs/{id}/record-views`
    shows the new static record.

### CUJ 3: Add an advertised prefix without resetting the BGP session

*   **Given** an enabled BGP VNF with an established peer session.
*   **When** the user adds one advertised prefix.
*   **Then** the BGP runtime reloads the advertised path set, the peer session
    stays established, the prefix appears in `GET /api/bgp-vnfs/{id}/status`, and
    the previous prefixes remain advertised.

### CUJ 4: Change a NAT44 DNAT rule while NAT44 is running

*   **Given** an enabled NAT44 VNF with an outside interface and one DNAT rule.
*   **When** the user changes the rule's local port or adds a second rule.
*   **Then** the API replaces the VNF's VPP static mappings in place, NAT44 and
    the BVI stay up, existing sessions are not torn down by a stop/start, and
    `GET /api/nat44-vnfs/{id}/sessions` still returns the current translations.

### CUJ 5: Upgrade an existing database and move Terraform state

*   **Given** a schema-60 database with DHCPv4, DHCPv6, DNS, NAT44, and BGP VNFs,
    their child rows in `vnfs.config`, and existing `rookery_vnf` Terraform
    state.
*   **When** the operator backs up the database, upgrades the API, daemon, UI, and
    provider, starts the API, and moves the Terraform state with `state rm` and
    `import`.
*   **Then** every VNF keeps its ID, name, network, labels, and runtime state,
    every child row is present in its typed table, `/api/vnfs` returns `404`, the
    four list endpoints return the merged inventory, and a second `terraform
    plan` is empty.

### CUJ 6: A child reload fails

*   **Given** an enabled VNF and a child write that passes validation but whose
    runtime reload fails.
*   **When** the API processes the write.
*   **Then** the server restores the previous child row, reloads the previous
    config, returns `500` with the failure, and the parent stays in its previous
    state. If the restore reload also fails, the parent is disabled and the
    response names both failures.

## Technical Plan (Architecture)

### 1. Boundary and Components

The design replaces one VNF service and one storage union with four public
services, one identity table, five typed parent tables, and per-item child
resources.

| Layer | Before | After |
| --- | --- | --- |
| Public API | `VNFService` at `/api/vnfs` | `DHCPVNFService`, `DNSVNFService`, `NAT44VNFService`, `BGPVNFService`; no common list |
| Storage | `vnfs.type` plus `vnfs.config` JSON | `vnfs` identity plus `dhcpv4_vnfs`, `dhcpv6_vnfs`, `dns_vnfs`, `nat44_vnfs`, `bgp_vnfs`, and child tables |
| Daemon | `VNFDaemon.EnsureVNF` and shared views | `DHCPDaemon`, `DNSDaemon`, `BGPDaemon`, `VNFRuntimeAdmin` |
| Runtime | `host.VNFManager` and `store.VNFConfig` | The same shared runtime; `VNFConfig` is an internal hydration shape only |
| UI | One `/api/vnfs` fetch | Four list fetches merged; per-type forms and child editors |
| Terraform | `rookery_vnf` with `config_json` | Four parent resources, six child resources, four parent data sources |

```text
UI / Terraform
  │  per-type HTTP and gRPC calls
  ▼
gateway (DHCPVNFService, DNSVNFService, NAT44VNFService, BGPVNFService)
  │
  ▼
lifecycle.Service (per-type create/update/delete/restart and child writes)
  │
  ├── store: vnfs identity + typed parent tables + child rows
  │
  ├── api: VPP taps/memifs, network BVI, NAT44, DNS activation
  │
  └── vnfd.Client ── per-type daemon RPCs ──▶ cmd/vnfs
                                                │
                                                ▼
                                     host.VNFManager (shared runtime)
```

The public handlers stay in `internal/gateway`. The lifecycle package owns the
transaction ordering between runtime changes and store writes. The `api` package
keeps VPP and network coordination. The `store` package owns typed persistence.
The `vnfd` package owns the daemon protocol. The shared runtime in
`internal/host` is not split.

### 2. Identity, Typed Storage, and Runtime Hydration

The `vnfs` table remains the identity for every VNF:

*   `id`, `name`, `network_id`, `type`, `enabled`
*   `tap_sw_if_index`, `tap_host_name`
*   `memif_sw_if_index`, `memif_socket`, `memif_host_name`
*   `created_at`, `updated_at`

Everything type-specific moves to a sibling table keyed by `vnf_id`. The
`config` column is dropped after the backfill. Labels stay on entity type `vnf`
with the same ID, so the labels API, UI chips, and Terraform `labels` attribute
keep working. The network foreign key and cascade keep their meaning. A unique
index on `(network_id, type)` enforces the one-VNF-of-each-type-per-network rule
at the database level.

`store.VNF` stays the shape the shared runtime expects: identity plus a
`Config VNFConfig` field. `VNFConfig` becomes read-only hydration. It is not
stored, not exposed in protobuf, and not accepted by a write method. Typed store
methods are the only write path. This keeps `host.VNFManager`, activation,
reconciliation, and the transport code unchanged while removing the union from
SQL and RPC.

`store.ListVNFs` remains for reconciliation. It selects identity rows, closes the
cursor, then hydrates each row from its typed tables. The two-phase read matters
because the store uses a single SQLite connection; a nested query while rows are
open would deadlock. `store.GetVNF` hydrates one row. Reconciliation still sees
every type in one pass.

Writes split into three families:

*   Identity writes: `UpdateVNFIdentity` changes name, enabled, and tap/memif
    metadata. Activation uses this after provisioning or tearing down runtime
    resources.
*   Parent config writes: `CreateDHCPVNF`, `UpdateDNSVNF`, and the other typed
    methods write the identity row and the typed parent row in one transaction.
*   Child writes: the per-item methods write one child row, bump the parent
    `updated_at`, and leave every sibling row alone. After the write, the
    lifecycle reloads the running runtime with the full hydrated VNF; it does not
    stop the daemon process or remove the lease file.

### 3. Data Model

The migration creates the following tables. The sketch is the contract; exact
column checks and indexes are refined in implementation but no type-specific
column is added to `vnfs`.

```sql
-- vnfs keeps identity. config is dropped after the backfill.
-- New: one VNF of each type per network.
CREATE UNIQUE INDEX vnfs_network_type_idx ON vnfs(network_id, type);

CREATE TABLE dns_vnfs (
  vnf_id  INTEGER PRIMARY KEY REFERENCES vnfs(id) ON DELETE CASCADE,
  forward INTEGER NOT NULL DEFAULT 0 CHECK (forward IN (0, 1))
);

CREATE TABLE dns_records (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  vnf_id  INTEGER NOT NULL REFERENCES dns_vnfs(vnf_id) ON DELETE CASCADE,
  name    TEXT NOT NULL,
  type    TEXT NOT NULL CHECK (type IN ('A', 'AAAA', 'CNAME', 'TXT', 'PTR')),
  value   TEXT NOT NULL,
  ttl     INTEGER NOT NULL DEFAULT 300 CHECK (ttl BETWEEN 1 AND 86400),
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
```

The parent tables are mutually exclusive through the `vnfs.type` value. A
`BEFORE INSERT` trigger on each parent table rejects a row whose `vnfs.type` is
not the matching type. The app never changes a VNF type; the public update
requests do not expose it. A `BEFORE INSERT` trigger on `vnfs` rejects unknown
types. These triggers keep a direct SQL write from creating a DNS row for a
DHCP VNF.

Foreign keys do the rest. Deleting the `vnfs` row cascades through the parent and
child tables. Deleting a DNS VNF sets `dhcpv4_vnfs.dns_vnf_id` and
`dhcpv6_vnfs.dns_vnf_id` to `NULL`; the lifecycle still pre-checks and returns
`409` while a DHCP VNF references it. The `dns_vnf_id` foreign key points at
`dns_vnfs(vnf_id)`, so a reference to a non-DNS VNF is impossible.

Child rows have their own `id` and their own RPCs. The unique keys are the
natural identity for each child: `(vnf_id, mac)` and `(vnf_id, ip)` for DHCPv4
static assignments, `(vnf_id, code)` for DHCP options, `(vnf_id, name, type,
value)` for DNS records, `(vnf_id, address)` for forwarders, `(vnf_id, protocol,
external_port)` for DNAT rules, and `(vnf_id, prefix)` for advertised prefixes.
DNS CNAME exclusivity and the "no CNAME with other records" rule stay in Go
because they span rows.

`lease_time` stays `TEXT`. The previous normalizer stored compact values such as
`1h3` for 1h30m, which `time.ParseDuration` rejects. The new normalizer and
`host.vnfLeaseTime` share a decoder that accepts both standard durations and the
legacy compact forms (`1h3` -> 1h30m, `3` -> 30m, `1h1` -> 1h10m, `2h2` ->
2h20m). The migration copies the text unchanged; no row is lost to a duration
parse. New writes store `time.Duration.String()`.

### 4. Public API

There is no `VNFService`, no `VNF` union message, and no `/api/vnfs` route. The
four services use the parent `vnfs.id` as the record ID. Child routes include
both the parent ID and the child ID so grpc-gateway can bind every path
variable by name.

| Service | Parent routes | Child routes | Type-specific reads |
| --- | --- | --- | --- |
| `DHCPVNFService` | `/api/dhcp-vnfs`, `/{id}`, `/{id}/restart` | `/{vnf_id}/static-assignments` (v4 only), `/{vnf_id}/options` | `/{id}/leases` |
| `DNSVNFService` | `/api/dns-vnfs`, `/{id}`, `/{id}/restart` | `/{vnf_id}/records`, `/{vnf_id}/forwarders` | `/{id}/record-views` |
| `NAT44VNFService` | `/api/nat44-vnfs`, `/{id}`, `/{id}/restart` | `/{vnf_id}/dnat-rules` | `/{id}/sessions` |
| `BGPVNFService` | `/api/bgp-vnfs`, `/{id}`, `/{id}/restart` | `/{vnf_id}/advertised-prefixes` | `/{id}/status` |

Each child collection supports `GET` and `POST`; each child item supports `GET`,
`PUT`, and `DELETE`. The child path variables are `{vnf_id}` and
`{assignment_id}`, `{option_id}`, `{record_id}`, `{forwarder_id}`, `{rule_id}`,
or `{prefix_id}`. The message fields use those exact names. Path variables are
not named `{id}` on child routes; grpc-gateway binds by field name and a mismatch
would not populate the request.

The parent record carries the scalar configuration plus computed status and
counts. It does not carry child arrays. A DHCP record has `family`, `gateway`,
`range_start`, `range_end`, `lease_time`, `dns`, `bind_ip`, `transport`,
`dns_vnf_id`, `dns_domain`, `dns_ttl`, `static_assignment_count`,
`option_count`, `lease_count`, status, tap metadata, and timestamps. A DNS record
has `forward`, `record_count`, `forwarder_count`, and status. A NAT44 record has
`gateway`, `outside_interface`, `outside_address`, `outside_gateway`,
`nat44_pool`, `dnat_rule_count`, and status. A BGP record has `transport`,
`local_as`, `router_id`, `peer_as`, `peer_ip`, `local_ip`, `peer_gateway`,
`multihop_ttl`, `passive`, `target_table_id`, `next_hop`,
`advertised_prefix_count`, and status. The BGP password is never returned in a
read; it is write-only and sensitive.

The parent create and update requests carry only scalar configuration. They do
not accept child arrays. The child create and update requests carry only that
child's fields. This is what makes the child rows independently owned by the UI
and Terraform.

The per-item write behavior is:

1.  Validate the child against its parent configuration and siblings.
2.  Write the child row and bump the parent `updated_at` in one transaction.
3.  If the parent is enabled and the runtime is running, reload it with the full
    new spec through the daemon's per-type reload RPC. The daemon process, lease
    file, and desired spec stay alive. If the parent is enabled but the runtime
    is not running, keep the row and leave the VNF degraded; the next start uses
    the new rows.
4.  If the reload fails, restore the previous child row, reload the previous
    config, and return `500`. If that restore also fails, set the parent
    `enabled` to false and return both errors. The user can fix the cause and
    enable the VNF again.

A NAT44 DNAT child write does not use the daemon. It writes the row and
re-applies the VNF's full DNAT rule set to VPP, keeping the NAT44 runtime and the
BVI up. The lifecycle removes the VNF's old static mappings and applies the new
set; a VPP failure restores the previous row and mapping set.

A derived DHCP gateway or `bind_ip` change from shared-memif or NAT44 setup uses
`SetDHCPVNFNetworkBindings` plus the DHCP reload path. The setter writes only the
derived columns; the lifecycle reloads the new config and restores the previous
values and runtime if the reload fails.

A child write on a disabled parent only writes the row. A parent `enabled`
transition to true loads every child row and activates once. A parent delete
stops the runtime and deletes the identity row; the foreign keys remove the
parent and child rows.

The parent list supports `network_id`; the DHCP list also supports `family`.
There is no unified list. The UI and provider fetch the four lists and merge
them. `GET` on a parent checks the stored `vnfs.type` and returns `404` when the
ID belongs to another type. Duplicate child keys return `409`; invalid values
return `400`; a missing runtime returns `503`. Create responses are `201`,
delete responses are `204`, and the gateway sets the type-specific `Location`
header.

### 5. Daemon RPC Surface

The daemon exposes four services. One server struct can implement all four and
share the manager, desired-spec map, and supervision loop; the wire boundaries
are separate.

| Service | Methods |
| --- | --- |
| `DHCPDaemon` | `EnsureDHCP`, `RemoveDHCP`, `ReloadDHCP`, `GetDHCPState`, `ListDHCPLeases`, `RemoveDHCPLeases` |
| `DNSDaemon` | `EnsureDNS`, `RemoveDNS`, `ReloadDNS`, `GetDNSState`, `ListDNSRecords` |
| `BGPDaemon` | `EnsureBGP`, `RemoveBGP`, `ReloadBGP`, `GetBGPState`, `GetBGPStatus` |
| `VNFRuntimeAdmin` | `ResetRuntime`, `CleanupLegacyNetwork` |

There is no `EnsureVNF`, no `RemoveVNF`, and no `ListVNFStates`. The daemon
supervisor still keeps the desired list internally, but the API queries the
state of a known type instead of listing every runtime.

Each ensure request embeds a shared `RuntimeSpec` and a type-specific config:

```text
RuntimeSpec: id, name, network_id, transport, tap_host_name, memif_socket,
             memif_sw_if_index, shared_address, shared_ipv6_address

EnsureDHCPRequest: RuntimeSpec + family + oneof (DHCPv4RuntimeConfig,
                   DHCPv6RuntimeConfig)
EnsureDNSRequest:  RuntimeSpec + DNSRuntimeConfig
EnsureBGPRequest:  RuntimeSpec + BGPRuntimeConfig
```

The DHCPv4 runtime config carries the scalar fields plus repeated static
assignments and options. The DHCPv6 config carries scalar fields plus options.
The DNS runtime config carries records and forwarders. The BGP runtime config
carries the scalar fields plus advertised prefixes. The child config messages
are the same shapes the public child resources use, without the row IDs.

Each type service also has a reload method. `ReloadDHCP` receives the full DHCP
runtime config, `ReloadDNS` receives the records and forwarders, and `ReloadBGP`
receives the advertised prefixes. The handler validates the new spec, calls
`VNFManager.Reload`, updates the desired spec, and restores the previous desired
spec if the reload fails. Reload does not call `RemoveVNF`, does not delete the
lease file, and does not change the enabled flag. A reload on a runtime that is
not running returns a state with `running=false`; the API persists the row and
leaves the VNF degraded instead of starting it.

The daemon's `Manager` interface gains `Reload(ctx, vnf store.VNF) error`, which
rebuilds the type-specific runtime config and swaps it into the running runtime.
For DHCP, the lease file and the server socket stay in place; the handler may be
swapped in-process. For DNS, the listener stays and the record/forwarder handler
is replaced. For BGP, the GoBGP server stays up; the advertised path set is
withdrawn and re-advertised.

Persistence changes from one `VNFSpec` per file to a wrapper with a `oneof` for
the DHCP, DNS, or BGP spec. `Load` ignores a file it cannot parse as the new
wrapper, including the old `VNFSpec` files; the API's startup reconciliation
re-sends every enabled VNF. The spec hash is computed over the type-specific
wrapper, so an unchanged spec is still a no-op.

The API's `VNFManager` interface changes in three places: `Stop` and `Running`
receive the VNF record (or its type) so the client can choose the per-type daemon
service, and `ReloadVNF` takes the hydrated VNF and calls the type's reload RPC.
`Start`, `StartMemif`, and `StartShared` keep their signatures; the client
switches on `vnf.Type` and calls `DHCPDaemon` or `BGPDaemon` for those
transports, and `DNSDaemon` for the shared DNS path. NAT44 never reaches the
daemon. `Leases`, `RemoveLeases`, `DNSRecords`, and `BGPStatus` stay
type-specific and call their service directly.

### 6. Lifecycle and Runtime Flow

A create starts with the parent row and no children:

```text
UI / Terraform         gateway              lifecycle              store / runtime
    | create parent      |                     |                        |
    |------------------->| validate + create  |                        |
    |                    |-------------------->| identity + typed row   |
    |                    |                     |----------------------->|
    | create children    |                     |                        |
    |------------------->| validate child     |                        |
    |                    |-------------------->| child row + parent ts  |
    |                    |                     | reload runtime         |
    |                    |                     |----------------------->|
    | enable parent      |                     |                        |
    |------------------->| update enabled     |                        |
    |                    |-------------------->| hydrate all children   |
    |                    |                     | start runtime          |
    |                    |                     |----------------------->|
```

The UI can create the parent with `enabled=false`, write every child, then set
`enabled=true`. That produces one start with the full child set. The Terraform
provider cannot do that inside one resource because child resources are separate;
a parent with `enabled=true` starts empty, and each child write reloads the
runtime. The final state is correct, with one reload per child change rather than
a process restart.

A parent scalar update loads the parent and, when enabled, stops and starts the
runtime through the existing activation path. A parent transport, tap/memif, or
enabled transition also stops and starts. A child create, update, or delete
reloads the running runtime after the row write. The reload contract is:
validate the full proposed spec, update the daemon's desired spec, swap the
runtime config, and keep the process, lease file, and listener in place. If the
reload fails, the daemon restores the previous desired spec, the lifecycle
restores the previous child row and reloads the previous config; if that restore
also fails, the VNF is left disabled. A derived DHCP gateway or `bind_ip` change
from shared-memif or NAT44 setup uses the typed setter plus the same reload path.
A NAT44 DNAT child write does not use the daemon; it replaces the VNF's VPP
static mappings while NAT44 and the BVI stay up.

The global `resourceMu` still serializes create, update, delete, restart, and
child writes with network and disk provisioning. This is the existing
concurrency invariant. Child writes are short database operations followed by a
reload under the same lock. A reload is shorter than a restart but still holds
the lock; per-VNF locks would reduce contention and are a separate change.

Delete order stays: stop runtime, delete identity row (cascade typed rows), then
NAT44 BVI cleanup where applicable. Deleting a DNS VNF is rejected with `409`
while a DHCP VNF references it; the foreign key `SET NULL` is the safety net for
a direct SQL delete. Network deletion still stops every VNF on the network and
relies on the identity cascade.

### 7. Migration and Backfill

The change adds `internal/store/migrations/0061_per_type_vnfs.sql`. The repo's
latest migration is `0060_lightvm_ephemeral.sql`; `AGENTS.md` is updated to name
`0061` as the latest. The migration is SQL-only and runs in the existing
single-transaction migration wrapper, so schema creation, data movement, the
unique index, and `PRAGMA user_version` commit or roll back together. There is
no Go backfill inside the transaction and therefore no second connection.

Before the migration runs, `store.Open` runs a read-only preflight when the
pending version is `61`. It checks:

*   Every `vnfs.type` is one of the five known types and every `config` is a
    valid JSON object.
*   Required scalar fields for each type are present.
*   Child arrays have the expected object or string shape.
*   Every `dns_vnf_id` points at a DNS VNF on the same network.
*   The unique child keys the new tables will enforce do not collide, and no
    network already has two VNFs of the same type.

If the preflight finds a problem it returns a report naming the VNF IDs and
fields, and the server does not migrate. The operator fixes or removes the named
rows and retries. If a database reports version `61` but still has
`vnfs.config` and no typed tables, it carries a pre-release `0061` stamp; the
preflight fails with a repair message instead of starting with a missing schema.

The data mapping is direct:

| `vnfs.type` | Parent table | Child rows from `vnfs.config` |
| --- | --- | --- |
| `dhcpv4` | `dhcpv4_vnfs` | `static_assignments`, `dhcp_options` |
| `dhcpv6` | `dhcpv6_vnfs` | `dhcp_options` |
| `dns` | `dns_vnfs` | `dns_records`, `dns_forwarders` |
| `nat44` | `nat44_vnfs` | `dnat_rules` |
| `bgp` | `bgp_vnfs` | `advertise_prefixes` |

Scalar fields keep their names. `config.dns` stays a comma-separated text
column. `config.transport=memif` on DHCPv4 is normalized to `dedicated`, matching
the current API. DNS record TTL `0` becomes `300`; DNS types are uppercased.
Child rows get fresh `AUTOINCREMENT` IDs; nothing depends on the old array
positions. The `config` column is dropped after the inserts.

The migration is a hard cut: an old server binary cannot read the new schema and
the new server cannot write the old one. The rollout is: stop the API and the
daemon, back up the database, upgrade both binaries with the provider and UI in
the same release, start the API, start the daemon. The API's startup
reconciliation re-sends every enabled VNF. A rollback restores the backup and the
previous binaries.

A migration test opens a fixture database at schema `60` with one VNF of each
type and every child shape, runs `store.Open`, and asserts the typed rows, child
IDs, `lease_time` text, `dns_vnf_id` links, and that `vnfs.config` is gone. A
second test proves the preflight rejects a duplicate MAC, a bad `dns_vnf_id`, and
an unknown type without changing `user_version`. A third test covers the legacy
lease-time values `1h3`, `3`, `1h1`, and `2h2`.

### 8. UI

`ui/src/inventory/vnfs/VNFsTab.tsx` replaces the single `/api/vnfs` call with
four list calls:

```text
GET /api/dhcp-vnfs?network_id=...
GET /api/dns-vnfs?network_id=...
GET /api/nat44-vnfs?network_id=...
GET /api/bgp-vnfs?network_id=...
```

The tab merges the four arrays, sorts by name, and keeps one table and one
`rookery:vnfs-changed` event. A failing type shows a partial list and a banner
instead of blanking the tab. The deep links stay `/vnfs/{id}/edit`,
`/vnfs/{id}/leases`, `/vnfs/{id}/sessions`, `/vnfs/{id}/bgp`, and
`/vnfs/{id}/dns`; the route effect resolves the ID from the merged list, so a
refresh still works.

`VNFPage` becomes a typed form. The parent scalar fields save through the
type-specific parent endpoint. The child editors load their rows from the child
collection endpoints and save a diff:

1.  Save the parent scalar fields. A parent scalar, transport, tap/memif, or
    enabled change may stop and start the runtime; an unchanged parent does not.
2.  Delete removed child rows, update changed rows, create new rows. Each child
    write reloads the running runtime; no disable/enable cycle is needed.
3.  If a child write fails, reload the child lists and show the error; the server
    has restored the previous row and runtime.

The order deletes first so a changed unique key (MAC, IP, record, port) does not
collide with the row being replaced. A cross-row swap still returns `409`; the
form shows the message and asks the user to resolve it. The form does not use a
bulk replace endpoint because child rows are independently owned.

The dialogs move their URLs: leases to `/api/dhcp-vnfs/{id}/leases`, sessions to
`/api/nat44-vnfs/{id}/sessions`, BGP status to `/api/bgp-vnfs/{id}/status`, and
the merged DNS view to `/api/dns-vnfs/{id}/record-views`. The DNS VNF selection
on a DHCP form fetches `/api/dns-vnfs`.

`ui/src/inventory/types.ts` replaces `VNF` and `VNFConfig` with a discriminated
union of `DHCPVNF`, `DNSVNF`, `NAT44VNF`, and `BGPVNF`, plus the child row types.
The UI no longer reads `vnf.config`. `NetworksTab.tsx` also merges the four
lists instead of fetching `/api/vnfs`.

### 9. Terraform Provider

The provider drops `rookery_vnf` and the `rookery_vnf` data source. It adds ten
resources and four parent data sources. Child resources require `vnf_id`
(forces replacement) and import by `<vnf_id>:<child_id>`.

| Resource | Parent | Key fields |
| --- | --- | --- |
| `rookery_dhcp_vnf` | none | family, name, network_id, enabled, scalar DHCP fields, labels |
| `rookery_dhcp_static_assignment` | DHCPv4 | vnf_id, mac, ip |
| `rookery_dhcp_option` | DHCP | vnf_id, code, format, value |
| `rookery_dns_vnf` | none | name, network_id, enabled, forward, labels |
| `rookery_dns_record` | DNS | vnf_id, name, type, value, ttl |
| `rookery_dns_forwarder` | DNS | vnf_id, address, position |
| `rookery_nat44_vnf` | none | name, network_id, enabled, scalar NAT44 fields, labels |
| `rookery_nat44_dnat_rule` | NAT44 | vnf_id, protocol, external_port, local_ip, local_port |
| `rookery_bgp_vnf` | none | name, network_id, enabled, scalar BGP fields, labels |
| `rookery_bgp_advertised_prefix` | BGP | vnf_id, prefix |

The parent resources expose computed `id`, `network_name`, status, tap metadata,
counts, and timestamps. `network_id` and the DHCP `family` force replacement.
There is no `config_json`, no `type` attribute (the resource name selects the
type), and no child arrays. The BGP `password`, DHCP option `value`, and DNS TXT
`value` are marked sensitive.

The old resource is removed rather than shimmed. A user with `rookery_vnf` state
runs `terraform state rm rookery_vnf.<name>`, writes the new parent and child
resources, and imports each existing object by ID. The migration guide is part of
the docs change. Terraform cannot move a `config_json` resource into a typed
resource reliably because the old state has no per-child IDs.

Child resources depend on the parent through `vnf_id`, so Terraform creates the
parent first and destroys children before the parent. The parent may be
`enabled=true` before children exist; each child write reloads the runtime, and
the final apply converges. Terraform also cannot batch the child writes, so an
apply that changes several children causes several reloads, not process
restarts. A parent scalar, transport, or enabled change still stops and starts
the runtime. That is a known cost of per-item ownership.

### 10. Documentation and Generated Artifacts

The proto source of truth moves from one `vnf.proto` service to per-type files:

*   `design/proto/rookery/v1/dhcp_vnf.proto`, `dns_vnf.proto`,
    `nat44_vnf.proto`, and `bgp_vnf.proto` define the four services, parent and
    child messages, and runtime config messages.
*   `design/proto/rookery/v1/vnfd.proto` is rewritten for the four daemon
    services and imports the per-type config messages.
*   `design/proto/rookery/v1/vnf.proto` keeps only genuinely shared messages
    such as leases, or is removed if nothing remains. `VNF` and `VNFConfig` are
    deleted.
*   `design/gen` and `design/openapi` are regenerated with `make proto-generate`;
    `make proto-check` must pass.

The store adds `0061_per_type_vnfs.sql` and typed VNF files; `store.go` loses the
union VNF write SQL and the `config` column scan. The API validation in
`internal/api/vnfs.go` splits by type; the large union payload struct is
removed. The gateway VNF files split into four services. The daemon client and
server split by type. The UI files split by type and child editor. The provider
adds the ten resources and four data sources.

The documentation updates are:

*   `docs/api.md`: the VNF endpoint table, request examples, and child resource
    examples; remove every `/api/vnfs` reference.
*   `docs/agent-notes/vnfs.md`: the records, RPCs, tables, and transport
    sections.
*   `docs/agent-notes/architecture.md`: the database and package map.
*   `docs/agent-notes/terraform.md`: the new provider resources and the state
    migration.
*   `docs/agent-notes/ui.md`: the merged list, forms, and event payload.
*   `AGENTS.md`: migration latest number and any invariant that names the common
    VNF API.

### 11. Risks and Mitigations

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Migration fails on real data | The server cannot start or partially migrates | Read-only preflight, single SQL transaction, backup before upgrade, fixture test with every child shape |
| Legacy `lease_time` values are rejected | A migrated VNF cannot start or be edited | Shared legacy decoder in the normalizer and `vnfLeaseTime`, tests for every compact form |
| Hard cut breaks API clients and Terraform state | Existing automation fails | Documented state removal and import, lockstep server/provider/UI release, no compatibility claims |
| A child reload fails or partially applies | The runtime keeps the old config while the row says otherwise | Validate the full spec, update the desired spec only after the reload succeeds, restore the previous row and config on failure; disable if the restore fails |
| Per-item writes reload the runtime | A DHCP handler swap can briefly interrupt service; Terraform does one reload per child change | Reload keeps the process, lease file, and desired spec; the UI applies children one by one; document the Terraform reload cost; a bulk child apply is future |
| Parent scalar, transport, or enabled changes still restart | A parent change can interrupt service | Keep restart for parent scalar, transport/tap/memif, and enabled changes; reload child-only changes; document the distinction |
| Child unique-key swaps collide | A rename or swap returns `409` | Delete-before-create in the UI, explicit `409` messages, no cross-item transaction |
| Parent is enabled before children exist | A short window with missing records or assignments | Child writes reload the runtime; the UI can create disabled; the final state converges |
| Old daemon state files cannot be loaded | Runtimes do not restart after daemon upgrade | Ignore old `VNFSpec` files; API startup reconciliation re-sends every enabled spec |
| `VNFConfig` hydration leaks into writes | Type-specific fields cross back into storage | Typed store write methods only; hydration is read-only; tests assert the union is never persisted |
| The global `resourceMu` serializes child writes | A slow reload or restart blocks VM, disk, and network work | Keep the existing invariant; record a per-VNF lock as a future improvement |
| A pre-release `0061` stamp exists on a dev database | The new migration is skipped and typed tables are missing | Preflight detects `config` plus no typed tables and fails with a repair message |
| Labels or cascades change by accident | Lost labels or orphan rows | Keep `entity_type='vnf'`, the label trigger, and foreign-key cascades; add tests |

## Alternatives Considered

The prior design's alternatives were re-read before writing this section. Several
of its decisions no longer hold: the author now wants all five types, per-item
child rows, per-type daemon RPCs, no common read endpoint, a retired
`rookery_vnf`, and no compatibility window. The alternatives below are the ones
that still bear on this design.

### Alternative A: Keep One `VNFService` and Add Typed Child Endpoints

Keep the generic parent service and the `VNF` message, and add typed child
endpoints for static assignments, records, options, and the rest.

*   **Pros:** The smallest API change. Existing list, get, and status calls keep
    working. No four-service split and no daemon change.
*   **Cons:** The parent remains a type-discriminated `VNFConfig` document. The
    storage stays one `vnfs.config` JSON blob, so the database still cannot
    enforce type rules. The child endpoints hang off the same generic
    `/api/vnfs/{id}` path.
*   **Reason for Rejection:** It does not deliver per-type tables or per-type
    parent services, which is the request. The child endpoints would be a facade
    over the same mixed configuration.

### Alternative B: Typed APIs over the Existing `vnfs.config`

Add the four parent services and typed messages but keep `vnfs.config` as the
only storage, converting to and from JSON on each request.

*   **Pros:** No schema migration and no backfill. The runtime and reconciler
    stay untouched.
*   **Cons:** The database cannot enforce unique static assignments, DNS records,
    DHCP options, DNAT ports, or advertised prefixes. Per-item writes still need
    read-modify-write over the whole JSON document. The typed API would be a
    facade over the fragile storage.
*   **Reason for Rejection:** The storage model is the part that cannot enforce
    invariants today. A typed API without typed tables leaves the main problem
    in place.

### Alternative C: Per-Type Root Tables Without the Shared `vnfs` Identity

Drop `vnfs` and give DHCPv4, DHCPv6, DNS, NAT44, and BGP independent root tables,
independent IDs, and independent lifecycle records.

*   **Pros:** The most literal separation. Each type owns its identity and can
    evolve without a shared row. A type bug cannot affect another type's rows.
*   **Cons:** Name uniqueness, the network foreign key, tap/memif metadata,
    timestamps, and the one-per-type rule are duplicated five times. Labels key
    on `entity_type='vnf'` plus `entity_id`; independent ID spaces collide, so
    either the label key becomes type-qualified (breaking the labels API, UI, and
    Terraform `labels`) or each type gets its own label entity. The reconciler
    needs a union of five lists. The DHCP-to-DNS reference crosses root tables
    without a foreign key.
*   **Reason for Rejection:** Rejected by author: the shared `vnfs` identity is
    required for labels, network attachment, and one reconciliation pass. The
    identity table is not a configuration table; the configuration separation is
    achieved by moving every type-specific field out of it.

### Alternative D: Shared Identity and Typed Tables With One Daemon `EnsureVNF`

Keep the four public services and the typed storage, but keep one daemon RPC that
accepts a `oneof` runtime spec.

*   **Pros:** Fewer daemon services and one supervisor entry point. Less proto
    churn in `cmd/vnfs`.
*   **Cons:** The daemon still exposes a common VNF entry point whose request
    changes shape by type. The per-type leases, DNS records, and BGP status calls
    still need type branches or separate methods. The author asked for per-type
    daemon RPCs.
*   **Reason for Rejection:** Rejected by author: the daemon RPC boundary must
    match the runtime type, not just the public API.

### Alternative E: Parent-Owned Child Rows

Keep one create/update per parent and replace its child rows in the same
transaction. Child data appears as nested blocks in the parent request and the
Terraform parent resource.

*   **Pros:** One write per VNF, atomic child replacement, one Terraform resource
    per parent, and no child IDs or ordering rules. A parent update cannot leave
    half a child set.
*   **Cons:** A stale list can delete rows another writer owns. There is no
    per-item import, drift detection, or independent lifecycle. A change to one
    record rewrites the whole list. This is the shape the earlier design rejected
    for DNS records and static assignments.
*   **Reason for Rejection:** Rejected by author: child rows need independent
    ownership and their own RPCs and Terraform resources.

### Alternative F: Hybrid Child Ownership

Make DHCP static assignments and DNS records per-item resources, and keep DHCP
options, DNS forwarders, NAT44 DNAT rules, and BGP advertised prefixes as nested
lists in their parents.

*   **Pros:** Covers the two child types with the clearest independent identity.
    Fewer resources and endpoints than full per-item ownership.
*   **Cons:** Two ownership patterns in one API and provider. A user has to learn
    which children are replaceable and which are individually owned. The
    inconsistent shape tends to spread as more child fields are added.
*   **Reason for Rejection:** Rejected by author: every child row is per-item.

### Alternative G: One Typed Public Service With a `oneof` Config

Keep one `VNFService` and one route prefix, but replace the mixed `VNFConfig`
with a `oneof` that selects a DHCPv4, DHCPv6, DNS, NAT44, or BGP message.

*   **Pros:** One list and one client connection. The message shape prevents
    cross-type fields. The service can expose per-type methods internally.
*   **Cons:** The route surface is still one common `/api/vnfs` with a type
    discriminator. OpenAPI and generated clients do not get per-type service
    boundaries. The user asked for dedicated tables and RPCs, not one service
    with a larger message.
*   **Reason for Rejection:** Rejected by author: separate services and routes
    are required.

### Alternative H: Per-Type Tables With Duplicated Runtime and Lifecycle

Give each type its own activation, tap/memif setup, shared-stack handling, lease
handling, DNS integration, reconciliation, and status code.

*   **Pros:** The deepest isolation. A DHCP runtime bug cannot reach DNS or BGP
    code. Each type can change its runtime independently.
*   **Cons:** Duplicates the most delicate code in the server: tap and memif
    creation, the shared gVisor stack, lease files, DNS integration, VPP
    reconciliation, and the supervision loop. Five copies will drift, and the
    shared-stack ownership rules are easy to break.
*   **Reason for Rejection:** Rejected for technical reasons: the runtime is
    intentionally shared. The split is at the data and RPC boundaries, not five
    runtimes in one process.

### Alternative I: Go Backfill or Go-in-Transaction Migration

Create the schema in SQL, then run a Go routine that decodes each `vnfs.config`
and writes the typed rows through a transaction.

*   **Pros:** Reuses Go validators and the lease-time decoder. Can report
    per-field errors with row context. Can normalize values that SQL cannot parse.
*   **Cons:** `store.Open` uses a single SQLite connection; a transaction that
    calls ordinary store methods deadlocks. The schema migration commits before
    the Go backfill, so a failure leaves schema version 61 with un-migrated JSON.
    Two failure domains make a partial upgrade likely.
*   **Reason for Rejection:** Rejected for technical reasons: a single SQL
    transaction for schema and data, plus a read-only Go preflight, is atomic and
    matches the existing migration wrapper. The one value SQL cannot parse, the
    legacy lease time, is handled by the runtime and API decoder instead of a
    data rewrite.

### Alternative J: Keep a Read-Only `/api/vnfs` Aggregate

Remove the writes but keep `GET /api/vnfs` and `GET /api/vnfs/{id}` as a
read-only merged view for the UI and provider.

*   **Pros:** One fetch for the UI and one data source for Terraform. Deep links
    resolve from one list. No client-side merge.
*   **Cons:** A common VNF endpoint remains, so the "no common VNF RPC" goal is
    only half met. The response is a union of five records or a generic record
    with a `type` field, which reintroduces the shape the split removes. The user
    explicitly rejected it.
*   **Reason for Rejection:** Rejected by author: there is no common endpoint;
    the UI and provider fetch each type and merge.

### Alternative K: Keep `rookery_vnf` With `config_json`

Retire the server's generic API but keep the Terraform `rookery_vnf` resource,
mapping `type` and `config_json` to the per-type service calls.

*   **Pros:** Smaller provider change and no state migration. Existing Terraform
    configurations keep working.
*   **Cons:** The provider boundary still hides every field inside a sensitive
    JSON string. Plans do not show DHCP ranges, DNS records, or BGP peers. Two
    ownership models would coexist once child resources are added.
*   **Reason for Rejection:** Rejected by author: retire the old resource and use
    the typed resources.

### Alternative L: Automated Terraform State Migration

Use `moved` blocks or a framework state upgrader to turn each `rookery_vnf` state
entry into a typed parent and child resources.

*   **Pros:** No `state rm` and import steps for the user. Terraform plans the
    move as a refactor instead of a destroy/create.
*   **Cons:** The old state stores one `config_json` string and no child IDs.
    A state upgrader cannot reliably reconstruct per-item identities, and it
    would duplicate the server migration's parsing and validation. Cross-type
    moves between different resource types are not a supported `moved` path for
    this shape.
*   **Reason for Rejection:** Rejected for technical reasons: the old state does
    not contain the identities the new resources need. A documented `state rm`
    and import by VNF ID and child ID is the reliable path.

### Alternative M: Stage the Split Across Releases

Split DHCP and DNS first, then NAT44 and BGP in a later release.

*   **Pros:** Smaller releases with less risk per change. The team can learn from
    the first migration before touching NAT44 and BGP.
*   **Cons:** The common VNF API, table, and daemon RPC remain for the unsplit
    types, so the system carries two models. The second migration repeats the
    same hard cut later. The prior design chose this shape and was superseded.
*   **Reason for Rejection:** Rejected by author: all five types are in this
    design.

### Alternative N: Compatibility Shim or Deprecation Window

Keep the old `/api/vnfs` writes and `rookery_vnf` working through translation for
one or more releases while the new services are introduced.

*   **Pros:** Existing clients and Terraform states upgrade at their own pace. A
    rollback does not require restoring the old binaries.
*   **Cons:** Two writable surfaces manage the same rows. Ownership is ambiguous
    during the window, tests double, and the old path still exposes the mixed
    configuration the split is meant to remove.
*   **Reason for Rejection:** Rejected by author: there is no compatibility
    window; the operator is the only user and the release is a hard cut.

### Alternative O: One Child Table With a `kind` Discriminator

Store every child row in one table with `kind` set to static assignment, option,
record, forwarder, DNAT rule, or advertised prefix, and nullable columns for the
union of fields.

*   **Pros:** One table and one route pattern for all children. Adding a child
    kind is a new enum value.
*   **Cons:** The table cannot enforce per-kind required fields, code ranges, or
    unique keys without a large set of triggers. A foreign key cannot distinguish
    a DNS child from a DHCP child. The schema becomes a row-level union, which is
    the same weakness as `vnfs.config` moved one level down.
*   **Reason for Rejection:** Rejected for technical reasons: typed child tables
    give the database the constraints this design is for.

### Alternative P: Child Arrays as JSON in Typed Parent Tables

Keep typed parent tables but store static assignments, records, forwarders,
options, DNAT rules, and prefixes as JSON columns on the parent.

*   **Pros:** Fewer tables and no child IDs or child RPCs. The parent row is the
    only row to read for a runtime spec.
*   **Cons:** No per-item identity, import, drift detection, or RPC. Unique keys
    need SQLite JSON expressions or Go checks. The per-item ownership the author
    chose is impossible.
*   **Reason for Rejection:** Rejected by author: child rows are per-item
    resources.

### Alternative Q: Restart on Every Child Write

Keep the existing stop/start activation path for a child create, update, or
delete instead of adding a reload RPC.

*   **Pros:** No daemon reload methods and no runtime handler swap. The existing
    activation and rollback path is reused without change.
*   **Cons:** The daemon tears down the runtime and lease file, so DHCP, DNS, or
    BGP is unavailable on every child change. A Terraform apply with several
    children causes several process restarts. The prior design's reload goal is
    lost.
*   **Reason for Rejection:** Rejected by author: child writes reload the
    running runtime in place.

### Alternative R: Reload Every Parent Config Change

Extend the reload path to every parent scalar change, including DHCP
gateway/range, DNS forwarding, and BGP peer settings.

*   **Pros:** Fewer restarts for parent edits and one reconfiguration path for
    parent and child changes.
*   **Cons:** Transport, tap/memif, and listener changes cannot reload because
    the interface itself changes. A BGP peer change may reset the session. The
    runtime contract grows for changes that are infrequent enough to restart,
    and the rollback surface widens.
*   **Reason for Rejection:** Rejected for this release: child writes reload;
    parent transport, tap/memif, and enabled changes still stop and start.
    Parent scalar reload is recorded as a future improvement.

--------------------------------------------------------------------------------

## Non-Functional Requirements

The critical concerns are security and rollout; the performance cost is
documented in the Technical Plan.

**Security.** BGP passwords, DHCP option values, and DNS TXT values can contain
secrets. The per-type services never return a BGP password on read, mark those
fields sensitive in Terraform, and do not log values. Reload validates the full
proposed spec before the runtime swap and never logs the config. The local API
remains unauthenticated and loopback-only by default; the public mTLS listener
still serves the gateway, and the new services register on the same mux. No new
listener or CSP exception is introduced.

**Performance.** Parent lists compute child counts in SQL and do not load child
rows. `store.ListVNFs` hydrates per VNF only for reconciliation. The main latency
cost is the reload on each child write. Reload keeps the process and lease file,
but a DHCP handler swap can briefly interrupt service. The UI applies child rows
one by one; Terraform does one reload per child. Parent scalar, transport,
tap/memif, and enabled changes still stop and start. The global `resourceMu`
keeps its current behavior and remains the serialization point for VNF, network,
and disk writes.

**Rollout and rollback.** This is a hard cut. The API, daemon, UI, generated
protos/OpenAPI, and Terraform provider ship in one release. The operator stops
both processes, backs up SQLite, upgrades, and starts the API before the daemon.
Rollback restores the backup and the previous binaries; there is no dual-write or
compatibility window.

--------------------------------------------------------------------------------

## Additional Resources

*   `docs/agent-notes/vnfs.md` for the current daemon, transport, DHCP, DNS, and
    BGP behavior.
*   `docs/agent-notes/api.md` for the gateway/native split and routing rules.
*   `docs/agent-notes/tooling.md` for gopls and TypeScript navigation.
*   The superseded design at commit `aa52c3e`
    (`docs/designs/dedicated-dhcp-dns-vnf-apis.md`) for the prior DHCP/DNS-only
    split and its review findings.
*   `internal/store/migrations/0008_vnfs.sql` through
    `0060_lightvm_ephemeral.sql` for the current schema history.

## Future Improvements

*   **Parent-config reload.** Extend the reload path to DHCP gateway/range, DNS
    forwarding, and BGP peer changes so a parent scalar update does not need a
    stop/start. Transport, tap/memif, and enabled changes still restart.
*   **Per-VNF write locks.** Replace the global `resourceMu` hold around child
    writes with a per-VNF lock so a DNS record change does not block VM or disk
    work.
*   **Child data sources.** Add read-only Terraform data sources for child rows
    if parent data sources are not enough for users who do not manage a child.
*   **Static-assignment lease conflict check.** Reject a static assignment whose
    address is actively leased to another client, with a `409` that names the
    holder. The current behavior is unchanged by this design.
*   **Bulk child apply for the UI.** A single request that applies a child diff
    would avoid one reload per child row while keeping per-item ownership in the
    API.

## Detailed Implementation Plan

### Implementation Order

1.  Write the four new protos, rewrite `vnf.proto` and `vnfd.proto`, regenerate,
    and get `make proto-check` clean. This fixes the wire contracts before any
    Go code depends on them.
2.  Add migration `0061`, the typed store, the preflight, and the migration and
    child-CRUD tests. Open a fixture database at schema 60 and prove the data
    move before changing the API.
3.  Add the legacy lease-time decoder in `internal/host` and its tests. This is
    independent of the API split and fixes an existing failure mode.
4.  Split the API validation and typed store writes, split the lifecycle
    service, add the `Backend.ReloadVNF` path, and update the `api.VNFManager`
    `Stop`/`Running`/`ReloadVNF` call sites.
5.  Add the four gateway services and the per-item child routes. Remove
    `/api/vnfs` and update the HTTP status and Location handling.
6.  Split the daemon into the four services, add the per-type reload RPCs and the
    `host.VNFManager.Reload` runtime path, update the persistence wrapper, and
    register the new services in `cmd/vnfs`.
7.  Update the UI list, forms, child editors, dialogs, and the networks tab, then
    rebuild the embedded bundle.
8.  Replace the Terraform provider's VNF resource and data source with the ten
    resources and four data sources, update the tests, README, and examples.
9.  Update the user docs and agent notes, run the full check suite, and run the
    manual migration and end-to-end smoke tests.

The reload path is the same for every runtime type:

```text
child write
  -> lifecycle: validate the full child set and the parent scalar config
  -> store: write the child row, bump parent updated_at
  -> Backend.ReloadVNF(vnf)
  -> vnfd.Client.ReloadVNF(vnf)
  -> DHCPDaemon.ReloadDHCP | DNSDaemon.ReloadDNS | BGPDaemon.ReloadBGP
  -> host.VNFManager.Reload(vnf)
  -> swap the runtime handler and update the desired spec
```

On failure the daemon restores the previous desired spec, the lifecycle restores
the previous child row and reloads the previous config, and if the restore also
fails the parent is disabled. NAT44 DNAT writes skip this path and replace the
VNF's VPP static mappings while NAT44 stays up.

### Target Files to Create/Modify

#### Protobuf and Generated Code

| File | Action | Rationale |
| --- | --- | --- |
| `design/proto/rookery/v1/dhcp_vnf.proto` | Create | Define `DHCPVNFService`, the DHCPv4/DHCPv6 parent and config messages, the per-item assignment and option messages, the lease view, and the runtime spec used by the daemon. |
| `design/proto/rookery/v1/dns_vnf.proto` | Create | Define `DNSVNFService`, the DNS parent and config messages, the record and forwarder resources, the merged record view, and the runtime spec. |
| `design/proto/rookery/v1/nat44_vnf.proto` | Create | Define `NAT44VNFService`, the NAT44 parent and config messages, the DNAT rule resource, and the session view. |
| `design/proto/rookery/v1/bgp_vnf.proto` | Create | Define `BGPVNFService`, the BGP parent and config messages, the advertised-prefix resource, and the BGP status view. |
| `design/proto/rookery/v1/vnf.proto` | Modify | Keep only the shared `VNFLease` message; remove `VNFService`, `VNF`, `VNFConfig`, and the union type-specific messages. |
| `design/proto/rookery/v1/vnfd.proto` | Modify | Replace `VNFDaemon` with `DHCPDaemon`, `DNSDaemon`, `BGPDaemon`, and `VNFRuntimeAdmin`; define the shared `RuntimeSpec`, per-type ensure and reload specs, the desired-runtime wrapper, and the per-type state/read messages. |
| `design/gen/rookery/v1/dhcp_vnf.pb.go` | Create | Generated message code for the DHCP service. |
| `design/gen/rookery/v1/dhcp_vnf.pb.validate.go` | Create | Generated PGV validation for the DHCP messages. |
| `design/gen/rookery/v1/dhcp_vnf.pb.gw.go` | Create | Generated grpc-gateway handlers for the DHCP HTTP routes. |
| `design/gen/rookery/v1/dhcp_vnf_grpc.pb.go` | Create | Generated gRPC client/server stubs for the DHCP service. |
| `design/gen/rookery/v1/dns_vnf.pb.go` | Create | Generated message code for the DNS service. |
| `design/gen/rookery/v1/dns_vnf.pb.validate.go` | Create | Generated PGV validation for the DNS messages. |
| `design/gen/rookery/v1/dns_vnf.pb.gw.go` | Create | Generated grpc-gateway handlers for the DNS HTTP routes. |
| `design/gen/rookery/v1/dns_vnf_grpc.pb.go` | Create | Generated gRPC client/server stubs for the DNS service. |
| `design/gen/rookery/v1/nat44_vnf.pb.go` | Create | Generated message code for the NAT44 service. |
| `design/gen/rookery/v1/nat44_vnf.pb.validate.go` | Create | Generated PGV validation for the NAT44 messages. |
| `design/gen/rookery/v1/nat44_vnf.pb.gw.go` | Create | Generated grpc-gateway handlers for the NAT44 HTTP routes. |
| `design/gen/rookery/v1/nat44_vnf_grpc.pb.go` | Create | Generated gRPC client/server stubs for the NAT44 service. |
| `design/gen/rookery/v1/bgp_vnf.pb.go` | Create | Generated message code for the BGP service. |
| `design/gen/rookery/v1/bgp_vnf.pb.validate.go` | Create | Generated PGV validation for the BGP messages. |
| `design/gen/rookery/v1/bgp_vnf.pb.gw.go` | Create | Generated grpc-gateway handlers for the BGP HTTP routes. |
| `design/gen/rookery/v1/bgp_vnf_grpc.pb.go` | Create | Generated gRPC client/server stubs for the BGP service. |
| `design/gen/rookery/v1/vnf.pb.go` | Modify | Regenerated with only the shared lease message. |
| `design/gen/rookery/v1/vnf.pb.validate.go` | Modify | Regenerated validation for the shared message. |
| `design/gen/rookery/v1/vnf.pb.gw.go` | Delete | The shared proto has no HTTP service; regeneration removes it. |
| `design/gen/rookery/v1/vnf_grpc.pb.go` | Delete | The shared proto has no gRPC service; regeneration removes it. |
| `design/gen/rookery/v1/vnfd.pb.go` | Modify | Regenerated for the four daemon services, per-type specs, and reload requests. |
| `design/gen/rookery/v1/vnfd.pb.validate.go` | Modify | Regenerated PGV validation for the daemon specs and reload requests. |
| `design/gen/rookery/v1/vnfd_grpc.pb.go` | Modify | Regenerated gRPC stubs for the four daemon services, including the reload methods. |
| `design/openapi/rookery.swagger.json` | Modify | Regenerated REST surface; `/api/vnfs` disappears and the four parent/child route groups appear. |

`Makefile` needs no change: `PROTO_FILES` already discovers every `.proto`, and
the generation target rewrites `design/gen` and the OpenAPI file.

#### Store and Migration

| File | Action | Rationale |
| --- | --- | --- |
| `internal/store/migrations/0061_per_type_vnfs.sql` | Create | Create the five parent and seven child tables, type triggers, the unique `(network_id, type)` index, backfill every JSON field, and drop `vnfs.config` in one transaction. |
| `internal/store/vnfs.go` | Create | Hold the VNF identity type, the typed write configs, the parent and child CRUD, the scalar and runtime hydration, the summary counts, the derived gateway/bind setter, and the preflight. This removes the VNF code from the 3,300-line `store.go`. |
| `internal/store/vnfs_ginkgo_test.go` | Create | Ginkgo specs for typed parent CRUD, child CRUD, unique keys, type triggers, cascade, label cleanup, one-per-type, and scalar-versus-runtime hydration. |
| `internal/store/vnf_migration_test.go` | Create | Standard tests for the 0061 preflight and backfill: one VNF of each type, every child shape, legacy lease times, bad type, duplicate child key, bad `dns_vnf_id`, and the pre-release `0061` stamp. |
| `internal/store/store.go` | Modify | Remove the VNF type definitions, VNF CRUD, `vnfSelect`, and `scanVNF`; call the preflight from `Open` before `migrate`; keep the migration runner and the other entities. |
| `internal/store/store_test.go` | Modify | Replace `CreateVNF`/`UpdateVNF` fixtures with typed helpers and keep the cascade and name-uniqueness coverage. |
| `internal/store/migration_test.go` | Modify | Update the existing `0008` migration assertion to read through the typed tables and add the 0061 migration path. |

`internal/store/labels.go`, `internal/store/vpp_addresses.go`, and the other store
files keep their SQL because the `vnfs` identity table and its label trigger
remain. No new Go dependency is needed; `modernc.org/sqlite` 1.62 uses SQLite
3.46, which supports JSON1, `DROP COLUMN`, and the triggers in this plan.

#### Host Runtime

| File | Action | Rationale |
| --- | --- | --- |
| `internal/host/vnf.go` | Modify | Replace the private lease-time parser with a shared decoder that accepts `time.ParseDuration` output and the legacy compact forms (`1h3`, `3`, `1h1`, `2h2`); add `Reload` to the manager so DHCP, DNS, and BGP swap their runtime config without removing the lease file or listener. |
| `internal/host/vnf_test.go` | Modify | Add table tests for every legacy lease-time form and reload tests that prove the lease file, listener, and existing runtime survive a config swap. |

The rest of `internal/host` keeps `store.VNFConfig` because it is the runtime
hydration shape; it does not read the removed `vnfs.config` column.

#### API and Store Backend

| File | Action | Rationale |
| --- | --- | --- |
| `internal/api/dhcp_vnf.go` | Create | Move DHCPv4/DHCPv6 normalization, static-assignment and option validation, lease-time decoding, DNS integration, and DHCP lease-range views out of the union file. |
| `internal/api/dns_vnf.go` | Create | Move DNS record, forwarder, and merged record-view validation and the DNS status/record helpers. |
| `internal/api/nat44_vnf.go` | Create | Move NAT44 gateway, outside, pool, and DNAT validation and the NAT session view; keep `nat44.go` for the VPP activation path. |
| `internal/api/bgp_vnf.go` | Create | Move BGP peer, family, transport, password, next-hop, and prefix validation and the BGP status view. |
| `internal/api/vnf_common.go` | Create | Hold shared name validation, the status view, the lease range check, the derived gateway/bind setter, and the reload/rollback helper used by both DHCP activation and NAT44. |
| `internal/api/vnfs.go` | Delete | Its union payload types and type-branching validation are replaced by the five files above; keeping it would leave the common path alive. |
| `internal/api/backend.go` | Modify | Replace `NormalizeVNFConfig(type, *VNFConfig)` with per-type normalizers, remove `vnfConfigPayloadFromProto`, add the proto-to-typed-config conversions, and implement `ReloadVNF` for the lifecycle backend. |
| `internal/api/server.go` | Modify | Change `VNFManager.Stop` and `Running` to receive the VNF record so the daemon client can select the per-type service, and add `ReloadVNF` to the manager interface. Keep the shared `Start`/`StartMemif`/`StartShared` methods. |
| `internal/api/nat44.go` | Modify | Use the typed DHCP config setter for NAT-adjusted gateway/bind values instead of writing the hydration union back through `UpdateVNF`; keep the VPP NAT44 rollback sequence. |
| `internal/api/network_vnf.go` | Modify | Replace the shared DNS/activation config writes with typed setters and `UpdateVNFIdentity`; keep the shared memif and BVI logic. |
| `internal/api/reconcile.go` | Modify | Pass the VNF record to `Running`, use `UpdateVNFIdentity` for tap/memif fields, and keep the single reconciliation pass. |
| `internal/api/memif.go` | Modify | Persist memif runtime fields through `UpdateVNFIdentity` instead of the removed union write. |
| `internal/api/api_test.go` | Modify | Move the VNF integration cases into the per-type test files, update the test harness and `VNFManager` fake signatures, and keep the non-VNF coverage. |
| `internal/api/dhcp_vnf_test.go` | Create | Cover DHCP create/update/delete/restart, v4/v6 validation, static assignment and option CRUD with reload, leases, DNS references, NAT-adjusted bindings, reload rollback, and wrong-type `404`. |
| `internal/api/dns_vnf_test.go` | Create | Cover DNS create/update/delete/restart, record and forwarder CRUD with reload, CNAME rules, merged record views, reload rollback, and wrong-type `404`. |
| `internal/api/nat44_vnf_test.go` | Create | Cover NAT44 create/update/delete/restart, DNAT CRUD with in-place mapping replacement, sessions, BVI rollback, and wrong-type `404`. |
| `internal/api/bgp_vnf_test.go` | Create | Cover BGP create/update/delete/restart, advertised-prefix CRUD with reload, status, password handling, and wrong-type `404`. |
| `internal/api/lease_cleanup_test.go` | Modify | Create DHCP VNFs through the typed helper and keep the orphaned-lease scenarios. |
| `internal/api/lease_cleanup_internal_test.go` | Modify | Update the internal fixtures to typed VNF creation. |

#### Lifecycle Service

| File | Action | Rationale |
| --- | --- | --- |
| `internal/lifecycle/vnfs.go` | Create | Hold the per-type parent create, update, delete, and restart methods, the DNS reference check, and the NAT-adjusted DHCP path. |
| `internal/lifecycle/vnf_children.go` | Create | Hold the per-item child create, update, and delete methods, the parent `updated_at` bump, the reload call, and the previous-row/config restore on failure. |
| `internal/lifecycle/lifecycle.go` | Modify | Keep the service struct, the error types, the `resourceMu`, and the network methods; replace the union `Backend.NormalizeVNFConfig` with the per-type normalizers and add `Backend.ReloadVNF`. |
| `internal/lifecycle/vnfs_test.go` | Create | Move the parent VNF lifecycle tests here; add per-type create/update/delete/restart and DNS-reference cases. |
| `internal/lifecycle/vnf_children_test.go` | Create | Cover child create/update/delete, unique conflicts, parent timestamp bumps, enabled and disabled parents, reload success, reload failure and restore, restart fallback when reload fails, and delete ordering. |
| `internal/lifecycle/lifecycle_test.go` | Modify | Keep the network and pipe coverage; remove the moved VNF cases and update the fake backend with `ReloadVNF`. |
| `internal/lifecycle/dns_integration_test.go` | Modify | Create the DNS and DHCP VNFs through typed helpers and keep the dynamic-record integration coverage. |

#### Gateway

| File | Action | Rationale |
| --- | --- | --- |
| `internal/gateway/dhcp_vnf.go` | Create | Implement `DHCPVNFService`: parent reads/writes/restart, child assignment and option routes, leases, status, and proto conversion. |
| `internal/gateway/dns_vnf.go` | Create | Implement `DNSVNFService`: parent reads/writes/restart, record and forwarder routes, the merged record view, status, and proto conversion. |
| `internal/gateway/nat44_vnf.go` | Create | Implement `NAT44VNFService`: parent reads/writes/restart, DNAT routes, sessions, status, and proto conversion. |
| `internal/gateway/bgp_vnf.go` | Create | Implement `BGPVNFService`: parent reads/writes/restart, advertised-prefix routes, status, and proto conversion. |
| `internal/gateway/vnf_common.go` | Create | Hold the per-type `VNFWriter` interface, the shared status/runtime interfaces, the child import and write error mapping, and the lease helper. |
| `internal/gateway/vnf_views.go` | Delete | Its shared status/lease/BGP/DNS view methods are replaced by per-type files. |
| `internal/gateway/vnf_writes.go` | Delete | Its generic `VNFWriter` and generic write methods are replaced by per-type files. |
| `internal/gateway/readonly.go` | Modify | Remove the `vnfService` struct, `ListVNFs`, `GetVNF`, and `vnfToProto`; keep the disk/media/network/interface/ACL/storage/host read services. |
| `internal/gateway/gateway.go` | Modify | Register the four services instead of `VNFService`, construct the four service structs, and keep the existing `WithVNFWriter`, `WithVNFStatusProvider`, and `WithVNFRuntime` options. |
| `internal/gateway/http_status.go` | Modify | Map each per-type create to `201` and each delete to `204`, and set the `Location` header to the matching parent path instead of `/api/vnfs/{id}`. |
| `internal/gateway/vnf_ginkgo_test.go` | Create | Cover all four services and every child route through the gateway, including wrong-type `404`, duplicate `409`, disabled-parent writes, status/lease/record/BGP/session views, and the native lifecycle writer. |
| `internal/gateway/vnf_native_test.go` | Delete | Its coverage moves into the Ginkgo service specs. |
| `internal/gateway/vnf_views_test.go` | Delete | Its coverage moves into the Ginkgo service specs. |
| `internal/gateway/vnf_writes_test.go` | Delete | Its coverage moves into the Ginkgo service specs. |
| `internal/gateway/vnf_validation_test.go` | Delete | Its validation cases move to the per-type request validation and the Ginkgo specs. |
| `internal/gateway/gateway_test.go` | Modify | Update the served path list, the registration expectations, and the removed `/api/vnfs` cases. |
| `internal/gateway/validation_test.go` | Modify | Replace the generic `CreateVNFRequest` validation case with the per-type request validation cases. |
| `internal/gateway/network_writes_test.go` | Modify | Create the DNS VNF through the typed store helper and keep the network-delete interaction. |
| `internal/gateway/ui_contract_test.go` | Modify | Update the VNF fixture and the API path assertions to the per-type routes. |

#### Daemon

| File | Action | Rationale |
| --- | --- | --- |
| `internal/vnfd/server.go` | Modify | Implement `DHCPDaemon`, `DNSDaemon`, `BGPDaemon`, and `VNFRuntimeAdmin`; add the per-type ensure/remove/reload/state/read methods, the desired-runtime wrapper, and the type-dispatching supervisor. Remove `EnsureVNF`, `RemoveVNF`, and `ListVNFStates`. |
| `internal/vnfd/client.go` | Modify | Add the four generated clients, per-type ensure/remove/reload/state/read calls, the `Stop`/`Running`/`ReloadVNF` type switch, and the per-type spec conversion. |
| `internal/vnfd/server_test.go` | Modify | Cover per-type ensure idempotency, spec-change restarts, reload success, reload failure restoring the previous desired spec, remove/state, supervision, persistence load of the new wrapper, and rejection of old `VNFSpec` files. |
| `internal/vnfd/client_test.go` | Modify | Cover the per-type client routing, dial targets, error mapping, the `Stop`/`Running` signatures, and reload routing. |
| `internal/vnfd/interceptor_test.go` | Modify | Update the traced method name from `EnsureVNF` to `EnsureDHCP`. |
| `cmd/vnfs/main.go` | Modify | Register the four daemon services on the gRPC server and keep the manager, persistence directory, and shutdown behavior. |

`internal/vnfd/interceptor.go` is method-agnostic and needs no change.

#### UI

| File | Action | Rationale |
| --- | --- | --- |
| `ui/src/inventory/vnfs/VNFsTab.tsx` | Modify | Fetch the four lists, merge and sort them, keep the tab count and `rookery:vnfs-changed` event, and route restart/delete to the parent type. |
| `ui/src/inventory/vnfs/VNFPage.tsx` | Modify | Become the typed form shell: choose the type, save the parent scalar fields, load child rows, apply the delete/update/create diff, and let each child write reload the runtime. No disable/apply/enable cycle. |
| `ui/src/inventory/vnfs/DHCPVNFForm.tsx` | Create | DHCPv4/DHCPv6 parent form: family, gateway, range, lease time, DNS, bind, transport, DNS integration, and enabled. |
| `ui/src/inventory/vnfs/DNSVNFForm.tsx` | Create | DNS parent form: forwarding, forwarders, and enabled. |
| `ui/src/inventory/vnfs/NAT44VNFForm.tsx` | Create | NAT44 parent form: gateway, outside interface/address/gateway, pool, and enabled. |
| `ui/src/inventory/vnfs/BGPVNFForm.tsx` | Create | BGP parent form: transport, AS/router/peer/local addresses, gateway, TTL, password, passive, table, next hop, and enabled. |
| `ui/src/inventory/vnfs/DHCPStaticAssignmentsEditor.tsx` | Create | Per-item MAC/IP editor with create/update/delete calls and duplicate-key error handling. |
| `ui/src/inventory/vnfs/DHCPOptionsEditor.tsx` | Create | Per-item code/format/value editor with the family-specific code range. |
| `ui/src/inventory/vnfs/DNSRecordsEditor.tsx` | Create | Per-item name/type/value/TTL editor with CNAME rules and duplicate errors. |
| `ui/src/inventory/vnfs/DNSForwardersEditor.tsx` | Create | Per-item forwarder editor with address and position. |
| `ui/src/inventory/vnfs/DNATRulesEditor.tsx` | Create | Per-item protocol/port/local-address editor with duplicate external-port errors. |
| `ui/src/inventory/vnfs/BGPAdvertisedPrefixesEditor.tsx` | Create | Per-item prefix editor with duplicate errors. |
| `ui/src/inventory/vnfs/VNFLeasesDialog.tsx` | Modify | Call `/api/dhcp-vnfs/{id}/leases`. |
| `ui/src/inventory/vnfs/VNFSessionsDialog.tsx` | Modify | Call `/api/nat44-vnfs/{id}/sessions`. |
| `ui/src/inventory/vnfs/VNFBGPStatusDialog.tsx` | Modify | Call `/api/bgp-vnfs/{id}/status`. |
| `ui/src/inventory/vnfs/VNFDNSRecordsDialog.tsx` | Modify | Call `/api/dns-vnfs/{id}/record-views` and render the static/dynamic sources. |
| `ui/src/inventory/vnfs/format.ts` | Modify | Add the per-type labels and summary helpers used by the merged table. |
| `ui/src/inventory/types.ts` | Modify | Replace `VNF`/`VNFConfig` with the discriminated parent union, child row types, counts, and the per-type status views. |
| `ui/src/inventory/networks/NetworksTab.tsx` | Modify | Merge the four lists instead of fetching `/api/vnfs`, and update the `VNF[]` event detail type. |
| `internal/webui/static/build/*` | Modify | Regenerated by `make ui-build`; do not edit by hand. Commit the new hashed bundle. |

`ui/src/inventory/vnfs/main.tsx`, `ui/src/inventory/main.ts`,
`ui/src/inventory/app.ts`, `ui/src/inventory/router.ts`, and
`ui/src/shared/api.ts` keep their current structure; the tab mount, full-page
route regex, and generic HTTP helper do not change.

#### Terraform Provider

| File | Action | Rationale |
| --- | --- | --- |
| `cmd/terraform-provider-rookery/internal/provider/resource_dhcp_vnf.go` | Create | Typed DHCP parent resource with family, scalar config, labels, enabled, computed status/counts, and import by VNF ID. |
| `cmd/terraform-provider-rookery/internal/provider/resource_dhcp_static_assignment.go` | Create | Per-item static assignment resource with `vnf_id`, `mac`, `ip`, and `vnf_id:child_id` import. |
| `cmd/terraform-provider-rookery/internal/provider/resource_dhcp_option.go` | Create | Per-item DHCP option resource with `vnf_id`, `code`, `format`, sensitive `value`, and import. |
| `cmd/terraform-provider-rookery/internal/provider/resource_dns_vnf.go` | Create | Typed DNS parent resource with forwarding, labels, enabled, and computed counts. |
| `cmd/terraform-provider-rookery/internal/provider/resource_dns_record.go` | Create | Per-item DNS record resource with name/type/value/ttl, sensitive `value`, and import. |
| `cmd/terraform-provider-rookery/internal/provider/resource_dns_forwarder.go` | Create | Per-item forwarder resource with address and position. |
| `cmd/terraform-provider-rookery/internal/provider/resource_nat44_vnf.go` | Create | Typed NAT44 parent resource with gateway/outside/pool fields, labels, and computed counts. |
| `cmd/terraform-provider-rookery/internal/provider/resource_nat44_dnat_rule.go` | Create | Per-item DNAT rule resource with protocol, external port, local address, and local port. |
| `cmd/terraform-provider-rookery/internal/provider/resource_bgp_vnf.go` | Create | Typed BGP parent resource with peers, addresses, transport, sensitive password, and computed counts. |
| `cmd/terraform-provider-rookery/internal/provider/resource_bgp_advertised_prefix.go` | Create | Per-item advertised-prefix resource with `vnf_id` and `prefix`. |
| `cmd/terraform-provider-rookery/internal/provider/data_source_dhcp_vnf.go` | Create | Read-only DHCP parent data source. |
| `cmd/terraform-provider-rookery/internal/provider/data_source_dns_vnf.go` | Create | Read-only DNS parent data source. |
| `cmd/terraform-provider-rookery/internal/provider/data_source_nat44_vnf.go` | Create | Read-only NAT44 parent data source. |
| `cmd/terraform-provider-rookery/internal/provider/data_source_bgp_vnf.go` | Create | Read-only BGP parent data source. |
| `cmd/terraform-provider-rookery/internal/provider/resource_vnf.go` | Delete | The generic `config_json` resource is retired. |
| `cmd/terraform-provider-rookery/internal/provider/resource_vnf_test.go` | Delete | Replaced by the typed resource tests. |
| `cmd/terraform-provider-rookery/internal/provider/data_source_vnf.go` | Delete | The generic data source is retired. |
| `cmd/terraform-provider-rookery/internal/provider/provider.go` | Modify | Register the ten resources and four data sources; remove the old VNF entries. |
| `cmd/terraform-provider-rookery/internal/provider/client.go` | Modify | Add the four generated service clients and remove the `VNFs` client. |
| `cmd/terraform-provider-rookery/internal/provider/client_test.go` | Modify | Update the client construction assertions. |
| `cmd/terraform-provider-rookery/internal/provider/helpers.go` | Modify | Add the child import-ID parser, the sensitive-value helpers, and the per-type status/count state mapping. |
| `cmd/terraform-provider-rookery/internal/provider/vnf_resources_test.go` | Create | Table-driven CRUD, plan, import, sensitive-field, and not-found tests for all ten resources and four data sources. |
| `cmd/terraform-provider-rookery/internal/provider/schema_test.go` | Modify | Update the expected resource and data source sets. |
| `cmd/terraform-provider-rookery/README.md` | Modify | Replace the `rookery_vnf` table, `config_json` section, import table, and examples with the ten resources and four data sources. |
| `cmd/terraform-provider-rookery/examples/complete/main.tf` | Modify | Replace the two `rookery_vnf` blocks with typed parents and child resources. |
| `cmd/terraform-provider-rookery/examples/data-sources/main.tf` | Modify | Replace the `rookery_vnf` data source with a typed parent data source. |

Every child resource write causes the server to reload the running runtime; the
provider does not need a reload attribute. A parent scalar, `enabled`, transport,
or tap/memif change still stops and starts the runtime, while a derived DHCP
gateway/`bind_ip` change reloads.

#### Documentation and Repository Notes

| File | Action | Rationale |
| --- | --- | --- |
| `docs/api.md` | Modify | Replace the `/api/vnfs` table and examples with the four parent services and every child route; document the hard cut and the reload-versus-restart rule. |
| `docs/architecture.md` | Modify | Update the VNF lifecycle diagram and prose from `EnsureVNF` to the four daemon services, typed storage, and the reload path. |
| `docs/networking.md` | Modify | Update the VNF section's API shape and the typed table references. |
| `docs/operations.md` | Modify | Add the stop/back-up/upgrade/start order and the preflight failure recovery for the hard cut. |
| `docs/agent-notes/vnfs.md` | Modify | Rewrite the RPC, record, endpoint, table, transport, and reload sections; keep the runtime behavior and sharp-edge notes. |
| `docs/agent-notes/api.md` | Modify | Update the gateway service list, VNF write path, reload path, routing, and status/lease/session views. |
| `docs/agent-notes/architecture.md` | Modify | Update the package map, the database table list, and the latest migration number. |
| `docs/agent-notes/terraform.md` | Modify | Replace the provider scope with the ten resources, four data sources, child reload behavior, and the state-migration procedure. |
| `docs/agent-notes/ui.md` | Modify | Update the VNF tab event payload, route deep links, per-type form/child editors, and the no-disable reload flow. |
| `AGENTS.md` | Modify | Set the latest migration to `0061_per_type_vnfs.sql`, remove the VNF `config` JSON exception from the typed-API rule, and update the API/gateway note with the reload path. |

### Verification Plan

**Compilation and generated artifacts**

*   `make proto-generate` followed by `make proto-check`; both must pass with no
    untracked or modified generated files after the check.
*   `make ui-typecheck` and `make ui-build`; the committed bundle must match the
    sources.
*   `go build ./...` and `make build`.

**Unit and integration tests**

*   `go test ./...` on a host with the normal prerequisites; `go test -short
    ./...` for the QEMU-free path.
*   Store: the Ginkgo VNF specs plus `internal/store/vnf_migration_test.go` and
    the updated `migration_test.go`.
*   API: the four per-type test files plus the updated lease-cleanup tests;
    include reload success, reload rollback, and NAT44 in-place mapping cases.
*   Lifecycle: the parent and child tests plus the DNS integration test;
    include reload success, reload failure restoring the previous row and
    config, and the disable fallback.
*   Gateway: the Ginkgo service specs plus the updated gateway, validation,
    network-write, and UI-contract tests.
*   Daemon: per-type server/client tests, reload success/failure, previous
    desired-spec restore, and the persistence/old-file cases.
*   Host: the legacy lease-time table tests and reload tests proving the lease
    file, listener, and runtime survive a config swap.
*   Provider: the table-driven resource/data-source tests and the schema test.

**Static analysis and coverage**

*   `go vet ./...`.
*   `gofmt -l .` must print nothing.
*   `golangci-lint run ./...`.
*   CI's 20% total statement coverage and 80% changed-line coverage; the new
    store, lifecycle, gateway, daemon, API, and provider tests are the coverage
    source for the changed lines.

**Migration verification**

*   Build a schema-60 database with one VNF of each type and every child shape,
    back it up, run the new `serve` against a copy, and confirm that all IDs,
    names, networks, labels, lease times, records, assignments, rules, and
    prefixes survive, and that `vnfs.config` is gone.
*   Run the preflight against a database with a duplicate MAC, a duplicate
    record, an unknown type, and a cross-network `dns_vnf_id`; each must fail
    with the row named and `user_version` unchanged.
*   Run the new server against a copy stamped with the pre-release `0061`; it
    must fail with the repair message, not start with missing tables.
*   Restore the backup and the previous binaries to prove the rollback path.

**End-to-end smoke**

*   Start `cmd/rookery serve` and `cmd/vnfs`; create a network, then one DHCPv4,
    one DHCPv6, one DNS, one NAT44, and one BGP VNF.
*   Create, update, and delete every child resource; confirm the runtime
    reloads, the process and lease file survive, and the parent's `updated_at`
    and counts change.
*   Confirm a child reload failure restores the previous row and config, and
    that a parent scalar, transport, or `enabled` change still performs a
    stop/start.
*   Confirm a BGP advertised-prefix reload keeps the session up and a NAT44 DNAT
    write updates the VPP mappings without stopping NAT44.
*   Read leases, the merged DNS records, NAT44 sessions, and BGP status from the
    new endpoints; confirm the old `/api/vnfs` routes return `404`.
*   Open `/vnfs`, create and edit each type, use every child editor and dialog,
    refresh each deep link, and confirm labels still load and save.
*   Apply the provider example against the local API, import each resource by ID
    and child ID, and run a second plan that must be empty.
*   Run `make test`, `go vet ./...`, `gofmt -l .`, `golangci-lint run ./...`,
    `make proto-check`, and the generated-bundle check before the final review.

--------------------------------------------------------------------------------

<!-- Provenance: Indicates initial structural drafting via EGM. -->

*Drafted with EGM*



