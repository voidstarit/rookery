# LightVM kernel configuration fragments

The builder produces three independent kernels instead of one kernel with every
feature enabled. Each variant is described by plain Kconfig fragments in this
directory; nothing is embedded in the Makefile.

| Variant   | Fragments merged                              | Purpose |
| --------- | --------------------------------------------- | ------- |
| `virtio`  | `common.config` + `virtio.config`             | Minimal LightVM guest: virtio-pci, virtio-fs, virtio-net, virtio-blk, virtio-scsi, virtio-console, vsock, 9p. The common base also enables namespaces, cgroup v2, eBPF, seccomp, overlayfs, and nested-container networking. |
| `storage` | `common.config` + `virtio.config` + `storage.config` | The virtio kernel plus LVM (device-mapper + dm-thin-pool), the iSCSI initiator/target (open-iscsi, LIO/targetcli), NBD, ublk, and the ext4/XFS on-disk filesystems. |
| `k8s`     | `common.config` + `virtio.config` + `storage.config` + `k8s.config` | The storage kernel plus Kubernetes prerequisites: HugeTLB pages and the cgroup v2 hugetlb controller, `/proc/config.gz`, kube-proxy IPVS and iptables matches, SCTP, the socket match/TPROXY with IPv4/IPv6 socket lookup, the CT/NOTRACK targets and conntrack zones, the XFRM/IPsec framework, ebtables, Geneve/IP-in-IP/GRE, dummy devices, ethtool netlink, tc/eBPF hooks with cgroup classid, FQ, and LWTUNNEL, policy routing (multiple FIB tables and rules), and eBPF LSM with vmlinux BTF. |

`storage` deliberately includes the virtio fragments because it must still boot
the same virtio-fs rootfs and virtio-net images as the `virtio` kernel. `k8s`
includes both `virtio` and `storage` for the same reason. Each variant is a
superset of the one above it, not a replacement.

The `k8s` variant fixes kubeadm's optional
`CGROUPS_HUGETLB: missing` warning by enabling `CONFIG_CGROUP_HUGETLB` (with
`CONFIG_HUGETLBFS`/`CONFIG_HUGETLB_PAGE`), which adds `hugetlb` to
`/sys/fs/cgroup/cgroup.controllers`. Its additional options are described under
[Kubernetes](#kubernetes).

## Building

From `extra/linux-kernel`:

```sh
make                     # all kernels and all four initramfs images
make kernel              # all kernel variants
make kernel-virtio       # only kernel-<version>-virtio
make kernel-storage      # only kernel-<version>-storage
make kernel-k8s          # only kernel-<version>-k8s
make initramfs-disk-rootfs  # generic disk/distribution root initramfs
make initramfs-overlay-rootfs  # writable overlay-root initramfs
make menuconfig KERNEL_VARIANT=storage
make reconfigure         # reset all variants to these fragments
make test-k8s            # boot k8s and verify hugetlb, LSM, BTF, config
```

Artifacts land in `OUT_DIR` (default `dist/lightvm`):

```
kernel-<version>-virtio
kernel-<version>-storage
kernel-<version>-k8s
kernel-lightvm-virtio -> kernel-<version>-virtio
kernel-lightvm-storage -> kernel-<version>-storage
kernel-lightvm-k8s -> kernel-<version>-k8s
initramfs-lightvm-<version>.cpio.gz
initramfs-lightvm-virtiofs-<version>.cpio.gz
initramfs-lightvm-disk-rootfs-<version>.cpio.gz
initramfs-lightvm-overlay-rootfs-<version>.cpio.gz
```

Each variant has its own build directory
(`/build/obj-<version>-<arch>-<variant>`) and its own generated `.config`, so
the builds never share configuration state.

## Initramfs images

The builder produces four initramfs images:

| Image | Purpose |
| ----- | ------- |
| `initramfs-lightvm` | Debug/test image. Mounts proc/sys/dev/cgroup2, optionally runs DHCP, prints device information, and idles or opens a shell. |
| `initramfs-lightvm-virtiofs` | Container-root image. Brings up networking and DHCP, mounts the `rootfs` virtio-fs tag, loads `/rootfs/.env`, and execs `/rootfs/.entrypoint` (or `lightvm.init=`) as PID 1. |
| `initramfs-lightvm-disk-rootfs` | Generic image for an existing Linux root filesystem. Finds a virtio-fs tag `root`/`rootfs`, then the `root=` device, then a block device whose virtio-blk serial is `root`/`rootfs`; mounts it and switch_roots into `init=` (or `/init`, `/sbin/init`, `/bin/init`). |
| `initramfs-lightvm-overlay-rootfs` | Writable overlay image. Layered `overlay=<N>` disks become lower/upper layers, the upper is mounted with `index=off,metacopy=off,redirect_dir=nofollow,uuid=null`, and the merged root is switch_rooted. |

The disk-root image does not implement the `.env`/`.entrypoint` container
contract and does not run DHCP; the distribution's own init owns the rest of
boot. Pair it with the `storage` or `k8s` kernel when the root is ext4 or XFS,
because the minimal `virtio` kernel has no on-disk filesystem drivers. Plain
filesystems on a disk or partition are supported; LVM, dm-crypt, MD/RAID, and
iSCSI roots need a distribution-specific initramfs with the matching tools.

The overlay-root image sorts disks by their `overlay=<N>` serial and uses the
highest N as the writable upper layer; all lower Ns are lower layers, with the
highest lower layer first in `lowerdir` so `overlay=2` overrides `overlay=1`.
It also accepts `overlay:<N>` and `overlay-<N>`, the forms allowed by Rookery's
disk-serial validation. On the upper filesystem it uses sibling `upper/` and
`work/` directories (override with `overlay-upper-dir=` and
`overlay-work-dir=`). Pair it with the `storage` or `k8s` kernel for ext4/XFS
layers.

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
   `virtio.config [storage.config] <name>.config` if it must boot LightVM
   images and build on the storage features) and
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
include Docker/Podman and the usual networking tools. The `virtio` and
`storage` variants do not enable the optional overlay/CNI data paths; the `k8s`
variant below adds Geneve, IP-in-IP, GRE, and Cilium's tc/eBPF hooks.

## Kubernetes

`k8s.config` builds on `storage.config` and adds the kernel features that
kubeadm, kubelet, kube-proxy, containerd/CRI-O, and common CNI plugins expect:

- **Kernel config parsing:** `CONFIG_IKCONFIG` and `CONFIG_IKCONFIG_PROC`
  expose the running config at `/proc/config.gz`. kubeadm's kernel checks read
  that file; without it, system-validators falls back to `modprobe configs`,
  which fails in a kernel built without `CONFIG_MODULES` with
  `failed to parse kernel config: unable to load kernel module: "configs"`.
- **HugeTLB:** `CONFIG_HUGETLBFS`, `CONFIG_HUGETLB_PAGE`, and
  `CONFIG_CGROUP_HUGETLB`. Without `CONFIG_CGROUP_HUGETLB`, kubeadm's
  `SystemVerification` preflight reports the optional warning
  `CGROUPS_HUGETLB: missing`; with it, `hugetlb` appears in
  `/sys/fs/cgroup/cgroup.controllers`. Hugepage pools are still sized from the
  guest, for example with `default_hugepagesz=2M hugepagesz=2M hugepages=512`
  on the kernel command line or through `/proc/sys/vm/nr_hugepages`.
- **kube-proxy IPVS mode:** `CONFIG_IP_VS`, the RR/WRR/SH schedulers, IPv6, and
  IPVS connection tracking (`CONFIG_IP_VS_NFCT`).
- **kube-proxy iptables mode:** SCTP, realm, statistic, and rpfilter matches,
  plus SCTP connection tracking. Conntrack, NAT, REDIRECT, MASQUERADE,
  multiport, and comment matching come from `common.config`.
- **Transparent proxying:** `CONFIG_NETFILTER_XT_MATCH_SOCKET` and
  `CONFIG_NETFILTER_XT_TARGET_TPROXY`, with the IPv4/IPv6 socket-lookup
  infrastructure (`CONFIG_NF_SOCKET_IPV4`/`CONFIG_NF_SOCKET_IPV6`) they
  select. kube-proxy's IPVS mode and service meshes/CNIs use TPROXY to
  redirect traffic to a transparent proxy without NAT.
- **Raw-table CT/NOTRACK:** `CONFIG_NETFILTER_XT_TARGET_CT`,
  `CONFIG_NETFILTER_XT_TARGET_NOTRACK`, and `CONFIG_NF_CONNTRACK_ZONES`.
  CNIs and kube-proxy emit `-j CT --notrack` in the raw tables; without the CT
  target the kernel rejects the rule with
  `Extension CT revision 0 not supported`. `CONFIG_IP_NF_RAW` and
  `CONFIG_IP6_NF_RAW` already come from `common.config`.
- **CNI and Services:** ebtables for bridge plugins; Geneve, IP-in-IP, and GRE
  for overlay/tunnel modes; `NET_SCHED`, `NET_CLS_BPF`, and `NET_ACT_BPF` for
  kube-proxy and eBPF-based CNIs; `CONFIG_CGROUP_NET_CLASSID` and
  `CONFIG_NET_CLS_CGROUP` for `bpf_get_cgroup_classid()` and cgroup-based
  packet classification; `NET_SCH_FQ` for the FQ qdisc that honours
  TCP pacing and is used by Cilium's bandwidth manager;
  `LWTUNNEL`/`LWTUNNEL_BPF` for BPF nexthop actions; `CONFIG_DUMMY` for CNI
  plugins and load-balancer
  implementations that create a dummy device to hold a stable address or VIP;
  and `CONFIG_ETHTOOL_NETLINK` for modern link-feature queries. VXLAN and
  `CONFIG_VETH` are already enabled by `common.config`.
- **IPsec/XFRM:** `CONFIG_XFRM`, `CONFIG_XFRM_USER` (the netlink
  configuration interface), and `CONFIG_XFRM_ALGO` (crypto algorithm
  wrappers, selecting `CRYPTO_AEAD`/`CRYPTO_HASH`/`CRYPTO_SKCIPHER`). CNI
  IPsec modes such as Calico's and some service meshes program SAs and
  policies through this framework. Transport and tunnel modes are XFRM core
  and always built with `CONFIG_XFRM`; the old
  `CONFIG_INET_XFRM_MODE_TRANSPORT`/`CONFIG_INET_XFRM_MODE_TUNNEL` Kconfig
  symbols no longer exist.
- **Policy routing:** `CONFIG_IP_ADVANCED_ROUTER`,
  `CONFIG_IP_MULTIPLE_TABLES`, `CONFIG_FIB_RULES`, and
  `CONFIG_IPV6_MULTIPLE_TABLES`, so CNIs and kube-proxy can use `ip rule`
  with multiple routing tables (including IPv6) for per-pod, per-interface,
  and VRF-style steering. `CONFIG_FIB_RULES` selects the IPv4 options, but all
  are listed so the required-config check catches a dropped dependency.
- **eBPF LSM:** `CONFIG_BPF_LSM` plus the security core
  (`CONFIG_SECURITY`, `CONFIG_SECURITY_NETWORK`, `CONFIG_SECURITY_PATH`) and a
  default LSM list of `yama,bpf`, so `bpf` is active without a custom `lsm=`
  kernel argument. `BPF_LSM` depends on `BPF_EVENTS`; because `common.config`
  disables the tracing menus, the variant enables `CONFIG_FTRACE`,
  `CONFIG_PERF_EVENTS`, and `CONFIG_UPROBE_EVENTS` to satisfy it. The variant
  also builds vmlinux BTF (`CONFIG_DEBUG_INFO_BTF` with DWARF4), because
  libbpf/bpftool resolve LSM hook IDs from `/sys/kernel/btf/vmlinux`.
  Generating BTF requires pahole; the builder image installs the `dwarves`
  package, and the resulting kernel is larger than the `virtio`/`storage`
  variants.

`/sys/kernel/security/lsm` lists the active LSMs (for example
`capability,yama,bpf`). A caller-provided `lsm=` kernel argument still
overrides `CONFIG_LSM`, so keep `bpf` in that list when customizing it.

The `k8s` variant keeps the storage kernel's ext4/XFS, LVM, iSCSI, NBD, and
ublk support, so it works for both container-root and disk-backed LightVMs. It
also carries debug info for BTF generation and vmlinux BTF, so it is the
largest of the three artifacts. It still does not ship a container runtime: the
guest image must provide containerd/CRI-O, kubeadm/kubelet/kube-proxy, and the
CNI binaries.

## On-disk filesystems

The `virtio` variant has no on-disk filesystem drivers; it boots from
virtio-fs or the initramfs. The `storage` and `k8s` variants add ext4 and XFS,
built into the kernel, so a guest can format and mount the LVM, iSCSI, NBD, and
ublk volumes it manages. ext4 also handles ext2/ext3 through
`CONFIG_EXT4_USE_FOR_EXT2`.

The kernel only provides the drivers. `mkfs.ext4`, `mkfs.xfs`, `mount`, and
the other userland tools must still be present in the LightVM image.

## Built-in vs loadable modules

All variants are built without `CONFIG_MODULES`; the options above are `=y`
and baked into the `bzImage`. LightVM boots a kernel plus an initramfs, so a
self-contained kernel avoids shipping and loading a module tree. If a variant
ever needs loadable modules, it must also package and install the matching
`.ko` files into the initramfs or the virtio-fs rootfs.
