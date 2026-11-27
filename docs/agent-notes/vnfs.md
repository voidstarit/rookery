# VNFs notes

The separate `vnfs` daemon, typed VNF storage, and the DHCP, DNS, and BGP runtimes.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Daemon process and RPCs

The API server and the DHCP/DNS/BGP runtimes are separate processes. `cmd/vnfs` owns
`host.VNFManager` (CoreDHCP, the DNS server, the gVisor memif stacks, and GoBGP) and exposes four
internal gRPC services from `design/proto/rookery/v1/vnfd.proto` (no HTTP annotations):

| Service | Methods |
| --- | --- |
| `DHCPDaemon` | `EnsureDHCP`, `RemoveDHCP`, `ReloadDHCP`, `GetDHCPState`, `ListDHCPLeases`, `RemoveDHCPLeases` |
| `DNSDaemon` | `EnsureDNS`, `RemoveDNS`, `ReloadDNS`, `GetDNSState`, `ListDNSRecords` |
| `BGPDaemon` | `EnsureBGP`, `RemoveBGP`, `ReloadBGP`, `GetBGPState`, `GetBGPStatus` |
| `VNFRuntimeAdmin` | `ResetRuntime`, `CleanupLegacyNetwork` |

There is no `EnsureVNF`, `RemoveVNF`, or `ListVNFStates`, and no shared state list: the API asks
for the state of a known type. One server struct implements all four services and shares the
manager, desired-spec map, and supervision loop, but the wire boundaries stay separate.

`rookery` connects through `internal/vnfd.Client` (implements `api.VNFManager` and
`gateway.VNFRuntime`) using `-vnf-addr` / `ROOKERY_VNF_ADDR` (default
`unix:///run/rookery/vnfs.sock`; `tcp://host:port` also works). The API owns the database and
every VPP operation (memif/tap creation, BVIs, NAT44); the daemon opens the VPP binary API socket
itself only to install/withdraw BGP-learned routes, so a BGP session keeps updating the FIB while
the API restarts.

The daemon persists each desired spec as `<run-dir>/vnfs/desired-<id>.json`, a `DesiredRuntime`
wrapper with a DHCP, DNS, or BGP `oneof` (mode `0600`, atomic write). `Load` ignores every file
that does not parse and validate as that wrapper, including the old single-`VNFSpec` files; the
API's startup reconciliation re-sends every enabled VNF. If the daemon was not
reachable during that first pass, restart the VNF (or the API) to re-send it. The spec hash is
computed over the type-specific runtime spec (the DHCP, DNS, or BGP message), so an unchanged
spec is a no-op and a changed one restarts the runtime.
The supervision loop checks every 5 seconds and restarts a dead or unhealthy runtime after a 10s
backoff. `ResetRuntime` stops every runtime but keeps the desired specs and pauses supervision
for 60s so the API can rebuild VPP resources first; the runtimes are then re-ensured. VNF runtime
state is queried on demand (views, start/stop/restart, reconciliation decisions), never by a
background API poll. `serve` starts without the daemon; VNFs report `degraded` until it is
reachable.

`vnfs` flags: `-vpp-socket`, `-listen`, `-run-dir`, `-vhost-dir` (env
`ROOKERY_VPP_SOCKET`, `ROOKERY_VNF_LISTEN`, `ROOKERY_RUN_DIR`, `ROOKERY_VHOST_DIR`).

## Records, typed storage, and API shape

Every VNF keeps one `vnfs` identity row: `id`, `name`, `network_id`, `type`, `enabled`,
`tap_sw_if_index`, `tap_host_name`, `memif_sw_if_index`, `memif_socket`, `memif_host_name`,
`created_at`, and `updated_at`. Type-specific configuration and child rows live in typed tables
created by `internal/store/migrations/0061_per_type_vnfs.sql`:

| Type | Parent | Child tables |
| --- | --- | --- |
| DHCPv4 | `dhcpv4_vnfs` | `dhcpv4_static_assignments`, `dhcpv4_options` |
| DHCPv6 | `dhcpv6_vnfs` | `dhcpv6_options` |
| DNS | `dns_vnfs` | `dns_records`, `dns_forwarders` |
| NAT44 | `nat44_vnfs` | `nat44_dnat_rules` |
| BGP | `bgp_vnfs` | `bgp_advertised_prefixes` |

The migration drops `vnfs.config`. `vnfs_network_type_idx` uniquely enforces
`(network_id, type)`, so a network has at most one VNF of each type. `BEFORE INSERT` triggers on
each parent table reject a row whose `vnfs.type` does not match, and `vnfs_type_insert` /
`vnfs_type_update` reject unknown types and type changes. Child natural keys are unique in the
database: `(vnf_id, mac)` and `(vnf_id, ip)` for static assignments, `(vnf_id, code)` for options,
`(vnf_id, name, type, value)` for records, `(vnf_id, address)` for forwarders,
`(vnf_id, protocol, external_port)` for DNAT rules, and `(vnf_id, prefix)` for advertised
prefixes. DNS CNAME exclusivity stays in Go because it spans rows. Deleting a `vnfs` row cascades
through its parent and child rows.

`store.Open` runs a read-only preflight before migrating a schema-60 database. It checks known
types, valid JSON config objects, required scalars, child array shapes, same-network
`dns_vnf_id` targets, child-key collisions, and duplicate `(network_id, type)` rows. On a problem
it refuses to migrate, names the VNF IDs and fields, and leaves `PRAGMA user_version` at 60.
Migration 0061 is one SQL transaction. The preflight also rejects a pre-release 0061 stamp (version
61 with `vnfs.config` and no typed tables) with a restore-the-backup message.

`store.VNF` still carries a `Config VNFConfig` field because `host.VNFManager`, activation, and
reconciliation expect it, but `VNFConfig` is read-only runtime hydration. The old persisted and
protobuf-exposed `VNFConfig` is gone; this shape is not stored, not exposed in protobuf, and never
accepted by a write method. `store.ListVNFs` selects identity rows, closes the cursor, then hydrates
each row from its typed tables; the two-phase read matters
because the store uses a single SQLite connection and a nested query while rows are open would
deadlock. Typed store methods are the only write path.

The public API is four typed services and has no `/api/vnfs` route (it returns `404`). Parent and
child paths are documented in [`docs/api.md`](../api.md#vnf-services). A parent read carries
`status`, `status_detail`, counts, tap metadata, and timestamps; create/update requests carry
flat scalar fields only. The BGP password is write-only. A read or write for an ID of another
type returns `404`, duplicate child keys return `409`, invalid values return `400`, an unavailable
VNF runtime write returns `503`, and a failed reload returns `500`. `status` is `running`,
`disabled`, or `degraded`.

## Reload and restart

A child create, update, or delete writes one child row and bumps the parent `updated_at` in one
transaction. If the parent is enabled, the lifecycle then calls the type's reload RPC with the
full hydrated VNF. Reload keeps the process, listener, lease file, and desired spec in place:
DHCP swaps its handler, DNS swaps the record/forwarder handler behind its listener, and BGP
withdraws and re-advertises the path set on the running GoBGP server. A NAT44 DNAT write does not
use the daemon; it replaces the VNF's VPP static mappings while NAT44 and the BVI stay up. A
child write on a disabled parent only stores the row; the next enable loads every child row and
starts once. A child write on an enabled parent whose runtime is stopped or unreachable also only
stores the row and keeps the VNF `degraded`; the daemon still persists the prepared desired spec
when it is reachable, so supervision or a manual restart starts the new configuration. When the
daemon is unreachable, the API queues the VNF and the infrastructure reconciler retries the reload
once the daemon is back (re-ensuring it if the desired spec was lost); the child row is not left
waiting for a manual restart.

A parent create with `enabled=true` starts after the insert (a failed start removes the new
record). Any parent `PUT` stops and starts the enabled runtime, including a name-only or scalar
change, a transport/tap/memif change, and an `enabled` transition; `POST /{id}/restart` is the
manual stop/start and does not drop leases. If the store write for a parent update fails after
the old runtime was stopped (for example a duplicate name), the lifecycle re-activates the previous
record and reports the original status; the row is never left `enabled=true` with a stopped
runtime. The global `resourceMu` serializes parent and child writes with network and disk
provisioning; a reload is shorter than a restart but still holds the lock.

Reload failure handling: the daemon applies the manager change before replacing the desired spec,
so a failed reload keeps the previous spec. The lifecycle restores the previous child row,
reloads the previous configuration, and returns `500`. If that restore reload also fails, it sets
the parent `enabled=false` and returns a `500` message naming the reload, restore, and disable
failures (also logged). A reload on a runtime that is not running returns `running=false` without
starting it, persists the new desired spec, and leaves the VNF degraded. Delete order stays:
stop the runtime, delete the identity row (cascade typed rows), then clean up the NAT44 BVI where
applicable. Each `Remove{DHCP,DNS,BGP}` first verifies that the stored desired runtime is of the
service's type; a mismatched ID returns `NotFound`. Deleting a DNS VNF is rejected with `409` while a DHCP VNF references it.

## Transports and shared stack

Shared VNF networking is the default for new and restarted DHCPv4 VNFs. `networks.vnf_transport`
defaults to `shared`; a DHCPv4 parent `transport` overrides it with `shared`, `dedicated`, or
`tap` (`""` follows the network default, and the legacy `memif` alias is normalized to
`dedicated`). Network creation creates one VPP memif master per shared network (`net<id>-vnf`,
socket `<memif-dir>/net-<id>.sock`, high-bit socket ID) and stores it in `networks.vnf_memif_*`;
reconciliation recreates it after a VPP restart. The first shared activation creates the network
BVI (`bvi<id>`) owning the gateway `.1` and assigns `.2` to the shared gVisor stack. All DHCPv4
servers and the DNS server on that network share one stack on that memif.

The daemon must receive `host.NetworkVNFMemifID(networkID)` (the high-bit memif interface ID) for
the shared memif handshake, not the network's VPP `sw_if_index`; an ID mismatch makes VPP drop the
slave after an initial connect. DHCPv6 is tap-only: the server normalizes any family-6 transport
to `tap`, and `dhcpv6_vnfs.transport` only accepts `''` or `tap`. BGP has its own `transport`
(`memif` or `tap`). A running VNF keeps its old transport until restarted; after a VPP restart or
VNF restart DHCPv4 migrates to the shared default and the old per-VNF memif is deleted.

## DHCP runtime and leases

`internal/host.VNFManager` (inside `cmd/vnfs`) runs one in-process CoreDHCP server per enabled
DHCP VNF. Each DHCP VNF uses a shared network memif stack or a `vnf<id>` VPP tap, a gateway
address, an allocator, and a JSON lease file under `<run-dir>/vnfs/<id>.json`. DHCPv4 uses the
bitmap allocator; DHCPv6 is stateful IA_NA (no RA/PD). DHCPv4 also owns
`/api/dhcp-vnfs/{vnf_id}/static-assignments` rows (unique MAC/IPv4 pairs): reserved addresses are
excluded from the dynamic pool, may sit inside or outside the DHCP range, and must not be the
gateway or service address. Static assignments are DHCPv4-only. `setupCoreDHCP` receives
positional arguments with two optional trailing JSON arguments (static assignments at index 9,
options at 10), and `buildDHCPv4Config` always sends both; parse each optional argument by its
position, never by the total argument count, or reservations are silently dropped. Leases are
surfaced by VNF ID
through `ListDHCPLeases` and the public leases route; status is `running`, `disabled`, or
`degraded`. Enabling/restarting needs VPP; a tap-backed runtime also needs UDP 67/547,
`SO_BINDTODEVICE`, and tap configuration, so it needs root or `CAP_NET_ADMIN` +
`CAP_NET_BIND_SERVICE` + `CAP_NET_RAW`. Failures roll back new runtime resources and leave the VNF
disabled.

`lease_time` is stored as text. The API normalizes new writes through
`host.DecodeLeaseTime` and stores `time.Duration.String()`, so `12h` reads back as `12h0m0s`.
The shared decoder also accepts the legacy compact forms written before the split: `1h3` is
1h30m, `3` is 30m, `1h1` is 1h10m, and `2h2` is 2h20m. `host.vnfLeaseTime` uses the same
decoder, so a migrated row keeps its original meaning; the migration copies the text unchanged.

## DHCP options

DHCP options are child rows under `/api/dhcp-vnfs/{vnf_id}/options` (`code`, `format`, `value`,
max 64 per VNF). `format` is `string`, `ipv4`, `ipv6`, `hex`, `uint8`, `uint16`, `uint32`, or
`domain-list` (DNS wire-format domain names). `host.NormalizeDHCPOptions` canonicalizes values and
rejects server-managed codes (v4: 1, 28, 51, 53, 54, 58, 59; v6: 1, 2, 3, 5, 6, 7, 8, 9, 13, 14,
25, 26) and duplicate codes. The values are JSON-encoded into the CoreDHCP plugin arguments;
DHCPv4 applies them as generic options on every reply, while DHCPv6 only sends options the client
requested in its ORO. Custom router/DNS options (v4 3/6, v6 23) override the gateway/DNS fields
because they are applied after the defaults. Use code 15 for a domain name, 42 for NTP, 119/24
for the domain search list. The UI suggests common codes, labels them, and auto-selects the
matching format.

## DNS

DNS VNFs run on the network's shared gVisor stack. `StartShared` dispatches DHCPv4 and DNS to one
stack per network; the DNS listener binds UDP/TCP 53 on the service address `.2` over IPv4 and
IPv6 and does not create a tap or dedicated memif. The shared stack receives both IPv4 and IPv6
service addresses from the API.

`dns_records` rows (name, type A/AAAA/CNAME/TXT/PTR, value, TTL; wildcard names such as
`*.lab1.test` are supported) are authoritative; the `dns_vnfs.forward` flag controls whether
unknown names go to the `dns_forwarders` rows (IP or IP:port, default port 53). With forwarding
enabled and no forwarders, the daemon reads `/etc/resolv.conf`; with forwarding disabled, unknown
names get NXDOMAIN. Local records override forwarding. `activateSharedDNS` forces the shared memif
even when the network default transport is tap/dedicated, so DNS requires the VNF daemon but no
per-VNF tap. DHCPv4/DHCPv6 parents can select a DNS VNF on the same network with `dns_vnf_id`,
`dns_domain`, and `dns_ttl`; the daemon reconciles active lease files into dynamic A/AAAA and PTR
records every second, preferring DHCP FQDN, then hostname plus suffix, then a MAC/DUID-derived
name. Records are removed on release/expiry; deleting a DNS VNF is blocked with `409` while
referenced.

The UI uses the **DNS** type in the VNF form, with per-item record and forwarder editors plus a
forwarding control; clients should use `.2` as their DNS server (set it in the DHCP parent
`dns`). DNS rows expose a **Records** dialog (`/vnfs/{id}/dns`) backed by
`/api/dns-vnfs/{id}/record-views`, which merges `dns_records` with `ListDNSRecords` output. Static
records stay visible when the daemon is unavailable and `dynamic_error` reports the failure.

## BGP runtime

BGP VNFs use the address family of `local_ip` (IPv4 or IPv6 unicast; peer, gateway, and
advertised prefixes must match). The parent `transport` selects `memif` (gVisor netstack over VPP
memif) or `tap` (VPP tap plus kernel TCP). Both embed GoBGP in-process; memif uses a loopback TCP
relay because GoBGP requires kernel TCP. The global RIB enables both families
(`Families: []uint32{0, 1}` — GoBGP's AfiSafi index list), the peer negotiates the session family,
and IPv6 advertisements use `PathAttributeMpReachNLRI`. BGP peer fields are local/peer AS, router
ID, peer/local addresses, optional peer gateway, `next_hop` (overrides the NEXT_HOP announced with
advertised prefixes; defaults to `local_ip`; same address family), multihop TTL (defaults to 64
when unset so replies survive routed topologies like a peer behind a BVI; TTL=1 only when
explicitly configured), passive mode, optional TCP MD5 password (tap only; memif rejects it), raw
target VPP table ID, and advertised-prefix child rows. Imported best paths are installed in the
target VPP table (IPv4 or IPv6) with `ip_route_add_del_v2` using the CLI FIB source and withdrawn
when the session closes or the VNF stops; next hops come from `PathAttributeNextHop` (IPv4) or
`PathAttributeMpReachNLRI` (IPv6). The password is never returned by a read.

`GET /api/bgp-vnfs/{id}/status` returns a live snapshot from the embedded GoBGP server (via
`bgpInstance.Status`): running/established flags, session state, peer address/AS, locally
advertised prefixes, and received prefixes with next hop and AS path. Note GoBGP reports
`NeighborIp` as the literal string `"<nil>"` for locally originated paths, and its
`ListPeer`/`ListPath` APIs are callback-style. The UI exposes it as a **Status** dialog on BGP
rows (`/vnfs/{id}/bgp`). GoBGP is wired to VPP through `host.BGPRouteWriter`, set by `cmd/vnfs`
with `vnfManager.SetBGPRouteWriter(networkRunner)`.

The memif gVisor side must use `vnfMemifBGPMAC` (`02:4f:4d:…`), never `VNFMemifMAC`
(`02:4d:4d:…`), because that MAC belongs to the VPP memif master port; both ends claiming the
same MAC breaks bridge-domain learning and the peer cannot connect to `local_ip:179` (passive mode
listens on 179 via gVisor, active mode dials `peer_ip:179`). The gVisor stack answers ICMP echo
for `local_ip` automatically; when `peer_gateway` is set it also becomes the stack's
default-route gateway so ICMP replies to routed (non-peer) sources survive the BVI hop, and
same-subnet replies stay on-link via the connected route. The BGP runtime logs its memif
socket/interface id/MAC, listen/dial addresses, the `127.0.0.1` relay ports, and every GoBGP
session state transition (`BGP session state changed`); the shared memif slave also logs
connect/disconnect events.

## Legacy cleanup and upgrades

Migration `0061_per_type_vnfs.sql` is the hard cut: an old binary cannot read the typed schema and
the new server cannot write `vnfs.config`. The rollout is stop API and daemon, back up SQLite,
upgrade API/daemon/UI/provider together, start the API (preflight + migration), then start the
daemon (reconciliation re-sends enabled specs). Rollback restores the backup and the previous
binaries. Old daemon state files are ignored, not imported; API reconciliation recreates them.

Migration `0008_vnfs.sql` converted existing network-level DHCP settings into a DHCPv4 VNF. On the
first startup reconciliation or manual restart, `internal/api.cleanupLegacyNetworkVNF` removes the
old `ndn<network-id>` tap and legacy DHCP files. The old network DHCP API/UI is removed; do not
add new code that writes the legacy `networks.dhcp_*` columns.
