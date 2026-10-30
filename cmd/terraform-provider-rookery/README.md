# Terraform provider for Rookery

This directory contains a Terraform **provider** for the Rookery API. In
Terraform, resources are created by a provider; modules consume this provider.
It talks to the public mTLS gRPC listener (`serve -grpc-mtls-addr`, default
`:9443`) and presents a client certificate issued by Rookery. Both managed
resources and data sources are provided.

## Build

Build from the repository root into a directory that Terraform can use as a
development override:

```bash
go build -o bin/terraform-provider-rookery ./cmd/terraform-provider-rookery
```

`make build` also builds it into `bin/terraform-provider-rookery`. The
Linux release archive contains a prebuilt
`terraform-provider-rookery_<version>_linux_amd64` with its version set from the
release tag; the same binary is published as a standalone release asset. Rename
it to `terraform-provider-rookery` or point `dev_overrides` at the directory
containing the suffixed binary.

For local development, use Terraform's `dev_overrides` instead of installing
the provider from a registry. The override points at the **directory**
containing the binary, not the binary itself. Add this to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "voidstarit/rookery" = "/absolute/path/to/rookery/bin"
  }
  direct {}
}
```

`terraform init` is not needed for an override, but Terraform will warn that the
provider is not installed from a registry. Run `terraform validate`/`plan` with
the override in place.

## Provider configuration

```hcl
provider "rookery" {
  endpoint    = "api.example.com:9443"
  ca_file     = "ca.crt"
  cert_file   = "client.crt"
  key_file    = "client.key"
  server_name = "api.example.com" # optional; use when connecting by IP
}
```

- `endpoint` (or `ROOKERY_ENDPOINT`) is the mTLS gRPC address.
- `ca_file`, `cert_file`, `key_file` (or `ROOKERY_CA_FILE`,
  `ROOKERY_CLIENT_CERT`, `ROOKERY_CLIENT_KEY`) are the files downloaded when a
  client certificate is issued in the Rookery UI.
- `ca_pem`, `cert_pem`, `key_pem` accept inline PEM instead of files.
- `timeout` defaults to `30s`; use strings such as `2m`.
- Client certificates are issued under **Client certificates** in the Rookery
  sidebar. The private key is shown once.
- The Rookery server must be started with `-grpc-mtls-addr` enabled. The HTTPS
  mTLS listener on `-mtls-addr` is separate and is not used by this provider.

## Resources

| Resource | Notes |
| --- | --- |
| `rookery_storage_backend` | LVM thin-pool backend |
| `rookery_disk` | Thin volume, template, or provisioned template clone |
| `rookery_media` | Read-only ISO/image path |
| `rookery_network` | VPP bridge domain + isolated VRF |
| `rookery_network_pipe` | L2 pipe pair between two networks |
| `rookery_interface` | vhost-user interface, optional VM, ACLs, route tables (`ipv4_table_id`/`ipv6_table_id` are VPP table ids; use `rookery_route_table.app.table_id`) |
| `rookery_host_tap` | Persistent VPP tap between a network and the Linux host |
| `rookery_vpp_interface_addresses` | Managed IPv4/IPv6 addresses and DHCP client for a Rookery VPP interface |
| `rookery_vpp_interface_route_tables` | IPv4/IPv6 FIB binding for a host interface, host tap, or pipe endpoint |
| `rookery_acl` | Ordered permit/deny rule set |
| `rookery_route_table` | Custom VRF table (IDs 100000+ are reserved) |
| `rookery_route` | Static route in a route table, including VPP path options |
| `rookery_vnf` | DHCPv4/DHCPv6/NAT44/BGP network function |
| `rookery_vm` | VM definition, disks, interfaces, boot media, cgroup class |
| `rookery_host_interface` | Take a physical NIC into VPP |
| `rookery_startup_script` | Server-level Rookery or VPP reconnect script |
| `rookery_cgroup_settings` | Host-wide cgroup limits and reusable VM classes |

VM power actions (`start`, `stop`, `reset`, `ctrl-alt-del`) are deliberately
not part of `rookery_vm`; use the Rookery UI or API for runtime operations.
Creating a VM starts it, so a freshly applied `rookery_vm` reports
`status = "running"`; stop it explicitly when you need it down.
Rookery also persists the desired power state, so VMs that were running come
back after an API restart or host reboot, while explicitly stopped VMs stay
down.
Changing `disk_ids` or `interface_ids` requires the VM to be stopped.
`rookery_media` and `rookery_route_table` are immutable and replaced when
changed. `rookery_network` names remain immutable, but its `ipv4_cidr` and
`ipv6_cidr` can be updated while the network has no VNFs, host taps, or BVI.
`rookery_route` is also immutable.

`rookery_disk` templates can set `provisioning_script` and
`provisioning_timeout`. Clones created with `template_disk_id` can pass
`provisioning_json = jsonencode({...})`; the object is sent to the template
script through `ROOKERY_PROVISION_JSON` under `properties`. An omitted or empty
`provisioning_json` skips provisioning entirely. The reference script
`scripts/template-provision.sh` handles both whole-disk filesystems and
partitioned LVM clones, logs the received payload and each stage, and always
attempts to unmount the clone. Set `resize_disk = true` in the provisioning
object to extend the root partition and grow the filesystem. The reference
script supports a DOS table with root followed by an extended/swap partition
(the swap is moved and recreated with the same UUID/label) and a single-root
partition. Other layouts are resized only within their current partition.

`rookery_network_pipe` exposes computed `endpoint_a_name` and
`endpoint_b_name` values (`pipe<instance>.0` and `.1`) for use with
`rookery_vpp_interface_addresses` and `rookery_vpp_interface_route_tables`.
Pipes are immutable; replace the resource to change the network pair.

`rookery_host_tap` creates a persistent host-side tap. Its `name` defaults to
`hostnet<network_id>`, and `ipv4_address` may be omitted. Use the VPP address
resource for additional IPv4/IPv6 addresses.

`rookery_vpp_interface_addresses` manages the complete Rookery address set and
IPv4 DHCP client state for one interface. Set `addresses = []` explicitly to
clear addresses and set `dhcp_client_enabled = false` to disable DHCP;
destroying the resource clears managed addresses and disables DHCP. The
interface can be a vhost-user interface (`name` from `rookery_interface`), a
host interface (`host-<nic>`), a host tap (its `name`), a pipe endpoint, or a
VNF tap.

`rookery_vpp_interface_route_tables` binds a host interface, host tap, or pipe
endpoint to IPv4/IPv6 tables; use `0` for the default table. Destroying the
resource removes the override and restores the automatic default (the network
VRF for host taps, or the default table for host interfaces and pipe
endpoints). Vhost-user interfaces use `rookery_interface`'s
`ipv4_table_id`/`ipv6_table_id` fields instead.

`rookery_startup_script` manages a server-level script with `kind = "rookery"`
(once per API process) or `kind = "vpp"` (on every VPP connection/reconnect).
Only one resource of each kind should exist. Destroying the resource disables
the script but keeps its text for troubleshooting.

`rookery_cgroup_settings` manages the singleton host-wide cgroup v2
configuration. Set `enabled = true` to place all QEMU processes under the
Rookery hierarchy and apply `cpus`, `mems`, `memory_max_bytes`, `cpu_weight`,
and per-block-device `io_limits`. Saving settings applies them to all running
VMs. Setting `enabled = false` or destroying the resource moves running QEMU
processes out of the Rookery hierarchy and removes the cgroups.

`classes` defines reusable, hierarchical profiles. Names are slash-separated
paths such as `prod`, `prod/web`, or `prod/web/frontend`; parent cgroups are
created automatically. Values inherit from the host or parent class when
omitted:

```hcl
resource "rookery_cgroup_settings" "host" {
  enabled          = true
  cpus             = "0-7"
  memory_max_bytes = 32 * 1024 * 1024 * 1024
  cpu_weight       = 500

  io_limits = [
    {
      device    = "/dev/nvme0n1"
      read_bps  = 500 * 1024 * 1024
      write_bps = 500 * 1024 * 1024
      weight    = 200
    },
  ]

  classes = [
    {
      name             = "prod"
      cpus             = "0-3"
      memory_max_bytes = 16 * 1024 * 1024 * 1024
      cpu_weight       = 400
    },
    {
      name             = "prod/web"
      cpus             = "0-1"
      memory_max_bytes = 6 * 1024 * 1024 * 1024
      cpu_weight       = 300
    },
  ]
}
```

Assign a class with `cgroup_class`; omit it or set it to an empty string for the
shared cgroup. Per-class limits require host cgroup limits to be enabled:

```hcl
resource "rookery_vm" "app" {
  name         = "app-vm0"
  vcpus        = 2
  memory_mb    = 2048
  disk_ids     = [rookery_disk.root.id]
  cgroup_class = "prod/web"

  depends_on = [rookery_cgroup_settings.host]
}
```

Use `depends_on` so classes exist before VMs are created or updated.

`rookery_route` exposes the VPP path options `route_count`, `next_hop_table`,
`weight`, `udp_encap`, `ipv4_lookup_table`, `ipv6_lookup_table`,
`mpls_lookup_table`, `resolve_via_host`, `resolve_via_connected`,
`rx_ipv4_interface`, `rx_ipv6_interface`, `out_labels`, `drop`, and
`description`. `next_hop_table` and the IPv4/IPv6 lookup tables take the
Rookery route-table name; use `default` for table `0`. Numeric options are
nullable: omit one to leave it out of the VPP command, or set it to `0`
explicitly when zero is meaningful, for example:

```hcl
resource "rookery_route" "lookup" {
  route_table_id    = 0
  ipv6              = false
  prefix            = "10.10.1.0/24"
  ipv4_lookup_table = "app-vrf"
}
```

`rookery_vnf.config_json` uses protobuf JSON. Write proto field names (for
example `range_start`, `outside_interface`, `advertise_prefixes`) and use
`jsonencode({...})`. The provider stores the configured JSON as-is, and the
server normalizes some fields (the `bind_ip` and `gateway` it derives for a
DHCPv4 VNF on a NAT network, the default `lease_time`). Use the `rookery_vnf`
data source when you need the server's canonical configuration. For DHCPv4,
`transport` can be omitted (use the network default), `"shared"`,
`"dedicated"`, or `"tap"`; `"memif"` is the legacy alias for a dedicated memif.

DHCPv4 and DHCPv6 VNFs can integrate with a DNS VNF on the same network by
setting `dns_vnf_id`, `dns_domain` (the suffix for generated names), and
optionally `dns_ttl` (default `300`). The DNS VNF maintains A/AAAA and PTR
records for active leases. Set the DHCP `dns` field to the DNS VNF's `.2`
service address (IPv4 for DHCPv4, IPv6 for DHCPv6) so clients use it.

## Data sources

Each resource has a matching data source:

| Data source | Lookup |
| --- | --- |
| `rookery_status` | No arguments; current thin-pool and VPP readiness |
| `rookery_storage_backend` | `id` or `name` |
| `rookery_disk` | `id` or `name` |
| `rookery_media` | `id` or `name` |
| `rookery_network` | `id` or `name` |
| `rookery_network_pipe` | `id`, or `network_a_id` + `network_b_id` |
| `rookery_interface` | `id` or `name` |
| `rookery_host_tap` | `id` or `name` |
| `rookery_vpp_interface_addresses` | `name` |
| `rookery_vpp_interface_route_tables` | `name` |
| `rookery_acl` | `id` or `name` |
| `rookery_route_table` | `id`, `table_id` + `ipv6`, or `name` + optional `ipv6` |
| `rookery_route` | `route_table_id` + `ipv6` + `id`, or `route_table_id` + `ipv6` + `prefix` |
| `rookery_vnf` | `id` or `name` |
| `rookery_vm` | `id` or `name` |
| `rookery_host_interface` | `nic` |
| `rookery_startup_script` | `kind` (`rookery` or `vpp`) |
| `rookery_cgroup_settings` | No arguments; host-wide settings and runtime status |

For route tables and routes, `route_table_id = 0` refers to the default table.
The two default route tables share record id 0, so set `ipv6` when looking one
up by id. Ambiguous name lookups (for example duplicate VM names) return an
error. See `examples/data-sources/main.tf`.

## Import

Most resources import by numeric id:

```bash
terraform import rookery_network.app 3
terraform import rookery_vm.app 0f8d4a5e-...
terraform import rookery_host_interface.uplink enp1s0
terraform import rookery_network_pipe.app_edge 7
terraform import rookery_host_tap.app 4
terraform import rookery_vpp_interface_addresses.app_vm0 app-vm0
terraform import rookery_vpp_interface_route_tables.app_tap hostnet1
terraform import rookery_startup_script.vpp vpp
terraform import rookery_cgroup_settings.host cgroup
```

Routes use a composite import id:

```bash
terraform import rookery_route.app 7/false/42
# <route_table_id>/<ipv6>/<route_id>

# Use route_table_id = 0 for the implicit default table.
```

## Example

See `examples/complete/main.tf` for a full network, disk, VM, ACL, route,
DHCP VNF, host tap, network pipe, address, route-table, startup-script,
cgroup-settings, and template-provisioning example. See
`examples/data-sources/main.tf` for the matching data sources.
