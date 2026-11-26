# LightVM notes

LightVM inventory, runtimes, lifecycle, consoles, and guest contract.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Inventory, uploads, and labels

LightVM inventory starts with kernels in `lightvm_kernels` (`0042_lightvm.sql`), initramfs in
`lightvm_initramfs` (`0043_lightvm_initramfs.sql`), instances in `lightvms` (`0044_lightvms.sql`,
with kernel command line `append` added in `0045_lightvm_append.sql`, lifecycle state in
`0046_lightvm_lifecycle.sql`, and `lifecycle_generation` in `0053_workload_disks.sql`), and console
ports in `lightvm_consoles` (`0047_lightvm_consoles.sql`). Uploads use the native `POST
/api/lightvm/uploads` multipart endpoint (local listener only), which streams files under
`-lightvm-dir` (`ROOKERY_LIGHTVM_DIR`, default `/var/lib/rookery/lightvm`) with a UUID-prefixed name
and a 1 GiB limit; mTLS clients can use `LightVMArtifactService.UploadArtifact`, a client-streaming
RPC that writes through the same managed storage and supports `DeleteArtifact` cleanup.

## Definition model

Inventory CRUD lives at `/api/lightvm/kernels`, `/api/lightvm/initramfs`, and
`/api/lightvm/instances`, and labels use entity types `lightvm_kernel`, `lightvm_initramfs`, and
`lightvm`. LightVMs use UUIDs and include name, description, vCPUs, memory, console flag, cgroup
class, kernel/initramfs IDs, an optional container image (disk-backed definitions omit it when disks
are attached), ordered env name/value pairs, ordered `config_files` (`name`/`content`; at most 64
files, 1 MiB each, 4 MiB combined, relative unique names without `.`/`..` components and with
`env`/`startup-cmd` reserved; migration `0059_lightvm_config_files.sql` stores them as JSON), either
an executable path and argument list or inline executable script content (`executable_script` added
in `0055_lightvm_executable_script.sql`), an ordered disk set, ordered host-directory virtiofs
shares, an ordered interface set, an ordered console list, and a kernel command line (`append`,
default `console=hvc0 rdinit=/init`).

## Shares, disks, and consoles

Host-directory shares (`lightvm_virtiofs_shares`, `0054_lightvm_virtiofs_shares.sql`) bind an
absolute existing directory to a unique guest tag, default to read-only, and reserve the tag
`rootfs`; each share gets its own attempt-scoped virtiofsd/QEMU device. VMs and LightVMs share
`workload_disks` (`0053_workload_disks.sql`): nullable `vm_id`/`lightvm_id` owner columns, exactly
one owner, one disk per workload, ordered positions, owner cascades, and `ON DELETE RESTRICT` on
disks. `vm_disks` is migrated into it and `vms.disk_id` remains inert. Disks support LVM thin, host
passthrough, and Kouja for both workload types. A LightVM allows at most 24 combined disks,
interfaces, and host-directory shares for new/growing definitions; grandfathered over-budget
definitions cannot start until reduced. Every LightVM has at least one console (up to 32); the first
is exposed as `org.example.console`, additional ones as `org.example.console.<N>`.

## Image-backed runtime

For an image-backed LightVM, `internal/lightvm.Runtime` pulls the container image with buildah if
missing, creates an attempt-unique ephemeral writable `rookery-lightvm-<uuid>-<token>` container,
mounts its rootfs, writes the definition's env pairs to `<rootfs>/.env` as one `NAME=value` line
each and an executable `<rootfs>/.entrypoint` script (path+arguments, or inline script content;
exactly one), then starts virtiofsd on `<run-dir>/lightvm-<uuid>-<token>.virtiofs.sock` (binary
resolved next to the rookery executable or by `-virtiofsd-bin`) and calls
`internal/qemu.StartLightVMWithVirtioFSAndPlan`.

## Disk-backed runtime and config drive

A LightVM with an empty `image` is disk-backed: it must have at least one attached disk, skips
buildah/container/rootfs/`.env`/`.entrypoint`, and passes no `rootfs` virtiofs device to QEMU;
virtiofsd is only needed for host-directory shares. A disk-backed definition may still set `env`,
`executable_path`/`executable_args`, or `executable_script`: `PrepareLightVM` writes an
attempt-scoped, 0600, read-only `rookery-config` tar (files `env` as `NAME=value` lines,
`startup-cmd` as an executable `#!/bin/sh` script, and every user `config_files` entry at its
relative path with mode 0644) and QEMU exposes it as a read-only `virtio-blk-pci` device with serial
`rookery-config` after the workload disks, so the guest can read
`/dev/disk/by-id/virtio-rookery-config` (or scan `/sys/block/*/serial`) and untar it; Rookery itself
does not execute the startup command, but the bundled `initramfs-lightvm-disk-rootfs` and
`initramfs-lightvm-overlay-rootfs` extract the archive to `/run/rookery`, export `env`, and exec
`startup-cmd` as PID 1 unless an explicit `init=`/`lightvm.init=` is set. LightVM starts with a
config drive also force explicit `virtio-blk-pci` devices for the workload disks
(`StartPlan.ExplicitVirtio`) so the config device is realized after them and does not shift guest
`vda`/`vdb` letters.

## Start coordination and QEMU arguments

Starts are two-phase (`PrepareLightVM`, `StartPreparedLightVM`, `CleanupPreparedLightVM`);
`lightvm.StartCoordinator` serializes starts per LightVM, refcounts stop/update/delete reservations,
cancels in-flight preparation, and limits concurrent preparation (default 2). Cleanup stops only the
attempt PID/socket/container; stop/delete enumerate attempt and legacy resources after QEMU is
confirmed gone. QEMU boots with `-kernel`/`-initrd`/`-append` and no `-machine` override, one virtio
drive per attached disk in attachment order, an optional read-only `rookery-config` config drive for
disk-backed definitions with env/startup data, a `vhost-user-fs-pci` device tagged `rootfs` only for
image-backed definitions, one virtio console socket per configured console under the run directory,
the persisted cgroup class, and vhost-user interfaces; the kernel image remains mandatory and
Rookery does not add or rewrite `root=`; it uses `-display none` because `-nographic` cannot be
combined with `-daemonize`, and it never adds a VNC or graphical device.

## Lifecycle, autostart, and ephemeral runs

Stop stops QEMU, virtiofsd, and removes the buildah container, so guest writes are discarded. `POST
/api/lightvm/instances` both creates and starts a LightVM definition, mirroring VM creation; a
failed start rolls the definition back (stopping/cleaning any partial runtime) so callers never see
a half-created LightVM, and the create HTTP route gets the same extended write deadline as `/start`
because a first start may pull the image. The Terraform `rookery_lightvm` resource can set
`wait_for_exit` to block its create until the run ends by polling `GetLightVMMetrics` (and, for
`ephemeral` definitions, until the server has deleted the definition, so attachments are released).
Explicit Start/stop is exposed at `/api/lightvm/instances/{id}/start` and `/stop`; `GET
/api/lightvm/instances/{id}/metrics` mirrors VM metrics with QEMU process CPU/memory/I/O, QMP
per-drive blockstats, and the attached VPP vhost interfaces' RX/TX counters, and the LightVMs table
opens the shared monitor dialog from a **Monitor** action; the dialog can expand or open the full
page at `/monitor.html?kind=lightvm&id=<uuid>`. The response exposes `disk_ids`/`disks` in
attachment order, `run_state` (`running`, `paused`, or `paused (io-error)`), `console_socket` for
the first console, and a `consoles` list with each port's `id`, `name`, `port_name`, and `socket`
while running; status/desired_state reconcile and desired-running LightVMs autostart on API startup
and after VPP restarts, except ephemeral definitions (migration `0060_lightvm_ephemeral.sql`):
`ephemeral` adds `-no-reboot` to QEMU and `panic=-1` to the kernel command line (unless the caller
set `panic=`), the API reaper in `internal/api/lightvm_ephemeral.go` stops QEMU, virtiofsd, the
attempt container, config drive, and console sockets when the guest exits, deletes the LightVM
definition so its `workload_disks`/`lightvm_interfaces` rows are released (the disk/network records
and data remain), leaves a failed start's definition with `status=error` for inspection, and never
auto-starts a one-shot LightVM again after a restart; a manual `/stop` of an ephemeral LightVM also
deletes it.

## Console access

The native WebSocket `/ws/lightvms/{id}/console?console=<id>` proxies the selected virtio console
socket; without `console`, the first console is used. Like the VM VNC proxy it is
local-listener-only, checks `console_enabled` server-side, and closes an open session when access is
disabled or the LightVM stops. An administrative `POST /api/lightvm/instances/{id}/console-access`
issues a hashed bearer token (TTL 1s–24h, stored in `lightvm_console_access` from
`0056_lightvm_console_access.sql`) for `/ws/lightvm-console/{token}`; that route is available on the
local listener and the public mTLS HTTP listener, requires `console_enabled` and a running LightVM,
and rechecks token expiry, access, and run state each second. The standalone UI page is
`/lightvm-console.html?vm=<uuid>&console=<id>` (xterm.js with bundled JetBrains Mono, persisted
color-theme and font-size selection, common Ctrl/Esc/Tab buttons, a Ctrl latch for arbitrary
Ctrl+key chords, and a console selector when the LightVM has multiple ports) and the LightVMs table
opens it with a **Console** action; this page is the only route whose CSP allows `style-src
'unsafe-inline'` so xterm can inject its cursor/DOM styles.

## Artifacts and the guest contract

`size_bytes` and `managed` are server-derived, and deleting an uploaded record removes its managed
file while host-path records leave the file alone. For image-backed definitions the environment and
executable fields are written into the mounted rootfs and are not passed over the kernel command
line; the initramfs defines the workload contract, mounts the `rootfs` virtio-fs tag, and execs
`/rootfs/.entrypoint` as PID 1 so an entrypoint exit is a guest exit (the kernel panics; with
`panic=-1` and QEMU `-no-reboot` an ephemeral LightVM then ends). For disk-backed definitions they
are passed only through the `rookery-config` block device; the bundled disk-root/overlay-root
initramfs consumes them, and a custom guest init may do the same or ignore them. `/api/status`
includes a `lightvm` component reporting buildah/virtiofsd availability; the LightVM tab shows a
runtime-unavailable banner and disables Start only for definitions that need a missing tool
(image-backed definitions need both, disk-backed definitions need virtiofsd only when they have
host-directory shares). `extra/linux-kernel` also builds `initramfs-lightvm-disk-rootfs`, a generic
disk-root initramfs that finds a virtio-fs tag `root`/`rootfs`, the `root=` device, or a virtio-blk
serial `root`/`rootfs`, mounts it, and runs `init=` (or `/init`, `/sbin/init`, `/bin/init`); pair it
with the storage kernel for ext4/XFS roots. The virtiofs, disk-root, and overlay-root images run the
chosen init (for the virtiofs image, `/rootfs/.entrypoint`) as PID 1 in a new session with
`/dev/console` as its controlling terminal (BusyBox `setsid -c`), so an interactive console shell
has job control and Ctrl+C reaches its foreground job, and a workload exit is a guest exit rather
than a child exit hidden behind a supervisor. `initramfs-lightvm-overlay-rootfs` builds a writable
overlay from serials `overlay=<N>`/`overlay:<N>`/`overlay-<N>` (highest N is the upper;
`index=off,metacopy=off,redirect_dir=nofollow,uuid=null`); pair it with the storage kernel too.

## Kernel variants

`extra/linux-kernel` builds three independent kernels from Kconfig fragments in `configs/`:
`virtio` (minimal LightVM guest), `storage` (virtio plus LVM/dm-thin, iSCSI, NBD, ublk, and
ext4/XFS), and `k8s` (storage plus Kubernetes prerequisites). The `k8s` variant adds
`/proc/config.gz` (`CONFIG_IKCONFIG_PROC`) so kubeadm can parse the kernel config without
`modprobe`, the cgroup v2 hugetlb controller (`CONFIG_CGROUP_HUGETLB`), kube-proxy IPVS and
iptables matches, SCTP, ebtables, Geneve/IP-in-IP/GRE, tc/eBPF hooks, and an active eBPF LSM
(`CONFIG_BPF_LSM` in `CONFIG_LSM`) with vmlinux BTF. Build it with
`make -C extra/linux-kernel kernel-k8s` (all variants: `make kernel`; `KERNEL_VARIANTS` selects
the set) and verify it with `make test-k8s`, which boots the kernel in QEMU and checks hugetlb,
LSM, BTF, and the required config. Each variant is a superset of the one above it, so the
`storage` and `k8s` kernels still boot the virtio-fs LightVM images.

