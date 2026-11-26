# Networking notes

VPP bridge domains, interfaces, ACLs, VRFs, routes, taps, NAT44, and reconciliation.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Bridge-domain pipes

Bridge-domain pipes (`network_pipes`, migration `0033_network_pipes.sql`) connect two Rookery
Networks with a VPP pipe pair. `GET`/`POST /api/network-pipes` and `DELETE /api/network-pipes/{id}`
manage them; a pair of networks can have only one pipe, and a network with a pipe cannot be deleted.
`internal/host.CreatePipe`/`EnsurePipe` create or find the pipe by VPP instance, attach both ends to
the selected bridge domains with a 1500-byte MTU, and roll back a newly created pipe on failure. The
API recreates recorded pipes after a VPP restart and refreshes their runtime indices. The generic
`/api/vpp/interfaces/{name}/addresses` endpoint recognizes the pipe endpoint names
(`pipe<instance>.0`/`.1`), so users can assign IPv4/IPv6 addresses to either end; those addresses
are persisted in `vpp_interface_addresses`, reapplied after restart, and cleaned up when the pipe
record is deleted. The UI exposes them as the **BD pipes** subtab with per-endpoint address actions.

## Networks and interfaces

Rookery Networks are VPP bridge domains (`networks`) and vhost-user interfaces
(`virtual_interfaces`). `POST /api/networks` and `POST /api/interfaces` provision VPP objects
through `internal/host.VPPNetwork`; attach/detach is DB-only and must be blocked while the target VM
is running. VM creation accepts optional `interface_ids`; VM update accepts `interface_ids` plus
`replace_interface_ids` (omitted preserves attachments; an empty list with the replace flag detaches
all) and must reject interface changes while QEMU is running. VM start turns each attached interface
into a QEMU vhost-user netdev; VM deletion detaches its interfaces. VPP state is in memory; on API
startup, bridge domains, vhost-user interfaces (by name), NAT44/BVI state, and VNF taps are
reconciled. QEMU chardevs use `reconnect=1`, so VMs started with the current arguments can reattach
after the vhost socket is recreated; VMs started by older servers without reconnect still need a
restart.

## ACLs

ACLs are named ordered permit/deny rule sets in `acls` (`0010_acls.sql`), with
`virtual_interfaces.ingress_acl_id`/`egress_acl_id` kept as the first entry of each direction for
compatibility and ordered assignments in `interface_acls` (`0016_interface_acls.sql`). Each rule
also has its own optional description (documentation only). Rules support CIDR prefixes, protocols,
and TCP/UDP port ranges; VPP enforcement uses the ACL plugin (`acl_add_replace`,
`acl_interface_set_acl_list`) and `vpp_index` persistence. ACLs are assigned by name through `PUT
/api/interfaces/{id}/acls`, which accepts repeated `ingress_acls`/`egress_acls` in evaluation order
(the legacy single `ingress_acl`/`egress_acl` fields still work); the UI labels ingress as **From
interface** and egress as **To interface**, with up/down ordering. Assignment requires VPP
readiness. Reconcile ACL definitions and interface assignments after vhost reconciliation and on VPP
restart. Deleting an assigned ACL is blocked. The UI lives under **Rookery Networks**, includes a
rule editor with full ACL preview, and shows assignments on interface rows.

## Route tables and VPP sentinels

Route tables are VPP IP FIB/VRF definitions in `route_tables` (`0017_route_tables.sql`); table `0`
is the implicit default for both families and is synthesized in `RouteTableService`, not stored.
Custom tables have a name, table ID, and IPv4/IPv6 family, with unique `(table_id, is_ipv6)` and
`(name, is_ipv6)`. `PUT /api/interfaces/{id}/route-tables` binds `ipv4_table_id`/`ipv6_table_id`;
`virtual_interfaces` stores both and defaults them to the owning network's `route_table_id` at
creation (migration `0036_route_table_names.sql` backfills existing all-zero rows), while
`interfaceSelect` joins the table names. Static entries live in `routes` (`0018_routes.sql`): table
ID/family, prefix, next hop, and an optional egress — either an inventory interface (`interface_id`)
or any VPP interface by name (`egress_interface`, added in `0019_route_egress_interface.sql` for
BVIs/network gateways; resolved via `SwIfIndexByName` from the `sw_interface_dump` at
install/reconcile time), plus a path `preference` metric — with a unique `(table_id, is_ipv6,
prefix)`. The next hop is optional when an egress is set, which installs an attached/glean route
(`ip route add <prefix> table N via <interface>`) instead of requiring ARP for a gateway; note that
a "via <gateway> dev bviX" route where the gateway *is* the BVI's own address can never resolve, so
interface-only routes are the way to import a bridge-domain subnet into a VRF. VPP's binary API has
a dangerous sentinel: a recursive path (next hop resolved by table lookup) must send `sw_if_index =
^uint32(0)` and a valid path `table_id` (the resolve table); Go zero values mean "interface 0 /
local0" and table 0, which produced silently unresolvable `via … local0` routes. `AddRoute` and
`AddBGPRoute` set the sentinels.

## FIB view, bindings, and route options

`GET`/`POST /api/route-tables/{id}/routes?ipv6=<bool>` list/add entries and `DELETE
/api/routes/{id}` removes one; routes in the default table use ID `0`. The UI's route editor offers
Rookery interfaces and network BVIs in the egress picker and a metric field. VPP uses
`ip_table_add_del`, `sw_interface_set_table`, and `ip_route_add_del`; startup reconciliation ensures
custom tables, reapplies interface bindings, then reinstalls routes. `GET
/api/route-tables/{id}/fib?ipv6=<bool>` reads the live VPP FIB with `ip_route_v2_dump`, including
connected, DHCP, ND, and adjacency entries with source names and paths; the route editor shows these
read-only as **Dynamic routes** for troubleshooting. Next hops resolve only within the route's own
table — a route in a non-default VRF needs that table to contain a route to the next hop (bind the
owning interface into the table, or add an egress-interface route). Deleting a custom table is
blocked while an interface or route references it. The UI has a **Route tables** subtab, an
interface-level binding dialog, and a per-table route editor. Next-hop and IPv4/IPv6 lookup options
store route-table names in `next_hop_table_name`/`ipv4_lookup_table_name`/`ipv6_lookup_table_name`
(resolved to current VPP table IDs at install/reconcile time; `default` means table 0) instead of
numeric IDs; MPLS lookup remains numeric. Numeric route options preserve explicit presence: blank
omits the CLI option, while `0` is persisted and emitted where supported. Host interfaces and host
taps can also be bound with `GET`/`PUT /api/vpp/interfaces/{name}/route-tables`; those bindings live
in `vpp_interface_route_tables` (`0034_vpp_interface_route_tables.sql`) and are reapplied after
VPP/API restarts. Vhost-user interfaces keep using
`virtual_interfaces.ipv4_table_id`/`ipv6_table_id`; the generic endpoints reject them. Route-table
interface counts include both sources, so a custom table cannot be deleted while a host interface or
tap uses it. A non-default binding overrides the interface's network VRF.

## CIDRs, VRFs, and reserved addresses

Every network requires an IPv4 and an IPv6 CIDR (UI defaults `10.0.0.0/16` and `fd00:20::/64`; the
API rejects missing/invalid CIDRs). `PUT /api/networks/{id}` updates both CIDRs and is blocked while
the network has VNFs, host taps, legacy DHCP configuration, or a BVI/gateway; the UI exposes this as
**Edit CIDRs**. Networks may reuse the same CIDRs because each has its own VPP VRF table
(`networks.route_table_id = 100000 + network ID`, IPv4 and IPv6); table IDs in that range are
reserved for networks and cannot be created manually. `ensureNetworkRouteTable` upserts
`route_tables` records (`network-<name>`, `network_id` set, `managed`) for both families so they
appear in the **Route tables** UI and accept static routes; `DeleteRouteTablesByNetwork` removes
them with the network. The BVI, shared network memif, per-VNF tap/memif, and vhost-user interfaces
are bound to the network table with `sw_interface_set_table`, so overlapping BVI addresses are
isolated. `reconcileVNFBindings` also moves already-running VNF interfaces into the network VRF
after an upgrade. The first five host addresses of each prefix are reserved for Rookery: `.1` is the
network BVI, `.2` is the shared VNF memif stack, and `.3`-`.5` are reserved. DHCP ranges, static
assignments, and DNAT local addresses must stay inside the network prefix and outside those reserved
addresses; NAT44's gateway must be the network BVI (`prefix + 1`). The IPv6 CIDR defines the allowed
IPv6 range and reservations; IPv6 BVI/memif address assignment will follow with shared DHCPv6.

## Host NIC takeover

Host NIC takeovers are persisted in `host_interfaces` (`0023_host_interfaces.sql`,
`internal/store/host_interfaces.go`). `POST /api/host/interfaces/{name}/attach` and `DELETE
/api/host/interfaces/{name}` manage them through `HostService`; the gateway validates that the NIC
is a physical, non-wireless interface without the host default route, calls
`host.VPPNetwork.EnsureHostInterface`, and stores `nic`, `host-<nic>`, sw_if_index, and purpose
(`manual` or `nat44`). NAT44 activation records its outside NIC automatically.
`ReconcileHostInterfaces` recreates every persisted `host-<nic>` after a VPP restart and refreshes
its sw_if_index; a stale/missing NIC is logged and retried. AF_PACKET host interfaces are created
with `rx-size 69632` and `rx-per-block 1` so GRO/GSO packets up to 64 KiB fit in one RX frame (VPP's
2048-byte default silently drops them as partial packets, which throttles downloads while uploads
stay fast). They are tagged `rookery-afpacket-rx69632-v1`; reconciliation recreates an older
untagged host interface once, and NAT44 is reapplied afterwards. The Linux NIC is also set to `gro
off`/`lro off` (RX) while `tso on`/`gso on` (TX) are kept enabled, because NAT44 rewriting coalesced
RX GSO packets causes heavy TCP retransmissions; this needs the `ethtool` binary. Rookery also pins
BVI, vhost-user, memif, and tap MTUs to 1500 instead of VPP's 9000 default. Detach refuses an
interface referenced by a NAT44 VNF and does not restore host IPs (renew DHCP on the NIC). The UI's
**Host interfaces** section has Attach/Detach actions for eligible NICs.

## Host taps

Network host taps are persisted in `network_host_taps` (`0026_network_host_taps.sql`,
`internal/store/network_host_taps.go`) and exposed as `GET`/`POST /api/host/taps` and `DELETE
/api/host/taps/{id}`. `POST` takes `network_id`, optional `name` (Linux limit 15, defaults to
`hostnet<id>`), and optional `ipv4_address` (CIDR matching the network prefix, outside the reserved
`.1`-`.5` addresses, DHCP range, and static assignments). `host.VPPNetwork.EnsureHostTap` creates a
tagged VPP tap (`rookery-host-tap:<name>`) in the network bridge domain, configures the Linux tap
(`ip addr replace`, link up), and is idempotent. `internal/api.Server.CreateNetworkHostTap` writes
the record first, binds the interface to the network VRF, and rolls back both VPP and the record on
failure; lifecycle deletion removes host taps before the bridge domain. `ReconcileNetworkHostTaps`
runs after bridge domains in startup/VPP-restart reconciliation, recreates missing taps, and
refreshes `sw_if_index`. Deleting a network removes its host taps. The UI's **Host taps** subtab
creates and deletes them.

## NAT44

NAT44 is IPv4-only (`internal/host/nat44.go`, `internal/api/nat44.go`). It creates a per-network BVI
(`bvi<id>`) and stores `bvi_host_name`, `bvi_sw_if_index`, and `ipv4_gateway` on `networks`
(`0009_nat44.sql`). Enabling it refuses any host default-route NIC and wireless NICs, flushes the
selected wired outside NIC, creates a VPP `host-<nic>` using the physical NIC's MAC (random MACs
break upstream ARP/switch port security), configures NAT44-ED via the binary API (interface features
are not idempotent in the VPP CLI), and uses a static outside address/default route or the VPP DHCP
client (which installs its leased address and default route). DNAT/port-forwarding rules live in
the `nat44_dnat_rules` typed child table and are applied as VPP NAT44 static mappings through
`Nat44AddDelStaticMappingV2`; use the outside interface's `sw_if_index` so the external address
follows DHCP changes, and tag mappings `rookery:<vnf-id>:<rule-index>` so applying/removing one VNF
never touches another VNF's rules. A DNAT child write replaces that VNF's mapping set in place
without stopping NAT44. Apply DNAT mappings after `ApplyNAT44` and remove them before
`RemoveNAT44`. Startup reconciliation order is bridge domains, enabled NAT44 VNFs, then DHCP VNFs. A
DHCPv4 VNF on a NAT network runs on `.2` while the BVI owns `.1`; `BindIP` is the server ID and must
not be compared with `gateway` in DHCP request filtering. Validate/take over the outside interface
before creating a BVI, roll back a newly created BVI when activation fails, and remove a leftover
BVI when deleting a NAT44 VNF unless another NAT44 or NAT-adjusted DHCPv4 VNF depends on it.

## Infrastructure reconciliation

On API startup, `internal/api.Server.RunInfrastructureReconciler` waits for VPP, ensures every
recorded network bridge domain exists with its stored ID (`EnsureBridgeDomain`), finds or recreates
every recorded vhost-user interface by name and updates its `sw_if_index`/socket metadata, restores
ACL definitions/assignments, restores enabled NAT44 VNFs, then re-ensures every enabled DHCP, DNS,
and BGP VNF through its typed daemon service, reusing an existing `vnf<id>` tap and recreating a
missing/migrated tap. A VPP restart removes
vhost-user interfaces, ACL state, NAT44 state, and VNF taps; API startup restores them; QEMU
reconnect lets current VMs reattach, while older QEMU processes may need a restart. While running,
`RunInfrastructureReconciler` control-pings the VPP API every five seconds and compares the API
socket identity; on restart it resets cached NAT/VNF runtime state and repeats the reconcile
sequence automatically. Reconciliation runs in a bounded background worker (2 minutes) so API
pings/identity checks continue; a further VPP restart queues another rebuild after the in-flight
worker finishes (workers must not overlap, because concurrent BVI creation aborts VPP). `vppctl`
calls have a 10-second timeout so a disappearing CLI socket cannot wedge the reconciler.

## VPP sockets and host interfaces

The VPP socket is the binary API socket (`socksvr`, default `/run/vpp/api.sock`), checked with
`go.fd.io/govpp`, not the CLI socket or a vhost-user socket. NAT44 also shells out to `vppctl`
(default CLI socket) for plugin/address/session commands, so it must be on `PATH` with CLI socket
access. `GET /api/host/interfaces` exposes physical and external host interfaces, link/address
state, and whether each has a matching `host-<nic>` interface in VPP; Rookery/VPP-created interfaces
(`vnf*`, `tap*`, `host-*`, etc.) are filtered out. Default-route takeover is blocked server-side.

## Interface counters

`GET /api/interfaces/counters` returns cumulative VPP RX/TX packet and byte counters for the
registered vhost-user interfaces (via the same `vppctl show interface <name>` path as VM metrics);
interfaces VPP has not recreated yet are omitted. The Interfaces tab polls it every 2s while visible
and VPP-ready and derives per-interface RX/TX rates (speed) from consecutive samples. The tab shell
keeps every tab component mounted and toggles panels with `hidden`, so the poll must gate on the
current route being inside the Network tab (not just section state) or it keeps firing on other
tabs. `DELETE /api/interfaces/counters` clears the counters for all VPP interfaces (`vppctl clear
interfaces`); the UI resets its rate baseline after it succeeds. Both RPCs must stay declared after
`GetInterface`/`DeleteInterface` in `interface.proto`: grpc-gateway registers handlers in
declaration order and tries the newest first, so the literal `/api/interfaces/counters` path must
beat the `/api/interfaces/{id}` pattern.

## TapCreateV2 sentinel

`TapCreateV2` must use `ID: ^uint32(0)` for automatic tap device allocation. Sending the Go zero
value requests device instance 0; after the first tap exists, later taps fail with `VppApiError:
Invalid interface (-71)`.
