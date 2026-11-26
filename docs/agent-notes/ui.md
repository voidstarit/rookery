# UI notes

Preact/TypeScript sources, generated bundles, HTTP client, VNF inventory, and console.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Sources and generated bundles

UI sources live under `ui/src`; `make ui-build` typechecks with `tsc` and bundles with esbuild into
`internal/webui/static/build`, which is committed and embedded by `go:embed static`. Edit sources,
not generated bundles. `make build` builds the UI and Go binary; `make ui-watch` rebuilds on change.
SQL migrations (`internal/store/migrations`) also use `go:embed`; keep runtime assets inside those
trees and avoid CDNs. noVNC v1.7.0 is vendored under `internal/webui/static/novnc` (MPL 2.0) and is
imported as an external browser module by the console bundle; do not replace it with a CDN. The UI
is fully Preact/TypeScript: the console page (`ui/src/console`) and inventory tabs
(`ui/src/inventory/{vms,disks,media,networks,vnfs}`) are components, while `ui/src/inventory/app.ts`
is the typed tab/readiness shell. The shell and tab islands communicate through `rookery:*` window
CustomEvents (`vms-changed`, `add-vm`, `readiness-changed`, `networks-changed`, `interfaces-data`,
`interfaces-changed`, `add-network`, `open-tab`, `disks-data`, `disks-changed`, `add-disk`,
`media-data`, `media-changed`, `add-media`, `vnfs-data`, `vnfs-changed`, `vpp-ready`, `add-vnf`,
`refresh-networks`, `tab-changed`); keep these bridges in sync. Inventory routing is path-based
(`ui/src/inventory/router.ts`): tabs are `/vms`, `/disks`, `/media`, `/networks`, `/vnfs`; dialogs
use deep links such as `/vms/new`, `/vms/{uuid}/edit`, `/interfaces/{id}/acls`, and
`/vnfs/{id}/leases`. `internal/webui.Handler` must keep serving `index.html` for HTML navigations
that do not match an embedded asset, or refreshing a deep link returns 404.

## VNFs

The **VNFs** tab fetches `/api/dhcp-vnfs`, `/api/dns-vnfs`, `/api/nat44-vnfs`, and
`/api/bgp-vnfs` with `Promise.allSettled`, tags each record with its type, merges and sorts them by
name, and renders one table with one `rookery:vnfs-changed` event. A failing type keeps the
previous rows for that type and shows a partial-list banner instead of blanking the tab. The event
payload is the merged `VNF[]` list; `rookery:vnfs-data` accepts a pushed `VNF[]`, and
`rookery:add-vnf` carries `{ networkId, type? }` to open the create page, scoped to a network
and/or a type. The tab's header and empty state render one **Add** button per type
(`dhcpv4`, `dhcpv6`, `dns`, `nat44`, `bgp`); each opens `/vnfs/new?type=<type>`, which preselects
and locks the type. A generic `/vnfs/new` (no `type`) still shows the type selector.
`NetworksTab.tsx` consumes the same merged list and re-fetches the four endpoints.

Deep links stay `/vnfs/{id}/edit`, `/vnfs/{id}/leases`, `/vnfs/{id}/sessions`,
`/vnfs/{id}/bgp`, and `/vnfs/{id}/dns`; the route effect resolves the ID from the merged list, so
a refresh works. `new` and `edit` are full pages; leases, sessions, BGP status, and DNS records are
dialogs whose endpoints are `/api/dhcp-vnfs/{id}/leases`, `/api/nat44-vnfs/{id}/sessions`,
`/api/bgp-vnfs/{id}/status`, and `/api/dns-vnfs/{id}/record-views`.

`VNFPage.tsx` is the typed create/edit shell. It renders a per-type form (`DHCPVNFForm`,
`DNSVNFForm`, `NAT44VNFForm`, or `BGPVNFForm`) and per-type child editors: static assignments and
options for DHCP, records and forwarders for DNS, DNAT rules for NAT44, and advertised prefixes for
BGP. Saving a
new VNF creates the parent with `enabled=false`, writes child rows in delete-then-update-then-create
order, then sends the requested `enabled` transition once. Saving an edit sends only changed parent
scalars, then the child diff, then the enabled transition if it changed. There is no
disable/apply/enable cycle: each child write reloads the running runtime, and a failed child write
reloads the child lists and shows the server's error while the server has restored the previous
row and configuration.

## HTTP client

Inventory UI HTTP calls go through `ui/src/shared/api.ts`. It shares in-flight GETs, retries
transient failures after 1s/2s/4s/10s, then fails with an `APIError` naming the method and path.
Mutations only retry `429`/`503` to avoid duplicate creates. Do not add unbounded retries or
unguarded polling loops.

## VM console

`GET /ws/vms/{id}/vnc` proxies the WebSocket to the VM's VNC Unix socket. Keep the
`responseRecorder` in `internal/api/server.go` implementing `http.Hijacker` (and `Flush`), or
WebSocket upgrades break. The UI console page is `/console.html?vm=<uuid>` and uses the vendored
noVNC client. It includes a sticky-modifier virtual keyboard; key definitions use noVNC's `KeyTable`
keysyms and standard `KeyboardEvent.code` scancodes. The console **Paste** dialog can type text as
US keyboard events (the reliable fallback for guests without clipboard tools) or send it through
noVNC `clipboardPasteFrom`, optionally reads the browser clipboard, and falls back to a manual text
box when clipboard permissions are unavailable.
