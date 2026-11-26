# Compute notes

QEMU/KVM virtual machines, cgroups, metrics, media, and console access.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## VM lifecycle and autostart

`POST /api/vms` creates the VM and starts it immediately; stopping a VM is always an explicit
`/stop`. When the QEMU start fails, creation rolls back the VM record (and stops any partial QEMU
process) and returns the start error, so callers never see a half-created VM. `vms.desired_state`
(`0038_vm_desired_state.sql`) records whether the VM should be running: start sets `running`, stop
sets `stopped`, and creation sets `running` after a successful start. On API startup (and after a
VPP restart) `internal/api.reconcileInfrastructure` calls `autostartVMs`, which starts every VM
whose desired state is `running` and whose QEMU process is gone; explicitly stopped VMs stay down.
Autostart runs after the VPP resources are reconciled so vhost-user sockets exist, holds the shared
run mutex, and logs (without failing reconciliation) when a VM cannot start. `POST
/api/vms/{id}/start` and `/stop` drive `internal/qemu`.

## QEMU launch, monitor, and run state

QEMU runs with KVM (`-accel kvm`), every attached thin-LVM disk as a virtio drive (in
`workload_disks.position` order), optional read-only CD-ROM boot media, and `-nic none`. VMs with
attached vhost-user interfaces also get `-object memory-backend-memfd,...,share=on` plus `-numa
node,memdev=mem`; without shared guest RAM VPP cannot map the virtqueues and the vhost link stays
down. VNC is exposed only as a Unix socket under `-run-dir` (default `/run/rookery`). Vhost-user
chardevs use `reconnect=1` so QEMU reconnects when VPP recreates its sockets after a restart. QEMU
always exposes a `qemu-vdagent` virtio-serial channel (`com.redhat.spice.0`); guests with
`spice-vdagent` can exchange clipboard text with the console, while guests without it ignore the
port. A QEMU monitor socket (`<uuid>.monitor`) is local-only and used for `SendCtrlAltDel` (`sendkey
ctrl-alt-delete`), `Reset` (`system_reset`), live `info status`/run-state reporting, and `Resume`
(`cont`). Running VMs expose `run_state` in VM responses; a remote disk error pauses the VM as
`paused (io-error)`, and the UI shows the paused state with a **Resume** action. `POST
/api/vms/{id}/resume` resumes only a paused VM. `QEMU.Running` checks the pid file first and falls
back to scanning `/proc` for `-uuid <vmID>`, so a QEMU process that survived an API restart or lost
its pid file is still detected; start is then idempotent and repairs the stored status, while stop
terminates the orphan by pid. QEMU start failures report the pid/name of a process holding one of
the VM disks if the image lock is taken, and `reconcileVMStatuses` aligns DB status with live QEMU
processes at startup.

## cgroup v2 resources

cgroup v2 resource management is in `internal/cgroup` and integrated through `internal/qemu`. Host
settings and reusable class definitions (CPU cpuset, memory.max, cpu.weight, per-device
io.max/io.weight, and slash-separated class paths such as `prod/web`) are stored in
`cgroup_settings` (`0039_cgroup.sql`) and managed by `GET`/`PUT /api/cgroup-settings`. VMs store
only `vms.cgroup_class` (`0040_cgroup_classes.sql`); an empty class selects the shared cgroup.
`serve -cgroup-root` (default `/sys/fs/cgroup/rookery-cgroup`) creates a parent cgroup with no
processes, a `shared` child, and class cgroups directly below the root (`<root>/prod/web`). Classes
are limited to three levels below the `rookery-cgroup` root and must have their parent class
defined; VMs may only be assigned to leaf classes, while parent classes group children and
distribute controllers. Class nodes inherit host/parent limits; omitted class values inherit. Every
QEMU start obtains a placement from `cgroup.Manager` and uses `SysProcAttr.UseCgroupFD` (plus a
`cgroup.procs` fallback) so all QEMUs run under the hierarchy. `/api/host/block-devices` lists whole
devices for the I/O picker; devices are resolved to major/minor with `DeviceNumber`. Settings are
validated and rolled back on kernel apply failures, and removing a class assigned to a VM is
rejected. VM updates apply class changes live through `QEMU.ApplyCgroup`, which moves the running
QEMU process between the shared and leaf class cgroups without restarting it. Host settings saves
also call `ApplyCgroup` for every running VM (via `systemService.applyCgroupsToRunningVMs`), so
enabling limits or changing host defaults takes effect immediately; failures are reported per VM
after the host settings are saved. Class limits require host-wide cgroup limits to be enabled.
Disabling host settings moves every process out of the Rookery hierarchy and removes the cgroups, so
no QEMU process remains constrained. `GET /api/cgroup-usage` reads `memory.current`/`memory.max`,
cumulative `cpu.stat`, `pids.current`/`pids.max`, `io.stat`, and `cpuset.cpus.effective` for the
root, shared cgroup, and every configured class; the Settings UI polls it every 2 seconds while
visible and derives CPU/I/O rates from consecutive samples.

## Process-level metrics

`GET /api/vms/{id}/metrics` returns process-level QEMU metrics from `/proc` (summed per-thread
`schedstat` CPU, `VmRSS`/`VmSize`, and `/proc/<pid>/io` counters). It also queries QEMU QMP
`query-blockstats` for per-drive `read_bytes`, `write_bytes`, operations, and failed/invalid
operation counters; every main drive has a stable `id=rookery-disk-<disk-id>` so the UI Monitor
dialog can map stats to disks. The endpoint also sums VPP vhost-interface RX/TX counters (via
`vppctl show interface <name>`) for the VM's interfaces. The UI **Monitor** dialog polls it every
2s, derives CPU/disk/network rates, keeps 60 samples for dependency-free canvas history charts, and
can expand into a multi-column view or open the standalone full page at
`/monitor.html?kind=vm&id=<uuid>`; stopped VMs return `running:false`. Do not present these as
guest-OS metrics.

## Monitor actions

`POST /api/vms/{id}/ctrl-alt-del` and `/reset` inject those commands through the QEMU monitor. The
console's Ctrl+Alt+Del button uses the API path first and falls back to VNC key events; Reset falls
back to a stop/start power cycle when the monitor socket is missing (for VMs started by older
servers).

## Boot media

VMs have an optional `boot_media_id`. It may be set on create and changed or cleared on `PUT`;
unlike disks, one media entry can be used by multiple VMs.

## Console access control

Console access is controlled by `vms.console_enabled`. New VMs default to disabled; migration
`0005_console_access.sql` enables existing VMs. The VNC WebSocket returns `403` when disabled and
rechecks every second, closing live sessions after an administrator disables access. Keep this
enforcement server-side; hiding the UI button alone is not sufficient.
