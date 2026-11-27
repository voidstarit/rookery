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
- `upload_timeout` (or `ROOKERY_UPLOAD_TIMEOUT`) defaults to `30m` and applies
  to streamed LightVM kernel/initramfs uploads.
- Client certificates are issued under **Client certificates** in the Rookery
  sidebar. The private key is shown once.
- The Rookery server must be started with `-grpc-mtls-addr` enabled. The HTTPS
  mTLS listener on `-mtls-addr` is separate and is not used by this provider.

## Resources

| Resource | Notes |
| --- | --- |
| `rookery_storage_backend` | LVM thin-pool backend |
| `rookery_disk` | Thin volume, host passthrough block device, template, or provisioned template clone |
| `rookery_disk_throttle_class` | Reusable QEMU disk throttling policy (BPS, IOPS, bursts, I/O size) |
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
| `rookery_dhcp_vnf` | DHCPv4/DHCPv6 parent VNF (family, address plan, transport, DNS integration) |
| `rookery_dhcp_static_assignment` | DHCPv4 MAC-to-IP reservation owned by a `rookery_dhcp_vnf` |
| `rookery_dhcp_option` | Extra DHCP option owned by a `rookery_dhcp_vnf` (sensitive value) |
| `rookery_dns_vnf` | DNS parent VNF (authoritative records, forwarding) |
| `rookery_dns_record` | DNS record owned by a `rookery_dns_vnf` (sensitive value) |
| `rookery_dns_forwarder` | Upstream resolver owned by a `rookery_dns_vnf` |
| `rookery_nat44_vnf` | NAT44 parent VNF |
| `rookery_nat44_dnat_rule` | Destination-NAT rule owned by a `rookery_nat44_vnf` |
| `rookery_bgp_vnf` | BGP parent VNF (write-only sensitive password) |
| `rookery_bgp_advertised_prefix` | Advertised prefix owned by a `rookery_bgp_vnf` |
| `rookery_vm` | VM definition, disks, interfaces, boot media, cgroup class |
| `rookery_lightvm_kernel` | Kernel image, either uploaded from Terraform or registered from a host path |
| `rookery_lightvm_initramfs` | Initramfs image, either uploaded from Terraform or registered from a host path |
| `rookery_lightvm` | Container-backed LightVM definition, disks, interfaces, virtiofs shares, consoles |
| `rookery_host_interface` | Take a physical NIC into VPP |
| `rookery_startup_script` | Server-level Rookery or VPP reconnect script |
| `rookery_cgroup_settings` | Host-wide cgroup limits and reusable VM classes |

Top-level resources and data sources expose a free-form `labels` set:
`rookery_storage_backend`, `rookery_disk`, `rookery_media`, `rookery_network`,
`rookery_network_pipe`, `rookery_interface`, `rookery_host_tap`,
`rookery_acl`, `rookery_route_table`, `rookery_route`, `rookery_dhcp_vnf`,
`rookery_dns_vnf`, `rookery_nat44_vnf`, `rookery_bgp_vnf`, `rookery_vm`,
`rookery_lightvm_kernel`, `rookery_lightvm_initramfs`, `rookery_lightvm`, and
`rookery_host_interface`. Child VNF resources inherit no labels of their own; the
parent carries them. Assign any number of labels:

```hcl
resource "rookery_vm" "app" {
  name     = "app-vm0"
  disk_ids = [rookery_disk.root.id]
  labels   = ["prod", "web", "critical"]
}
```

Labels are stored by the Rookery label API. Changing labels updates the entity
in place, including media, routes, route tables, pipes, and host taps whose
other fields are immutable. Data sources expose the current label set as a
computed `labels` attribute.

VM power actions (`start`, `stop`, `reset`, `ctrl-alt-del`) are deliberately
not part of `rookery_vm`; use the Rookery UI or API for runtime operations.
Creating a VM starts it, so a freshly applied `rookery_vm` reports
`status = "running"`; stop it explicitly when you need it down.
Rookery also persists the desired power state, so VMs that were running come
back after an API restart or host reboot, while explicitly stopped VMs stay
down.
Changing `disk_ids` or `interface_ids` requires the VM to be stopped.
`rookery_vm` and `rookery_lightvm` also accept `disk_serials`, a map from disk
ID (as a string key) to the guest-visible virtio-blk serial. Serials are at
most 20 bytes, use `[A-Za-z0-9._:-]`, and must be unique within the workload.
The guest can read them from `/sys/block/vdX/serial` or udev's `ID_SERIAL`.
`rookery_media` and `rookery_route_table` are immutable and replaced when
changed. `rookery_network` names remain immutable, but its `ipv4_cidr` and
`ipv6_cidr` can be updated while the network has no VNFs, host taps, or BVI.
`rookery_route` is also immutable.

`rookery_disk` can register an existing host block device (a whole disk,
partition, logical volume, or RAID array) instead of creating a thin volume:

```hcl
resource "rookery_disk" "host_data" {
  name        = "host-data"
  host_device = "/dev/mapper/vg-data"
}
```

The server validates the path, derives `size_gb`/`size_bytes`, and never
creates, resizes, or deletes the underlying device. Destroying the resource
removes only the inventory record. Passthrough disks cannot be templates or
template clones. Use `GET /api/host/block-devices/attachable` (or the UI) to
see candidate devices and whether they are mounted, in use, or already
registered.

Set `read_only = true` to make a disk shareable by multiple VMs and LightVMs.
QEMU attaches it read-only for every guest; a read-write disk remains
single-owner. The disk must be detached before `read_only` can change, and
templates cannot be read-only.

Kouja disk cleanup is asynchronous. Set `force_delete = true` to remove the
inventory record when cleanup fails, instead of leaving the resource in state
with a `delete_failed` error. The remote volume may remain and must be removed
on the storage backend.

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

## LightVM resources

`rookery_lightvm_kernel` and `rookery_lightvm_initramfs` manage LightVM boot
artifacts. Set `source_path` to upload a file from the machine running
Terraform over the mTLS gRPC endpoint; Rookery streams it in chunks into its
managed LightVM directory. Alternatively set `path` to register a file already
present on the Rookery host.

```hcl
resource "rookery_lightvm_kernel" "app" {
  name        = "vmlinuz-6.12"
  source_path = "${path.module}/images/vmlinuz-6.12"
  source_hash = filemd5("${path.module}/images/vmlinuz-6.12")
  labels      = ["app", "kernel"]
}

resource "rookery_lightvm_initramfs" "app" {
  name        = "initramfs-6.12"
  source_path = "${path.module}/images/initramfs-6.12.img"
  source_hash = filemd5("${path.module}/images/initramfs-6.12.img")
}
```

Uploaded files are marked `managed = true`; destroying the resource removes
them. Host-path registrations are not deleted by Rookery. `source_hash` is
optional but should be set to a local file hash when uploads are used, because
Terraform otherwise cannot detect that the file contents changed without a
configuration change. `size_bytes` and `managed` are derived by the server from
the stored file, so plans that upload a new or rebuilt file show both as
`(known after apply)`; the applied state reflects the new file's size.

`rookery_lightvm` creates the definition and starts it, like the VM resource;
stop it through the Rookery UI or API. A first create of an image-backed
LightVM may pull the container image, so raise the provider `timeout` (for
example `timeout = "10m"`) when that can take longer than the default 30s. The
LightVM must be stopped before changing it. Disks,
interfaces, shares, and consoles are replaced from their configured sets on
each update; set an explicit empty list to clear one. `image` is optional when
`disk_ids` is non-empty: a LightVM without an image is disk-backed and boots
from its disks via the kernel and initramfs. A disk-backed LightVM may still set
`env`, `executable_path` plus `executable_args`, or `executable_script`; Rookery
publishes them in a read-only `rookery-config` virtio-blk tar device for the
guest init to consume. Set `ephemeral = true` for a one-shot LightVM: when the
guest exits (reboot, panic, or poweroff), Rookery stops QEMU, virtiofsd, and
the attempt container, deletes the definition, and releases its disk and
interface attachments so other workloads can use them. The disk and network
records themselves are kept. Ephemeral definitions are not auto-started after a
restart.

Set `wait_for_exit = true` to keep the apply blocked until the LightVM stops
running. Combined with `ephemeral = true` this makes the resource behave like a
one-shot job: the apply waits for the guest to exit, and Rookery deletes the
definition and releases its disk and interface attachments. The resource then
disappears from Terraform state on the next plan, and a later apply starts a new
run. With `ephemeral = false` the definition remains and the wait only waits for
its QEMU process to end. The wait has no built-in time limit, so cancel
Terraform (or bound the CI job) if the guest may never exit. `wait_for_exit`
only affects creation.

```hcl
resource "rookery_lightvm" "one_shot" {
  name            = "migration-01"
  kernel_id       = rookery_lightvm_kernel.app.id
  initramfs_id    = rookery_lightvm_initramfs.app.id
  image           = "alpine:3.20"
  executable_path = "/usr/local/bin/migrate"
  ephemeral       = true
  wait_for_exit   = true
  disk_ids        = [rookery_disk.data.id]
}
```

```hcl
resource "rookery_lightvm" "app" {
  name           = "app-01"
  description    = "container-backed application"
  vcpus          = 2
  memory_mb      = 512
  kernel_id      = rookery_lightvm_kernel.app.id
  initramfs_id   = rookery_lightvm_initramfs.app.id
  image          = "alpine:3.20"
  # Use either an executable path plus arguments, or executable_script.
  executable_path = "/usr/local/bin/app"
  executable_args = ["serve"]
  append          = "console=hvc0 rdinit=/init"
  console_enabled = true
  # Optional: tear down the runtime when the guest exits.
  ephemeral       = true
  labels          = ["app", "lightvm"]

  env = [
    { name = "APP_ENV", value = "prod" },
  ]

  consoles = [
    { name = "serial" },
  ]

  virtiofs_shares = [
    {
      host_path = "/srv/app-data"
      tag       = "data"
      read_only = true
    },
  ]

  disk_ids      = [rookery_disk.app.id]
  interface_ids = [rookery_interface.app.id]
}
```

`config_files` is only valid for disk-backed LightVMs. Each entry is a
`{ name, content }` object added to the `rookery-config` tar at the given
relative path; the guest initramfs extracts it under `/run/rookery`. Content is
text. Names must
be unique, relative, and free of `.`/`..` components, and the root names `env`
and `startup-cmd` are reserved. Up to 64 files are allowed, each at most 1 MiB
with 4 MiB combined content. Typical usage reads a local file:

```hcl
config_files = [
  { name = "app/config.yaml", content = file("${path.module}/config.yaml") },
]
```

Set `executable_script` instead of `executable_path` to supply an inline
entrypoint script. Rookery writes it to `/.entrypoint` in the image root and
adds `#!/bin/sh` when the content does not start with a shebang. Script mode
does not accept `executable_args`. For a disk-backed LightVM the same content is
written to the `startup-cmd` file in the `rookery-config` tar archive instead.

```hcl
resource "rookery_lightvm" "scripted" {
  name              = "scripted-01"
  kernel_id         = rookery_lightvm_kernel.app.id
  initramfs_id      = rookery_lightvm_initramfs.app.id
  image             = "alpine:3.20"
  executable_script = <<-EOT
    #!/bin/sh
    echo "starting LightVM"
    exec /usr/local/bin/app
  EOT
}
```

`consoles` is optional; omitting it creates the default
`org.example.console` port. The tag `rootfs` is reserved. Shares count toward
the combined 24-device LightVM limit with disks and interfaces.

`rookery_disk_throttle_class` defines a reusable QEMU block-throttling policy.
The 19 limits are optional; zero means unset. BPS values are bytes per second,
IOPS values are operations per second, burst lengths are seconds, and
`iops_size` is bytes. A class must set at least one positive BPS or IOPS base.
Removing a numeric attribute preserves its prior state value; set it to `0` to
clear it. Assign a class to a disk with `throttle_class_id`; `0` means
unlimited, and omitting the attribute preserves the current class. Templates
cannot carry a class.

```hcl
resource "rookery_disk_throttle_class" "standard" {
  name       = "standard"
  bps_read   = 524288000
  bps_write  = 314572800
  iops_total = 2000
}

resource "rookery_disk" "root" {
  name               = "app-vm0-root"
  size_gb            = 20
  storage_backend_id = rookery_storage_backend.pool.id
  throttle_class_id  = rookery_disk_throttle_class.standard.id
}
```

Limits are resolved at VM start, so class edits affect assigned disks on their
next start. The feature is tested with QEMU 10.0.13; earlier versions are not
verified.

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

`classes` defines reusable, hierarchical profiles below `rookery-cgroup`.
Names are slash-separated paths such as `prod`, `prod/web`, or
`prod/web/frontend`, up to three levels deep. Define the parent class before its
children. A class that has child classes is a grouping node; assign VMs only to
leaf classes. Values inherit from the host or parent class when omitted:

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
    {
      name             = "prod/web/frontend"
      cpus             = "0"
      memory_max_bytes = 3 * 1024 * 1024 * 1024
      cpu_weight       = 200
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
  cgroup_class = "prod/web/frontend"

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

VNF configuration is typed. Each parent resource owns its scalar fields and the
children are first-class resources: `rookery_dhcp_static_assignment` and
`rookery_dhcp_option` for DHCP, `rookery_dns_record` and `rookery_dns_forwarder`
for DNS, `rookery_nat44_dnat_rule` for NAT44, and
`rookery_bgp_advertised_prefix` for BGP. There is no `config_json` and no `type`
attribute; the resource name selects the VNF type.

Child resources require `vnf_id`, which forces replacement when it changes.
Changing a child reloads the running DHCP, DNS, or BGP runtime (or re-applies
NAT44 DNAT mappings) without restarting it. Changing a parent scalar field,
transport, or `enabled` stops and starts the runtime, so an apply that changes a
parent and its children sees one restart at most. The BGP `password`, DHCP option
`value`, and DNS record `value` are sensitive. The API never returns the BGP
password, so Terraform keeps the configured value in state.

For DHCPv4, `transport` can be omitted (use the network default), `"shared"`,
`"dedicated"`, or `"tap"`; `"memif"` is the legacy alias for a dedicated memif.
The server derives a `gateway`/`bind_ip` for a DHCPv4 VNF on a NAT network and
defaults `lease_time` to `12h`; read them back with the data source or let the
computed attributes settle after the first apply.

DHCPv4 and DHCPv6 VNFs can integrate with a DNS VNF on the same network by
setting `dns_vnf_id`, `dns_domain` (the suffix for generated names), and
optionally `dns_ttl` (default `300`). The DNS VNF maintains A/AAAA and PTR
records for active leases. Set the DHCP `dns` field to the DNS VNF's `.2`
service address (IPv4 for DHCPv4, IPv6 for DHCPv6) so clients use it.

`rookery_dns_record` accepts `A`, `AAAA`, `CNAME`, `TXT`, and `PTR`. A CNAME owns
its name alone: the same `name` cannot also hold another record type. Names,
CNAME targets, and PTR targets are stored lower-case with the trailing dot, so
both spellings plan cleanly:

```hcl
resource "rookery_dns_vnf" "dns" {
  name       = "lab-dns"
  network_id = rookery_network.lab.id
}

resource "rookery_dns_record" "web" {
  vnf_id = rookery_dns_vnf.dns.id
  name   = "web.lab.test"
  type   = "A"
  value  = "10.20.0.50"
}

resource "rookery_dns_record" "alias" {
  vnf_id = rookery_dns_vnf.dns.id
  name   = "www.lab.test"
  type   = "CNAME"
  value  = "web.lab.test" # planned and stored as web.lab.test.
}
```

The provider plans the canonical form the API stores, so an apply does not fail
with `Provider produced inconsistent result after apply`: a DNS record `name` or
CNAME/PTR target without the trailing dot plans as its fully qualified form, and
a planned `ttl` of `0` becomes the API default `300`.

The old `rookery_vnf` resource and its data source are retired with no
compatibility shim. Remove the old state and import each object into the typed
resources instead:

```bash
terraform state rm rookery_vnf.dhcp
terraform import rookery_dhcp_vnf.dhcp 7
terraform import rookery_dhcp_static_assignment.laptop 7:3
```

Terraform cannot split a `config_json` blob into typed children automatically;
import the parent first, then each child by `<vnf_id>:<child_id>`.

## Data sources

Most resources have a matching data source; VNF child resources are managed only
through their parent:

| Data source | Lookup |
| --- | --- |
| `rookery_status` | No arguments; current thin-pool and VPP readiness |
| `rookery_storage_backend` | `id` or `name` |
| `rookery_disk` | `id` or `name` |
| `rookery_disk_throttle_class` | `id` or `name` |
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
| `rookery_dhcp_vnf` | `id` or `name` |
| `rookery_dns_vnf` | `id` or `name` |
| `rookery_nat44_vnf` | `id` or `name` |
| `rookery_bgp_vnf` | `id` or `name` |
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
terraform import rookery_disk_throttle_class.standard 1
terraform import rookery_startup_script.vpp vpp
terraform import rookery_cgroup_settings.host cgroup
```

VNF child resources use a composite import id:

```bash
terraform import rookery_dhcp_static_assignment.laptop 7:3
terraform import rookery_dhcp_option.ntp 7:4
terraform import rookery_dns_record.router 5:8
terraform import rookery_dns_forwarder.upstream 5:2
terraform import rookery_nat44_dnat_rule.web 9:1
terraform import rookery_bgp_advertised_prefix.app 11:6
# <vnf_id>:<child_id>
```

Routes use a composite import id:

```bash
terraform import rookery_route.app 7/false/42
# <route_table_id>/<ipv6>/<route_id>

# Use route_table_id = 0 for the implicit default table.
```

## Example

See `examples/complete/main.tf` for a full network, disk, VM, ACL, route,
DHCP/DNS/NAT44/BGP VNFs with child resources, host tap, network pipe, address,
route-table, startup-script, cgroup-settings, and template-provisioning example.
See `examples/data-sources/main.tf` for the matching data sources.
