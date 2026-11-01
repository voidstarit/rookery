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
ISO as boot media, then starts the VM; stopping it is always an explicit action. VMs run under QEMU with KVM acceleration, and the UI opens an
embedded noVNC client through a server-side WebSocket-to-Unix-socket proxy.
Rookery Networks are VPP bridge domains; vhost-user interfaces created on a
network can be attached to VMs and become virtio-net devices when the VM starts.
An optional NAT44 VNF can give a dedicated network an IPv4 gateway and an
outside uplink.

The server has three commands:

- `init` — idempotently create the LVM thin pool and verify the FD.io VPP API.
- `serve` — run the gateway API and UI. It starts even when prerequisites are
  missing and reports readiness. The bootstrapped default storage backend needs its thin
  pool; additional storage backends are validated when used. VM creation needs
  the thin pool and VPP, and starts the VM, so QEMU/KVM must be available too. Installation media only registers existing files and
  has no host prerequisites.
- `create-client-cert <name>` — issue an mTLS client certificate using the
  server's CA and database. It writes the client certificate, private key, and
  CA certificate to the selected output directory. The alias
  `issue-client-cert` is also accepted.

The API intentionally has **no authentication or authorization**, including the
VNC console proxy. Run it on a trusted network or behind a reverse proxy that
adds access control; otherwise anyone who can reach the HTTP port can control
running VMs.

## Host prerequisites

- Go 1.26 or newer (build only)
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
- QEMU with KVM support (`qemu-system-x86_64` and `/dev/kvm`), including
  `vhost-user-fs-pci` for LightVM image sharing
- `buildah` for LightVM image pull/mount, with access to its container storage
- `virtiofsd` next to the `rookery` binary (or set `-virtiofsd-bin` /
  `ROOKERY_VIRTIOFSD_BIN`); the LightVM tab reports a runtime-unavailable error
  and disables Start if either tool is missing. Run `make virtiofsd` to download
  it into `extra/virtiofsd/`; release archives include it next to `rookery`.
- Permission to create `/run/rookery` (or configure another `-run-dir`) and to
  start QEMU processes
- cgroup v2 mounted at `/sys/fs/cgroup` and write access below `-cgroup-root`
  (default `/sys/fs/cgroup/rookery-cgroup`) to enable host and class resource limits;
  Rookery reports cgroup availability in Settings and refuses to start a VM
  when enabled limits cannot be applied

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

When upgrading both binaries, restart `vnfs` first and then restart
`rookery serve`. On startup the API re-sends the current database configuration
to already-running VNFs once; the daemon compares the spec hash and only
restarts a runtime when the configuration changed. This repairs desired specs
written by an older daemon, including DHCP DNS integration and the shared stack
IPv6 address. If `vnfs` is restarted independently after an upgrade, restart
the API or restart the affected VNFs to re-send their configuration.

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
**Rookery Networks**, **VNFs**, and **LightVM** tabs. The **Disks** tab manages storage
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

The **LightVM** tab currently provides kernel, initramfs, and LightVM inventory.
Images are uploaded through the local UI/API, streamed into `-lightvm-dir`
(`ROOKERY_LIGHTVM_DIR`, default `/var/lib/rookery/lightvm`), and stored under
`kernels/` or `initramfs/`. Each record has a unique name, an optional
description, labels, and the original file name. Uploads are limited to 1 GiB.
Deleting an uploaded image removes the managed file; records registered with an
existing absolute host path are not allowed to delete that host file.

A LightVM definition includes a name, description, vCPUs, memory, console flag,
cgroup class, kernel and initramfs, a container image, environment variables,
an executable path and argument list, a kernel command line, and network
interfaces. New definitions default the kernel command line to
`console=hvc0 rdinit=/init`. **Start** launches QEMU with the kernel and
initramfs (no block device), a virtio console Unix socket in the QEMU run
directory, the selected cgroup class, and vhost-user interfaces. Rookery uses
`-display none` rather than `-nographic` because QEMU does not allow
`-nographic` together with `-daemonize`. **Stop** terminates the process.
Status and desired state are reconciled after API/VPP restarts, and the
container image/environment/executable fields are stored for the guest runtime.
On start, Rookery uses `buildah` to pull `image` only if it is missing, creates
an **ephemeral writable** container named `rookery-lightvm-<uuid>`, mounts its
rootfs, writes the environment as `<rootfs>/.env` (one `NAME=value` per line),
starts `virtiofsd` on `<run-dir>/<uuid>.virtiofs.sock`, and passes a
`vhost-user-fs-pci` device with tag `rootfs` to QEMU. The initramfs is
responsible for mounting that tag and can read `/rootfs/.env`; Rookery does not
modify `append`. In this mode the guest writes to its own rootfs overlay and
those writes are discarded when the LightVM stops, because the container is
unmounted and removed. The UI environment editor accepts one `NAME=value` per
line; blank lines, `export ` prefixes, and `#` comments are ignored, and names
must match `[A-Za-z_][A-Za-z0-9_]*`. Values are written verbatim on a single
line.
`virtiofsd` is resolved next to the `rookery`
executable unless `-virtiofsd-bin` is set. The container image/environment/
executable fields are not passed over the kernel cmdline; the initramfs
supplies its own contract. `/api/status` includes a `lightvm` component that
reports whether `buildah` and `virtiofsd` are available; when either is missing
the LightVM tab shows the error and disables Start. Run `make virtiofsd` (or
`make -C extra/virtiofsd virtiofsd`) to download the upstream static x86_64 binary into
`extra/virtiofsd/`; `make release` includes it next to `rookery`.
While a LightVM is running, the API returns `console_socket`, the Unix socket
path for its virtio console; there is no graphical display or VNC device. The
table's **Console** action opens `/lightvm-console.html?vm=<uuid>`, an xterm.js
terminal connected through `/ws/lightvms/{id}/console`. The page uses bundled
JetBrains Mono and lets each user choose a persisted color theme
(green/white/amber/blue on black, solarized dark, or black on white) and font
size. It also has buttons for common control chords (Ctrl+C/D/Z/A/E/K/U/W/L/R,
Esc, Tab); the **Ctrl** button latches for the next key so arbitrary Ctrl+key
combinations can be sent. Console access follows `console_enabled` and is
checked by the server; disable it to block new and existing console sessions. Interfaces can belong to
either a VM or a LightVM, not both.


## VM lifecycle and console

Each VM has a UUID. Creating a VM launches QEMU immediately with `-accel kvm`,
every attached thin-LVM disk as a virtio drive (in attachment order), and the
optional ISO as a read-only CD-ROM. Attached network interfaces become
vhost-user virtio-net devices with their stored MAC addresses. VMs with
attached interfaces also get a shared `memory-backend-memfd` and a single NUMA
node, because vhost-user backends such as VPP cannot map guest virtqueues from
anonymous RAM. If VPP shows a vhost interface as link-down with zero memory
regions, check that the QEMU process has this shared memory backend. If QEMU
cannot start, the API removes the new VM record again and reports the error, so
a failed create leaves no VM behind. The **Start** action launches a VM that was
explicitly stopped, and **Stop** shuts it down.

Rookery stores a desired power state per VM. Starting a VM (or creating one)
marks it as wanting to run, and stopping it marks it as wanting to stay down.
On API startup, and after VPP is rebuilt, every VM that wants to run but has no
live QEMU process is started again — so VMs that were running when the host
rebooted come back, while VMs that were stopped explicitly stay down. Startup
auto-start runs after the VPP bridge domains and vhost-user interfaces are
restored, and failures are logged without stopping the API.

The **Monitor** action opens a live telemetry dialog that samples the QEMU
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


## cgroup v2 resource limits

Settings → **Cgroup v2 resources** configures a host-wide parent cgroup and
reusable **classes** for QEMU. When enabled, every QEMU process is launched
there, using `clone3(CLONE_INTO_CGROUP)` when the kernel supports it and an
explicit `cgroup.procs` move otherwise. Rookery creates:

- `<cgroup-root>/shared` for VMs without a class.
- `<cgroup-root>/<path>` for each class path, for example
  `<cgroup-root>/prod/web/frontend`.

Class names are slash-separated paths such as `prod`, `prod/web`, or
`prod/web/frontend`, nested up to three levels below `rookery-cgroup`. Define
the parent class first; the tree editor shows the hierarchy. VMs are assigned
only to leaf classes so a class node that has child classes stays free of
processes while controllers are distributed, satisfying cgroup v2's
no-internal-process rule. Parent classes group children and can carry inherited
limits. Class values inherit from the host and parent class when omitted.

Host and class settings map to cgroup v2 attributes:

- CPU cores → `cpuset.cpus`; memory nodes → `cpuset.mems`
- total/per-class memory → `memory.max`
- CPU weight → `cpu.weight`
- per-block-device bandwidth/IOPS → `io.max` (`rbps`, `wbps`, `riops`, `wiops`)
- per-block-device weight → `io.weight`

The UI lists online CPUs and whole block devices from `/sys/class/block`.
Devices are resolved to their major/minor numbers when limits are applied.
`io.weight` requires a kernel with the BFQ I/O scheduler; hosts without it
return an error when a weight is requested instead of silently ignoring it.
Class limits require host-wide cgroup limits to be enabled. When host-wide
limits are disabled, Rookery moves all running QEMU processes out of the
Rookery cgroups and removes the hierarchy; VMs start without cgroup limits until
host-wide limits are enabled again. Enabling host-wide limits applies the
current class assignments to all running VMs.

VMs store only a `cgroup_class` name. A VM without a class runs in the shared
child cgroup and inherits host ceilings. Assigning a class applies that class's
CPU, memory, CPU weight, and I/O limits. Saving a VM applies its class change
immediately (when cgroup limits are enabled): Rookery moves the running QEMU
process (all of its threads) into the shared or leaf class cgroup as needed,
without stopping it. Saving host-wide settings also re-places every running VM
immediately. Shrinking the host CPU or memory-node set clamps running child
cgroups to the remaining CPUs/nodes so the parent update can succeed. Removing
a class that is still assigned to a VM is rejected while cgroup limits are
enabled; reassign the VM first. If the host rejects a limit (for example because
the cgroup2 delegation is missing or a CPU set is outside the parent set), the
API rolls the settings back and reports the kernel error. If the settings save
but one running VM cannot accept the new limits, the API reports that VM.

`GET`/`PUT /api/cgroup-settings` manage the host configuration and classes,
`GET /api/cgroup-usage` returns live memory, CPU, task, and I/O counters for the
root, shared, and class cgroups, and `GET /api/host/block-devices` lists devices
for the I/O picker. Settings polls cgroup usage every two seconds while visible
and derives CPU cores and I/O rates from consecutive samples.

## Flags and environment variables

| Flag              | Environment variable   | Default             | Used by         | Description |
| ----------------- | ---------------------- | ------------------- | --------------- | ----------- |
| `-config`         | `ROOKERY_CONFIG`       | —                   | `serve`, `init`, `create-client-cert`, `vnfs` | YAML configuration file |
| `-addr`           | `ROOKERY_ADDR`       | `127.0.0.1:8080`    | `serve`         | HTTP listen address |
| `-db`             | `ROOKERY_DB`         | `rookery.db`      | `serve`, `create-client-cert` | SQLite database path |
| `-rate-limit`     | `ROOKERY_RATE_LIMIT` | `3`                 | `serve`         | HTTP requests per second for each URL path; `0` disables |
| `-vg`             | `ROOKERY_VG`         | empty               | `serve`, `init` | Bootstrap LVM volume group; optional for `serve` |
| `-thin-pool`      | `ROOKERY_THIN_POOL`  | `rookery`         | `serve`, `init` | Bootstrap thin pool name or `<vg>/<name>`; `serve` can use UI backends instead |
| `-vpp-socket`     | `ROOKERY_VPP_SOCKET` | `/run/vpp/api.sock` | `serve`, `init`, `vnfs` | VPP binary API Unix socket |
| `-qemu-bin`       | `ROOKERY_QEMU_BIN`   | `qemu-system-x86_64`| `serve`         | QEMU system emulator binary |
| `-run-dir`        | `ROOKERY_RUN_DIR`    | `/run/rookery`    | `serve`, `vnfs` | QEMU pid/VNC sockets; VNF state and lease files |
| `-lightvm-dir`    | `ROOKERY_LIGHTVM_DIR`| `/var/lib/rookery/lightvm` | `serve` | Uploaded LightVM kernels and initramfs files |
| `-buildah-bin`    | `ROOKERY_BUILDAH_BIN`| `buildah`          | `serve` | buildah binary for LightVM image pull/mount |
| `-virtiofsd-bin`  | `ROOKERY_VIRTIOFSD_BIN` | `virtiofsd` next to `rookery` | `serve` | virtiofsd binary for LightVM rootfs sharing |
| `-cgroup-root`    | `ROOKERY_CGROUP_ROOT`| `/sys/fs/cgroup/rookery-cgroup` | `serve`   | Parent cgroup v2 for QEMU resource limits |
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
| `-pki-dir`        | `ROOKERY_PKI_DIR`      | `/var/lib/rookery/pki` | `serve`, `create-client-cert` | PKI directory (CA and server certificate; also used for client issuance) |
| `-grpc-addr`      | `ROOKERY_GRPC_ADDR`  | —                   | `serve`         | Optional gRPC listen address |
| `-days`           | —                      | `365`               | `create-client-cert` | Client certificate validity in days (maximum 3650) |
| `-out-dir`        | —                      | `.`                 | `create-client-cert` | Directory for `<name>.crt`, `<name>.key`, and `<name>-ca.crt` |
| `-force`          | —                      | `false`             | `create-client-cert` | Overwrite existing output files |
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
  lightvm_dir: /var/lib/rookery/lightvm
  buildah_bin: buildah
  virtiofsd_bin: ""
  cgroup_root: /sys/fs/cgroup/rookery-cgroup
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
