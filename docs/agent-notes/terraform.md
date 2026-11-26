# Terraform notes

Terraform provider resources, data sources, and build.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Provider scope

`cmd/terraform-provider-rookery/` is a command in the main Go module (framework v1.18) implementing
`rookery_*` resources and matching data sources over the mTLS gRPC endpoint: storage backends,
disks, disk throttle classes, media, networks, network pipes, interfaces, host interfaces, host
taps, VPP interface addresses and route-table bindings, ACLs, route tables, routes, VNFs, VMs,
LightVM kernels/initramfs images and instances (`rookery_lightvm_kernel`,
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

## Disk force delete

Kouja disk cleanup is asynchronous. Set `force_delete = true` on `rookery_disk` to remove the
inventory record when backend cleanup is pending or has failed, instead of leaving the resource
in state with a `delete_failed` error. The remote volume may remain and must be removed on the
storage backend. The `rookery_disk` data source exposes the computed `force_delete` value.

