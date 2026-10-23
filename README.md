# rookery

Ansible playbook to set up a thin LVM pool for VMs on a remote machine.

The repository also contains a small Go API server for tracking the VMs themselves,
with an embedded web UI and SQLite database.

## Requirements

- Ansible with `community.general` collection installed:
  ```bash
  ansible-galaxy collection install community.general
  ```
- Target host with LVM tooling installed and an existing volume group

## Parameters

| Variable          | Required | Default      | Description                                  |
| ----------------- | -------- | ------------ | -------------------------------------------- |
| `vg_name`         | yes      | -            | Existing volume group to create the pool in  |
| `thin_pool_size`  | no       | unset        | Thin pool size (e.g. `200G`, `50%VG`)        |
| `thin_pool_name`  | no       | `rookery`  | Thin pool name                               |

## Template playbook (`setup-template.yml`)

Downloads a cloud image, extracts it if needed, and writes it to a template
LV in the thin pool.

| Variable          | Required | Default                                                               | Description                                  |
| ----------------- | -------- | --------------------------------------------------------------------- | -------------------------------------------- |
| `vg_name`         | yes      | -                                                                     | Volume group to use                          |
| `thin_pool_name`  | no       | `rookery`                                                           | Thin pool name                               |
| `template_lv_name`| no       | `debian-template`                                                     | Name of the template LV                      |
| `image_url`       | no       | Debian 13 nocloud (trixie) tar.xz                                     | Cloud image URL (`.raw`/`.img` or archive)   |
| `temp_dir`        | no       | `/tmp/rookery`                                                      | Directory to download/extract the image      |

```bash
ansible-playbook setup-template.yml -i hosts.ini -e vg_name=vg0 -K
```

Raw images (`.raw`, `.img`) are written directly to the LV; archives
(`tar.xz`, `tar.gz`, `zip`, ...) are extracted first and the contained
`.raw`/`.img` file is written instead.

The playbook checks the thin pool's free data space before writing and
extends the pool automatically (needs free space in the VG) so the image
always fits; it fails with a clear message otherwise.

## Instance disk playbook (`create-instance-disk.yml`)

Creates a VM instance disk as a thin snapshot of a base (template) LV.
Snapshots are thin-provisioned and only store deltas, so they are instant
and use no extra pool space upfront.

| Variable    | Required | Default | Description                                  |
| ----------- | -------- | ------- | -------------------------------------------- |
| `vg_name`   | yes      | -       | Volume group to use                          |
| `base_lv`   | yes      | -       | Base/template LV to snapshot                 |
| `disk_lv`   | yes      | -       | Name of the new instance disk                |
| `disk_size` | no       | base LV size | Target size (e.g. `20G`); must be >= base LV size. Applied by extending the snapshot after creation (LVM ignores `-L` when snapshotting a thin origin) |

```bash
ansible-playbook create-instance-disk.yml -i hosts.ini \
  -e vg_name=vg0 -e base_lv=debian-13-template -e disk_lv=vm01-root -e disk_size=20G -K
```

The disk is available at `/dev/<vg_name>/<disk_lv>`.

## Delete disk playbook (`delete-instance-disk.yml`)

Removes an instance disk LV (frees its space back to the thin pool).

| Variable  | Required | Default | Description            |
| --------- | -------- | ------- | ---------------------- |
| `vg_name` | yes      | -       | Volume group to use    |
| `disk_lv` | yes      | -       | Instance disk to delete |

```bash
ansible-playbook delete-instance-disk.yml -i hosts.ini -e vg_name=vg0 -e disk_lv=k8s1 -K
```

## User/password playbook (`configure-instance-user.yml`)

Mounts an instance disk and configures a user with a password by editing
the files inside the image (`/etc/passwd`, `/etc/shadow`, `/etc/group`).
Also enables password SSH auth unless disabled.

If the disk contains a partition table (GPT/MBR), the playbook maps it
with `losetup --partscan` (the `partx` BLKPG ioctl does not work on device-
mapper devices) and mounts the real root filesystem instead of the raw LV.
It also handles LVM inside the image: partitions marked as LVM PVs are
detected, the embedded VG is activated (if its name clashes with a host VG
it is imported under a unique name via `vgimportclone`), and the root LV is
mounted. Everything is unmounted/deactivated again at the end.

| Variable            | Required | Default           | Description                                  |
| ------------------- | -------- | ----------------- | -------------------------------------------- |
| `vg_name`           | yes      | -                 | Volume group to use                          |
| `disk_lv`           | yes      | -                 | Instance disk to configure                   |
| `user`              | yes      | -                 | User to create/configure                     |
| `password`          | yes      | -                 | Password (plaintext)                         |
| `mount_point`       | no       | `/mnt/<disk_lv>`  | Where to mount the disk                      |
| `sudo_group`        | no       | `sudo`            | Group for sudo rights                        |
| `user_sudo`         | no       | `true`            | Add user to sudo group                       |
| `ssh_password_auth` | no       | `true`            | Enable `PasswordAuthentication yes` in sshd  |
| `setup_dhcp`        | no       | `true`            | Enable DHCP on `en*`/`eth*` via systemd-networkd (Debian images) |
| `vm_ip`             | no       | -                 | Static address (e.g. `10.0.10.10/24`); when set, replaces DHCP (needed with VPP NAT44) |
| `vm_gateway`        | no       | -                 | Static gateway (e.g. `10.0.10.1`)        |
| `vm_dns`            | no       | `1.1.1.1`         | DNS server(s) for the static config          |
| `generate_host_keys`| no       | `true`            | Regenerate fresh SSH host keys (`ssh-keygen -A -f`) in the image |

```bash
ansible-playbook configure-instance-user.yml -i hosts.ini \
  -e vg_name=vg0 -e disk_lv=k8s1 -e user=admin -e password=secret -K
```

## FD.io VPP setup (`setup-vpp.yml`)

Installs FD.io VPP from the official packagecloud repo and configures a
bridge domain with vhost-user interfaces for QEMU VMs (plus an optional
uplink host-interface).

| Variable           | Required | Default                  | Description                                        |
| ------------------ | -------- | ------------------------ | -------------------------------------------------- |
| `vpp_apt_suite`    | no       | `bookworm`               | Repo suite (FD.io only publishes bookworm Debian packages) |
| `vpp_repo_trusted` | no       | `true`                   | Use `trusted=yes` for the FD.io repo (needed on trixie: apt's sqv rejects FD.io's SHA1 key) |
| `vpp_main_core`    | no       | `1`                      | CPU core for VPP main thread                       |
| `vpp_bridge_id`    | no       | `10`                     | L2 bridge domain ID shared by all interfaces       |
| `vpp_vms`          | no       | `[]`                     | List of `{name, socket}`; socket defaults to `/run/vpp/<name>.sock` |
| `vpp_uplink`       | no       | `""`                     | Host NIC to attach as uplink (via `host-interface`); flush its IPs first |
| `vpp_nat44`        | no       | `false`                  | Enable NAT44-ED: uplink becomes routed outside, loopback BVI becomes inside gateway |
| `vpp_inside_gateway`| no      | `10.0.10.1/24`       | Inside gateway address on the NAT44 BVI (loop0)    |
| `vpp_uplink_address`| no      | `""`                     | Static uplink address (e.g. `192.168.1.50/24`); empty = VPP DHCP client |
| `vpp_nat_sessions` | no       | `10000`                  | Max NAT44 sessions                                 |
| `vpp_socket_mode`  | no       | `0660`                   | Permissions for vhost-user sockets                 |
| `vpp_qemu_users`   | no       | `["libvirt-qemu"]`       | Users added to the `vpp` group for socket access   |

```bash
ansible-playbook setup-vpp.yml -i hosts.ini -K \
  -e 'vpp_vms=[{"name":"k8s1"},{"name":"k8s2","socket":"/run/vpp/k8s2.sock"}]' \
  -e vpp_uplink=eth1 \
  -e 'vpp_qemu_users=["libvirt-qemu","virt-machines"]'
```

The QEMU user must be a member of the `vpp` group (sockets are `root:vpp` mode `0660`).

QEMU/libvirt attaches a VM to its socket with:

```bash
qemu-system-x86_64 ... \
  -chardev socket,id=char0,path=/run/vpp/k8s1.sock,reconnect=1 \
  -netdev type=vhost-user,id=net0,chardev=char0,vhostforce \
  -device virtio-net-pci,netdev=net0
```

libvirt domain XML equivalent:

```xml
<interface type='vhostuser'>
  <source type='unix' path='/run/vpp/k8s1.sock' mode='client'/>
  <model type='virtio'/>
</interface>
```

### Internet access

Two options are available:

**Host tap**

Creates a tap interface (Linux side `vpp_host_tap`, VPP side `tap0`),
bridges it into the bridge domain together with the VM ports, and
assigns `vpp_tap_gateway` to it on the host. The tap is the **only**
host interface touched. NAT/routing is not configured — the playbook
prints the commands for you to run yourself.

```bash
ansible-playbook setup-vpp.yml -i hosts.ini -K \
  -e vpp_host_tap=vmtap \
  -e 'vpp_vms=[{"name":"k8s1"}]'
```

| Variable          | Default            | Description                    |
| ----------------- | ------------------ | ------------------------------ |
| `vpp_host_tap`    | `""` (disabled)    | Linux tap name, e.g. `vmtap`   |
| `vpp_tap_gateway` | `10.0.10.1/24`     | Address assigned to the tap    |
| `vpp_tap_subnet`  | `10.0.10.0/24`     | Subnet used in the printed NAT rules |

Printed (manual) instructions, replace `<wan>` with your internet NIC:

```bash
echo 'net.ipv4.ip_forward=1' | sudo tee /etc/sysctl.d/99-vm-forward.conf
sudo sysctl -p /etc/sysctl.d/99-vm-forward.conf
sudo iptables -t nat -A POSTROUTING -s 10.0.10.0/24 -o <wan> -j MASQUERADE
sudo iptables -A FORWARD -i vmtap -o <wan> -j ACCEPT
sudo iptables -A FORWARD -i <wan> -o vmtap -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
```

**VPP NAT44 with a dedicated uplink NIC**

With `vpp_nat44=true` + `vpp_uplink=eth1`, VPP takes over that NIC via
AF_PACKET (the Linux interface is effectively consumed — flush its IPs
first) and does the NAT itself: loopback BVI `loop0` is the inside
gateway and the uplink is the outside interface (DHCP client or
`vpp_uplink_address`). Use a dedicated NIC, never your management link.

## Usage

Pass `vg_name` with `-e`:

```bash
ansible-playbook setup-thin-pool.yml -i <inventory> -e vg_name=<vg-name>
```

Example with size:

```bash
ansible-playbook setup-thin-pool.yml -i hosts.ini -e vg_name=vg0 -e thin_pool_size=200G
```

Override the pool name:

```bash
ansible-playbook setup-thin-pool.yml -i hosts.ini -e vg_name=vg0 -e thin_pool_name=my-pool
```

An example inventory is provided in `hosts.ini`; edit hosts/users to match your environment.

With an inline host:

```bash
ansible-playbook setup-thin-pool.yml -i 'myhost.example.com,' -e vg_name=vg0 -K
```

`-K` prompts for the sudo password (the playbook uses `become: true`).

## VM API server (Go)

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

### Prerequisites

- Go 1.24 or newer (build only)
- LVM2 tools (`vgs`, `lvs`, `lvcreate`) and an existing volume group
- FD.io VPP with its binary API enabled (`socksvr`), normally `/run/vpp/api.sock`
- The VPP vhost-user plugin (`vpp-plugin-core`) installed and loaded; check with
  `vppctl show plugins | grep vhost`
- The VPP ACL plugin loaded for interface ACLs; check with
  `vppctl show plugins | grep acl`
- No external DHCP or DNS daemon is required; DHCPv4/DHCPv6 use the embedded CoreDHCP servers and DNS VNFs run the built-in resolver
- A spare **wired** Ethernet NIC for NAT44 outside use; wireless and the host
  default-route NIC are rejected
- `vppctl` on `PATH` with access to VPP's CLI socket for NAT44 VNFs
  (`setup-vpp.yml` installs it with VPP)
- Root or `CAP_NET_ADMIN` + `CAP_NET_BIND_SERVICE` + `CAP_NET_RAW` for VNF tap setup,
  UDP 67/547 binding, and NAT44 host-NIC takeover
- `iproute2` (`ip`) for assigning IPv4/IPv6 gateway addresses to VNF taps and
  flushing an outside NIC before VPP takes it over
- QEMU with KVM support (`qemu-system-x86_64` and `/dev/kvm`)
- Permission to create `/run/rookery` (or configure another `-run-dir`) and to
  start QEMU processes

The socket is VPP's **binary API** socket, not the CLI socket
(`/run/vpp/cli.sock`) or a QEMU vhost-user socket. `setup-vpp.yml` configures
`/run/vpp/api.sock`.

Build a standalone binary:

```bash
go build -o bin/rookery ./cmd/rookery
```

### Initialize

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

### Serve

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

### Public mTLS API

`serve` starts two public mTLS listeners for API clients:

- `-mtls-addr` (default `:8443`, env `ROOKERY_MTLS_ADDR`) serves the generated
  HTTPS gateway API.
- `-grpc-mtls-addr` (default `:9443`, env `ROOKERY_GRPC_MTLS_ADDR`) serves the
  generated gRPC services, which is what the Terraform provider uses.

Both require a client certificate signed by the server's CA. The UI, native
routes, and certificate-management endpoints are not exposed on either
listener. Set either address empty to disable that listener. Enabling either
listener initializes the CA under `-pki-dir`; `serve` fails at startup if that
directory cannot be created.

The **Client certificates** item in the left sidebar (under **Access**) issues
client certificates:

- Download the CA certificate once and distribute it to clients.
- Issue a certificate for a client name; the client certificate, private key,
  and CA are shown once and can be downloaded or copied. Rookery never stores
  the private key.
- Revoke certificates from the list; revocation is checked on every TLS
  handshake (session tickets are disabled).

The CA and server keys live under `-pki-dir` (default `/var/lib/rookery/pki`,
env `ROOKERY_PKI_DIR`) with `0700`/`0600` permissions. Protect and back up that
directory: losing the CA invalidates every issued client certificate and
changes the certificate clients must trust.
The server certificate is generated for `localhost`, the hostname, all host IP
addresses, and any comma-separated `-mtls-sans` (env `ROOKERY_MTLS_SANS`)
entries; add public DNS names or IPs there if clients connect through them.

Example HTTPS client:

```bash
curl --cert client.crt --key client.key --cacert ca.crt \
  https://api.example.com:8443/api/status
```

The local UI/API listener is still unauthenticated and should remain bound to
localhost or sit behind an access-controlled proxy.

### Terraform provider

`cmd/terraform-provider-rookery/` implements a Terraform provider that creates
Rookery resources over the public mTLS gRPC listener. It
covers storage backends, disks, media, networks, interfaces, ACLs, route tables
and routes, VNFs, VMs, and host-interface takeover. Matching data sources look
up existing resources, and `rookery_status` exposes current readiness. Configure
it with the CA, client certificate, and private key downloaded from the
**Client certificates** dialog:

```hcl
provider "rookery" {
  endpoint  = "api.example.com:9443"
  ca_file   = "ca.crt"
  cert_file = "client.crt"
  key_file  = "client.key"
}
```

See `cmd/terraform-provider-rookery/README.md` for the resource and data source
list, local `dev_overrides` setup, import formats, and examples.

### Logging

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

Rookery Networks are VPP bridge domains. The **Rookery Networks** tab creates a
bridge domain (the server allocates a free ID), then creates vhost-user interfaces
with generated MAC addresses. Attach an interface to a stopped VM; QEMU gets a
virtio-net device using that vhost socket on the next start. The VM creation form
can also select multiple unattached interfaces at once (`interface_ids`), and the
edit dialog can add or remove interfaces while the VM is stopped. On update,
omitting `interface_ids` leaves attachments unchanged; send
`replace_interface_ids: true` with an empty array to detach all interfaces. Only unattached or currently attached interfaces are offered,
and attachments change only when a VM starts.
Interfaces remain in VPP when a VM is deleted or the interface is detached, so
they can be reused.
VPP state is in memory. After a VPP restart, API startup reconciliation
recreates missing bridge domains, vhost-user interfaces, NAT44/BVI state, and
VNF taps. QEMU chardevs use `reconnect=1`, so VMs started with the current
arguments can reattach after VPP recreates the sockets; VMs started by an older
server without reconnect still need a stop/start. Vhost-user interfaces
require the VPP `vpp-plugin-core` plugin; if `vppctl show plugins` does not list
`vhost_plugin`, install the package and restart VPP. API-created networks are
isolated L2 bridge domains by default. NAT44 can be
added as a VNF on a dedicated network; the API then creates a BVI
(`bvi<network-id>`) and takes over the selected outside NIC. The standalone
`setup-vpp.yml` NAT44 setup is independent; do not point both at the same
outside NIC.

### Network ACLs

The **Rookery Networks** tab also manages named ACLs. An ACL has a unique name,
title, description, and an ordered list of permit/deny rules. Each rule also
has its own optional description for documentation. Rules support IPv4/IPv6
source and destination CIDRs, `any`/`tcp`/`udp`/`icmp`/`icmpv6` protocols, and
TCP/UDP source/destination port ranges. The first matching rule
wins; unmatched traffic is denied.

ACLs are applied to Rookery Virtual Interfaces, not networks or VMs. Use **ACLs**
on an interface row to choose one or more **From interface** and **To interface**
ACLs by name. The dialog lets you order them: VPP evaluates the list in order,
so the first matching rule across the selected ACLs wins. "From interface"
filters traffic coming from that VM into VPP; "To interface" filters traffic
going from VPP to that VM. Rules are installed in VPP through the ACL plugin and
survive API/VPP reconciliation. The editor and assignment dialog both show the
full effective ACL text, including the implicit deny. An ACL cannot be deleted
while assigned to an interface.

### Route tables

Route tables are VPP IP FIB/VRF definitions. The **Route tables** subtab always
shows the default VRF (table `0`) for both IPv4 and IPv6, plus custom tables.
Custom tables have a name, a table ID, and an address family; the same ID may be
used once for IPv4 and once for IPv6. Create a table with **Add route table**,
then use **Route tables** on an interface row to bind its IPv4 and IPv6 FIBs.
Table `0` is always available and cannot be deleted. Deleting a custom table is
blocked while an interface or static route references it. VPP table definitions,
interface bindings, and static routes are recreated during startup reconciliation
after a VPP restart.

Use **Routes** on a table row to list, add, and delete static entries. Each route
has a prefix, next hop, and optional egress interface; leave the interface empty
to let VPP resolve the next hop through the table. The route editor works for
the default VRF `0` as well as custom tables.

The route editor is a full page at
`/route-tables/{id}?ipv6=<bool>` (the legacy
`/route-tables/{id}/routes?ipv6=<bool>` form also works), so refreshing the
browser keeps the selected VRF open. The create page is `/route-tables/new`.
It also shows **Dynamic routes** read from the live VPP FIB:
connected/interface routes, DHCP and IPv6 ND routes, adjacency entries, and
other non-API sources. They are read-only and include source and forwarding
path details for troubleshooting.

The **Network** tab includes a **Troubleshoot** section. It currently renders
the active VPP packet-processing graph from `vppctl show vlib graphviz filter`;
node size is cycles/packet and color is packets/call. Interface-related nodes
(`*-input`, `*-output`, `*-tx`, BVI/VNF/host nodes) have a blue ring. The graph is
laid out top-to-bottom with input nodes at the top and terminal/drop nodes at the
bottom; feedback edges are dashed. Use the **Focus** picker to focus the
graph on one interface or packet feature (ACL plugin, NAT44, DHCP) and its
directly related nodes, or choose **All nodes**.
Drag to pan, scroll or use the `+`/`-` buttons to zoom, and use **Fit** to reset
the view. **Labels** shows all node names, while hovering a node isolates that
node and its directly connected neighbors. The graph is refreshed on demand, and
nodes are only shown if they were active since the last VPP stats clear. Interface
nodes appear only after the interface has processed traffic.

Each row in the **Network → Interfaces** tab shows its ACL assignments and compact
per-interface match totals. **ACL details** expands the row to show per-direction
and per-rule counters (`matched`, `denied`, and `permitted`) from VPP's applied
lookup contexts. These count explicit ACL rule matches since the ACL was applied;
implicit-deny packets are not included. If you only need a specific rule's count,
the rule row shows its own packet total and action.

The same **Troubleshoot** section includes **Packet trace**. Select a registered VPP node (or type a node name), set the packet count, and click
**Start trace**. Generate traffic through that node, then click **Show trace** to
read the VPP trace buffer. **Clear** removes the trace. The node picker is
populated from all registered VPP graph nodes; traces are capped at 1,000 packets.

The same **Troubleshoot** section includes **Packet capture**. Choose a VPP
interface (or `Any interface`), direction, maximum capture size, and snapshot
length. The API converts the size and snapshot length into VPP's packet-ring
limit, starts `pcap trace`, and stops it on demand. Captures are written by VPP
under `/tmp` as `rookery-*.pcap` and can be downloaded, opened in the embedded
WebShark viewer, or deleted. The active capture file cannot be deleted until the
capture is stopped. WebShark v3.0.0 is vendored under
`internal/webui/static/webshark` (GPL-2.0), with Roboto and Material Icons fonts
served locally; the upstream `ffmpeg-core.wasm` is omitted, so RTP audio playback
is unavailable while packet dissection works.

Network functions (**VNFs**) are attached to Rookery Networks. The **VNFs** tab
supports `dhcpv4`, `dhcpv6`, `nat44`, `bgp`, and `dns` functions; the VNF record
has a type, network, enable flag, and type-specific configuration. A network can
have one VNF of each type. Adding or editing a VNF opens a full page at `/vnfs/new`
or `/vnfs/{id}/edit`, so refreshing keeps the form open; lease and session
views remain dialogs.

DNS VNFs provide a small authoritative DNS server on the network's shared VNF
memif, reachable at the network service address (`.2`, UDP/TCP 53). Define
records with a name, type (`A`, `AAAA`, `CNAME`, `TXT`), value, and TTL; names
are fully qualified and may use a leading wildcard such as `*.lab1.test`.
Set **Forward names without a local record** to send other queries to the
configured upstream servers (IP or IP:port), or disable it to answer unknown
names with NXDOMAIN. If forwarding is enabled without upstreams, the daemon uses
`/etc/resolv.conf`. Local records always take precedence. Point DHCP clients at
the `.2` service address through the DHCP VNF's DNS field.

BGP VNFs are embedded GoBGP speakers with one peer, using the address family of
`local_ip` (IPv4 or IPv6 unicast). Choose a `memif` transport for a gVisor
userspace stack over VPP, or `tap` for kernel TCP. The configuration accepts
local/peer ASNs, router ID, local CIDR, peer address, optional peer gateway,
an optional advertised next hop (defaults to `local_ip`), multihop TTL
(default 64), passive mode, an optional TCP MD5 password (tap only), a raw
target VPP table ID, and advertised prefixes matching the session family. Received best paths are installed in the target VPP table and withdrawn
when the session or VNF stops; the BGP Status dialog shows the session state,
advertised prefixes, and received prefixes with next hop and AS path. DHCP VNFs each create a VPP tap (`vnf<id>`) in the network's
bridge domain, assign their configured address to the Linux tap, and run an
in-process CoreDHCP server bound to that tap.

### Host interfaces

The **Host interfaces** section (under **Rookery Networks**) lists the Linux
NICs visible to the server. Physical, non-wireless interfaces that do not carry
the host default route can be **Attached to VPP**; this creates a
`host-<nic>` AF_PACKET interface in VPP (flushing host IP addresses on the
NIC). Attachments are persisted and recreated after a VPP restart. AF_PACKET
interfaces use 64 KiB RX frames so GRO/GSO bursts are not truncated, and
Rookery-created vhost/memif/BVI/tap interfaces use a 1500-byte MTU matching
the guest and typical physical networks. Rookery disables the Linux NIC's
GRO/LRO receive offloads (via `ethtool`) because NAT44 rewrites of coalesced
RX packets cause heavy TCP retransmissions, while keeping TSO/GSO enabled for
upload throughput. Upgrading from a version that used VPP's 2048-byte RX
frames recreates the host takeover once and reapplies NAT44. **Detach** removes
the VPP interface and returns the NIC to the host; the host may need to renew
DHCP. An interface used by a NAT44 VNF cannot be detached here.

### Host taps

**Host taps** (the next subtab under **Rookery Networks**) connect a Rookery
Network's bridge domain to a Linux tap interface on the server, so processes on
the host can reach VMs and other machines on that network without going through
a VM. Create one by choosing a network and an optional Linux interface name
(defaults to `hostnet<network id>`); the name is limited to 15 characters by
Linux. An optional IPv4 address inside the network CIDR (for example
`10.20.0.50/24`) is assigned to the host side of the tap; it must use the
network's prefix length and stay outside the first five reserved addresses, the
DHCP range, and DHCP static assignments. Leave the address empty to use DHCP or
configure the interface manually.

Host taps create a VPP tap (`hostnet…`) in the network bridge domain and bind it
to the network's VRF. They are stored in `network_host_taps` and recreated by
API startup reconciliation and after VPP restarts. Deleting a network also
deletes its host taps. The API is `GET`/`POST /api/host/taps` and
`DELETE /api/host/taps/{id}`; the create body accepts `network_id`, optional
`name`, and optional `ipv4_address`.

### Network addressing

Every Rookery Network requires an IPv4 CIDR and an IPv6 CIDR. The create dialog
defaults to `10.0.0.0/16` and `fd00:20::/64`. The first five host addresses of
each prefix are reserved for Rookery:

- `.1` — network BVI (gateway)
- `.2` — shared VNF memif stack
- `.3`–`.5` — reserved for future internal use

DHCP ranges, DHCP static assignments, and NAT44 DNAT local addresses must stay
inside the network prefix and outside the reserved addresses. The NAT44 gateway
must be the network BVI address (`.1`). The IPv6 CIDR currently defines the
allowed IPv6 range and reservations; IPv6 BVI/memif address assignment follows
with shared DHCPv6.

### Shared VNF networking

DHCPv4 VNFs use one shared memif per network by default. Creating a network
creates the VPP memif master (`net<id>-vnf`) and stores its socket and
interface index on the network; the first shared VNF also creates the network
BVI owning the gateway (`.1`) and assigns the VNF address `.2` (the next host
address) to the shared gVisor stack. All DHCPv4 VNFs on that network use the
same stack, so there is no per-VNF memif or gateway tap.

Each network also has its own VPP VRF table
(`100000 + network ID`, IPv4 and IPv6). The network BVI, shared VNF memif,
per-VNF taps/memifs, and vhost-user interfaces are all bound to that table, so
overlapping CIDRs across networks are valid and isolated. The table ID is
returned as `route_table_id`; use it as a BGP `target_table_id` to install
learned routes in the network's VRF.

The network VRF appears in the **Route tables** tab as `network-<name>` for
both IPv4 and IPv6, with table ID `100000 + network ID` and a
`network-managed` label. You can add static routes there; route table IDs from
`100000` upward are reserved for network VRFs and cannot be created manually.

The default is controlled by `networks.vnf_transport` (currently `shared`).
Each VNF can override it with `config.transport`:

- `shared` — network-wide memif and `.2` VNF address (DHCPv4 default)
- `dedicated` — per-VNF memif, as in earlier releases
- `tap` — VNF tap interface

DHCPv6 currently remains on a tap, and BGP keeps its dedicated memif/tap
transport. Running VNFs keep their existing transport until restarted; a VPP
restart or VNF restart migrates DHCPv4 to the shared memif.

DHCP and BGP runtimes live in a separate `vnfs` daemon, so restarting the API
does not interrupt them. Run it alongside the API:

```sh
sudo vnfs -vpp-socket /run/vpp/api.sock -listen unix:///run/rookery/vnfs.sock
```

`vnfs` owns the CoreDHCP servers, the gVisor memif stacks, and GoBGP, writes
leases under `-run-dir`, restores its desired VNFs after its own restart, and
connects directly to the VPP binary API to install/withdraw BGP-learned routes.
The API keeps the database and all VPP provisioning (memif/tap creation, BVIs,
NAT44) and drives the daemon over an internal gRPC control channel
(`-vnf-addr`, default `unix:///run/rookery/vnfs.sock`; `tcp://host:port` is
supported). The API starts even when the daemon is absent — enabled VNFs show
`degraded` until it is reachable, and desired runtimes are re-ensured within
when the daemon is reachable. The API queries runtime state on demand (VNF
views and start/stop/restart decisions) instead of polling; the daemon restores
its persisted desired specs on its own restart.

DHCPv4 uses CoreDHCP's DHCPv4 server with a per-VNF bitmap allocator. DHCPv6
uses CoreDHCP's DHCPv6 server with stateful IA_NA allocation from the configured
IPv6 range; it does not provide router advertisements or prefix delegation, so
clients must be configured to request DHCPv6. DHCPv4 VNFs can also define
`static_assignments`, a list of unique MAC/IPv4 pairs. The reserved address may
be inside or outside the dynamic range, but it must be a usable host in the
DHCP subnet and cannot be the gateway or service address. Reserved addresses
are kept out of the dynamic allocator, so a client MAC always receives its
assigned address. Static assignments are DHCPv4-only. Lease files live under
`<run-dir>/vnfs/<vnf-id>.json`, and the **Leases** action shows client ID,
address, hostname, expiry, and active state. The server does not use CoreDHCP's
`range` plugin because that plugin imports the CGO `mattn/go-sqlite3` driver;
leases are stored as JSON instead, keeping the project CGO-free.

DHCPv4 can run over a VPP **memif** (the default) or a kernel tap. Set
`-dhcp-transport tap` (or `ROOKERY_DHCP_TRANSPORT=tap`) to fall back to the
old transport. VPP creates the memif master and its socket under
`/run/rookery/memif/<vnf-id>.sock` (or `-memif-dir`), and the API process
attaches as a slave. A
gvisor netstack owns the DHCP service address, answers on UDP/67, and keeps
CoreDHCP's existing allocator/lease plugin. The VPP memif interface uses the
VNF name (for example `lab-dhcpv4`), not a numeric `vnf<id>`. On NAT networks
the BVI keeps `.1` and the memif netstack uses `.2`. On plain L2 networks the
memif netstack owns the configured gateway address; there is no separate
gateway tap and no forwarding to the host. Use a NAT network when VMs need
external connectivity. DHCPv6 has not been migrated yet and still uses the tap
path.

NAT44 is IPv4-only. Use a dedicated network whose only purpose is NAT. On
enable, the NAT44 VNF:

- creates or reuses the network BVI (`bvi<network-id>`), adds the configured
  gateway (for example `10.20.0.1/24`), and stores it on the network record;
- refuses any host default-route NIC and wireless NICs, flushes the selected
  wired outside NIC's host addresses, and creates a VPP `host-<nic>` interface
  with the physical NIC's MAC address (required for upstream ARP/switch compatibility);
- enables VPP NAT44-ED and marks the BVI inside and the outside interface out;
- uses a static outside address and default route when `outside_address` is set,
  or starts the VPP DHCP client (which installs the leased address and default
  route) when it is empty; `outside_gateway` is required for a static default
  route;
- optionally adds a NAT44 pool with `nat44_pool` (address or `start-end`);
- optionally forwards TCP/UDP ports from the outside address to services on the
  NAT network with `dnat_rules` (DNAT / port forwarding).

A DNAT rule has a `protocol` (`tcp` or `udp`), an `external_port`, and the
internal destination's `local_ip` and `local_port`. The external address is the
outside interface's current address, so rules also work when the address comes
from DHCP. The internal address must be a usable host in the NAT44 subnet.
Rules are stored with the VNF and are restored after VPP restarts. For example:

```json
"config": {
  "gateway": "10.20.0.1/24",
  "outside_interface": "enp9s0",
  "dnat_rules": [
    {"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80},
    {"protocol": "udp", "external_port": 5353, "local_ip": "10.20.0.101", "local_port": 53}
  ]
}
```

Saving the NAT44 VNF restarts its NAT44 configuration, so existing
translations may be interrupted briefly.

If a DHCPv4 VNF exists on a NAT network, the API keeps its DHCP server tap on the
next address (`.2`) and hands out the BVI gateway (`.1`) as the client router.
Creating a DHCPv4 VNF later on a network with a BVI applies the same rule. The
DHCP range must be inside the NAT subnet and must not include the service
address. NAT44 does not create a tap or leases; use the **Sessions** action or
`GET /api/vnfs/{id}/sessions` to inspect VPP translations. Disabling/deleting
NAT44 removes its interface, pool, and DNAT static-mapping rules. The BVI is
removed with the network, but the VPP `host-<nic>` interface and outside
addressing remain until VPP is restarted or the interface is removed manually;
host addresses flushed during takeover are not restored automatically.

A VNF is `running` when its server is active (or NAT44 is applied), `disabled`
when the enable flag is off, and `degraded` when it is enabled but the runtime
is missing. When the API service starts, it waits for VPP, ensures every
recorded Rookery Network bridge domain exists (recreating a missing domain with
its DB-assigned ID), reconciles recorded vhost-user interfaces by name,
restores ACL definitions and interface assignments, reconciles enabled NAT44 VNFs (BVI, outside interface, and NAT rules), and then
reconciles enabled DHCP VNFs. An existing `vnf<id>` tap is
reused; a missing or migrated tap is recreated with its CoreDHCP server. This
happens before any manual **Restart** is needed.

While the API is running, it control-pings the VPP binary API every five
seconds and compares the API socket identity. If VPP restarted, the API clears
its cached NAT/VNF runtime state and rebuilds bridge domains, vhost-user
interfaces, ACLs, NAT/BVI state, and VNF taps automatically. VMs started with
`reconnect=1` can reattach after the vhost socket is recreated; older VMs may
still need a restart. The rebuild runs in a background worker so health checks
continue, and a further VPP restart queues another rebuild after the in-flight
one finishes.

A VPP restart also removes vhost-user interfaces and the VM connections that
used them. On API startup, recorded vhost-user interfaces are looked up by name
and recreated if missing, with their stored MAC and socket path; the database
`sw_if_index` is updated. QEMU's chardev `reconnect=1` lets current VMs reattach
after the socket is recreated; older VMs may still need a stop/start. Deleting a VNF stops it, removes its tap, and
deletes its leases. Existing network-level DHCP configuration is migrated into a
DHCPv4 VNF; the first startup reconciliation/restart of that VNF removes the
old `ndn<network-id>` tap.

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

| Flag              | Environment variable   | Default             | Used by         | Description |
| ----------------- | ---------------------- | ------------------- | --------------- | ----------- |
| `-config`         | `ROOKERY_CONFIG`       | —                   | `serve`, `init`, `vnfs` | YAML configuration file |
| `-addr`           | `ROOKERY_ADDR`       | `127.0.0.1:8080`    | `serve`         | HTTP listen address |
| `-db`             | `ROOKERY_DB`         | `rookery.db`      | `serve`         | SQLite database path |
| `-rate-limit`     | `ROOKERY_RATE_LIMIT` | `3`                 | `serve`         | HTTP requests per second for each URL path; `0` disables |
| `-vg`             | `ROOKERY_VG`         | empty               | `serve`, `init` | Bootstrap LVM volume group; optional for `serve` |
| `-thin-pool`      | `ROOKERY_THIN_POOL`  | `rookery`         | `serve`, `init` | Bootstrap thin pool name or `<vg>/<name>`; `serve` can use UI backends instead |
| `-vpp-socket`     | `ROOKERY_VPP_SOCKET` | `/run/vpp/api.sock` | `serve`, `init` | VPP binary API Unix socket |
| `-qemu-bin`       | `ROOKERY_QEMU_BIN`   | `qemu-system-x86_64`| `serve`         | QEMU system emulator binary |
| `-run-dir`        | `ROOKERY_RUN_DIR`    | `/run/rookery`    | `serve`         | QEMU pid files and VNC Unix sockets |
| `-memif-dir`      | `ROOKERY_MEMIF_DIR`    | `/run/rookery/memif`| `serve`         | VNF memif socket directory |
| `-vhost-dir`      | `ROOKERY_VHOST_DIR`  | `/run/vpp`          | `serve`         | VPP vhost-user socket directory |
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

### Configuration file

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
  vg: msd-beelink-vg
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

### Protobuf API design

The API design lives under `design/`. Proto files under `design/proto/rookery/v1`
define the request/response messages, HTTP annotations, and validation rules.
Generation uses `protoc` directly through the root Makefile; `buf` is not used.
Generated Go, gRPC, grpc-gateway, validation, and OpenAPI manifests are committed
under `design/gen` and `design/openapi`.

```bash
make proto-tools     # install pinned generator binaries
make proto-generate  # regenerate Go, gRPC, gateway, validation, OpenAPI
make proto-compile   # compile protos to a descriptor set
make proto-check     # verify committed generated output is current
```

Request validation is generated by `protoc-gen-validate` and runs in a gRPC
unary interceptor, which also covers HTTP gateway traffic. The first gateway
slice under `internal/gateway` exposes system health/status, read-only list/get
operations for VMs, disks, media, networks, interfaces, ACLs, VNFs, storage
backends, and host interfaces, plus VM/disk/media/network/interface/ACL/storage
writes, VM lifecycle actions and metrics, interface ACL assignment, storage
disk import, network lifecycle deletion, troubleshoot/capture services, and VNF
writes. 64-bit JSON fields
are emitted as numbers to match the existing REST API, and creates/deletes use
`201`/`204`. VNF writes call the native lifecycle service in
`internal/lifecycle` so activation/NAT behavior is shared without an HTTP round
trip. The main `serve`
listener routes `/api` through the gateway;
`-gateway-addr` adds an optional second gateway listener and `-grpc-addr`
exposes direct gRPC with health checks and server reflection. WebSocket VNC, WebShark SSE/static assets, and streamed pcap
downloads are served by `api.Server.NativeHandler`, which contains only those
native routes plus the static UI.

## API

| Method   | Path               | Description                       |
| -------- | ------------------ | --------------------------------- |
| `GET`    | `/api/healthz`     | Liveness and database check       |
| `GET`    | `/api/status`      | Thin pool and VPP readiness       |
| `GET`    | `/api/storage-backends` | List registered storage backends |
| `POST`   | `/api/storage-backends` | Register an LVM thin-pool backend |
| `GET`    | `/api/storage-backends/{id}` | Get one storage backend |
| `PUT`    | `/api/storage-backends/{id}` | Update an unused backend |
| `DELETE` | `/api/storage-backends/{id}` | Delete a backend with no disks |
| `GET`    | `/api/storage-backends/{id}/importable-disks` | List existing thin volumes and registration state |
| `POST`   | `/api/storage-backends/{id}/import-disks` | Register existing thin volumes as disks |
| `GET`    | `/api/disks`       | List all disks                    |
| `POST`   | `/api/disks`       | Create a thin-LVM disk or template clone |
| `GET`    | `/api/disks/{id}`  | Get one disk                      |
| `PUT`    | `/api/disks/{id}`  | Mark or unmark a disk as a template |
| `DELETE` | `/api/disks/{id}`  | Delete an unattached disk         |
| `GET`    | `/api/media`       | List installation media           |
| `POST`   | `/api/media`       | Register an existing ISO file     |
| `GET`    | `/api/media/{id}`  | Get one media entry               |
| `DELETE` | `/api/media/{id}`  | Remove an unused media entry      |
| `GET`    | `/api/networks`    | List Rookery Networks (VPP bridge domains) |
| `POST`   | `/api/networks`    | Create a VPP bridge domain        |
| `GET`    | `/api/networks/{id}` | Get one network                 |
| `DELETE` | `/api/networks/{id}` | Delete an empty network and its VNFs |
| `GET`    | `/api/host/interfaces` | List physical/external NICs, link state, and VPP host-interface correlation |
| `GET`    | `/api/host/taps` | List persisted network-to-host taps (`?network_id=<id>` optional) |
| `POST`   | `/api/host/taps` | Create a network-to-host tap (`network_id`, optional `name`, `ipv4_address`) |
| `DELETE` | `/api/host/taps/{id}` | Delete a network-to-host tap |
| `GET`    | `/api/troubleshoot/vpp-graph` | Active VPP packet-processing node graph |
| `GET`    | `/api/troubleshoot/acl-counters` | Per-interface ACL rule-match counters |
| `POST`   | `/api/troubleshoot/trace` | Start a VPP packet trace (`node`, `count`) |
| `GET`    | `/api/troubleshoot/trace/nodes` | List all registered VPP graph nodes |
| `GET`    | `/api/troubleshoot/trace` | Read the current VPP trace buffer |
| `DELETE` | `/api/troubleshoot/trace` | Clear the VPP trace buffer |
| `GET`    | `/api/troubleshoot/capture/interfaces` | List VPP interfaces for capture |
| `GET`    | `/api/troubleshoot/capture` | Current capture status and retained files |
| `POST`   | `/api/troubleshoot/capture` | Start a capture (`interface`, `direction`, `max_bytes`, `snap_length`) |
| `POST`   | `/api/troubleshoot/capture/stop` | Stop the active capture |
| `GET`    | `/api/troubleshoot/capture/files` | List retained pcap files |
| `GET`    | `/api/troubleshoot/capture/files/{name}` | Download a pcap file |
| `DELETE` | `/api/troubleshoot/capture/files/{name}` | Delete a retained pcap file |
| `GET`    | `/webshark/`       | Embedded WebShark pcap viewer |
| `GET`    | `/api/vnfs`        | List VNFs (`?network_id=<id>` optional) |
| `POST`   | `/api/vnfs`        | Create a DHCPv4, DHCPv6, NAT44, BGP, or DNS VNF |
| `GET`    | `/api/vnfs/{id}`   | Get one VNF                       |
| `PUT`    | `/api/vnfs/{id}`   | Update name, config, or enable flag |
| `DELETE` | `/api/vnfs/{id}`   | Stop and delete a VNF             |
| `POST`   | `/api/vnfs/{id}/restart` | Recreate the tap and start the VNF |
| `GET`    | `/api/vnfs/{id}/leases` | Inspect current and expired DHCP leases |
| `GET`    | `/api/vnfs/{id}/sessions` | Inspect VPP NAT44 translations |
| `GET`    | `/api/acls`        | List ACL definitions              |
| `POST`   | `/api/acls`        | Create an ACL                     |
| `GET`    | `/api/acls/{id}`   | Get one ACL and its full rules    |
| `PUT`    | `/api/acls/{id}`   | Update an ACL                     |
| `DELETE` | `/api/acls/{id}`   | Delete an unassigned ACL          |
| `GET`    | `/api/route-tables` | List default and custom IP FIB/VRF tables |
| `POST`   | `/api/route-tables` | Create an IPv4 or IPv6 route table |
| `DELETE` | `/api/route-tables/{id}` | Delete an unassigned route table |
| `GET`    | `/api/route-tables/{id}/routes?ipv6=<bool>` | List static routes in a table |
| `POST`   | `/api/route-tables/{id}/routes` | Add a static route |
| `DELETE` | `/api/routes/{id}` | Delete a static route |
| `GET`    | `/api/route-tables/{id}/fib?ipv6=<bool>` | Inspect live VPP FIB entries, including dynamic routes |
| `GET`    | `/api/interfaces`  | List vhost-user interfaces        |
| `POST`   | `/api/interfaces`  | Create a vhost-user interface      |
| `GET`    | `/api/interfaces/{id}` | Get one interface              |
| `PUT`    | `/api/interfaces/{id}` | Attach/detach to a VM          |
| `PUT`    | `/api/interfaces/{id}/acls` | Assign ordered ingress/egress ACL lists by name |
| `PUT`    | `/api/interfaces/{id}/route-tables` | Bind IPv4/IPv6 FIB tables |
| `DELETE` | `/api/interfaces/{id}` | Delete an unattached interface |
| `GET`    | `/api/vms`         | List all VMs                       |
| `POST`   | `/api/vms`         | Create a VM with an existing disk |
| `GET`    | `/api/vms/{id}`    | Get one VM                         |
| `GET`    | `/api/vms/{id}/metrics` | QEMU CPU, memory, and I/O telemetry |
| `PUT`    | `/api/vms/{id}`    | Update VM settings                 |
| `DELETE` | `/api/vms/{id}`    | Delete a VM, freeing its disks     |
| `POST`   | `/api/vms/{id}/start` | Start the VM with QEMU/KVM      |
| `POST`   | `/api/vms/{id}/stop`  | Stop the VM                     |
| `POST`   | `/api/vms/{id}/ctrl-alt-del` | Send Ctrl+Alt+Del via QEMU's monitor |
| `POST`   | `/api/vms/{id}/reset` | Force a VM reset; power-cycles legacy VMs without a monitor |
| `GET`    | `/ws/vms/{id}/vnc`    | WebSocket proxy to the VM's VNC Unix socket |

Create a root disk and, optionally, additional data disks:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/disks \
  -H 'Content-Type: application/json' \
  -d '{"name":"web-01-root","size_gb":40}'
curl -sS -X POST http://127.0.0.1:8080/api/disks \
  -H 'Content-Type: application/json' \
  -d '{"name":"web-01-data","size_gb":100}'
```

`POST /api/disks` accepts an optional `storage_backend_id`; omit it for the
default backend. Empty disks run `lvcreate` against the selected backend's thin
pool. A backend with `supports_templates` can also create a disk from another
disk marked as a template using `template_disk_id`; the backend's
`preparation_script` creates the clone LV/device. The clone size defaults to the
template size and may only be increased.
Existing thin volumes can be imported without running LVM commands:

```bash
curl -sS http://127.0.0.1:8080/api/storage-backends/1/importable-disks
curl -sS -X POST http://127.0.0.1:8080/api/storage-backends/1/import-disks \
  -H 'Content-Type: application/json' \
  -d '{"names":["old-root","old-data"]}'
```

Import only adds inventory rows in a transaction; it never creates, copies, or
resizes an LV. The list marks volumes that are already registered, and the
reported size is rounded up to whole GB. Deleting an imported disk is still
destructive: it removes the underlying thin volume.
The response includes the
LVM location:

```json
{
  "id": 1,
  "name": "web-01-root",
  "size_gb": 40,
  "vg": "vg0",
  "thin_pool": "rookery",
  "device": "/dev/vg0/web-01-root"
}
```

Then register an installation ISO (the file must already exist on the host):

```bash
curl -sS -X POST http://127.0.0.1:8080/api/media \
  -H 'Content-Type: application/json' \
  -d '{"name":"debian-13-netinst","path":"/srv/isos/debian-13.1.0-amd64-netinst.iso"}'
```

The server validates the path and records its size:

```json
{
  "id": 1,
  "name": "debian-13-netinst",
  "path": "/srv/isos/debian-13.1.0-amd64-netinst.iso",
  "size_bytes": 681574400
}
```

Then create a VM with one or more disk IDs and optional boot media:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/vms \
  -H 'Content-Type: application/json' \
  -d '{"name":"web-01","disk_ids":[1,2],"boot_media_id":1}'
```

The first disk ID is the boot disk. Omit `boot_media_id` to boot from the disks
instead. The created VM has a UUID
`id`; use that UUID in all VM paths and in the console URL.

Start and stop the VM:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/vms/<uuid>/start
curl -sS -X POST http://127.0.0.1:8080/api/vms/<uuid>/stop
```

Open the noVNC console at:

```text
http://127.0.0.1:8080/console.html?vm=<uuid>
```

The page connects to `ws://127.0.0.1:8080/ws/vms/<uuid>/vnc`. The server dials
QEMU's VNC Unix socket (`<run-dir>/<uuid>.vnc`) and proxies RFB frames directly;
no TCP VNC port is exposed. The console page uses noVNC v1.7.0, vendored under
`internal/webui/static/novnc` (no CDN). Its toolbar provides Ctrl+Alt+Del
(injected through QEMU's monitor, with a VNC key-event fallback), a forced Reset,
reconnect, and a virtual keyboard with sticky Ctrl/Alt/Shift/Super modifiers,
a full US key layout, function keys, and navigation keys. The **Paste** button
opens a clipboard dialog. **Type into VM** sends the text as US keyboard input,
which works without guest tools and is the reliable option for guests without a
clipboard agent. **Set clipboard** uses noVNC's `clipboardPasteFrom`; QEMU now
provides a `qemu-vdagent` virtio-serial channel, so guests with `spice-vdagent`
(or the Windows equivalent) can then paste normally. Clipboard text can also be
pasted manually into the dialog when browser clipboard permissions are blocked.
Type into VM supports printable ASCII, Tab, and Enter (up to 4096 characters);
use Set clipboard for Unicode or larger content. VMs started before the vdagent
channel was added need one restart to expose it. For clipboard support in a
Debian/Ubuntu guest, install `spice-vdagent`; Windows guests can use
`spice-guest-tools`. Guests without an agent should use **Type into VM**. The monitor socket is
`<run-dir>/<uuid>.monitor` and is only used by the server; it is never exposed
through the HTTP API.

If a VM was started by an older server and has no monitor socket, Ctrl+Alt+Del
falls back to VNC key events, and Reset performs a stop/start power cycle that
also recreates the VM with a monitor socket.

Create a Rookery Network, add a vhost-user interface, and attach it to a stopped
VM:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/networks \
  -H 'Content-Type: application/json' \
  -d '{"name":"lab"}'

curl -sS -X POST http://127.0.0.1:8080/api/interfaces \
  -H 'Content-Type: application/json' \
  -d '{"name":"web-01-eth0","network_id":1}'

curl -sS -X PUT http://127.0.0.1:8080/api/interfaces/1 \
  -H 'Content-Type: application/json' \
  -d '{"attached_vm_id":"<uuid>"}'
```

Use `{"attached_vm_id":null}` to detach. The interface must be created while VPP
is ready, and the VM must be stopped when its attachment changes.

Add a DHCPv4 VNF to the network:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/vnfs \
  -H 'Content-Type: application/json' \
  -d '{"name":"lab-dhcpv4","network_id":1,"type":"dhcpv4","enabled":true,
       "config":{"gateway":"10.20.0.1/24","range_start":"10.20.0.100",
                 "range_end":"10.20.0.200","dns":"1.1.1.1,8.8.8.8",
                 "lease_time":"12h"}}'
```

Add a DHCPv6 VNF with an IPv6 gateway and range:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/vnfs \
  -H 'Content-Type: application/json' \
  -d '{"name":"lab-dhcpv6","network_id":1,"type":"dhcpv6","enabled":true,
       "config":{"gateway":"fd00:20::1/64","range_start":"fd00:20::100",
                 "range_end":"fd00:20::200","dns":"2001:4860:4860::8888",
                 "lease_time":"12h"}}'
```

DHCP VNFs also accept extra options through `dhcp_options`. Each option has a
`code`, a `format` (`string`, `ipv4`, `ipv6`, `hex`, `uint8`, `uint16`,
`uint32`, or `domain-list`), and a `value`:

```json
{
  "dhcp_options": [
    {"code": 15, "format": "string", "value": "lab.test"},
    {"code": 42, "format": "ipv4", "value": "10.20.0.1"},
    {"code": 119, "format": "domain-list", "value": "lab.test, apps.lab.test"}
  ]
}
```

For DHCPv6 use the corresponding DHCPv6 codes, for example option `24` with
`domain-list` for the domain search list. Server-managed structural options
such as the subnet mask, lease timers, and DHCPv6 IA options are rejected.
Router and DNS options (v4 `3`/`6`, v6 `23`) are allowed and override the
gateway and `dns` fields because custom options are applied last. The UI
suggests common option codes and automatically selects the matching format.

Add a DNS VNF with local records and upstream forwarding:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/vnfs \
  -H 'Content-Type: application/json' \
  -d '{"name":"lab-dns","network_id":1,"type":"dns","enabled":true,
       "config":{
         "dns_records":[
           {"name":"web.lab.test","type":"A","value":"10.20.0.50"},
           {"name":"web.lab.test","type":"AAAA","value":"fd00:20::50"},
           {"name":"*.apps.lab.test","type":"A","value":"10.20.0.51"},
           {"name":"alias.lab.test","type":"CNAME","value":"web.lab.test"},
           {"name":"info.lab.test","type":"TXT","value":"lab DNS"}
         ],
         "dns_forward":true,
         "dns_forwarders":["1.1.1.1","8.8.8.8"]
       }}'
```

The DNS server listens on the network service address `.2` (UDP/TCP 53).
Set the DHCP VNF's `dns` field to that address if clients should use it.

Create a dedicated NAT network, then add NAT44 with a static outside address
and DHCPv4 for clients. The outside NIC must not be the host default route; the
API flushes its host addresses and VPP takes it over.

```bash
curl -sS -X POST http://127.0.0.1:8080/api/networks \
  -H 'Content-Type: application/json' \
  -d '{"name":"nat"}'

curl -sS -X POST http://127.0.0.1:8080/api/vnfs \
  -H 'Content-Type: application/json' \
  -d '{"name":"nat44","network_id":2,"type":"nat44","enabled":true,
       "config":{"gateway":"10.20.0.1/24","outside_interface":"eth1",
                 "outside_address":"198.51.100.10/24",
                 "outside_gateway":"198.51.100.1"}}'

curl -sS -X POST http://127.0.0.1:8080/api/vnfs \
  -H 'Content-Type: application/json' \
  -d '{"name":"nat-dhcpv4","network_id":2,"type":"dhcpv4","enabled":true,
       "config":{"gateway":"10.20.0.1/24","range_start":"10.20.0.100",
                 "range_end":"10.20.0.200","dns":"1.1.1.1"}}'
```

For a VPP DHCP client outside address, omit `outside_address` and
`outside_gateway`. Use `nat44_pool` only when the ISP routes a separate NAT pool
to the host. `GET /api/host/interfaces` reports which NIC carries the default
route and whether a matching `host-<nic>` interface currently exists in VPP;
that default-route NIC and wireless interfaces are rejected by the server. Use
a spare wired Ethernet NIC for the outside uplink.

Use `PUT /api/vnfs/{id}` with `{"enabled":false}` to stop a DHCP VNF and remove
its tap, or to remove NAT44 rules. Enabled VNFs are reconciled automatically
when the API starts; use
`POST /api/vnfs/{id}/restart` for manual recovery or after recreating a network
whose VPP state was lost. A VNF's type and network are immutable; delete and
recreate it to move it. CoreDHCP runs inside the API process and binds UDP 67
(DHCPv4) or UDP 547 (DHCPv6) on the VNF tap. NAT44 runs through VPP and
requires `vppctl` access.
The process needs root or `CAP_NET_ADMIN` + `CAP_NET_BIND_SERVICE` +
`CAP_NET_RAW` (CoreDHCP uses `SO_BINDTODEVICE`). A bind failure or invalid
configuration returns an error and rolls back the tap.

While prerequisites are not ready, creation returns a JSON error explaining
which check failed. `GET /api/status` exposes the same information:

```json
{
  "ready": false,
  "thin_pool": {
    "ready": false,
    "state": "missing",
    "detail": "thin pool vg0/rookery does not exist; ...",
    "vg": "vg0",
    "name": "rookery"
  },
  "vpp": {
    "ready": true,
    "state": "ready",
    "detail": "control ping succeeded",
    "socket": "/run/vpp/api.sock"
  }
}
```

VM fields:

VM IDs are UUIDs. Migration `0004_vm_uuid.sql` converts existing integer VM IDs
to UUIDs; disk and media IDs remain integers.

| Field         | Required | Default   | Notes                                      |
| ------------- | -------- | --------- | ------------------------------------------ |
| `id`          | read-only | -        | UUID generated on creation                 |
| `name`        | yes      | -         | Unique, case-insensitive, max 64 chars     |
| `disk_ids`    | yes (create) / no (edit) | - | IDs of existing unattached disks; at least one. On edit, replaces the attached set while the VM is stopped. `disk_id` is still accepted as a single-disk alias on create |
| `disks`       | read-only | -         | Attached disk summaries, in attachment order |
| `interface_ids` | no (create/edit) | `[]`  | IDs of unattached virtual interfaces to attach at creation, or the desired set on update with `replace_interface_ids` |
| `replace_interface_ids` | no (edit) | `false` | Explicitly replace the interface set; allows clearing all interfaces |
| `replace_disk_ids` | no (edit) | `false` | Explicitly replace the disk set; empty is rejected because a VM needs a disk |
| `boot_media_id` | no     | -         | ID of an installation media entry; omit to boot from disk |
| `console_enabled` | no   | `false`   | Allow the VNC console for this VM          |
| `description` | no       | `""`      | Max 2000 chars                             |
| `status`      | no       | `stopped` | See statuses below                         |
| `vcpus`       | no       | `1`       | 1–128                                      |
| `memory_mb`   | no       | `512`     | 128–1048576                                |

New VMs have console access disabled by default. Existing VMs are enabled by
migration `0005_console_access.sql` so upgrades do not cut off active consoles.
Toggle `console_enabled` with `PUT /api/vms/{id}`. When disabled, the VNC
WebSocket returns `403` and any already-open console is closed within about a
second.

Disk fields: `name` (required, unique, max 127 chars), `size_gb` (default `10`,
1–65536), `storage_backend_id`, `template_disk_id`, and `is_template`. Disk size
is provisioned as a thin-LVM virtual size in GB. Templates are backend-scoped,
cannot be attached to a VM, and can be marked or unmarked with
`PUT /api/disks/{id}`. Deleting a template with clones is blocked unless the
backend sets `allow_template_delete_with_clones`.

Template-capable backends add `supports_templates`, `preparation_script`, and
`preparation_timeout` to their config. The script runs as the API user, receives
disk details in the `ROOKERY_DISK_JSON` environment variable, and is responsible
for creating the clone LV/device. Its JSON contains `operation` (`create` or
`delete`), `request_id`, `disk`, optional `template`, and `backend` objects. On
failure the script output is logged and the operation is rolled back; clones
created by a script are removed by the script on deletion.

`scripts/lvm-thin-snapshot-disk.sh` implements this contract for LVM thin
backends. It reads `ROOKERY_DISK_JSON`, creates a thin snapshot of the template,
clears the thin-snapshot activation-skip flag, activates the clone, and
optionally extends it to the requested `disk.size_gb`. On `delete` it removes
the clone; it also refuses to remove an unrelated pre-existing LV during
create-failure cleanup. Set the backend's `preparation_script` to the script's
absolute path. See `scripts/README.md` for requirements and standalone CLI
usage.

Media fields: `name` (required, unique, max 127 chars) and `path` (required,
absolute path to an existing regular file). `size_bytes` is read from the file
and cannot be set by clients. Media entries are read-only: the API never copies
or modifies the file. `DELETE /api/media/{id}` removes only the inventory entry
and is rejected while a VM uses the media as boot media.

Valid statuses are `stopped`, `starting`, `running`, `stopping`, `paused`, and
`error`. A VM can have multiple disks. At creation, provide `disk_ids` with at
least one existing unattached disk; the first ID is the boot disk. To replace
the attached set, send `disk_ids` on `PUT` while the VM is stopped; omit it to
leave the disks unchanged. A disk can be attached to only one VM and cannot be
deleted while attached. Deleting a VM makes all of its disks available again.
Deleting a disk removes both its LVM thin volume and its inventory record. The
VM response includes `disk_ids` and a `disks` array with disk details.
Unlike disks, a VM's boot media can be changed or cleared by `PUT`; sending
`boot_media_id` selects media and omitting it clears the selection.
Error responses use `{"error":"..."}` with a suitable HTTP status.

The UI is a thin client over these endpoints, so it can be replaced by any other
API consumer. Disk, media, QEMU lifecycle, and VPP network management are wired
up. Guest networking uses VPP vhost-user interfaces attached through Rookery
Networks; `-nic none` only disables QEMU's default NIC.

### UI development

The management UI is Preact + TypeScript under `ui/`. Sources are typechecked
with `tsc` and bundled with esbuild into `internal/webui/static/build`, which is
committed and embedded in the Go binary with `go:embed`. Do not edit the
generated bundles directly.

```bash
make ui-install   # npm ci
make ui-typecheck
make ui-build     # typecheck + bundle
make ui-watch     # rebuild while developing
make build        # UI bundle + Go binaries in bin/
make test         # UI build + go test ./...
```

Node.js and npm are only needed when changing UI sources; plain `go build` and
`go test` use the committed bundle. The UI is fully Preact/TypeScript: the
console page and all inventory tabs are components, with
`ui/src/inventory/app.ts` providing the typed tab/readiness shell. Preact
islands communicate through `rookery:*` window events; keep those bridges in
sync when changing shared state.

Inventory URLs are path-based and survive refresh: `/vms`, `/disks`, `/media`,
`/networks`, and `/vnfs` select tabs; dialog routes include `/vms/new`,
`/vms/{uuid}/edit`, `/vms/{uuid}/monitor`, `/interfaces/{id}/acls`, and
`/vnfs/{id}/leases`. The server serves `index.html` for browser navigations that
do not match an embedded asset, so deep links work without hash routing.

Inventory HTTP calls go through `ui/src/shared/api.ts`. In-flight GET requests
are shared, and transient failures (network errors, `429`, and server errors)
are retried after 1s, 2s, 4s, and 10s. After the fifth failed attempt the call
stops and returns an error naming the method and path. Mutations only retry
`429`/`503` so a create is not duplicated; validation and other `4xx` errors
are returned immediately.

### Tests

```bash
go test ./...
go vet ./...
```

`internal/qemu` includes an integration test that launches a real QEMU/KVM
process and is skipped automatically when `/dev/kvm` or `qemu-system-x86_64`
is unavailable. Use `go test -short ./...` to skip it explicitly.

### Releases

Pushing a tag such as `v0.1.0` runs `.github/workflows/release.yml`, builds the
API server and VNF daemon for Linux x86_64/amd64, and attaches a versioned
archive plus SHA-256 checksum to the GitHub release. Publishing an existing
release also triggers the workflow, and it can be run manually from the Actions
tab with an existing tag (for example `v0.1.0`).

```text
rookery-v0.1.0-linux-amd64.tar.gz
  rookery
  vnfs
  config.example.yaml
  README.md
  scripts/lvm-thin-snapshot-disk.sh
```

The workflow runs the UI build before compiling so the API binary contains the
release UI. To build the same archive locally:

```sh
make release VERSION=v0.1.0
```

If a release already exists but only shows GitHub's generated source archives,
open **Actions → Release → Run workflow**, enter the existing tag, and run it.
That uploads the binary archive to the existing release.

Binaries are statically linked (`CGO_ENABLED=0`), stripped, and built with
`-trimpath`; the Terraform provider is intentionally not part of this archive.

