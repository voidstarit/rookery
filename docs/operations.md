# Operations

This document covers host prerequisites, first-time initialization, running
the API and VNF daemon, configuration, logging, and VM lifecycle. See
[Networking](networking.md) for VPP/VNF details and [API](api.md) for the
public API.

## Install

Prebuilt static binaries for Linux x86_64/amd64 are published on the
[releases page](https://github.com/voidstarit/rookery/releases). The release
archive contains `rookery`, `vnfs`,
`terraform-provider-rookery_<version>_linux_amd64`, `config.example.yaml`, the
`docs/` documentation set, and the LVM snapshot helper script. The Terraform provider is also published as a standalone
release asset.

For a source build, see [Development](development.md).

## Running the API

A small management server tracks VM, disk, and installation-media inventory in
SQLite. It uses the Go standard library HTTP server and the pure-Go
`modernc.org/sqlite` driver, so no CGO or external database is required. The web
UI, favicon, and SQL migrations are embedded into the binary with `go:embed`.
Creating a disk provisions a blank thin-LVM volume in a registered storage backend;
VM creation attaches one or more of those pre-created disks and optionally a read-only
ISO as boot media. VMs run under QEMU with KVM acceleration, and the UI opens an
embedded noVNC client through a server-side WebSocket-to-Unix-socket proxy.
Rookery Networks are VPP bridge domains; vhost-user interfaces created on a
network can be attached to VMs and become virtio-net devices when the VM starts.
An optional NAT44 VNF can give a dedicated network an IPv4 gateway and an
outside uplink.

The server has two commands:

- `init` — idempotently create the LVM thin pool and verify the FD.io VPP API.
- `serve` — run the gateway API and UI. It starts even when prerequisites are
  missing and reports readiness. The bootstrapped default storage backend needs its thin
  pool; additional storage backends are validated when used. VM creation needs
  the thin pool and VPP. Installation media only registers existing files and
  has no host prerequisites.

The API intentionally has **no authentication or authorization**, including the
VNC console proxy. Run it on a trusted network or behind a reverse proxy that
adds access control; otherwise anyone who can reach the HTTP port can control
running VMs.

## Host prerequisites

- Go 1.24 or newer (build only)
- LVM2 tools (`vgs`, `lvs`, `lvcreate`) and an existing volume group
- FD.io VPP with its binary API enabled (`socksvr`), normally `/run/vpp/api.sock`
- The VPP vhost-user plugin (`vpp-plugin-core`) installed and loaded; check with
  `vppctl show plugins | grep vhost`
- The VPP ACL plugin loaded for interface ACLs; check with
  `vppctl show plugins | grep acl`
- No external DHCP or DNS daemon is required; DHCPv4/DHCPv6 use the embedded
  CoreDHCP servers and DNS VNFs run the built-in resolver
- A spare **wired** Ethernet NIC for NAT44 outside use; wireless and the host
  default-route NIC are rejected
- `vppctl` on `PATH` with access to VPP's CLI socket for NAT44 VNFs
- `ethtool` for disabling RX GRO/LRO on AF_PACKET host NICs; Rookery logs a
  warning and continues without it, but NAT44 download performance can suffer
- Root or `CAP_NET_ADMIN` + `CAP_NET_BIND_SERVICE` + `CAP_NET_RAW` for VNF tap setup,
  UDP 67/547 binding, and NAT44 host-NIC takeover
- `iproute2` (`ip`) for assigning IPv4/IPv6 gateway addresses to VNF taps and
  flushing an outside NIC before VPP takes it over
- QEMU with KVM support (`qemu-system-x86_64` and `/dev/kvm`)
- Permission to create `/run/rookery` (or configure another `-run-dir`) and to
  start QEMU processes

The socket is VPP's **binary API** socket, not the CLI socket
(`/run/vpp/cli.sock`) or a QEMU vhost-user socket. The default is
`/run/vpp/api.sock`.

Build standalone binaries:

```bash
go build -o bin/rookery ./cmd/rookery
go build -o bin/vnfs ./cmd/vnfs
```

## Initialize

`init` is safe to run repeatedly. It verifies the volume group, creates the thin
pool only if it does not exist, and then checks that the VPP API socket exists
and answers a GoVPP `control_ping`.

```bash
sudo ./bin/rookery init \
  -vg vg0 \
  -thin-pool rookery \
  -size 100%FREE \
  -vpp-socket /run/vpp/api.sock
```

- An existing thin pool is left untouched (it is not resized).
- `-size` defaults to `100%FREE` and accepts normal LVM size syntax.
- `-thin-pool vg0/rookery` can be used instead of passing `-vg` separately.
- `init` fails if VPP is not reachable or the socket is not a Unix socket.

Run as root or as a user with permission to create logical volumes.

## Serve

```bash
./bin/rookery serve \
  -vg vg0 \
  -thin-pool rookery \
  -vpp-socket /run/vpp/api.sock
```

Open <http://127.0.0.1:8080>. No flags are required to start the server: if the
thin pool or VPP are unavailable, the UI shows the reason and the readiness
state. The server rechecks every 10 seconds. Run the server as root or as a user
with permission to run `lvcreate`/`lvremove`, start QEMU, access `/dev/kvm`, and
access the VPP socket; disk creation and deletion operate on the host.


## Start the VNF daemon

The API and VNF daemon are separate processes. Start `vnfs` before or alongside
the API; the API starts even when the daemon is unavailable and reports enabled
VNFs as `degraded` until it connects.

```sh
sudo vnfs -config /etc/rookery/config.yaml
```

The daemon owns the CoreDHCP, DNS, gVisor memif, and GoBGP runtimes and restores
its persisted desired VNFs after a restart. The API owns SQLite and all VPP
provisioning (memifs, taps, BVIs, NAT44) and controls the daemon over the
internal gRPC socket configured by `api.vnf_addr` / `vnf.listen`.

## Logging

Both binaries log through `zerolog`. `-log-level` (`trace`, `debug`, `info`,
`warn`, `error`; default `info`) and `-log-format` (`console` or `json`;
default `console`, with colors only on a terminal) control the output and can
also be set with `ROOKERY_LOG_LEVEL` and `ROOKERY_LOG_FORMAT`. Every line is
tagged `service=rookery` or `service=vnfs`, and structured fields such as
`vnf_id`, `network`, `method`, `path`, `status`, and `duration` stay typed
integers, strings, and durations instead of being pre-formatted. Use
`-log-format json` when shipping logs to journald, Loki, or ELK. At
`-log-level trace`, the VNF daemon logs every control RPC (method, duration,
status), client connection changes, supervision decisions, DHCPv4/DHCPv6
request/reply details, DNS queries/records/forwarding results, and BGP
advertised/installed/withdrawn routes. DHCP lease allocation and release are
logged at `info`; renewals at `debug`.

## UI and inventory

The UI has **Virtual machines**, **Disks**, **Installation media**,
**Rookery Networks**, and **VNFs** tabs. The **Disks** tab manages storage
backends and creates disks on the selected backend; VM creation additionally
requires VPP and at least one unattached disk. The VM form uses a
dropdown-and-Add picker for disks; the first added disk is the boot disk, and
disks can be added or removed while the VM is stopped.
The `default` backend is bootstrapped from
the API `-vg`/`-thin-pool` flags when supplied; otherwise `serve` starts with
the first registered storage backend, and adding a backend in the UI updates
readiness. LVM thin-pool backends can be registered and selected from the UI.
Each configured LVM backend has an **Import** action that lists existing thin
volumes in its pool and registers selected volumes as disks without modifying
LVM. Already registered volumes are shown as unavailable. The driver and config
are stored separately so future drivers (for example Ceph) can be added without
changing the disk inventory. While blocked, the corresponding create buttons are
disabled and the API returns `503 Service Unavailable`:

- `POST /api/disks` requires a valid selected storage backend; the bootstrapped
  default backend must have a ready thin pool.
- `POST /api/vms` requires a ready thin pool, a ready VPP, and an available disk.

Media entries are read-only references to existing ISO files on the host. The
server records the absolute path and file size but never copies or modifies the
file, and any number of VMs can use the same media as boot media.

Console access is a per-VM setting. The VM editor has a **Console access**
checkbox; new VMs default to disabled. The VNC WebSocket returns `403` while it
is disabled, and open consoles are closed shortly after an administrator turns
it off. Because the API has no authentication, this flag is not a security
boundary against API access; restrict `PUT /api/vms/*` to administrators behind
an authenticating proxy if end users can reach the API.


## VM lifecycle and console

Each VM has a UUID. The **Start** action launches QEMU with `-accel kvm`, every
attached thin-LVM disk as a virtio drive (in attachment order), and the optional
ISO as a read-only CD-ROM. Attached network interfaces become vhost-user virtio-net devices with
their stored MAC addresses. VMs with attached interfaces also get a shared
`memory-backend-memfd` and a single NUMA node, because vhost-user backends such
as VPP cannot map guest virtqueues from anonymous RAM. If VPP shows a vhost
interface as link-down with zero memory regions, check that the QEMU process
has this shared memory backend. The **Monitor** action opens a live telemetry dialog that samples the QEMU
process every two seconds and shows CPU usage, RSS/virtual memory, disk read
and write rates, network RX/TX rates, uptime, and PID. It also keeps 60 samples
(about two minutes) and draws CPU, memory RSS, disk I/O, and network I/O history
on embedded canvas charts. Network counters are read from the VM's VPP vhost
interfaces. It reports process- and vhost-level metrics, not guest OS metrics.
The same data is available from `GET /api/vms/{id}/metrics`; CPU, disk, and
network counters are cumulative and the UI derives rates between samples.

The **Console** action opens the embedded noVNC
client, which connects to
`/ws/vms/<uuid>/vnc`; the server proxies that WebSocket to QEMU's VNC Unix socket
in the run directory. QEMU is launched with `-nic none` plus explicit
vhost-user netdevs for interfaces attached through Rookery Networks.


## Flags and environment variables

| Flag              | Environment variable   | Default             | Used by         | Description |
| ----------------- | ---------------------- | ------------------- | --------------- | ----------- |
| `-config`         | `ROOKERY_CONFIG`       | —                   | `serve`, `init`, `vnfs` | YAML configuration file |
| `-addr`           | `ROOKERY_ADDR`       | `127.0.0.1:8080`    | `serve`         | HTTP listen address |
| `-db`             | `ROOKERY_DB`         | `rookery.db`      | `serve`         | SQLite database path |
| `-rate-limit`     | `ROOKERY_RATE_LIMIT` | `3`                 | `serve`         | HTTP requests per second for each URL path; `0` disables |
| `-vg`             | `ROOKERY_VG`         | empty               | `serve`, `init` | Bootstrap LVM volume group; optional for `serve` |
| `-thin-pool`      | `ROOKERY_THIN_POOL`  | `rookery`         | `serve`, `init` | Bootstrap thin pool name or `<vg>/<name>`; `serve` can use UI backends instead |
| `-vpp-socket`     | `ROOKERY_VPP_SOCKET` | `/run/vpp/api.sock` | `serve`, `init`, `vnfs` | VPP binary API Unix socket |
| `-qemu-bin`       | `ROOKERY_QEMU_BIN`   | `qemu-system-x86_64`| `serve`         | QEMU system emulator binary |
| `-run-dir`        | `ROOKERY_RUN_DIR`    | `/run/rookery`    | `serve`, `vnfs` | QEMU pid/VNC sockets; VNF state and lease files |
| `-memif-dir`      | `ROOKERY_MEMIF_DIR`    | `/run/rookery/memif`| `serve`         | VNF memif socket directory |
| `-vhost-dir`      | `ROOKERY_VHOST_DIR`  | `/run/vpp`          | `serve`, `vnfs` | VPP vhost-user socket directory |
| `-vnf-addr`       | `ROOKERY_VNF_ADDR`   | `unix:///run/rookery/vnfs.sock` | `serve` | VNF daemon control address (`unix://` or `tcp://`) |
| `-log-level`      | `ROOKERY_LOG_LEVEL`  | `info`              | `serve`, `vnfs` | `trace`, `debug`, `info`, `warn`, or `error` |
| `-log-format`     | `ROOKERY_LOG_FORMAT` | `console`           | `serve`, `vnfs` | `console` or `json` |
| `-check-interval` | —                      | `10s`               | `serve`         | Readiness recheck interval |
| `-dhcp-transport` | `ROOKERY_DHCP_TRANSPORT` | `memif`         | `serve`         | DHCP VNF transport: `memif` or `tap` (DHCPv4 only) |
| `-gateway-addr`   | `ROOKERY_GATEWAY_ADDR` | —                 | `serve`         | Optional grpc-gateway HTTP listen address |
| `-mtls-addr`      | `ROOKERY_MTLS_ADDR`    | `:8443`             | `serve`         | Public mTLS API listen address; empty disables |
| `-mtls-sans`      | `ROOKERY_MTLS_SANS`    | —                   | `serve`         | Extra comma-separated DNS/IP SANs for the mTLS server certificate |
| `-grpc-mtls-addr` | `ROOKERY_GRPC_MTLS_ADDR` | `:9443`            | `serve`         | Public mTLS gRPC listen address; empty disables |
| `-pki-dir`        | `ROOKERY_PKI_DIR`      | `/var/lib/rookery/pki` | `serve`       | CA and server certificate directory |
| `-grpc-addr`      | `ROOKERY_GRPC_ADDR`  | —                   | `serve`         | Optional gRPC listen address |
| `-size`           | —                      | `100%FREE`          | `init`          | Thin pool size when creating |

The database directory is created automatically and migrations run on startup.
Set `-addr :8080` to expose the server beyond localhost. The rate limit applies
per URL path, and requests over it receive `429 Too Many Requests` with
`Retry-After: 1`.

`vnfs` starts with `-config`, `-vpp-socket`, `-listen`, `-run-dir`,
`-vhost-dir`, `-log-level`, and `-log-format`; `-listen` defaults to
`unix:///run/rookery/vnfs.sock` and accepts `tcp://host:port`.


## YAML configuration

`rookery serve`, `rookery init`, and `vnfs` accept a YAML file through
`-config /path/to/config.yaml` or `ROOKERY_CONFIG`. A single file can hold both
an `api:` section and a `vnf:` section; each binary reads its own section. The
repository contains a documented `config.example.yaml`.

Precedence is:

```text
command-line flags > environment variables > YAML file > built-in defaults
```

Unknown YAML fields are rejected so typos do not silently fall back to
defaults. Durations use Go syntax (`10s`, `2m`); the API rate limit is a number
(`0` disables it). For example:

```yaml
api:
  vg: vg0
  thin_pool: rookery
  vpp_socket: /run/vpp/api.sock
  addr: 127.0.0.1:8080
  db: /var/lib/rookery/rookery.db
  check_interval: 10s
  rate_limit: 3
  log_level: info
  mtls_addr: ":8443"

vnf:
  vpp_socket: /run/vpp/api.sock
  listen: unix:///run/rookery/vnfs.sock
  run_dir: /run/rookery
  vhost_dir: /run/vpp
  log_level: info
```

Then both processes can use the same file:

```sh
sudo rookery serve -config /etc/rookery/config.yaml
sudo vnfs -config /etc/rookery/config.yaml
```

When the `vnf` section omits `vpp_socket`, `run_dir`, or `vhost_dir`, the values
from the `api` section are used. `rookery init` also reads the `api` section,
using `init_size` for its `-size` default.
