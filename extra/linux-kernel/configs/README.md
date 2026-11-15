# LightVM kernel configuration fragments

The builder produces two independent kernels instead of one kernel with every
feature enabled. Each variant is described by plain Kconfig fragments in this
directory; nothing is embedded in the Makefile.

| Variant   | Fragments merged                              | Purpose |
| --------- | --------------------------------------------- | ------- |
| `virtio`  | `common.config` + `virtio.config`             | Minimal LightVM guest: virtio-pci, virtio-fs, virtio-net, virtio-blk, virtio-scsi, virtio-console, vsock, 9p. The common base also enables namespaces, cgroup v2, eBPF, seccomp, overlayfs, and nested-container networking. |
| `storage` | `common.config` + `virtio.config` + `storage.config` | The virtio kernel plus LVM (device-mapper + dm-thin-pool), the iSCSI initiator/target (open-iscsi, LIO/targetcli), NBD, ublk, and the ext4/XFS on-disk filesystems. |

`storage` deliberately includes the virtio fragments because it must still boot
the same virtio-fs rootfs and virtio-net images as the `virtio` kernel. It is a
superset, not a replacement.

## Building

From `extra/linux-kernel`:

```sh
make                     # both kernels and all four initramfs images
make kernel              # both kernels
make kernel-virtio       # only kernel-<version>-virtio
make kernel-storage      # only kernel-<version>-storage
make initramfs-disk-rootfs  # generic disk/distribution root initramfs
make initramfs-overlay-rootfs  # writable overlay-root initramfs
make menuconfig KERNEL_VARIANT=storage
make reconfigure         # reset both variants to these fragments
```

Artifacts land in `OUT_DIR` (default `dist/lightvm`):

```
kernel-<version>-virtio
kernel-<version>-storage
kernel-lightvm-virtio -> kernel-<version>-virtio
kernel-lightvm-storage -> kernel-<version>-storage
initramfs-lightvm-<version>.cpio.gz
initramfs-lightvm-virtiofs-<version>.cpio.gz
initramfs-lightvm-disk-rootfs-<version>.cpio.gz
initramfs-lightvm-overlay-rootfs-<version>.cpio.gz
```

Each variant has its own build directory
(`/build/obj-<version>-<arch>-<variant>`) and its own generated `.config`, so
the two builds never share configuration state.

## Initramfs images

The builder produces four initramfs images:

| Image | Purpose |
| ----- | ------- |
| `initramfs-lightvm` | Debug/test image. Mounts proc/sys/dev/cgroup2, optionally runs DHCP, prints device information, and idles or opens a shell. |
| `initramfs-lightvm-virtiofs` | Container-root image. Brings up networking and DHCP, mounts the `rootfs` virtio-fs tag, loads `/rootfs/.env`, and runs `/rootfs/.entrypoint` (or `lightvm.init=`) under BusyBox init. |
| `initramfs-lightvm-disk-rootfs` | Generic image for an existing Linux root filesystem. Finds a virtio-fs tag `root`/`rootfs`, then the `root=` device, then a block device whose virtio-blk serial is `root`/`rootfs`; mounts it and switch_roots into `init=` (or `/init`, `/sbin/init`, `/bin/init`). |
| `initramfs-lightvm-overlay-rootfs` | Writable overlay image. Layered `overlay=<N>` disks become lower/upper layers, the upper is mounted with `index=off,metacopy=off,redirect_dir=nofollow,uuid=null`, and the merged root is switch_rooted. |

The disk-root image does not implement the `.env`/`.entrypoint` container
contract and does not run DHCP; the distribution's own init owns the rest of
boot. Pair it with the `storage` kernel when the root is ext4 or XFS, because
the minimal `virtio` kernel has no on-disk filesystem drivers. Plain
filesystems on a disk or partition are supported; LVM, dm-crypt, MD/RAID, and
iSCSI roots need a distribution-specific initramfs with the matching tools.

The overlay-root image sorts disks by their `overlay=<N>` serial and uses the
highest N as the writable upper layer; all lower Ns are lower layers, with the
highest lower layer first in `lowerdir` so `overlay=2` overrides `overlay=1`.
It also accepts `overlay:<N>` and `overlay-<N>`, the forms allowed by Rookery's
disk-serial validation. On the upper filesystem it uses sibling `upper/` and
`work/` directories (override with `overlay-upper-dir=` and
`overlay-work-dir=`). Pair it with the `storage` kernel for ext4/XFS layers.

## How a variant is assembled

For each variant the build runs:

1. `make tinyconfig` in the variant's object directory.
2. `scripts/kconfig/merge_config.sh` with `common.config` followed by the
   variant fragments.
3. `make olddefconfig` to resolve dependencies.
4. A check that every symbol listed in `KERNEL_REQUIRED_CONFIG_<variant>` is
   `=y` in the final `.config`; the build fails instead of silently shipping a
   kernel that is missing the features its variant promises.

## Adding another variant

1. Add `configs/<name>.config`.
2. In the Makefile add `KERNEL_FRAGMENTS_<name>` (usually
   `virtio.config <name>.config` if it must boot LightVM images) and
   `KERNEL_REQUIRED_CONFIG_<name>`.
3. Append `<name>` to `KERNEL_VARIANTS`.
4. Build it with `make kernel-<name>`.

## Nested containers

`common.config` enables the kernel side of running containers inside a
LightVM: user, PID, network, IPC, UTS, time, and mount namespaces; cgroup v2
with the cpu, cpuset, memory, pids, and io controllers; cgroup eBPF for device
policies; POSIX message queues (the mqueue mount each container expects);
seccomp filters; keyrings; overlayfs; and the bridge/veth/netfilter stack with
nftables and legacy iptables for both IPv4 and IPv6.

The initramfs mounts cgroup2 at `/sys/fs/cgroup` with
`nsdelegate,memory_recursiveprot` before `switch_root` (falling back to
`nsdelegate`, then plain cgroup2). Rootful Docker/Podman can create cgroups
directly under that mount. Rootless container managers still need a delegated
cgroup subtree, normally provided by systemd; the initramfs only provides the
root hierarchy.

The kernel does not ship a container runtime or its userland: the image must
include Docker/Podman and the usual networking tools. Optional drivers such as
Geneve, IP-in-IP, or Cilium's tc/eBPF data path are not enabled here.

## On-disk filesystems

The `virtio` variant has no on-disk filesystem drivers; it boots from
virtio-fs or the initramfs. The `storage` variant adds ext4 and XFS, built
into the kernel, so a guest can format and mount the LVM, iSCSI, NBD, and ublk
volumes it manages. ext4 also handles ext2/ext3 through
`CONFIG_EXT4_USE_FOR_EXT2`.

The kernel only provides the drivers. `mkfs.ext4`, `mkfs.xfs`, `mount`, and
the other userland tools must still be present in the LightVM image.

## Built-in vs loadable modules

Both variants are built without `CONFIG_MODULES`; the options above are `=y`
and baked into the `bzImage`. LightVM boots a kernel plus an initramfs, so a
self-contained kernel avoids shipping and loading a module tree. If a variant
ever needs loadable modules, it must also package and install the matching
`.ko` files into the initramfs or the virtio-fs rootfs.
