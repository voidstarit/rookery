# Storage notes

Storage backends, disks, templates, passthrough devices, throttle classes, and media.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Storage backends

Storage backends are records in `storage_backends` (`0011_storage_backends.sql`): name, title,
description, driver, JSON config, and disk count. Drivers are `lvm-thin` (`config.vg`,
`config.thin_pool`, `config.supports_templates`, `config.preparation_script`,
`config.preparation_timeout`, `config.allow_template_delete_with_clones`) and `kouja`
(`config.kouja` with endpoint, protocol, storage class, initiator/host NQN, PEM-encoded CA/client
certificate/client key content or legacy file paths, server name, and portal); keep the driver field
and API shape extensible for future drivers such as Ceph. `serve` bootstraps a `default` backend
from `-vg`/`-thin-pool` when supplied and associates legacy disks with matching `vg`/`thin_pool`;
without those flags it uses the first registered backend and updates readiness when a backend is
added in the UI. CRUD is at `/api/storage-backends`; changing the driver, volume group, or thin pool
is blocked while disks use it, while template/script settings may be updated. Deleting a backend
with disks is blocked. LVM backends expose `GET /api/storage-backends/{id}/importable-disks` and
`POST /api/storage-backends/{id}/import-disks`; import registers existing thin volumes (via `lvs
--units b`) transactionally, marks already-registered volumes, does not mutate LVM, and records the
exact `size_bytes` alongside a `size_gb` rounded up to whole GB.

## Kouja remote iSCSI

Kouja backends are remote iSCSI storage: `POST /api/disks` with a kouja backend creates and exports
a volume through the pinned kouja client (the volume ID and request ID are persisted in
`backend_metadata`), returns a secret-free `locator`, and leaves `device` empty. QEMU attaches the
volume with its own iSCSI block driver and a transient 0600 CHAP secret under `<run-dir>`; the host
never opens an `iscsiadm` session and the CHAP secret never appears in QEMU args or SQLite. `DELETE
/api/disks/{id}` on a kouja disk returns `202 Accepted`, marks the row `deleting`, and the
background reclaimer unexports/deletes it with bounded retries; `delete_failed` rows can be retried
with the same call, and `DELETE /api/disks/{id}?force=true` on a `deleting`/`delete_failed` disk
removes the inventory record immediately without backend cleanup (the remote volume may remain). VM
starts use `vmstart.Planner` to fetch a fresh export/credential and `qemu.StartWithPlan`; API
autostart uses the same planner. Kouja stores session observations at its reconcile interval, so a
stale `SESSION_ACTIVE` must not block a start after QEMU logs out; only a fresh non-zero
`Observation.ActiveSessions` is authoritative. When a fresh active session is reported, the driver
waits briefly and re-prepares the export, then the gateway returns `Unavailable` so the UI retries
after the previous QEMU/iSCSI session drains. The UI backend form supports kouja, the disk table
shows source/protocol/locator/deletion state, and VM creation requires the thin pool only for
LVM-backed disks. Deep data-path testing is not implemented yet (`TestStorageBackendDataPath`
returns `Unimplemented`).

## Templates and clones

Disks are first-class LVM-backed resources: `POST /api/disks` selects a `storage_backend_id` (or the
default backend), runs `lvcreate` for a blank thin volume, or invokes the backend preparation script
to clone a template when `template_disk_id` is supplied. Template and clone disks are recorded in
migration `0013_disk_templates.sql`; templates are backend-scoped, cannot attach to VMs, and cloning
requires `supports_templates` plus a configured script. The script runs as the API user, receives
`ROOKERY_DISK_JSON` (operation, request id, disk, template, backend), has a configurable timeout,
logs output, and must create the clone LV/device. Template disks may also configure
`provisioning_script`/`provisioning_timeout` (`0037_disk_template_provisioning.sql`); after the
preparation script creates the clone, Rookery runs it as the API user with `ROOKERY_PROVISION_JSON`
(`operation`, request id, disk, template, backend, and arbitrary caller `properties` from the
clone's `provisioning` JSON), and rolls back the clone on failure. `scripts/template-provision.sh`
is the reference example; it logs each stage and always unmounts the clone. `PUT /api/disks/{id}`
marks/unmarks templates; deleting a template with clones requires
`allow_template_delete_with_clones`. `DELETE /api/disks/{id}` still removes empty and clone LVs,
with script-based removal for script-created clones.

## Attachments, serials, and read-only disks

VMs and LightVMs attach disks through the shared `workload_disks` table (`0053_workload_disks.sql`,
with the per-attachment guest `serial` added in `0057_workload_disk_serial.sql`); `vms.disk_id` is
retained but inert. Create/update requests accept a `disk_serials` map keyed by disk ID; serials are
limited to 20 bytes from `[A-Za-z0-9._:-]`, unique per workload, and `internal/qemu.buildDiskArgs`
switches to an explicit `virtio-blk-pci` device with `serial=` when one is set. VM creation requires
at least one `disk_ids` entry (`disk_id` is accepted as a single-disk alias); `PUT` with `disk_ids`
replaces the set only while stopped, and omitting it preserves the set. A disk can attach to only
one VM or LightVM unless it is read-only: migration `0058_readonly_shared_disks.sql` adds
`disks.read_only`, a per-attachment `read_only` snapshot, a partial unique index for read-write
attachments, and allows any number of read-only owners; `internal/qemu.buildDiskArgs` emits
`readonly=on` for such disks, and `read_only` may change only while the disk has no attachments.
Deleting a VM or LightVM releases its attachment rows. Keep the host operations and store records
consistent (rollback the LV if the DB insert fails; delete the LV before the record).

## Host passthrough devices

Migration `0048_disk_host_devices.sql` adds `disks.source`, `disks.device`, and `disks.size_bytes`;
`source` is `lvm-thin` for managed volumes or `host-device` for passthrough. `GET
/api/host/block-devices/attachable` lists whole disks, partitions, LVs, and RAID arrays (with
mount/swap/holder/registration state), and `POST /api/disks` registers one with `host_device`
instead of `storage_backend_id`/`size_gb`. Passthrough disks cannot be templates or clones; deleting
one removes only the inventory record; the device path is stored verbatim and devices are matched by
`os.SameFile` for duplicate detection, with whole-disk/partition overlap rejected via sysfs
ancestry. `internal/host.ValidateAttachableBlockDevice` rejects non-block paths,
read-only/mounted/swap devices, devices that back another dm/md object, and configured thin pools,
and `internal/qemu.Start` revalidates host devices so a device mounted after registration is not
handed to a guest.

## Disk throttle classes and activation

Disks may reference one reusable `disk_throttle_classes` record; a class stores total/directional
BPS and IOPS limits, burst maximums/lengths, and `iops_size`. The class JSON is resolved through
normal disk/VM reads at start, and `internal/qemu.buildDiskArgs` emits every configured
`throttling.*` option for local and remote drives. Templates cannot carry a class; marking a
template clears it. Class deletion is blocked while assigned, and a class edit applies on the next
VM start. Migration `0051_disk_throttle_classes.sql` adds the inventory and reference;
`0052_drop_disk_bandwidth.sql` drops the obsolete raw `read_bps`/`write_bps` columns in the same
release. Before starting an LVM-backed VM, `internal/qemu.Start` calls `host.LVM.ActivateThinVolume`
(`lvchange -ay -K` for the pool and volume) when the device node is missing, because thin volumes
can remain in LVM metadata while inactive after a reboot. VM creation requires the thin pool only
when at least one selected disk is LVM-backed; VPP remains required.

## Installation media

Installation media are read-only path-backed records: `POST /api/media` stats an existing absolute
regular file, records its server-derived size, and never copies or modifies it. `DELETE
/api/media/{id}` removes only the inventory entry and is blocked while a VM uses the media as boot
media.
