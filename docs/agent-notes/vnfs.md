# VNFs notes

The separate `vnfs` daemon and its DHCP, DNS, and BGP runtimes.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Daemon process and RPCs

The API server and the DHCP/DNS/BGP runtimes are separate processes. `cmd/vnfs` owns
`host.VNFManager` (CoreDHCP, the DNS server, the gVisor memif stacks, and GoBGP) and exposes the
internal `VNFDaemon` gRPC service (`design/proto/rookery/v1/vnfd.proto`, no HTTP annotations):
`EnsureVNF` (idempotent start; restarts only when the spec hash changes), `RemoveVNF`,
`ResetRuntime`, `ListVNFStates`, `ListLeases`, `ListDNSRecords`, `GetBGPStatus`, and
`CleanupLegacyNetwork`. `rookery` connects through `internal/vnfd.Client` (implements
`api.VNFManager` and `gateway.VNFRuntime`) using `-vnf-addr` / `ROOKERY_VNF_ADDR` (default
`unix:///run/rookery/vnfs.sock`; `tcp://host:port` also works). The API still owns the database and
every VPP operation (memif/tap creation, BVIs, NAT44); the daemon opens the VPP binary API socket
itself only to install/withdraw BGP-learned routes, so a BGP session keeps updating the FIB while
the API restarts. The daemon persists desired specs under `<run-dir>/vnfs/desired-<id>.json`,
restores them on its own restart, and supervises runtimes (restarting dead ones after a 10s backoff;
`ResetRuntime` pauses supervision for 60s so the API can rebuild VPP resources first). VNF runtime
state is queried on demand (VNF views and reconciliation decisions), not by a background poll;
reconciliation runs at API startup and after a VPP restart, and the daemon restores its persisted
specs after its own restart. `serve` starts without the daemon; VNFs report `degraded` until it is
reachable. On API startup, `ReconcileVNFs` re-sends current specs once to already-running VNFs; the
daemon's spec hash makes this a no-op unless a desired spec is stale (for example after a daemon
upgrade). `vnfs` flags: `-vpp-socket`, `-listen`, `-run-dir`, `-vhost-dir` (env
`ROOKERY_VPP_SOCKET`, `ROOKERY_VNF_LISTEN`, `ROOKERY_RUN_DIR`, `ROOKERY_VHOST_DIR`).

## VNF records and endpoints

Network functions are first-class records in `vnfs` (`0008_vnfs.sql`): `dhcpv4`, `dhcpv6`, `nat44`,
`bgp`, and `dns` types, a network, an enable flag, typed JSON config, and tap metadata. A network
can have at most one VNF of each type; use separate networks for separate DHCP pools. The UI exposes
a **VNFs** tab; `POST /api/vnfs`, `PUT /api/vnfs/{id}`, `DELETE /api/vnfs/{id}`, `POST
/api/vnfs/{id}/restart`, `GET /api/vnfs/{id}/leases`, `GET /api/vnfs/{id}/sessions`, and `GET
/api/vnfs/{id}/dns-records` manage and inspect them.

## BGP runtime

BGP VNFs use the address family of `config.local_ip` (IPv4 or IPv6 unicast; peer, gateway, and
advertised prefixes must match). `config.transport` selects `memif` (gVisor netstack over VPP memif)
or `tap` (VPP tap plus kernel TCP). Both embed GoBGP in-process; memif uses a loopback TCP relay
because GoBGP requires kernel TCP. The global RIB enables both families (`Families: []uint32{0, 1}`
— GoBGP's AfiSafi index list), the peer negotiates the session family, and IPv6 advertisements use
`PathAttributeMpReachNLRI`. BGP peer fields are local/peer AS, router ID, peer/local addresses,
optional peer gateway, `config.next_hop` (overrides the NEXT_HOP announced with advertised prefixes;
defaults to `local_ip`; same address family), multihop TTL (defaults to 64 when unset so replies
survive routed topologies like a peer behind a BVI; TTL=1 only when explicitly configured), passive
mode, optional TCP MD5 password (tap only; memif rejects it), raw target VPP table ID, and
advertised prefixes. Imported best paths are installed in the target VPP table (IPv4 or IPv6) with
`ip_route_add_del_v2` using the CLI FIB source and withdrawn when the session closes or the VNF
stops; next hops come from `PathAttributeNextHop` (IPv4) or `PathAttributeMpReachNLRI` (IPv6). `GET
/api/vnfs/{id}/bgp` returns a live snapshot from the embedded GoBGP server (via
`bgpInstance.Status`): running/established flags, session state, peer address/AS, locally advertised
prefixes, and received prefixes with next hop and AS path; note GoBGP reports `NeighborIp` as the
literal string `"<nil>"` for locally originated paths, and its `ListPeer`/`ListPath` APIs are
callback-style. The UI exposes it as a **Status** dialog on BGP rows (`/vnfs/{id}/bgp`). GoBGP is
wired to VPP through `host.BGPRouteWriter`, set by `cmd/vnfs` with
`vnfManager.SetBGPRouteWriter(networkRunner)`. The memif gVisor side must use `vnfMemifBGPMAC`
(`02:4f:4d:…`), never `VNFMemifMAC` (`02:4d:4d:…`), because that MAC belongs to the VPP memif master
port; both ends claiming the same MAC breaks bridge-domain learning and the peer cannot connect to
`local_ip:179` (passive mode listens on 179 via gVisor, active mode dials `peer_ip:179`). The gVisor
stack answers ICMP echo for `local_ip` automatically; when `peer_gateway` is set it also becomes the
stack's default-route gateway so ICMP replies to routed (non-peer) sources survive the BVI hop, and
same-subnet replies stay on-link via the connected route. The BGP runtime logs its memif
socket/interface id/MAC, listen/dial addresses, the `127.0.0.1` relay ports, and every GoBGP session
state transition (`BGP session state changed`); the shared memif slave also logs connect/disconnect
events.

## Shared VNF transport

Shared VNF networking is the default for new and restarted DHCPv4 VNFs. `networks.vnf_transport`
defaults to `shared`; a VNF's `config.transport` overrides it with `shared`, `dedicated`, or `tap`.
Network creation creates one VPP memif master per shared network (`net<id>-vnf`, socket
`<memif-dir>/net-<id>.sock`, high-bit socket ID) and stores it in `networks.vnf_memif_*`;
reconciliation recreates it after a VPP restart. The first shared DHCPv4 activation creates the
network BVI (`bvi<id>`) owning the gateway `.1` and sets the VNF bind address to the next host
address `.2`; all DHCPv4 servers on the network share one gVisor stack on that memif. The daemon
must receive `host.NetworkVNFMemifID(networkID)` (the high-bit memif interface ID) for the shared
memif handshake, not the network's VPP `sw_if_index`; an ID mismatch makes VPP drop the slave after
an initial connect. DHCPv6 remains tap-only, and BGP keeps its dedicated memif/tap transports for
now. Running VNFs keep their old transport until restarted; after a VPP restart or VNF restart they
migrate to the shared default (the old per-VNF memif is deleted).

## DHCP runtime and leases

`internal/host.VNFManager` (inside `cmd/vnfs`) runs one in-process CoreDHCP server per enabled DHCP
VNF. Each DHCP VNF gets a `vnf<id>` VPP tap, a gateway address, an allocator, and a JSON lease file
under `<run-dir>/vnfs/<id>.json`. DHCPv4 uses the bitmap allocator; DHCPv6 is stateful IA_NA (no
RA/PD). DHCPv4 also supports `static_assignments` (unique MAC/IPv4 pairs): reserved addresses are
excluded from the dynamic pool, may sit inside or outside the DHCP range, and must not be the
gateway or service address. Static assignments are DHCPv4-only. Leases are surfaced by VNF ID, and
status is `running`, `disabled`, or `degraded`. Enabling/restarting needs VPP, UDP 67/547,
`SO_BINDTODEVICE`, and tap configuration, so it needs root or `CAP_NET_ADMIN` +
`CAP_NET_BIND_SERVICE` + `CAP_NET_RAW`. Failures must roll back the tap and leave the VNF disabled.

## DHCP options

DHCP VNFs additionally accept `config.dhcp_options` (`code`, `format`, `value`, max 64) for extra
DHCPv4/DHCPv6 options. `format` is `string`, `ipv4`, `ipv6`, `hex`, `uint8`, `uint16`, `uint32`, or
`domain-list` (DNS wire-format domain names). `host.NormalizeDHCPOptions` canonicalizes values and
rejects server-managed codes (v4: 1, 28, 51, 53, 54, 58, 59; v6: 1, 2, 3, 5, 6, 7, 8, 9, 13, 14, 25,
26) and duplicate codes. The values are JSON-encoded into the CoreDHCP plugin arguments; DHCPv4
applies them as generic options on every reply, while DHCPv6 only sends options the client requested
in its ORO. Custom router/DNS options (v4 3/6, v6 23) override the gateway/DNS fields because they
are applied after the defaults. Use code 15 for a domain name, 42 for NTP, 119/24 for the domain
search list. The UI suggests common codes, labels them, and auto-selects the matching format.

## DNS

DNS VNFs (`0008_vnfs.sql` config JSON; no schema change) run on the network's shared gVisor stack
too. `StartShared` dispatches DHCPv4 and DNS to one stack per network; the DNS listener binds
UDP/TCP 53 on the service address `.2` over IPv4 and IPv6 and does not create a tap or dedicated
memif. The shared stack receives both IPv4 and IPv6 service addresses from the API.
`config.dns_records` (name, type A/AAAA/CNAME/TXT/PTR, value, TTL; wildcard names such as
`*.lab1.test` are supported) is authoritative; `config.dns_forward` controls whether unknown names
are sent to `config.dns_forwarders` (IP or IP:port, default port 53). With forwarding enabled and no
forwarders, the daemon reads `/etc/resolv.conf`; with forwarding disabled, unknown names get
NXDOMAIN. Local records override forwarding. `activateSharedDNS` forces the shared memif even when
the network default transport is tap/dedicated, so DNS requires the VNF daemon but no per-VNF tap.
DHCPv4/DHCPv6 VNFs can select a DNS VNF on the same network with `config.dns_vnf_id`,
`config.dns_domain`, and `config.dns_ttl`; the daemon reconciles active lease files into dynamic
A/AAAA and PTR records every second, preferring DHCP FQDN, then hostname plus suffix, then a
MAC/DUID-derived name. Records are removed on release/expiry; deleting a DNS VNF is blocked while
referenced. The UI uses the **DNS** type in the VNF form, with a record editor and forwarding
controls; clients should use `.2` as their DNS server (set it in the DHCP VNF's `config.dns`). DNS
rows expose a **Records** dialog (`/vnfs/{id}/dns`) that lists configured static records and live
lease-derived records together; the endpoint merges store records with `VNFDaemon.ListDNSRecords`
output and still shows static records when the daemon is unavailable.

## DHCPv4 memif transports

DHCPv4 uses a memif transport by default. With the network default `shared` (or
`config.transport=shared`), one gVisor stack per network (`internal/host/memif_shared.go`) connects
to the network memif, owns the `.2/prefix` VNF address, and serves every DHCPv4 VNF on that network;
the network BVI owns `.1`. With `config.transport=dedicated`, the legacy per-VNF memif path
(`internal/host/memif_dhcp.go`, socket `<memif-dir>/<vnf-id>.sock`, interface name = VNF name) is
used; on non-NAT networks that stack owns the gateway address directly. `tap` falls back to a
per-VNF tap. VPP is always the memif master; the VNF daemon is the slave using
`go.fd.io/govpp/extras/gomemif/memif` (do not vendor or replace it). The shared stack binds UDP/67
on the DHCP `BindIP` (`.2`) and reuses the CoreDHCP plugin handler. `-dhcp-transport memif` /
`ROOKERY_DHCP_TRANSPORT=memif` enables memif at all; `tap` disables it. DHCPv6 still uses the tap
path. The memif socket directory is `-memif-dir` / `ROOKERY_MEMIF_DIR`.

## Legacy DHCP cleanup

Migration `0008_vnfs.sql` converts existing network-level DHCP settings into a DHCPv4 VNF. On the
first startup reconciliation or manual restart, `internal/api.cleanupLegacyNetworkVNF` removes the
old `ndn<network-id>` tap and legacy DHCP files. The old network DHCP API/UI is removed; do not add
new code that writes the legacy `networks.dhcp_*` columns.
