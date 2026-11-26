# Terraform notes

Terraform provider resources, data sources, and build.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Provider scope

`cmd/terraform-provider-rookery/` is a command in the main Go module (framework v1.18) implementing
`rookery_*` resources and matching data sources over the mTLS gRPC endpoint: storage backends,
disks, disk throttle classes, media, networks, network pipes, interfaces, host interfaces, host
taps, VPP interface addresses and route-table bindings, ACLs, route tables, routes, typed VNF
resources, VMs, LightVM kernels/initramfs images and instances (`rookery_lightvm_kernel`,
`rookery_lightvm_initramfs`, `rookery_lightvm`), host cgroup settings with reusable classes
(`rookery_cgroup_settings`), startup scripts, plus a `rookery_status` data source. LightVM artifact
resources stream `source_path` files over the `LightVMArtifactService.UploadArtifact` gRPC RPC or
register a host `path`; `upload_timeout`/`ROOKERY_UPLOAD_TIMEOUT` bounds uploads. VM resources and
data sources expose `cgroup_class` for assigning a class; host cgroup classes carry CPU, memory,
weight, and per-device I/O limits. Provider config accepts CA/client/key file paths or inline PEM
and `server_name`; the client uses TLS 1.3, verifies the CA, and always presents the client
certificate. The provider intentionally does not manage VM power actions. `rookery_route` exposes
all VPP route path options (`route_count`, next-hop/lookup tables, weights, UDP encapsulation,
resolve/rx flags, labels, drop, and description); optional numeric fields are nullable so an omitted
value stays unset while an explicit `0` is sent to the API. `rookery_disk` exposes template
`provisioning_script`/`provisioning_timeout`, clone `provisioning_json`, and `throttle_class_id`;
`rookery_disk_throttle_class` and its data source manage the reusable policy. Build with `go build
./cmd/terraform-provider-rookery`; `examples/complete`, `examples/data-sources`, and
`examples/lightvm` are validated with Terraform `dev_overrides`.

## VNF resources

`rookery_vnf` and its data source are retired. The provider exposes ten typed VNF resources and
four parent data sources; child resources require `vnf_id` (forces replacement) and import by
`<vnf_id>:<child_id>`.

| Resource | Parent | Key fields |
| --- | --- | --- |
| `rookery_dhcp_vnf` | none | `family`, name, network, enabled, DHCP scalars, labels |
| `rookery_dhcp_static_assignment` | DHCPv4 | `vnf_id`, `mac`, `ip` |
| `rookery_dhcp_option` | DHCP | `vnf_id`, `code`, `format`, sensitive `value` |
| `rookery_dns_vnf` | none | name, network, enabled, `forward`, labels |
| `rookery_dns_record` | DNS | `vnf_id`, `name`, `type`, sensitive `value`, `ttl` |
| `rookery_dns_forwarder` | DNS | `vnf_id`, `address`, `position` |
| `rookery_nat44_vnf` | none | name, network, enabled, NAT44 scalars, labels |
| `rookery_nat44_dnat_rule` | NAT44 | `vnf_id`, `protocol`, `external_port`, `local_ip`, `local_port` |
| `rookery_bgp_vnf` | none | name, network, enabled, BGP scalars, write-only sensitive `password` |
| `rookery_bgp_advertised_prefix` | BGP | `vnf_id`, `prefix` |

Data sources: `rookery_dhcp_vnf`, `rookery_dns_vnf`, `rookery_nat44_vnf`, and `rookery_bgp_vnf`,
each looked up by `id` or `name`. Parent resources expose computed `id`, `network_name`, status,
tap metadata, counts, and timestamps; `network_id` and the DHCP `family` force replacement. There
is no `config_json` and no `type` attribute. `enabled` defaults to `true` on create; set it to
`false` when the parent should stay inert. A child write still reloads a running runtime rather
than restarting it.

## VNF child reload behavior

A child create, update, or delete reloads the running DHCP, DNS, or BGP runtime in place (or
re-applies NAT44 DNAT mappings to VPP), without restarting the process or dropping leases. A
parent scalar, transport, tap/memif, or `enabled` change still stops and starts the runtime.
Terraform creates children after the parent and destroys them before it; an apply that changes
several children causes one reload per child, not a process restart. Because a child write depends
on a running parent, a parent with `enabled=true` may start empty and converge as each child is
applied.

## State migration

The old `rookery_vnf` state cannot be converted automatically: its `config_json` has no per-child
IDs. Remove the old state and import each object into the typed resources:

```bash
terraform state rm rookery_vnf.dhcp
terraform import rookery_dhcp_vnf.dhcp 7
terraform import rookery_dhcp_static_assignment.laptop 7:3
terraform import rookery_dhcp_option.ntp 7:4
terraform import rookery_dns_vnf.dns 5
terraform import rookery_dns_record.router 5:8
terraform import rookery_nat44_vnf.nat 9
terraform import rookery_nat44_dnat_rule.web 9:1
terraform import rookery_bgp_vnf.edge 11
terraform import rookery_bgp_advertised_prefix.app 11:6
```

Import the parent first, then each child by `<vnf_id>:<child_id>`; the child import reads the
parent ID from the composite value and sets `vnf_id`. Do not attempt to roll back to
`rookery_vnf` without restoring the pre-0061 database backup described in
[`docs/operations.md`](../operations.md#upgrade-to-per-type-vnf-storage).

## Disk force delete

Kouja disk cleanup is asynchronous. Set `force_delete = true` on `rookery_disk` to remove the
inventory record when backend cleanup is pending or has failed, instead of leaving the resource
in state with a `delete_failed` error. The remote volume may remain and must be removed on the
storage backend. The `rookery_disk` data source exposes the computed `force_delete` value.

