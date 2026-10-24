# Host setup with Ansible

The Ansible playbooks in the repository root are optional host-preparation
helpers. They create and extend the LVM thin pool, install a template disk,
create instance disks, configure guest users, and install/configure FD.io VPP.
They are independent of the Go API and can be used on their own.

## Requirements

- Ansible with `community.general` collection installed:
  ```bash
  ansible-galaxy collection install community.general
  ```
- Target host with LVM tooling installed and an existing volume group

## Thin pool playbook (`setup-thin-pool.yml`)

Creates the thin pool inside an existing volume group. The playbook is
idempotent and leaves an existing pool untouched.

### Parameters

| Variable          | Required | Default      | Description                                  |
| ----------------- | -------- | ------------ | -------------------------------------------- |
| `vg_name`         | yes      | -            | Existing volume group to create the pool in  |
| `thin_pool_size`  | no       | unset        | Thin pool size (e.g. `200G`, `50%VG`)        |
| `thin_pool_name`  | no       | `rookery`  | Thin pool name                               |

## Thin pool usage

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
  -e vg_name=vg0 -e base_lv=debian-template -e disk_lv=vm01-root -e disk_size=20G -K
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
