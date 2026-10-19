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
| `thin_pool_name`  | no       | `nerds-vms`  | Thin pool name                               |

## Template playbook (`setup-template.yml`)

Downloads a cloud image, extracts it if needed, and writes it to a template
LV in the thin pool.

| Variable          | Required | Default                                                               | Description                                  |
| ----------------- | -------- | --------------------------------------------------------------------- | -------------------------------------------- |
| `vg_name`         | yes      | -                                                                     | Volume group to use                          |
| `thin_pool_name`  | no       | `nerds-vms`                                                           | Thin pool name                               |
| `template_lv_name`| no       | `debian-template`                                                     | Name of the template LV                      |
| `image_url`       | no       | Debian 13 nocloud (trixie) tar.xz                                     | Cloud image URL (`.raw`/`.img` or archive)   |
| `temp_dir`        | no       | `/tmp/nerds-vms`                                                      | Directory to download/extract the image      |

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
- No external DHCP daemon is required; DHCPv4/DHCPv6 VNFs use the embedded CoreDHCP servers
- A spare **wired** Ethernet NIC for NAT44 outside use; wireless and the host
  default-route NIC are rejected
- `vppctl` on `PATH` with access to VPP's CLI socket for NAT44 VNFs
  (`setup-vpp.yml` installs it with VPP)
- Root or `CAP_NET_ADMIN` + `CAP_NET_BIND_SERVICE` + `CAP_NET_RAW` for VNF tap setup,
  UDP 67/547 binding, and NAT44 host-NIC takeover
- `iproute2` (`ip`) for assigning IPv4/IPv6 gateway addresses to VNF taps and
  flushing an outside NIC before VPP takes it over
- QEMU with KVM support (`qemu-system-x86_64` and `/dev/kvm`)
- Permission to create `/run/nerds-vms` (or configure another `-run-dir`) and to
  start QEMU processes

The socket is VPP's **binary API** socket, not the CLI socket
(`/run/vpp/cli.sock`) or a QEMU vhost-user socket. `setup-vpp.yml` configures
`/run/vpp/api.sock`.

Build a standalone binary:

```bash
go build -o bin/nerds-vms-api ./cmd/nerds-vms-api
```

### Initialize

`init` is safe to run repeatedly. It verifies the volume group, creates the thin
pool only if it does not exist, and then checks that the VPP API socket exists
and answers a GoVPP `control_ping`.

```bash
sudo ./bin/nerds-vms-api init \
  -vg vg0 \
  -thin-pool nerds-vms \
  -size 100%FREE \
  -vpp-socket /run/vpp/api.sock
```

- An existing thin pool is left untouched (it is not resized).
- `-size` defaults to `100%FREE` and accepts normal LVM size syntax.
- `-thin-pool vg0/nerds-vms` can be used instead of passing `-vg` separately.
- `init` fails if VPP is not reachable or the socket is not a Unix socket.

Run as root or as a user with permission to create logical volumes.

### Serve

```bash
./bin/nerds-vms-api serve \
  -vg vg0 \
  -thin-pool nerds-vms \
  -vpp-socket /run/vpp/api.sock
```

Open <http://127.0.0.1:8080>. No flags are required to start the server: if the
thin pool or VPP are unavailable, the UI shows the reason and the readiness
state. The server rechecks every 10 seconds. Run the server as root or as a user
with permission to run `lvcreate`/`lvremove`, start QEMU, access `/dev/kvm`, and
access the VPP socket; disk creation and deletion operate on the host.

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
on an interface row to choose a **From interface** and/or **To interface** ACL by
name. "From interface" filters traffic coming from that VM into VPP; "To
interface" filters traffic going from VPP to that VM. Rules are
installed in VPP through the ACL plugin and survive API/VPP reconciliation.
The editor and assignment dialog both show the full effective ACL text,
including the implicit deny. An ACL cannot be deleted while assigned to an
interface.

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
supports `dhcpv4`, `dhcpv6`, and `nat44` functions; the VNF record has a type,
network, enable flag, and type-specific configuration. A network can have one
VNF of each type. DHCP VNFs each create a VPP tap (`vnf<id>`) in the network's
bridge domain, assign their configured address to the Linux tap, and run an
in-process CoreDHCP server bound to that tap.

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
| `-addr`           | `NERDS_VMS_ADDR`       | `127.0.0.1:8080`    | `serve`         | HTTP listen address |
| `-db`             | `NERDS_VMS_DB`         | `nerds-vms.db`      | `serve`         | SQLite database path |
| `-vg`             | `NERDS_VMS_VG`         | empty               | `serve`, `init` | Bootstrap LVM volume group; optional for `serve` |
| `-thin-pool`      | `NERDS_VMS_THIN_POOL`  | `nerds-vms`         | `serve`, `init` | Bootstrap thin pool name or `<vg>/<name>`; `serve` can use UI backends instead |
| `-vpp-socket`     | `NERDS_VMS_VPP_SOCKET` | `/run/vpp/api.sock` | `serve`, `init` | VPP binary API Unix socket |
| `-qemu-bin`       | `NERDS_VMS_QEMU_BIN`   | `qemu-system-x86_64`| `serve`         | QEMU system emulator binary |
| `-run-dir`        | `NERDS_VMS_RUN_DIR`    | `/run/nerds-vms`    | `serve`         | QEMU pid files and VNC Unix sockets |
| `-vhost-dir`      | `NERDS_VMS_VHOST_DIR`  | `/run/vpp`          | `serve`         | VPP vhost-user socket directory |
| `-check-interval` | —                      | `10s`               | `serve`         | Readiness recheck interval |
| `-gateway-addr`   | `NERDS_VMS_GATEWAY_ADDR` | —                 | `serve`         | Optional grpc-gateway HTTP listen address |
| `-grpc-addr`      | `NERDS_VMS_GRPC_ADDR`  | —                   | `serve`         | Optional gRPC listen address |
| `-size`           | —                      | `100%FREE`          | `init`          | Thin pool size when creating |

The database directory is created automatically and migrations run on startup.
Set `-addr :8080` to expose the server beyond localhost.

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
| `POST`   | `/api/disks`       | Create a thin-LVM disk            |
| `GET`    | `/api/disks/{id}`  | Get one disk                      |
| `DELETE` | `/api/disks/{id}`  | Delete an unattached disk         |
| `GET`    | `/api/media`       | List installation media           |
| `POST`   | `/api/media`       | Register an existing ISO file     |
| `GET`    | `/api/media/{id}`  | Get one media entry               |
| `DELETE` | `/api/media/{id}`  | Remove an unused media entry      |
| `GET`    | `/api/networks`    | List Rookery Networks (VPP bridge domains) |
| `POST`   | `/api/networks`    | Create a VPP bridge domain        |
| `GET`    | `/api/networks/{id}` | Get one network                 |
| `DELETE` | `/api/networks/{id}` | Delete an empty network and its VNFs |
| `GET`    | `/api/host/interfaces` | List host NICs for NAT44 outside selection |
| `GET`    | `/api/troubleshoot/vpp-graph` | Active VPP packet-processing node graph |
| `GET`    | `/api/troubleshoot/acl-counters` | Per-interface ACL rule-match counters |
| `GET`    | `/api/troubleshoot/capture/interfaces` | List VPP interfaces for capture |
| `GET`    | `/api/troubleshoot/capture` | Current capture status and retained files |
| `POST`   | `/api/troubleshoot/capture` | Start a capture (`interface`, `direction`, `max_bytes`, `snap_length`) |
| `POST`   | `/api/troubleshoot/capture/stop` | Stop the active capture |
| `GET`    | `/api/troubleshoot/capture/files` | List retained pcap files |
| `GET`    | `/api/troubleshoot/capture/files/{name}` | Download a pcap file |
| `DELETE` | `/api/troubleshoot/capture/files/{name}` | Delete a retained pcap file |
| `GET`    | `/webshark/`       | Embedded WebShark pcap viewer |
| `GET`    | `/api/vnfs`        | List VNFs (`?network_id=<id>` optional) |
| `POST`   | `/api/vnfs`        | Create a DHCPv4, DHCPv6, or NAT44 VNF |
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
| `GET`    | `/api/interfaces`  | List vhost-user interfaces        |
| `POST`   | `/api/interfaces`  | Create a vhost-user interface      |
| `GET`    | `/api/interfaces/{id}` | Get one interface              |
| `PUT`    | `/api/interfaces/{id}` | Attach/detach to a VM          |
| `PUT`    | `/api/interfaces/{id}/acls` | Assign ingress/egress ACLs by name |
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
default backend. This runs `lvcreate` against the selected backend's thin pool.
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
  "thin_pool": "nerds-vms",
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
route; that NIC and wireless interfaces are rejected by the server. Use a
spare wired Ethernet NIC for the outside uplink.

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
    "detail": "thin pool vg0/nerds-vms does not exist; ...",
    "vg": "vg0",
    "name": "nerds-vms"
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
1–65536). Disk size is provisioned as a thin-LVM virtual size in GB.

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
make build        # UI bundle + Go binary in bin/
make test         # UI build + go test ./...
```

Node.js and npm are only needed when changing UI sources; plain `go build` and
`go test` use the committed bundle. The UI is fully Preact/TypeScript: the
console page and all inventory tabs are components, with
`ui/src/inventory/app.ts` providing the typed tab/readiness shell. Preact
islands communicate through `rookery:*` window events; keep those bridges in
sync when changing shared state.

### Tests

```bash
go test ./...
go vet ./...
```

`internal/qemu` includes an integration test that launches a real QEMU/KVM
process and is skipped automatically when `/dev/kvm` or `qemu-system-x86_64`
is unavailable. Use `go test -short ./...` to skip it explicitly.

