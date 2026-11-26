# Troubleshooting notes

VPP graph, trace, ACL counter, and packet-capture tooling.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## VPP graph and packet trace

The Network tab includes a **Troubleshoot** section. `GET /api/troubleshoot/vpp-graph` runs `vppctl
show vlib graphviz filter`, parses the active nodes and edges, and returns them as JSON; the UI
renders a dependency-free layered top-to-bottom SVG with input nodes at the top, terminal/drop nodes
at the bottom, barycenter ordering to reduce crossings, and dashed feedback edges. Interface-related
nodes are ringed and a **Focus** picker filters the graph to one interface group or packet feature
(ACL plugin, NAT44, DHCP) plus its direct neighbors; hover isolates a node and its direct neighbors,
and labels/edges for everything else are hidden. It supports wheel/button zoom, drag-to-pan, Fit,
and a Labels toggle. VPP's CLI pager can split long DOT node lines across physical lines, so join
quoted statements until complete instead of parsing one statement per line. Node size represents
cycles/packet and color represents packets/call. Interface nodes only exist in the graph when active
since the last stats clear. Packet capture lives in the separate capture endpoints described below.
The same Troubleshoot UI has packet trace controls: `POST /api/troubleshoot/trace` clears the trace
and runs `vppctl trace add <node> <count>` (count 1–1000), `GET /api/troubleshoot/trace` returns
`vppctl show trace` output, and `DELETE /api/troubleshoot/trace` clears it. The node picker is
populated from `GET /api/troubleshoot/trace/nodes`, which runs `show vlib graphviz` and returns
every registered VPP node (not only active nodes); it also accepts typed names. Use `runCLIRaw` for
`show trace` because packet text may contain words like "invalid" that the generic CLI failure
heuristic would misclassify. Keep the `vppctl` timeout and VPP readiness checks when extending these
endpoints.

## ACL counters

`GET /api/troubleshoot/acl-counters` exposes per-interface ACL rule-match counters by combining
`show acl-plugin lookup context` with `show acl-plugin tables applied`. The `hitcount` values are
explicit per-rule matches per lookup context (ingress/egress), split by action into denied/permitted
and returned with ACL/rule indexes; implicit-deny packets are not counted. The UI shows compact
totals and expandable per-rule counters in the Interfaces tab, where ACLs are assigned. Keep
`VPPInterfaces` for the sw_if_index-to-name mapping.

## Packet capture

Packet capture is managed by `internal/host/pcap.go` through `vppctl pcap trace`. VPP writes only a
basename under `/tmp`, so capture files are named `rookery-<timestamp>-<random>.pcap` and read from
`/tmp`. The API keeps one active capture; `max <packets>` is derived from the requested max size and
snapshot length (`ceil(max_bytes/(snap_length+16))`, capped at 1,000,000). Capture
listing/download/delete is available even when VPP is down; the active file cannot be deleted until
capture stops. Endpoints live under `/api/troubleshoot/capture`; `VPPInterfaces` uses govpp for the
picker. Like NAT44, capture requires the VPP CLI socket and root/CLI access.
