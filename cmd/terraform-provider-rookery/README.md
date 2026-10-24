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
| `rookery_disk` | Thin volume or template clone |
| `rookery_media` | Read-only ISO/image path |
| `rookery_network` | VPP bridge domain + isolated VRF |
| `rookery_interface` | vhost-user interface, optional VM, ACLs, route tables (`ipv4_table_id`/`ipv6_table_id` are VPP table ids; use `rookery_route_table.app.table_id`) |
| `rookery_acl` | Ordered permit/deny rule set |
| `rookery_route_table` | Custom VRF table (IDs 100000+ are reserved) |
| `rookery_route` | Static route in a route table |
| `rookery_vnf` | DHCPv4/DHCPv6/NAT44/BGP network function |
| `rookery_vm` | VM definition, disks, interfaces, boot media |
| `rookery_host_interface` | Take a physical NIC into VPP |

VM power actions (`start`, `stop`, `reset`, `ctrl-alt-del`) are deliberately
not part of `rookery_vm`; use the Rookery UI or API for runtime operations.
Changing `disk_ids` or `interface_ids` requires the VM to be stopped.
`rookery_media`, `rookery_network`, and `rookery_route_table` are immutable and
replaced when changed. `rookery_route` is also immutable.

`rookery_vnf.config_json` uses protobuf JSON. Write proto field names (for
example `range_start`, `outside_interface`, `advertise_prefixes`) and use
`jsonencode({...})`. The provider reads configuration back with proto field
names. For DHCPv4, `transport` can be omitted (use the network default),
`"shared"`, `"dedicated"`, or `"tap"`; `"memif"` is the legacy alias for a
dedicated memif.

## Data sources

Each resource has a matching data source:

| Data source | Lookup |
| --- | --- |
| `rookery_status` | No arguments; current thin-pool and VPP readiness |
| `rookery_storage_backend` | `id` or `name` |
| `rookery_disk` | `id` or `name` |
| `rookery_media` | `id` or `name` |
| `rookery_network` | `id` or `name` |
| `rookery_interface` | `id` or `name` |
| `rookery_acl` | `id` or `name` |
| `rookery_route_table` | `id`, `table_id` + `ipv6`, or `name` + optional `ipv6` |
| `rookery_route` | `route_table_id` + `ipv6` + `id`, or `route_table_id` + `ipv6` + `prefix` |
| `rookery_vnf` | `id` or `name` |
| `rookery_vm` | `id` or `name` |
| `rookery_host_interface` | `nic` |

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
```

Routes use a composite import id:

```bash
terraform import rookery_route.app 7/false/42
# <route_table_id>/<ipv6>/<route_id>

# Use route_table_id = 0 for the implicit default table.
```

## Example

See `examples/complete/main.tf` for a full network, disk, VM, ACL, route, and
DHCP VNF example.
