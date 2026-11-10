# LightVM Disk Attachments

*   **Author:** Rookery maintainers
*   **Status:** Proposed
*   **Date:** 2026-11-10

--------------------------------------------------------------------------------

## Overview

LightVM instances combine an uploaded kernel and initramfs with a container
image. At start, Rookery pulls the image, mounts its root filesystem, exposes it
to the guest over virtio-fs as the `rootfs` tag, and launches QEMU. Disks are
inventory records, but only VMs can attach them. A LightVM
has no way to receive a block device, even though the disk inventory may hold
unused LVM thin volumes, host devices, or Kouja iSCSI volumes.

This design adds disk attachments to LightVM definitions. A LightVM can be
created with an ordered disk set, and that set can be replaced while the
LightVM is stopped. At start, each attached disk becomes a virtio block device
in the guest. The same disk cannot belong to a VM and a LightVM at the same
time. VM and LightVM disk ownership moves to one attachment table with nullable
`vm_id` and `lightvm_id` foreign keys, replacing the VM-only `vm_disks` table.
The attachment table becomes the only owner record. Deleting a VM or LightVM
releases its attachments through `ON DELETE CASCADE`; the disk foreign key
rejects disk deletion while an attachment exists. The legacy `vms.disk_id`
column is retained only to avoid a `vms` table rebuild; the migration sets it
to NULL, and no runtime code reads or writes it after migration.
`reportLegacyDiskConflicts` reads it before migration to log ignored, skipped,
and dangling pointers; the migration uses it only to backfill pointer-only VMs.

The baseline is `main` at commit `2c2ba2e`, which includes reusable disk
throttle classes (`0051_disk_throttle_classes.sql`) and removal of the raw
per-disk bandwidth columns (`0052_drop_disk_bandwidth.sql`). LightVM disk
attachments must resolve those classes exactly as VM attachments do: a disk
with a class ID whose row or limits cannot be resolved blocks start, while a
disk with no class ID is unthrottled.

The change does not define how a guest mounts a disk. The image rootfs remains
required. Rookery does not implement a disk-root boot mode; if a guest initramfs
expects a different root, the caller declares that in `append`.

## The Problem

LightVM guests that need persistent or bulk block storage cannot receive it.
Disks can attach only to VMs, so an operator who needs both the LightVM boot
model and block storage has no supported configuration. Disks that are
unattached cannot be attached to a LightVM, and no ownership record spans both
workload kinds.

## Goals

*   Allow `POST /api/lightvm/instances` and `PUT /api/lightvm/instances/{id}` to
    attach zero or more existing disks through ordered disk IDs. Updating with a
    replacement list changes attachments only while the LightVM is stopped.
*   Expose every attached disk as a virtio block device when QEMU starts,
    covering LVM thin volumes, host passthrough devices, and Kouja iSCSI volumes
    through the existing start planner and QEMU disk argument builder.
*   Enforce single-owner attachment across VMs and LightVMs so one disk cannot
    be assigned to two QEMU processes. `UNIQUE (disk_id)` and the exact-owner
    `CHECK` enforce single ownership; `ON DELETE CASCADE` releases attachments
    when an owner row is deleted. The API rejects conflicting writes, and the
    UI hides ineligible disks.
*   Cap new or growing LightVM definitions at 24 combined disk and interface
    devices. Permit existing definitions above 24 to be edited as long as the
    edit does not increase the total device count.
*   Report LightVM disk ownership in the disk inventory API and UI, and show
    attached disks on the LightVM list and edit form.
*   Resolve each attached disk's current throttle class before QEMU start and
    apply its limits through the existing QEMU disk argument builder, matching
    VM behavior. An invalid or missing class must block the LightVM start with
    the same throttle-class error `buildDiskArgs` reports for a VM.
*   Release LightVM disk attachments when the LightVM is deleted without
    deleting the disk records.
*   Preserve existing VM behavior and data through the attachment-table
    migration, including databases where only the legacy `vms.disk_id` pointer
    exists.

## Non-Goals

*   No disk-root boot mode. A guest initramfs may choose to mount an attached
    disk, but Rookery does not implement or support that boot path and does not
    inject, rewrite, or validate `root=`.
*   The container image requirement remains: a LightVM definition requires
    `image`.
*   No guest-side formatting, partitioning, mounting, or filesystem management.
    The guest initramfs and guest image own that behavior.
*   No disk hotplug. Disks can be changed only while the LightVM is stopped.
*   No disk creation or import from the LightVM form. Disks continue to be
    created on the Disks tab and attached by ID.
*   No new disk lifecycle endpoints or provisioning operations. Ownership
    checks in existing template, provisioning, and deletion paths are in scope,
    plus attachment release when a LightVM is deleted. Throttle-class assignment
    remains allowed while a disk is attached and applies at the next start, for
    both VM and LightVM disks. Existing throttle classes apply unchanged.
*   No boot media, CD-ROM, or firmware boot path for LightVMs.
*   No LightVM resume action in this change. The response exposes
    `run_state`, and a paused LightVM is recovered by stopping and starting it;
    the API does not expose a resume path yet.
*   No direct transfer of an attachment between workloads; a disk must be
    detached before another workload can attach it.

## Open Questions

None.

## Critical User Journeys (CUJs)

### CUJ 1: Attach a data disk to a new LightVM

*   **Given**: An attachable, unattached disk exists in the Disks inventory, and
    the kernel, initramfs, and container image for the LightVM are available.
*   **When**: The operator creates a LightVM with that disk ID and starts it.
*   **Then**: QEMU starts with the disk attached as a virtio block device, the
    LightVM details show the disk, and the disk inventory shows the LightVM as
    its owner. The guest can mount the device if its initramfs supports it.

### CUJ 2: Replace disks on a stopped LightVM

*   **Given**: A stopped LightVM has two attached disks.
*   **When**: The operator saves a one-item replacement list naming a different
    disk.
*   **Then**: The old attachments are removed, the new set is stored in order,
    and the released disks become attachable by another VM or LightVM.

### CUJ 3: Reject a disk that is already owned

*   **Given**: A disk is attached to a running or stopped VM.
*   **When**: The operator tries to create or update a LightVM with that disk.
*   **Then**: The API rejects the request. For a create, no LightVM is created;
    for an update, the definition is unchanged. The disk remains attached to
    the VM.

### CUJ 4: Delete a LightVM with disks

*   **Given**: A stopped LightVM has attached disks.
*   **When**: The operator deletes the LightVM.
*   **Then**: If the LightVM is running, QEMU is stopped first; a stop failure
    aborts the delete and leaves the record. Otherwise the LightVM and its
    attachment rows are removed, every disk record still exists, and the disks
    become attachable again. No disk is deleted.

### CUJ 5: Start with a Kouja iSCSI disk

*   **Given**: A LightVM is attached to a Kouja disk and the storage backend is
    reachable.
*   **When**: The operator starts the LightVM.
*   **Then**: QEMU attaches the iSCSI volume as a virtio block device, and the
    temporary secret file is removed after QEMU starts or fails. The preparation
    and lock ordering are described in the Start path.

### CUJ 6: Start with a throttled disk

*   **Given**: A LightVM is attached to a disk whose throttle class defines
    bandwidth or IOPS limits.
*   **When**: The operator starts the LightVM.
*   **Then**: The class is resolved at start and QEMU receives the corresponding
    `throttling.*` options. If the class row is missing or its limits are
    invalid, the start fails with the class-specific error and no QEMU process
    is left running.

## Architecture

### Component view

```text
                     +----------------------------------+
                     |          SQLite inventory        |
                     |  disks                           |
                     |  workload_disks (owner)          |
                     |  disk_throttle_classes           |
                     +----------------+-----------------+
                                      |
                 +--------------------+--------------------+
                 |                                         |
        +--------v---------+                     +---------v---------+
        |   VM service     |                     |  LightVM service  |
        | gateway + API    |                     | gateway + API     |
        +--------+---------+                     +---------+---------+
                 |                                         |
                 | vmstart.Planner                         | gateway/API plan
                 | (remote attachments)                    | -> lightvm.Runtime
                 v                                         v
        +--------+------------------------------------------+---------+
        |                      internal/qemu                          |
        | buildArgsWithPlan / buildLightVMArgs -> buildDiskArgs       |
        | temp CHAP secret files under run-dir, removed after start   |
        +-------------------------------------------------------------+
```

The design keeps disk ownership in the inventory, keeps runtime attachment
planning in `internal/vmstart`, and keeps QEMU argument construction in
`internal/qemu`. LightVM start is two-phase: image preparation and planning run
under `startCoordinator`, and only short lifecycle transitions and the final QEMU
start take `mu`. It does not duplicate the disk argument rules or take the
shared disk mutex; attachment
ownership is durable, and disk deletion refuses an attached disk before it
reaches local volume removal.

### Locking model

Four names recur in this document:

*   **`resourceMu`** is the shared host-resource mutex (`gateway.options.diskMutex`,
    `api.Server.resourceMu`). Disk create/delete, network/interface writes,
    storage-backend writes, VM create, and LightVM create/update hold it.
*   **`mu`** is the shared workload lifecycle mutex (`gateway.options.vmMutex`,
    `api.Server.runMu`). VM and LightVM start/stop, VM/LightVM update, and
    VM/LightVM delete hold it.
*   **`runMu`** in `internal/api` is the same mutex as `mu`; the two names refer
    to the same instance after `gateway.New` wiring.
*   **`startCoordinator`** is a shared per-LightVM start coordinator. It
    serializes preparation and the final QEMU start only for the same LightVM;
    different LightVMs prepare concurrently. It stores a per-ID cancel function
    so stop, update, and delete can abort an in-flight image preparation. It is
    not `mu`; a slow image pull under `startCoordinator` does not block VM
    create/update, disk/network writes, or another LightVM’s start. Rookery
    assumes one API process manages a given run directory; cross-process
    starts of the same LightVM are unsupported, so the per-process
    `startCoordinator` is the authority. Attempt-resource reaping still checks
    live QEMU command lines so an orphan or externally started QEMU is not
    disrupted.

The lock order is `reservation` → `resourceMu` → `mu`, and `Begin` slot → `mu`.
The `Begin` slot and the lifecycle reservation are distinct: the slot is held
only by an active start, while stop/update/delete hold a reservation without
taking the start slot. A reservation may be held while acquiring
`resourceMu`/`mu`; the `Begin` slot must not acquire `resourceMu`. LightVM start
takes the `Begin` slot for the whole start, then `mu` only for short lifecycle
transitions (intent write, failure/status write, final runtime start), never for
image preparation or cleanup. LightVM create takes `resourceMu` only. LightVM
update takes a reservation, then `resourceMu`, then `mu`. Disk delete takes
`resourceMu` only. Do not add a LightVM path that acquires `resourceMu` while
holding `mu`, or `resourceMu` while holding the `Begin` slot; cleanup runs
outside `mu` under the reservation or `Begin` slot.

### Terminology

*   A **workload** is a VM or a LightVM. An **attachment** is a
    `workload_disks` row. The **owner** is the workload named by that row’s
    non-null `vm_id` or `lightvm_id`.
*   A **disk template** is a disk marked `is_template`, used as a clone source
    and not attachable to a workload.
*   A disk in the **provisioning**, **deleting**, or **delete_failed** state is
    not attachable. `provisioning` means a clone is being prepared; `deleting`
    means asynchronous removal is queued; `delete_failed` means the last
    reclaimer attempt failed.
*   The **background reclaimer** is the worker that retries `provisioning`
    cleanup and `deleting` remote/LVM disks. It skips any disk with an
    attachment. `delete_failed` is terminal until an explicit `DeleteDisk`
    calls `MarkDiskDeleting` again; the reclaimer does not claim it.
*   A **storage backend** is a `storage_backends` record that selects a driver
    (`lvm-thin` or `kouja`) and its configuration. `lvm-thin` disks are local;
    Kouja disks are remote iSCSI volumes. A **host-device disk** is a
    registered whole disk or partition (`source=host-device`) passed through to
    the guest; it has no storage backend.
*   A LightVM **interface** is a vhost-user network NIC attached through
    `lightvm_interfaces`; each interface consumes one device slot. A LightVM
    **console** is a virtio-console port; all consoles share one
    `virtio-serial-pci` controller and do not each consume a PCI slot.
*   The **single database connection** is the store's SQLite pool setting:
    `store.Open` sets `SetMaxOpenConns(1)`. A helper that queries `s.db` while a
    transaction is open waits for that same connection and deadlocks.
*   **`append`** is the LightVM kernel command line field. Its default is
    `console=hvc0 rdinit=/init`. The caller controls it, including any `root=`
    selector.
*   **`desired_state`** is the persisted power intent (`running` or `stopped`)
    used by API autostart. It is distinct from the observed `status`.
*   **`lifecycle_generation`** is a monotonically increasing LightVM counter
    incremented by start, stop, update, and status/desired-state writes. A start
    captures it and rechecks it under `mu` to detect a concurrent lifecycle
    change during image preparation.
*   **`APP_HOSTNAME`** is a kernel command-line parameter read by the guest
    initramfs/DHCP client as the DHCP hostname/FQDN. **`lightVMAppend`** is the
    helper in `internal/qemu/lightvm.go` that builds the kernel command line and
    appends `APP_HOSTNAME=<name>` unless `append` already contains a
    whitespace-delimited field beginning with `APP_HOSTNAME=`.
*   The **QEMU run directory** is `qemu.DefaultRunDir` (`/run/rookery`) unless
    `-run-dir` overrides it. LightVM runtime artifacts use the
    `lightvm-<uuid>` prefix under that directory.
*   A **secret file** is a 0600 file under the QEMU run directory that contains
    the CHAP credential for a remote disk. The credential value is not stored
    in SQLite or QEMU argument text.
*   A **start plan** is `qemu.StartPlan`, a struct carrying an
    `Attachments map[int64]blockdev.Attachment`. Each entry describes a
    prepared remote disk (protocol, portal/target/LUN or equivalent, and
    credentials); local disks have no entry, and an empty plan is valid.
*   An **importable disk** is an existing thin volume returned by a storage
    backend's import list; already-registered volumes appear with their owner
    state.
*   `store.VMDisk` is the Go attachment row type. `rookeryv1.VMDisk` is the
    protobuf response message. This document qualifies the name when the
    distinction matters.

### Unified disk ownership table

`vm_disks` is replaced by `workload_disks`. A row has nullable `vm_id` and
`lightvm_id` foreign keys, a non-null `disk_id`, and an ordered `position`.
Exactly one owner column must be non-null. The exact DDL is specified once in
Detailed Plan item 1.

The unique disk constraint enforces single ownership and provides the disk
lookup index. The two owner/position constraints enforce ordered attachments;
SQLite treats NULL values as distinct in UNIQUE constraints, so a row with a
NULL `vm_id` does not conflict on `UNIQUE (vm_id, position)` and each constraint
applies only to its own workload type. `ON DELETE CASCADE` on the owner foreign keys
releases attachments when a VM or LightVM is deleted. `ON DELETE RESTRICT` on
the disk foreign key prevents deleting an attached disk. No triggers are
required.

The legacy `vms.disk_id` column is retained only to avoid a full `vms` table
rebuild. The migration copies `vm_disks` with deterministic positions, then
treats `vm_disks` as authoritative for a VM that has any rows: its legacy
pointer is ignored and logged if it names a different disk. A pointer is
backfilled only for a VM with no `vm_disks` rows, only when the disk still
exists, and only when it is not already attached to any workload. The migration
then sets `vms.disk_id` to
NULL and drops `vm_disks`. The column is inert after migration: no runtime code
reads or writes it; `reportLegacyDiskConflicts` reads it before the migration
only to log skipped, ignored, or dangling pointers. `store.Open` logs each
pointer before applying the migration, and `docs/operations.md` provides a
pre-upgrade query so an operator can inspect it. A database with a conflicting,
ignored, or dangling pointer still opens.

Workload IDs are generated UUIDs. Runtime artifacts are namespaced by kind:
VMs use `<uuid>` paths under the run directory, while LightVMs use
`lightvm-<uuid>` paths, and LightVM process matching requires the LightVM
pidfile. A manual database insert or a UUID collision therefore cannot make a VM
stop or clean up a LightVM. The design does not add a cross-table ID uniqueness
check; the namespace is the safety boundary.

A manual non-NULL write to `vms.disk_id` is unsupported: the loader ignores the
column, so it cannot create or change ownership. The column remains only until
a later migration can rebuild `vms` without its column-level `UNIQUE`/foreign
key constraints.

The disk inventory query left joins `workload_disks` and then both owner tables.
An inner join would hide every unattached disk. The query exposes both
`attached_vm_id`/`attached_vm_name` and
`attached_lightvm_id`/`attached_lightvm_name`; exactly one pair is set while a
disk is attached. This is a new public API representation.

Every place that treats `attached_vm_id` as the only ownership signal must use
the same ownership predicate. That includes template, provisioning, deletion,
reclaimer, import, host attachable device, and UI picker paths.

### Disk loading and throttle classes

Both workload types load attachments through the same row shape: disk ID, name,
size, backend, source, device, protocol, and throttle class. Attachment
position orders the loader result but is not exposed on `VMDisk`. `VMDisk` is
the shared attachment type for both workloads. The loader
always selects `d.throttle_class_id` and the joined
`disk_throttle_classes` name and limits. When the class ID is present but the
row or limits cannot be resolved, the loader records a throttle error that
blocks start. When the class ID is absent, the disk is unthrottled. Both
workload loaders use the same throttle resolution; omitting the class column
would silently start a throttled disk without limits.

### Store and transaction rules

*   The replacement contract is: a non-empty `disk_ids` always replaces the
    set; an empty `disk_ids` preserves the set unless `replace_disk_ids` is
    true, which clears it. Create with no disks is valid. The store derives
    the effective flag as “`ReplaceDiskIDs` is true or `DiskIDs` is non-empty”;
    a non-empty list always replaces, and the computed flag is authoritative.
*   `CreateLightVM` normalizes and validates disk IDs before `BeginTx`, then
    inserts the LightVM row and attachment rows in one transaction. Validation
    inside the transaction would block on the store's single database
    connection.
*   `UpdateLightVM` normalizes and validates before `BeginTx`, then deletes the
    existing LightVM attachment rows (`WHERE lightvm_id = ?`) and inserts the
    new ordered set in the same transaction as the editable fields.
    Delete-before-insert avoids transient unique-position and unique-disk
    conflicts. When `replace` is false, the transaction performs no attachment
    delete or insert. `UpdateLightVM` is allowed only while stopped and always
    persists `desired_state='stopped'`.
*   A LightVM has a combined disk-plus-interface device budget of 24 for a new
    definition or an edit that grows the device set. **24 is a conservative
    product cap, not a hardware-derived maximum.** The supported capacity
    platform is `linux/amd64`; Rookery does not pass `-machine`, so QEMU uses
    the default `pc-i440fx` machine (32 PCI slots). The rootfs
    `vhost-user-fs-pci`, the single `virtio-serial-pci` controller, the
    host/PIIX3 bridges, and the default VGA consume fixed slots even with
    `-display none`; each disk and interface consumes one more. Consoles share
    the serial controller. A non-skippable unit test asserts the generated
    argument list and the documented slot arithmetic. A separate QEMU/KVM-gated
    integration slot probe (KVM, or `-accel tcg` with `-cpu max` when KVM is
    unavailable) measures actual headroom with real vhost-user peers. The
    integration probe is not a runtime startup probe; it is the capacity proof
    for local/release verification. If it shows fewer than 24 usable device
    slots, lower `MaxLightVMDevices` to the measured value in the same release. LightVM deliberately preserves its existing no-`-machine` boot path;
    switching it to the VM’s `q35` machine is a separate change. The LightVM
    disk normalizer is pure: it preserves order, deduplicates, rejects
    non-positive IDs and oversized lists, and permits an empty result. The VM
    normalizer keeps its one-disk minimum.
*   Existing LightVMs above 24 devices are grandfathered. The store loads the
    existing disk count and existing interface count before the update
    transaction. For an update, `newDisks` is the normalized replacement list
    when `replace` is true and the existing disk count otherwise;
    `newInterfaces` is the normalized request interface set because
    `UpdateLightVM` replaces interfaces. If the existing total is above 24, the
    update is allowed only when the new total does not exceed the existing
    total. If the existing total is at most 24, the new total must also be at
    most 24; a create request is allowed only when its total is at most 24.
    This permits a rename or a same-size edit of a grandfathered high-count
    definition and rejects disk/interface growth. A definition above 24 may be
    edited but `StartLightVM` rejects it with `FailedPrecondition` until it is
    reduced. The create/update sentinel is `ErrLightVMDeviceLimit`.
*   The legacy pointer is inert (see Unified disk ownership table).
    `VM.DiskID`/`VM.Disk` are position 0 of the ordered attachment set. New VMs
    always have at least one disk; a legacy VM with no `vms.disk_id` and no
    `vm_disks` row may have nil fields until it is edited.
*   Three checks replace the existing availability check: an ownership
    predicate that rejects any attachment except the supplied workload (an
    empty owner means “reject any owner”); a not-deleting check that rejects
    any non-empty `DeletionState` (`provisioning`, `deleting`, or
    `delete_failed`); and an attach-availability predicate that combines both
    and also rejects templates. Attach paths and marking a template use
    attach-availability. Provisioning settings use ownership plus
    not-deleting, because the disk is already a template. Unmarking a template,
    deleting a disk, and reclaimer ownership checks use ownership only.
    `MarkDiskDeleting` uses none of them as a pre-check; it is the
    conditional-update exception.
*   Deleting a LightVM removes its attachment rows through the owner foreign
    key cascade. The disk rows are untouched.
*   LightVM create uses only `resourceMu`; update uses a reservation, then
    `resourceMu`, then `mu`. Delete uses `startCoordinator.Reserve`/`Cancel`
    plus `mu`; start uses the `Begin` slot, then `mu` only for short
    intent/status transitions and
    the final QEMU start. `UNIQUE (disk_id)` is the authoritative constraint;
    unique violations map to a wrapped
    `ErrDiskInUse` that names the owner. A foreign-key violation on an
    attachment insert is handled by explicitly rolling back the transaction,
    then re-reading the disk; a missing disk maps to `ErrDiskNotFound`, and any
    other foreign-key failure maps to `ErrDiskInUse`. Never call a helper that
    uses `s.db` while a transaction is open.
*   The deletion-state transition is one conditional update guarded by the
    absence of an attachment. After a zero-row result, `MarkDiskDeleting`
    re-reads the disk outside any transaction to distinguish `ErrDiskNotFound`
    from `ErrDiskInUse`. This is the authoritative guard for the reclaimer
    path. A disk that is both attached and in a non-empty deletion state is an
    inconsistent row (possible through manual SQL or a race that predates this
    guard):
    attach validation rejects it with `ErrDiskDeleting`, delete names the
    owner, and the reclaimer skips it. Repair requires detaching the disk or
    clearing `deletion_state` after verifying the owner; the API does not
    silently delete it.

### Start path

LightVM start is two-phase. The gateway and API autostart share one
`startCoordinator`; it gives each LightVM its own start slot, so different
LightVMs prepare concurrently up to the global preparation limit (default 2),
and only a second start of the same LightVM or a preparation beyond the limit
waits.
`startCoordinator` creates a cancellable start context, and stop/update/delete
call its `Cancel` method to abort an in-flight pull. `mu` is held only for short
lifecycle transitions and the final QEMU start, not for image pull or buildah
preparation.

The numbered flow below is conceptual; Detailed Plan item 10 is authoritative
for exact status writes, lock scopes, and cleanup order.

1.  Load the LightVM with its ordered disk set and resolved throttle limits;
    a missing row returns `NotFound`. If
    `len(vm.Disks)+len(vm.Interfaces) > MaxLightVMDevices`, return
    `FailedPrecondition` with the counts and remediation before `Begin`, image
    preparation, or any runtime call. Capture
    `requestGeneration := vm.LifecycleGeneration` and
    `loadedDesired := vm.DesiredState` as the queued-start tokens.
2.  Call `startCoordinator.Begin(ctx, vm.ID)` for a cancellable `startCtx`, a
    deferred `done`, and an error. A canceled wait returns `Aborted` without
    any store write or preparation. Different LightVMs proceed concurrently; a
    second start of this LightVM waits here.
3.  Take `mu` in a short scope and re-read as `current`. If the row disappeared, return
    `NotFound`. If QEMU is running: when `current.DesiredState == stopped`, a
    stop won (possibly a stop whose QEMU kill failed), so return `Aborted`
    without changing intent; otherwise call `RunState` and return
    `FailedPrecondition` for a `paused` value or monitor error, or repair
    `status=running`/`desired_state=running` and return `AlreadyExists`. If
    QEMU is not running, return `Aborted` when `current.DesiredState !=
    loadedDesired` or its generation differs from `requestGeneration` (a
    stop/update/delete landed while queued). Recheck the device budget; if it
    now exceeds `MaxLightVMDevices`, return `FailedPrecondition` before setting
    the start intent or calling the runtime. Otherwise record the previous
    `desired_state`, set `desired_state=running` as the start intent, capture
    the returned `lifecycle_generation` as `startGeneration`, and assign it
    back to `current.LifecycleGeneration` for monotonic attempt tokens. Release
    `mu` before image preparation.
4.  Prepare the runtime outside `mu` with the start context: stop stale
    virtiofsd for this LightVM only when no QEMU is alive, pull the container
    image when needed, create and mount an attempt-named ephemeral buildah
    container, write the environment pairs to `<rootfs>/.env`, and write the
    executable command line to `<rootfs>/.entrypoint`. The guest initramfs
    reads those files. Preparation may reap only older-token or legacy
    resources after the live-QEMU guard; it never removes the current/newer
    attempt's resources or anything referenced by a live QEMU.
5.  Build the start plan with `PlanDisks(startCtx, current.Disks)`. The plan is built
    after image preparation so a long pull cannot expire remote credentials.
    On a preparation or planning failure, take `mu` and re-read. If the row is
    gone, release `mu`, clean up with a fresh
    bounded context, and return `NotFound`. If QEMU is running, apply the same
    QEMU-running branch as step 3: `desired_state=stopped` returns `Aborted`;
    a `paused` value or monitor error returns `FailedPrecondition`; otherwise
    repair `status`/`desired_state` and return `AlreadyExists`. Release `mu`
    before cleaning up, and do not write an error status in that branch. If
    QEMU is not running and the generation changed, `desired_state` is no
    longer `running`, or the error is `context.Canceled`, release `mu`, clean
    up, and return `Aborted` without a status write; restore the previous
    desired state only when it was `stopped` and the current desired state is
    still `running`, and never overwrite a stop’s `stopped`. If the error is
    `qemu.ErrAlreadyRunning` but QEMU is not running, set `status=error`,
    restore the previous desired state when it was `stopped`, release `mu`,
    clean up, and return `Aborted` with “start raced an exiting QEMU; retry”
    instead of the `AlreadyExists` mapper. Only when the row, QEMU state (not
    running), generation, and desired state are unchanged and the error is not
    a cancellation, set `status=error`, restore the previous desired state
    when it was `stopped`, release `mu`, clean up, and return
    `lightVMStartStatusError(err)`. Cleanup uses a fresh bounded context.
    `startCoordinator` is released by the deferred `done`.
6.  Take `mu`, re-read as `current`, and recheck QEMU. If the LightVM disappeared, release
    `mu`, clean up the prepared container with a fresh bounded context, and
    return `NotFound` without writing status. If QEMU is running and
    `current.DesiredState == stopped`, a stop won (possibly a failed QEMU kill):
    release `mu`, clean up only this attempt's prepared container, and return
    `Aborted` without repairing status. If QEMU is running, call `RunState`; if
    it begins with `paused`, release `mu`, clean up, and return
    `FailedPrecondition` with “LightVM is paused by an I/O error; stop and
    start to recover.” If `RunState` errors while QEMU is alive, release `mu`,
    clean up, and return `FailedPrecondition` with “cannot read LightVM run
    state; stop and start to recover.” Otherwise repair
    `status`/`desired_state`, release `mu`, clean up, and return `AlreadyExists`. If `lifecycle_generation` no longer
    equals `startGeneration`, an update
    happened: restore the previous desired state only when the current desired
    state is still `running`, release `mu`, clean up, and return `Aborted`. If
    `desired_state` is no longer `running`, a stop happened: do not restore it,
    release `mu`, clean up, and return `Aborted`. A changed disk set also
    increments the generation, so the generation branch covers it; the caller
    gets `Aborted` and starts again. A grandfathered definition above 24 may be
    edited but cannot start until reduced; the budget check happens before
    image preparation, so the UI displays the `FailedPrecondition` counts and
    remediation without a pull. The UI does not auto-retry. Cleanup runs outside
    `mu` but while `startCoordinator` is still held, using a fresh context with a
    bounded buildah timeout.
7.  With `mu` still held, start the attempt-specific virtiofsd and call the
    plan-aware QEMU start with that socket; cleanup for this attempt uses only
    its exact PID/socket/container.
    `internal/qemu` validates local disks (reactivating LVM thin volumes when
    the device node is missing and revalidating host devices), writes temporary
    0600 secret files for remote disks under the `lightvm-<uuid>` prefix,
    builds the kernel/initramfs/append arguments, appends one virtio drive per
    disk in attachment order, starts QEMU, and removes the secret files. Secret
    removal is deferred so a validation, argument-build, or QEMU-start failure
    removes them too.
8.  Immediately before the final runtime call, if `startCtx.Err()` is
    canceled, restore the previous desired state when it was `stopped` and the
    current desired state is still `running`, release `mu`, perform the
    post-start cleanup below, and return `Aborted` without a status write.
    Otherwise call `StartPreparedLightVM`. If it returns nil, record success
    even when `startCtx` was canceled after launch: set `status=running` and
    `desired_state=running` with a fresh context, then release `mu`. If it
    returns an error, re-read QEMU. If QEMU is running and
    `prepared.QEMUStarted` is false, apply the QEMU-running branch from step 6
    (desired stopped → `Aborted`; paused/monitor error → `FailedPrecondition`;
    otherwise repair/`AlreadyExists`) without an error status or cleanup of
    that QEMU. If the error is `context.Canceled` or `startCtx.Err() != nil`,
    restore the previous desired state when it was `stopped` and the current
    desired state is still `running`, release `mu`, perform the cleanup below,
    and return `Aborted` without a status write. Otherwise, if the runtime
    returns `qemu.ErrAlreadyRunning`, re-read. If QEMU is no longer running,
    set `status=error`, restore the previous desired state when it was
    `stopped`, perform the cleanup below, and return `Aborted` with “start
    raced an exiting QEMU; retry” instead of `AlreadyExists`. If QEMU is
    running and desired is stopped, return `Aborted`; if QEMU is running, call
    `RunState`, return `FailedPrecondition` for a `paused` value or monitor
    error, or repair/`AlreadyExists` otherwise; do not write error status or
    stop QEMU in that branch. On any other runtime failure, set `status=error`,
    restore the previous desired state when it was `stopped`, release `mu`, and
    return `lightVMStartStatusError(err)`. For an actual
    `StartPreparedLightVM` failure other than `qemu.ErrAlreadyRunning` with
    `QEMUStarted=true`, the caller calls `StopQEMULightVM` with a fresh bounded
    context if QEMU is still alive and only after it confirms QEMU is gone
    calls `CleanupPreparedLightVM` to stop only this attempt's exact virtiofsd
    and remove its container. If QEMU never started, the caller skips
    `StopQEMULightVM` but still calls `CleanupPreparedLightVM` for the attempt
    container/virtiofsd. If the stop fails or QEMU remains alive, skip
    cleanup, retain the rootfs, and return the stop error. The
    `ErrAlreadyRunning` branch never stops QEMU. If `StartPreparedLightVM`
    succeeded but a later status write failed, leave the running QEMU and its
    resources alone and return the store error. On
    pre-start aborts (AlreadyExists, paused, generation change, missing row,
    over-budget), `CleanupPreparedLightVM` removes only this attempt's container
    (and this attempt's virtiofsd only when `VirtiofsdStarted`) and never stops
    an existing QEMU. The runtime does not write status; the gateway or API
    autostart records `error`. `startCoordinator` is released by the deferred
    `done` on every return path.

The gateway does not hold `resourceMu` during start. Once a disk is attached,
ownership prevents deletion; `mu` prevents an attachment change while the
final QEMU start is in progress; `startCoordinator` prevents two preparations
of the same LightVM from racing, while different LightVMs proceed concurrently. Disk, network, interface, storage-backend, VM-create, and
LightVM-create writes that take `resourceMu` continue during a slow image pull.
A LightVM update that takes `resourceMu` then waits for `mu` waits only for a
short intent/status write, the final start, or a concurrent QEMU stop (bounded
by the stop timeout), not for the image pull.

API autostart uses the same `startCoordinator` and runtime methods without
calling the gateway service. It lists LightVMs without a global lock, then
processes each item in a helper closure. It loads the item and captures
`requestGeneration` before `startCoordinator.Begin`, so a stop/update/delete
that lands while the start is queued changes the generation and skips the
start. Under `runMu`, it re-reads, skips when the generation changed or
`desired_state` is no longer `running`, repairs `status`/`desired_state` and
skips if QEMU is running, and otherwise sets `desired_state=running` and
captures the returned `lifecycle_generation`. It prepares the image and plan outside `runMu`, then
acquires `runMu`, re-reads, and verifies the generation is unchanged,
`desired_state == running`, QEMU is not running, and the disk-plus-interface
count is at most 24; the budget is checked before preparation so an over-budget
definition does not pull an image on every restart. An over-budget definition
is logged, set to `status=error` and `desired_state=stopped`, cleaned up, and
skipped. It starts the prepared runtime while holding `runMu`, sets
`status=running` on success and `status=error` on failure, except that a final
`qemu.ErrAlreadyRunning` repairs status/desired and skips, and a cancellation
skips without a status write; it also skips without a status write on any
change. If `StartPreparedLightVM` failed and QEMU is alive, it calls
`StopQEMULightVM`; only after QEMU is confirmed gone does it clean up the
container/virtiofsd outside `runMu` with a fresh bounded context. If the stop
fails or the process remains, it retains the resources and logs the error. A
preparation or planning failure re-reads under `runMu` and writes `error` only
when the row still exists, QEMU is not running, the generation and
`desired_state` are unchanged, and the error is not a cancellation.

`StopLightVM`, `UpdateLightVM`, and `DeleteLightVM` call
`release := startCoordinator.Reserve(id)`, `defer release()`, then
`Cancel(id)` before waiting on `mu`. An in-flight image preparation is canceled
promptly instead of running to the buildah timeout, and a new start cannot race
cleanup; concurrent reservations are refcounted until every release runs.
Stop is intent-first and split from cleanup. Under `mu`, `StopLightVM` writes
`desired_state=stopped`, calls the QEMU-only stop, and writes `status=stopped`.
It releases `mu` before stopping virtiofsd and removing the container, using a
fresh bounded context, only after QEMU is confirmed gone. If the QEMU stop
fails, cleanup is skipped, the row keeps a running/error status, desired remains
stopped, and the stop error is returned. A cleanup error after a successful QEMU
stop is returned, but `status`/`desired_state` remain stopped, so a later API
restart does not autostart a LightVM the operator stopped. `DeleteLightVM`
uses the same reservation, writes the stopped intent, and stops QEMU under
`mu`; it then runs virtiofsd/container cleanup outside `mu` and deletes the row
only after cleanup succeeds, so a cleanup failure leaves a retryable row.

Attachment order determines QEMU `-drive` order only. The guest kernel assigns
`/dev/vdX` names, so a device-name root is not a stable contract. `PARTUUID=` or
`LABEL=` is the recommended selector. Rookery does not add, remove, rewrite, or
validate `root=`. `lightVMAppend` appends `APP_HOSTNAME=<name>` unless
`append` already contains a whitespace-delimited field beginning with
`APP_HOSTNAME=`.

There is no new readiness gate. Disk creation still occurs on the Disks tab and
is gated by the selected storage backend's readiness (see
[`docs/operations.md`](../operations.md)). LightVM start fails with the
underlying storage or QEMU error when a disk device is unavailable, which
matches VM start behavior.

### API changes

Every LightVM response (create, update, get, list) gains `disk_ids` (field 25),
`disks` (field 26), and `run_state` (field 27, observed QEMU monitor state such
as `running`, `paused`, or `paused (io-error)`; empty when stopped). `CreateLightVMRequest` gains
`disk_ids` (field 16) with
`max_items: 32` and positive-ID item validation, matching the existing
`interface_ids` pattern. `UpdateLightVMRequest` gains `disk_ids` (field 17) with
the same validation and `replace_disk_ids` (field 18). The proto `max_items`
remains 32; the store and gateway enforce the combined disk-plus-interface
LightVM device budget of 24.

The replacement contract is:

| `disk_ids` | `replace_disk_ids` | Result |
| --- | --- | --- |
| non-empty | true, false, or omitted | Replace the set with this ordered list |
| empty | true | Clear the set |
| empty | false or omitted | Preserve the current set |
| empty or omitted on create | not applicable | Create with no disks |

The store computes
`replace := ReplaceDiskIDs || len(DiskIDs) > 0`, so a non-empty list always
replaces even when `replace_disk_ids` is omitted or false. `PUT` remains a full
replacement for the other editable fields; a client that only wants to change
disks must send the current values for interfaces, consoles, and the other
required fields. Any update to a running LightVM is rejected before any write
with the existing `Aborted` message “stop the LightVM before editing it”; disk
changes follow that rule and have no separate error.

The `Disk` message gains `attached_lightvm_id` (field 28) and
`attached_lightvm_name` (field 29). `ImportableDisk` gains the same fields at 8
and 9. Throttle class fields on `VMDisk` are returned through
`vmDiskToProto`, so LightVM disk responses show the same class information as
VM responses.

Errors use the VM attach contract: `InvalidArgument` for an unknown disk or an
invalid disk ID list; `InvalidArgument` when a combined device count exceeds 24,
or exceeds the existing total for a grandfathered definition; `Aborted` for a
template disk, a disk with any
non-empty `DeletionState` (`provisioning`, `deleting`, or `delete_failed`), or a
disk owned by another workload. `FailedPrecondition` is returned by start when
the LightVM is paused by an I/O error or when a grandfathered definition still
exceeds 24 devices; the message includes recovery/remediation guidance. The
`ErrDiskInUse` path is wrapped with owner kind and name so the message can
identify the VM or LightVM. The LightVM gateway uses a LightVM-specific start
error mapper so `kouja.ErrSessionActive` maps to `Unavailable` (HTTP 503),
which the UI mutation retry policy retries at 1s/2s/4s/10s; other start errors
require an operator retry. API autostart does not return gRPC
status; it logs the error and records `status=error`.

For VMs, `disk_id` and `disk` are compatibility projections of position 0 in
the ordered attachment set; they are populated from `workload_disks` and are
not read from `vms.disk_id`. LightVM responses expose only the ordered
`disk_ids` and `disks` fields.

### Runtime and QEMU changes

The start planner gains `PlanDisks(ctx, disks) (qemu.StartPlan, error)`, which
prepares remote credentials and attachments without requiring a `store.VM`.
`Plan(ctx, vm)` delegates to it. The gateway and API autostart call `PlanDisks`
after image preparation and before the final `mu`-guarded start.
`internal/lightvm.Runtime` does not own a planner. It exposes two phases:
`PrepareLightVM` pulls the image, creates/mounts the container, and writes the
guest files; `StartPreparedLightVM` starts virtiofsd and QEMU with the prepared
rootfs and plan. `CleanupPreparedLightVM` removes the container on abort or
failure. The runtime remains store-free. The old `StartLightVM` and
`StartLightVMWithVirtioFS` wrappers are removed in phase 3, after the
gateway/API interfaces move. Secret-file handling stays in
`internal/qemu`; the runtime does not remove secret files itself.

The gateway and API autostart share one `StartCoordinator`. It is created in
`cmd/rookery` and passed to both; it serializes preparation and the final start
per LightVM and stores the cancel function used by stop/update/delete, but does
not replace `mu` or hold `resourceMu`. Different LightVMs prepare concurrently.

LightVM QEMU artifacts use the `lightvm-<uuid>` prefix under the run
directory: console sockets, pidfile, monitor socket, and disk secret files.
The virtiofsd socket/pid/log and buildah container add a per-attempt token
(`lightvm-<uuid>-<token>.virtiofs.sock`,
`rookery-lightvm-<uuid>-<token>`), so a failed attempt cannot remove another
attempt's rootfs or virtiofsd. The QEMU `-uuid` argument remains the LightVM
UUID. `RunningLightVM` and `StopLightVM` match a process by the LightVM pidfile path,
not only by `-uuid`, and VM process matching excludes LightVM pidfiles. This
keeps a VM and a LightVM with the same UUID from stopping each other. Until
the next major release, `RunningLightVM` and `StopLightVM` also recognize the
legacy unprefixed LightVM pidfile and console socket so an already-running
LightVM can be reached after the upgrade; remove these fallbacks in that
release.

The runtime exposes the QEMU monitor run state (`RunState`) and the gateway
maps it to `LightVM.run_state`. If QEMU is alive but paused (for example after a
remote-disk I/O error), `StartLightVM` returns `FailedPrecondition` with
“LightVM is paused by an I/O error; stop and start to recover” instead of
`AlreadyExists`. The UI shows the paused state and the same guidance.

The LightVM argument builder appends `buildDiskArgs` output for every disk after
the conditional memory-backend block and before `-display none` and the console
devices. The existing `vhost-user-fs-pci` rootfs device remains present because
`image` is still required. The builder preserves `lightVMAppend`'s existing
`APP_HOSTNAME` suffix behavior and never rewrites `root=`.

### UI changes

The LightVM form gains a disk picker that behaves like the VM form: add from the
unattached disk list, show selected disks in order, remove, and submit
`disk_ids`. The picker hides templates, every disk with a non-empty
`DeletionState` (`provisioning`, `deleting`, or `delete_failed`), disks attached
to a VM, and disks attached to another LightVM. On edit, the dialog initializes
its selection from the LightVM response's `disks` field and sends
`replace_disk_ids: true`. Selection order is visible because it determines QEMU
`-drive` order, not guest `/dev/vdX` names.

Shared UI helpers cover attachment state, owner kind and name, and availability
to a workload. Availability is false for templates, any non-empty deletion
state, a different VM owner, or a different LightVM owner; it is true only for
an unattached disk or the same workload. The LightVM table gains a disk column.
Its disk-event listeners consume `event.detail` from `rookery:disks-changed` and
`rookery:disks-data` without refetching or dispatching. Only the save/delete
handler reloads `/api/disks` and dispatches both events with the fresh list.

The VM form and the Disks tab must stop treating `attached_vm_id` as the only
ownership field. A disk attached to a LightVM must be hidden from the VM disk
picker and must block deletion, template changes, and provisioning changes with
a message naming the LightVM. The Disks tab shows the owning VM or LightVM name.
An edit that would grow a grandfathered LightVM above its current device count
is rejected by the API; the form displays the disk and interface totals from the
returned error rather than trying to compute the limit locally.

The generated UI bundle under `internal/webui/static/build` is rebuilt from the
TypeScript sources; it is not edited by hand.

### Compatibility and rollout

Migration `0053_workload_disks.sql` implements the ownership table described
above: copy `vm_disks`, renumber positions, backfill a legacy pointer only for
a VM with no `vm_disks` rows, ignore the pointer when `vm_disks` has rows, set
`vms.disk_id` to NULL, and drop `vm_disks`. No triggers are created; owner
deletion is a foreign-key cascade and the disk foreign key is restrict. The
legacy column is inert. Existing VM attachments remain, and the API derives
`disk_id`, `disk`, `disk_ids`, and `disks` from `workload_disks`. Conflicting,
ignored, and dangling pointers are logged, with the pre-upgrade query in
`docs/operations.md` for inspection.

Proto changes are additive. Older clients that do not send `disk_ids` keep the
current LightVM behavior. Older clients that treat `attached_vm_id` as the only
ownership signal will show LightVM-owned disks as unattached; they must read
`attached_lightvm_id`/`attached_lightvm_name` or upgrade. The database migration
is one-way, so a code rollback requires restoring a database backup taken
before the upgrade.

### Security and failure handling

Remote disk credentials remain runtime-only: the secret contents live in a 0600
file under the QEMU run directory and are never written to SQLite or QEMU
argument text. QEMU receives the secret-file path and a secret object, not the
credential value. `internal/qemu` owns the secret lifecycle. LightVM secret
files follow the `lightvm-<uuid>.disk<id>.<protocol>.secret` naming pattern. The
plan-aware start
runs stale console/pid/monitor cleanup before preparing secrets, defers secret
removal, and removes partial secret files if preparation fails. At QEMU
manager construction, `cleanupStaleSecrets` removes `.secret` files older than
one hour, covering both VM and LightVM prefixes. `StopLightVM` and `DeleteLightVM`
call `cleanupSecrets` after QEMU is confirmed stopped, so a crashed start’s
credentials are removed by the next stop or delete, not only by a server
restart. Local host devices are revalidated at start through the VM disk
validation path, so a device mounted after inventory registration is not handed
to a LightVM.

The gateway or API autostart prepares the image and builds the plan while
holding `startCoordinator` but not `mu`; the exact preparation/planning failure guard
is in Start path. A virtiofsd or QEMU failure stops virtiofsd, removes the
container, returns the error, and leaves secret removal to QEMU's deferred
cleanup; the gateway start path sets status `error` and restores a previous
`stopped` desired state, while API autostart leaves the desired state unchanged
and writes only `status=error`. The gateway maps
`kouja.ErrSessionActive` to `Unavailable` through a LightVM-specific helper so
recoverable storage errors remain retryable. API autostart logs the error and
does not return a gRPC status. `MarkDiskDeleting` uses only a conditional update
that refuses any attached disk; it does not perform a separate ownership
pre-check. `DeleteDisk` and the background reclaimer refuse to touch a disk
that has any `workload_disks` row. Synchronous disk deletion holds
`resourceMu` from the ownership check through local volume removal and row
deletion; the foreign key is a backstop for the row, not for a thin volume that
has already been removed.

## Alternatives Considered

### Alternative A: Separate `lightvm_disks` table

*   **Pros**: Mirrors `vm_disks`, gives LightVM attachments their own foreign
    keys, and leaves the existing VM table untouched.
*   **Cons**: Two attachment tables require every ownership path to check both.
    A trigger or application check is needed to enforce cross-table uniqueness,
    and the disk inventory needs a union or two joins. It does, however, leave
    `vm_disks` and all VM disk code untouched.
*   **Reason for Rejection**: The one-table ownership model is a product
    decision recorded for this design: one ownership namespace and one
    `UNIQUE (disk_id)`. The chosen Alternative I accepts a larger one-way
    migration (copy `vm_disks`, backfill the legacy pointer, rewrite the VM
    loader) in exchange for fewer ownership paths. Alternative A would avoid
    that migration but add a cross-table uniqueness mechanism and duplicate
    ownership code.

### Alternative B: Disk IDs in JSON columns on `lightvms`

*   **Pros**: Smallest schema change. No migration of `vm_disks` and no join
    table.
*   **Cons**: No referential integrity and no uniqueness. Attachment conflicts
    can only be found by scanning every LightVM row, and the disk inventory
    cannot reliably report LightVM ownership.
*   **Reason for Rejection**: It breaks the existing disk ownership model and
    makes the single-owner rule an application-wide search problem.

### Alternative C: Explicit boot flag on each disk

*   **Pros**: The API can distinguish data disks from a root disk, the UI can
    label the intended root, and validation could require a boot disk when the
    container image is absent.
*   **Cons**: The guest already receives its root device through `append`.
    Adding a flag duplicates that contract, creates two fields that can
    disagree, and still cannot guarantee how a custom initramfs uses the flag.
*   **Reason for Rejection**: This design uses a declarative model where
    `append` is the only boot contract and the container image stays required.
    A flag cannot guarantee how a custom initramfs uses the root device.

### Alternative D: Rookery injects `root=/dev/vda`

*   **Pros**: A disk-backed LightVM could start without the operator editing
    `append`.
*   **Cons**: It does not match PARTUUID/LABEL roots, a different guest device
    order, or initramfs images that ignore `root=`.
*   **Reason for Rejection**: Automatic rewriting hides the boot contract and
    makes failures harder to diagnose. The caller sets `append`.

### Alternative E: Separate `root` field

*   **Pros**: The root device is explicit and does not need to be parsed out of
    a command line.
*   **Cons**: It duplicates kernel command-line semantics, adds API and UI
    surface, and can conflict with a `root=` already present in `append`.
*   **Reason for Rejection**: It solves a problem the kernel command line
    already solves and creates a second source of truth.

### Alternative F: Local disks only

*   **Pros**: LightVM start would not need the storage planner or the temporary
    credential-file path, so the change stays small.
*   **Cons**: Kouja disks could not be used, even though they are supported
    disks for VMs, and the LightVM form would have to explain a storage-source
    limitation.
*   **Reason for Rejection**: This design requires parity with every disk
    source that VMs support.

### Alternative G: Defer remote disks to a follow-up

*   **Pros**: The first implementation could ship with local LVM and host
    devices only.
*   **Cons**: The API and data model would advertise a limit that has to be
    enforced in several places, and adding remote support later would change
    the start path a second time.
*   **Reason for Rejection**: Building the plan-aware start now avoids a second
    migration of the runtime interface and keeps behavior consistent.

### Alternative H: Separate `PUT /api/lightvm/instances/{id}/disks` endpoint

*   **Pros**: Disk changes are isolated from the general LightVM update and
    visible in one request.
*   **Cons**: Creating a LightVM with disks would take two calls, leaving a
    window where the definition exists without its requested disks. It also
    diverges from how VM create and update already carry `disk_ids`.
*   **Reason for Rejection**: The existing create/update shape already handles
    ordered disk IDs and replacement semantics without an invalid intermediate
    state.

### Alternative I: Nullable owner foreign keys in one table (chosen)

*   **Pros**: A single table with nullable `vm_id` and `lightvm_id` foreign
    keys, a check that exactly one is set, and `UNIQUE (disk_id)` gives real
    referential integrity and cascaded deletes without triggers or a
    polymorphic ID.
*   **Cons**: It adds two nullable owner columns and a check constraint. Every
    new workload kind requires a schema change, and queries must join two
    owner columns.
*   **Reason for Selection**: It satisfies the one-table requirement and
    provides the ownership foreign keys and cascaded deletes that the
    polymorphic table only approximates with triggers. The two-owner shape is
    acceptable because Rookery has exactly two workload kinds; a third kind
    would be a deliberate schema change, not a hidden trigger update. The
    migration preserves `vm_disks` rows and skips conflicting legacy pointers.

### Alternative J: Compatibility view or dual-write during rollout

*   **Pros**: A `vm_disks` view over `workload_disks`, or one release of
    dual-write, would let older queries and binaries read the old shape during
    rollout.
*   **Cons**: SQLite views are read-only unless paired with `INSTEAD OF`
    triggers. Dual-write adds a second write path and a second source of truth
    that can drift.
*   **Reason for Rejection**: The API and code deploy together, and the
    migration backfills legacy pointers before dropping `vm_disks`. A view or
    dual-write keeps the old ownership shape alive without removing the
    migration risk. A pre-upgrade database backup is the rollback path.

### Alternative K: `workloads` supertype

*   **Pros**: A `workloads(id, kind)` table referenced by `vms.id` and
    `lightvms.id`, with `workload_disks.workload_id REFERENCES
    workloads(id) ON DELETE CASCADE`, gives a uniform owner foreign key, a
    global ID namespace, and cascaded deletes.
*   **Cons**: It adds a table and backfills every VM and LightVM row. It also
    gives ownership a third representation (kind plus ID) in addition to the
    two workload tables. The runtime still needs kind-specific process and
    artifact names.
*   **Reason for Rejection**: The chosen nullable-owner table gets the same
    foreign keys and cascade behavior with one fewer table and no backfill.
    Runtime artifacts are namespaced by kind, so a shared UUID cannot
    cross-stop a workload. A supertype is the better choice only if a third
    workload kind is added.

### Alternative L: `virtio-scsi` with one LUN per disk

*   **Pros**: One PCI slot can carry many disks, so the disk portion of the
    24-device budget and its grandfathering rule could be removed. Interfaces
    still consume one slot each. It is the standard QEMU transport for high
    disk counts and supports hotplug.
*   **Cons**: It changes the disk argument builder for both VMs and LightVMs,
    requires guest `virtio-scsi` support, adds LUN/ordering semantics, and is
    not the existing VM path. Throttle options still apply per drive, but the
    controller and device sequence is different.
*   **Reason for Rejection**: The design keeps `virtio-blk` parity with VMs and
    reuses `buildDiskArgs`. The 24-device cap only constrains high-count
    LightVMs, and the grandfather rule preserves existing definitions. A
    `virtio-scsi` transport is a viable future change if high disk counts
    become common.

### Decision

The design uses **Alternative I**: one `workload_disks` table with nullable
`vm_id` and `lightvm_id` foreign keys, exactly one owner per row,
`UNIQUE (disk_id)`, and cascaded owner deletes. It satisfies the one-table
requirement, removes the polymorphic owner ID, requires no owner/delete
triggers, and keeps the migration bounded to copying `vm_disks` and backfilling
the legacy pointer. Alternative A is rejected because it violates the one-table
requirement; Alternative K is rejected because it adds a supertype without a
third workload kind; Alternative L is rejected because it changes the VM disk
transport for a capacity limit that only affects high-count LightVMs. This
decision is final for this design; there is no fallback list left to the
implementer.

--------------------------------------------------------------------------------

## Risks & Mitigations

| Risk | Severity | Trigger | Impact | Mitigation | Verification |
| --- | --- | --- | --- | --- | --- |
| Legacy `vms.disk_id` conflicts with an existing attachment | 🔴 Blocker | Pre-0053 database has a non-null `vms.disk_id` naming a disk already owned by another VM, a VM with `vm_disks` rows has a different legacy pointer, or the pointer names a missing disk | Blind backfill would either abort the upgrade, reassign a disk, add an unrequested attachment, or hit a dangling foreign key | `vm_disks` is authoritative for a VM with rows; backfill only pointer-only VMs with an existing, unattached disk; log skipped/ignored/missing pointers; pre-upgrade query in `docs/operations.md` | Migration tests for pointer-only, rows-plus-stale-pointer, dangling pointer, non-first pointer, duplicate positions, and a skipped conflict |
| A skipped legacy pointer leaves a VM diskless | 🟡 Major | Conflicting/dangling pointer is skipped and `vm_disks` has no row for that VM | `StartVM` fails with “vm has no disk”; operator has no obvious repair | StartVM already returns `Aborted` “vm has no disk”; docs show the pre-upgrade query and instruct editing the VM to attach a replacement disk; migration test asserts nil disk fields | Diskless migration fixture and StartVM error test |
| Reclaimer or delete path destroys a LightVM-owned disk | 🔴 Blocker | Disk enters `deleting` state while a LightVM owns it | Data loss under a running guest | `MarkDiskDeleting` uses a conditional `NOT EXISTS` update; `DeleteDisk` holds `resourceMu` through local volume removal and uses `DiskAttachedToAnyWorkload`; foreign key is a backstop | Store, gateway, and reclaimer tests with a LightVM-owned disk and a concurrent attach |
| Template, provisioning, or delete path rejects an unattached template | 🔴 Blocker | Ownership check reused as the full availability check | Existing VM template lifecycle breaks, or unmark leaves dormant provisioning settings | Use ownership only for unmark/delete; attach-availability when marking; ownership plus not-deleting for provisioning; unmark clears template-only settings | Regression tests for unmark, provisioning update, template delete, and unmark cleanup |
| Attach candidate is provisioning, deleting, or delete_failed | 🔴 Blocker | Attach helper rejects only some non-empty deletion states | Guest receives a disk whose volume is being reclaimed | Reject any non-empty `DeletionState`; map to `Aborted` | Store and gateway tests for all three states |
| Create validation deadlocks on the single connection | 🔴 Blocker | Availability check or FK re-read runs inside an open transaction | Create request hangs until context cancellation | Validate before `BeginTx`; on an attachment FK violation, roll back explicitly before re-reading the disk | Create and update tests with a store timeout |
| Two concurrent writes claim one disk | 🔴 Blocker | LightVM and VM write paths use different mutexes | Double attachment or `Internal` error | LightVM create takes `resourceMu`; update takes a reservation, then `resourceMu`, then `mu`; unique violations map to wrapped `ErrDiskInUse` | Concurrency test for VM/LightVM create and disk delete |
| Image prep blocks unrelated disk/network writes | 🟡 Major | Slow registry while `resourceMu` or `mu` is held | Disk, network, interface, storage-backend, VM-create, or LightVM-create writes stall for the pull duration | Two-phase start: image preparation under `startCoordinator` only; `mu` is taken only for short intent/status transitions and the final start; global preparation limit 2 | Test that disk/network/VM-create writes proceed during a slow start |
| Many LightVMs prepare at once | 🟡 Major | A burst of starts for different LightVMs | Unbounded buildah/registry/disk/CPU load | `StartCoordinator` global preparation semaphore (default 2); autostart sequential | Two-LightVM blocking-prepare test and load test |
| Start overwrites a concurrent stop/update during image preparation | 🟡 Major | Stop or update completes while the start is pulling an image | Start later marks `error`/`running` over the newer intent | `startCoordinator` serializes starts for the same LightVM and `Cancel` aborts the pull; `desired_state=running` is set before preparation; `lifecycle_generation` is rechecked under `mu` before every status write; a QEMU-stopped generation mismatch aborts, and a QEMU-running branch aborts whenever the intent is `stopped` | Tests for stop, update, and delete during a blocking prepare and while queued |
| Stop/update waits for a full image-pull timeout | 🟡 Major | A stop arrives while `PrepareLightVM` is pulling an image | UI says stopped but preparation continues and the next start waits | `startCoordinator.Cancel(id)` on stop/update/delete cancels the start context; cleanup uses a fresh bounded context | Blocking-prepare cancel test and startCoordinator release test |
| Stop cleanup races a new start | 🟡 Major | A new start acquires the slot while stop cleanup is removing the container | New start’s rootfs is removed by the old stop | `StartCoordinator.Reserve(id)` returns a refcounted release; `Begin` waits through QEMU stop, row write, and cleanup; deferred release after cleanup | Stop-cleanup-racing-restart test |
| Stop cleanup failure leaves stale virtiofsd/container resources | 🟡 Major | QEMU stops but virtiofsd/container cleanup returns an error | Resources remain, but the LightVM must not restart | Persist `desired_state=stopped` and `status=stopped` under `mu` before cleanup; return the cleanup error so stop/delete can be retried; cleanup outside `mu` cannot change intent | Fake runtime stop-failure test and delete-cleanup-failure retry test |
| Runtime writes status | 🟡 Major | Runtime gains a store-backed status write | Duplicated or racing status writes | Gateway and API autostart own status; runtime remains store-free | Lifecycle and status-ownership tests |
| Boot root mismatches the guest device | 🟡 Major | `root=/dev/vdX` depends on guest enumeration | Guest initramfs cannot find the intended root | Document that attachment order controls QEMU `-drive` order only; guest names are not guaranteed; recommend `PARTUUID=`/`LABEL=`; preserve caller `root=` | Argument test and append-preservation check |
| Secret file survives a failed start or stop | 🟡 Major | QEMU failure after secret creation, or a stopped/deleted LightVM with stale secrets | CHAP credential remains on disk | `internal/qemu` defers `removeSecretFiles`; `prepareDiskSecrets` removes partial results; `StopLightVM`/delete call `cleanupSecrets`; stale sweep covers both prefixes | Success, partial-prepare, start-failure, stop, and delete cleanup tests |
| Orphaned `workload_disks` row blocks a disk | 🟡 Major | Manual SQL inserts a row with a nonexistent owner while `foreign_keys` is off | Disk appears unattached but cannot be attached or deleted | `Open` sets `PRAGMA foreign_keys=ON`; the exact-owner `CHECK` rejects both/neither; `UNIQUE (disk_id)` prevents double ownership; repair by deleting the orphan row | Check-constraint, cascade, and FK-enforcement tests |
| Global workload ID collision | 🟡 Major | A VM and LightVM have the same UUID | Wrong process stopped or wrong secret cleaned up | LightVM artifacts use `lightvm-<uuid>`; process matching includes the LightVM pidfile; VM matching excludes LightVM pidfiles | Runtime process-matching test with a shared UUID |
| Migration cannot be rolled back in place | 🟡 Major | Code rollback after 0053 | Old binary cannot read the new schema | Take a database backup before upgrade; document restore path | Restore rehearsal from backup |
| Image/buildah/virtiofsd outage blocks LightVM start | 🟡 Major | Registry or runtime tool unavailable | LightVM cannot start even though the disk path is local | Keep image required by decision; document dependency; future improvement to make image optional | Manual outage check and operations documentation |
| PCI/device capacity exhausted at high counts | 🟡 Major | Disks plus interfaces exceed the `pc-i440fx` headroom on the supported `linux/amd64` platform | QEMU start fails | 24 is a conservative policy cap checked by a non-skippable argument/slot-arithmetic test and a QEMU/KVM-gated 16-disk/8-interface start probe; grandfathered definitions above 24 may be edited but Start returns `FailedPrecondition` until reduced | Slot arithmetic test, gated integration probe, start-time `FailedPrecondition`, grandfather, and boundary tests |
| Older clients misread LightVM-owned disks as unattached | 🟡 Major | Client reads only `attached_vm_id` | Client offers a disk that the API will reject | Document read limitation; new fields; provider update | API compatibility test |
| Empty `disk_ids` is misinterpreted (empty + true read as preserve, or empty + false read as clear) | 🟡 Major | Client omits `replace_disk_ids` with an empty list | Attachments stranded or silently removed | Store derives the effective replacement flag as “`ReplaceDiskIDs` is true or `DiskIDs` is non-empty”; truth table: non-empty replaces, true + empty clears, false/omitted + empty preserves | Store and gateway tests for all four combinations |
| Throttle class unresolved starts unthrottled | 🟡 Major | Loader omits class ID or class row is missing | I/O limits silently lost | Loader always selects class ID and sets `ThrottleError`; `buildDiskArgs` fails | Store and QEMU start tests |
| Remote disk I/O error pauses a LightVM with no resume path | 🟡 Major | Kouja iSCSI drive returns an I/O error; QEMU uses `werror=stop,rerror=stop` | LightVM is paused; only stop/start recovers | Expose `run_state` (`paused` or `paused (io-error)`), return `FailedPrecondition` with stop/start guidance from Start, and show the paused state in the UI; future resume action | Paused-QEMU test, start-guidance test, and operations note |

--------------------------------------------------------------------------------

## Non-Functional Requirements

*   **Security**: See Risks & Mitigations for secret cleanup and host-device
    revalidation. The change adds no authentication or authorization surface;
    the existing local and mTLS listener rules are unchanged (see
    [architecture](../architecture.md)).
*   **Performance**: Disk lookup adds a join to `workload_disks`; throttle
    resolution adds a join to `disk_throttle_classes`. The loader performs one
    ordered disk query per list or get call, not one query per disk. Image pull
    and container preparation run under `startCoordinator`, not `mu` or
    `resourceMu`; disk, network, interface, storage-backend, VM-create, and
    LightVM-create writes continue during a slow pull. Only a second start of
    the same LightVM waits on `startCoordinator`; gateway starts for different
    LightVMs prepare concurrently up to the global preparation limit (default
    2), while API autostart is sequential to bound startup load. A stop/update/delete cancels the in-flight preparation
    context, so it does not wait for the image-pull timeout. VM and
    LightVM start/stop still serialize on `mu` for short intent/status
    transitions and the final runtime start; a LightVM update may wait
    for a concurrent QEMU stop, bounded by the QEMU stop timeout, not by the
    image pull. Virtiofsd/container cleanup runs outside `mu` with a fresh
    bounded context.
*   **Rollout and rollback**: See Compatibility and rollout. Take a database
    backup before the upgrade.
*   **Compatibility**: See Compatibility and rollout for the migration and
    older-client read limitation. A regression in VM disk ownership is a
    release blocker.

--------------------------------------------------------------------------------

## Additional Resources

*   [Kouja storage backend](kouja-storage-backend.md) describes the remote
    iSCSI attachment and credential model reused by the LightVM start planner.
*   [Disk throttle classes](disk-throttle-classes.md) describes the
    throttle-class model and the QEMU option builder that LightVM disks
    inherit.
*   [API reference](../api.md), [operations guide](../operations.md),
    [architecture](../architecture.md), [README](../../README.md), and
    [AGENTS.md](../../AGENTS.md) are the user-facing and repository references
    for this design.

--------------------------------------------------------------------------------

## Future Improvements

*   **Disk hotplug**: Attach or detach a disk on a running LightVM through QEMU
    blockdev commands. This design deliberately requires a stop because the
    existing lifecycle and ownership checks assume a static disk set.
*   **Guest-side disk contract**: A guest helper or documented initramfs
    convention for discovering, formatting, and mounting attached disks would
    remove the need for each guest image to implement it.
*   **Boot metadata**: If a disk-root mode is added, an optional root hint such
    as a PARTUUID or LABEL selector could make it easier without rewriting
    `append`.
*   **Terraform LightVM resource**: The provider can report disk ownership but
    does not manage LightVM instances; adding that resource would let Terraform
    manage LightVM definitions.
*   **Image-optional disk-root guests**: Make `image` optional when the guest
    initramfs mounts an attached disk as root, removing the registry/buildah/
    virtiofsd dependency for that mode.
*   **Preparation cache**: Reuse a prepared container rootfs across repeated
    starts of the same image to shorten start latency. Containers remain
    ephemeral per run for isolation.
*   **Workload supertype**: If a third workload kind is added, introduce a
    `workloads(id, kind)` table so ownership has one foreign key and one global
    ID namespace instead of adding a third nullable owner column.
*   **LightVM resume action**: Add `/api/lightvm/instances/{id}/resume`,
    matching VM resume, so a paused LightVM can recover without a stop/start.
    `run_state` is already exposed by this design.

--------------------------------------------------------------------------------

## Detailed Implementation Plan

### Implementation Phases

1.  **Store and deletion-safety phase**: land `0053_workload_disks.sql`, the
    store changes, the migration tests, the ownership tests, and the
    reclaimer/delete guards from item 12 together, including the gateway
    `DeleteDisk` `DiskAttachedToAnyWorkload` pre-check and the `resourceMu`
    LVM-removal guard. The migration and store queries must not be split, and
    phase 1 is not deployable without those guards, or a LightVM-owned deleting
    disk or thin volume can be reclaimed. Include a phase-1 LightVM-owned LVM
    delete test in `disk_writes_test.go`: the test holds the shared disk mutex,
    creates a LightVM and its `workload_disks` row through the store APIs in
    that critical section, then issues `DELETE /api/disks/{id}` from another
    goroutine; assert the delete waits for the mutex and then aborts without
    removing the volume. Gate:
    `go build ./...` and
    `go test ./internal/store/... ./internal/storage/... ./internal/gateway/...`.
2.  **Runtime phase (items 20–26)**: update `vmstart`, `qemu`, and `lightvm`,
    with the planner, two-phase, and secret tests. Keep deprecated
    `QEMU.StartLightVM`, `QEMU.StartLightVMWithVirtioFS`, and
    `Runtime.StartLightVM` wrappers that call the new two-phase path with an
    empty plan; phase 3 removes them when the gateway/API interfaces change.
    Gate: `go build ./...` and
    `go test ./internal/vmstart/... ./internal/qemu/... ./internal/lightvm/...`.
3.  **API, gateway, and wiring phase (items 7–11, 13–19, 27, and the
    gateway/import remainder of item 12)**: update the proto files, regenerate,
    change the gateway and API interfaces, remove the deprecated phase-2
    wrappers, wire the planner, mutex, and shared start coordinator, add the
    disk-deletion error/import mappings, and update the lifecycle tests. Gate:
    `make proto-check`, `go build ./...`, and
    `go test ./internal/gateway/... ./internal/api/... ./internal/lightvm/...
    ./cmd/rookery/...`.
4.  **UI phase**: update types, predicates, dialogs, tabs, and rebuild the
    embedded bundle. Gate: `make ui-build`.
5.  **Provider and documentation phase**: update the Terraform disk fields and
    the API, operations, architecture, README, and `AGENTS.md` documents. Gate:
    `go test ./cmd/terraform-provider-rookery/...` and `make build`.

### Database and store

#### 1. `internal/store/migrations/0053_workload_disks.sql` (Create)

*   **Type / Scope**: SQLite schema migration.
*   **Proposed Changes**: Create `workload_disks` exactly as follows:

    ```sql
    CREATE TABLE workload_disks (
        vm_id      TEXT REFERENCES vms(id) ON DELETE CASCADE,
        lightvm_id TEXT REFERENCES lightvms(id) ON DELETE CASCADE,
        disk_id    INTEGER NOT NULL REFERENCES disks(id) ON DELETE RESTRICT,
        position   INTEGER NOT NULL CHECK (position >= 0),
        CHECK ((vm_id IS NOT NULL) <> (lightvm_id IS NOT NULL)),
        UNIQUE (disk_id),
        UNIQUE (vm_id, position),
        UNIQUE (lightvm_id, position)
    );
    ```

    Copy `vm_disks` with deterministic positions:
    `ROW_NUMBER() OVER (PARTITION BY vm_id ORDER BY position, disk_id) - 1`.
    Backfill each non-null `vms.disk_id` only when the VM has no `vm_disks`
    rows and the disk is not already in `workload_disks`:

    ```sql
    INSERT INTO workload_disks (vm_id, lightvm_id, disk_id, position)
    SELECT v.id, NULL, v.disk_id,
           COALESCE((SELECT MAX(position) + 1
                     FROM workload_disks wd WHERE wd.vm_id = v.id), 0)
    FROM vms v
    WHERE v.disk_id IS NOT NULL
      AND EXISTS (SELECT 1 FROM disks d WHERE d.id = v.disk_id)
      AND NOT EXISTS (SELECT 1 FROM vm_disks vd WHERE vd.vm_id = v.id)
      AND NOT EXISTS (SELECT 1 FROM workload_disks wd
                      WHERE wd.disk_id = v.disk_id);
    ```

    Add `ALTER TABLE lightvms ADD COLUMN lifecycle_generation INTEGER NOT NULL DEFAULT 0`.
    Then `UPDATE vms SET disk_id = NULL` and `DROP TABLE vm_disks`. Create no
    triggers. Leave `vms.disk_id` in place, NULL and inert. Add a
    `reportLegacyDiskConflicts(ctx, db)` call in `store.Open` before `migrate`
    when the schema is below version 53 and both `vm_disks` and `vms.disk_id`
    exist; it logs pointers ignored because the VM has `vm_disks` rows,
    pointers skipped because the disk is owned elsewhere, and pointers skipped
    because the referenced disk row is missing. Add the pre-upgrade query to
    `docs/operations.md` in this phase (item 43 keeps the final docs wording),
    and use that exact query against `vms`/`vm_disks`/`disks`, including
    `disk_name`; do not query `workload_disks`, which migration 0053 has not
    created yet. Log
    `vm_id`, `vm_name`, `disk_id`, `disk_name` (when present), and the reason.
    The migration itself never aborts on a conflict or dangling pointer. Use
    the package-level `zerolog/log` logger; `store.Open` keeps its current
    signature and the helper reads `PRAGMA user_version`, `sqlite_master` for
    `vm_disks`, and `pragma_table_info('vms')` for `disk_id` before running.
*   **Rationale**: `workload_disks` is the only ownership record. The unique
    disk constraint is the database-level single-owner guarantee; the owner
    foreign keys and cascades enforce owner integrity without triggers. The
    legacy column stays only because dropping a column with a column-level
    `UNIQUE` constraint and foreign key would require a full `vms` table
    rebuild. `lifecycle_generation` gives start a monotonic token that detects
    any concurrent stop, update, or lifecycle write during image preparation.

#### 2. `internal/store/store.go` (Modify)

*   **Type / Scope**: Store model, queries, and transaction helpers.
*   **Proposed Changes**:
    *   Add `AttachedLightVMID` and `AttachedLightVMName` to `Disk`.
    *   Add `MaxWorkloadDisks = 32`, `MaxLightVMDevices = 24`,
      `ErrInvalidDiskIDs`, `ErrLightVMDeviceLimit`, and a pointer-returned
      `*DiskInUseError{OwnerKind, OwnerID, OwnerName}` whose pointer
      `Error()`/`Unwrap()` methods unwrap to `ErrDiskInUse`. Gateway code uses
      `errors.As` with this pointer type and tests owner extraction.
    *   Add `DiskAttachedToAnyWorkload(disk Disk) bool`,
      `checkDiskOwnership(ctx context.Context, disk Disk, ownerKind, ownerID string) error`,
      `checkDiskNotDeleting(disk Disk) error`, and
      `checkDiskAvailableForAttach(ctx context.Context, diskID int64, ownerKind, ownerID string) error`.
      An empty `ownerKind`/`ownerID` in `checkDiskOwnership` means “reject any
      owner.” `checkDiskAvailableForAttach` calls the ownership check, the
      not-deleting check, and rejects templates.
    *   Replace the existing `checkDiskAvailable` with
      `checkDiskAvailableForAttach`. Use it from `CreateVM`, `SetVMDisks`,
      `CreateLightVM`, `UpdateLightVM`, and `SetDiskTemplate` when marking a
      template. For `SetDiskProvisioning`, use ownership plus not-deleting
      (the disk is already a template). Use ownership only when unmarking a
      template and from `DeleteDisk`; when unmarking, clear
      `provisioning_script` and `provisioning_timeout` in the same update so a
      non-template disk does not retain dormant template settings. Do not use a
      pre-check from `MarkDiskDeleting`; that method is one conditional update.
    *   Change `CreateVM` to insert `vms` without `disk_id` and insert
      `workload_disks` rows with `vm_id` set and `lightvm_id` NULL. Change
      `SetVMDisks` to delete the existing VM rows and insert the new set; keep
      `UPDATE vms SET updated_at = ?` and remove the `disk_id` assignment.
      On an attachment unique violation, call `tx.Rollback()` explicitly, then
      load the existing attachment through `s.GetDisk` and return
      `DiskInUseError`. On an attachment foreign-key violation, do the same
      rollback/re-read: a missing disk returns `ErrDiskNotFound`, and any other
      failure returns `DiskInUseError`. Alternatively, query the owner through
      `tx` before rollback. Never call an `s.db` helper while the transaction
      remains open.
    *   Remove the `vms.disk_id` join and scan from `vmSelect`/`scanVM`. Change
      `loadVMDisks` to derive `DiskID`, `Disk`, `DiskIDs`, and `Disks` only from
      `workload_disks` ordered by `position`. The existing `vmDisks(vm)` helper
      in `internal/qemu`/`internal/vmstart` returns `vm.Disks` when non-empty
      and otherwise `vm.Disk`; keep it for legacy VMs that predate the loader
      rewrite and for tests. New VMs always populate `Disks`.
    *   Add `loadWorkloadDisks(ctx, ownerKind string, ownerIDs []string)` that
      both `loadVMDisks` and `loadLightVMDisks` call. It filters
      `wd.vm_id IN (...)` for `vm` or `wd.lightvm_id IN (...)` for `lightvm`,
      orders by `wd.position, d.name COLLATE NOCASE`, resolves the LVM
      `devicePath`, normalizes an empty protocol to `local`,
      fills the storage-backend name, joins `disk_throttle_classes`, and calls
      `applyVMDiskThrottle` so `ThrottleError` is populated.
    *   Change `diskSelect` to `LEFT JOIN workload_disks wd ON wd.disk_id = d.id`,
      then `LEFT JOIN vms v ON v.id = wd.vm_id`, then
      `LEFT JOIN lightvms lv ON lv.id = wd.lightvm_id`. Scan
      `AttachedVMID`/`AttachedLightVMID` from `wd.vm_id`/`wd.lightvm_id`, and
      use the joins only for owner names. This keeps orphan attachments
      visible to `DiskAttachedToAnyWorkload` even if FK enforcement was off.
    *   Remove the `vms.disk_id` branch from `classifyVMCreateError`. A
      `workload_disks` unique violation is caught in the attachment insert
      loop, not the `vms` insert classifier.
    *   Generalize `ErrDiskInUse` and `ErrDiskIsTemplate` messages to workload
      wording.
*   **Rationale**: The store enforces the invariant. `workload_disks` is the
    only ownership record; `vms.disk_id` is inert. One shared loader preserves
    device-path, backend, and throttle behavior for both workload types. The
    explicit rollback avoids the single-connection deadlock on FK re-read.

#### 3. `internal/store/lightvms.go` (Modify)

*   **Type / Scope**: LightVM store model and CRUD.
*   **Proposed Changes**:
    *   Add `DiskIDs []int64`, `Disks []VMDisk`, and
      `LifecycleGeneration int64` to `LightVM`. Add
      `l.lifecycle_generation` to `lightVMSelect` and `scanLightVM`.
    *   Add `ReplaceDiskIDs bool` with the struct tag `json:"-"`.
    *   Add pure `normalizeLightVMDiskIDs(ids []int64) ([]int64, error)`. It
      preserves order, deduplicates, rejects non-positive IDs with
      `ErrInvalidDiskIDs`, enforces `MaxWorkloadDisks` with
      `ErrInvalidDiskIDs`, and permits an empty result. It performs no store
      access.
    *   Add
      `validateLightVMDeviceBudget(existingDisks, existingInterfaces, newDisks, newInterfaces int, create bool) error`.
      The exact rule is:
        *   `create`: reject when `newDisks+newInterfaces > MaxLightVMDevices`.
        *   `update` with `existingDisks+existingInterfaces > MaxLightVMDevices`:
            reject when `newDisks+newInterfaces` exceeds the existing total;
            otherwise allow.
        *   `update` with an existing total at most `MaxLightVMDevices`:
            reject when `newDisks+newInterfaces > MaxLightVMDevices`.
      Return `ErrLightVMDeviceLimit` with both totals in the wrapped message.
    *   Load attachments through `loadLightVMDisks`, which calls
      `loadWorkloadDisks(ctx, "lightvm", ids)`. `ListLightVMs` and `GetLightVM`
      call it alongside the interface and console loaders.
    *   In `CreateLightVM`, normalize disk IDs and interface IDs, validate
      availability, and validate the budget with zero existing devices before
      `BeginTx`. Insert the LightVM row and `workload_disks` rows with
      `lightvm_id` set in the create transaction.
    *   In `UpdateLightVM`, load the existing LightVM (`s.GetLightVM`) before
      `BeginTx` for its disk and interface counts. Normalize disk IDs. When
      replacing, validate availability for the replacement set; when preserving
      (`replace` false), validate ownership only for existing attachments and
      do not re-run attach-availability, so a rename with a deleting/template
      disk still succeeds. Compute `replace := vm.ReplaceDiskIDs || len(vm.DiskIDs) > 0`, compute
      `newDisks` from the replacement list or the existing count, and validate
      the budget with the existing counts and the normalized request interface
      count. In the transaction, when `replace` is true, delete
      `workload_disks WHERE lightvm_id = ?` and insert the new ordered set;
      when false, perform no attachment delete or insert. Never call a helper
      that uses `s.db` while the transaction is open.
    *   Increment `lifecycle_generation` in `SetLightVMStatus`,
      `SetLightVMDesiredState`, and the `UpdateLightVM` row update. The
      `UpdateLightVM` SET list also writes `desired_state='stopped'`, because
      updates are allowed only while stopped; this clears a canceled start’s
      `running` intent. A start captures the generation immediately after
      setting its desired-state intent and requires it unchanged before starting
      QEMU. Status writes on failure also require an unchanged generation; the
      QEMU-running repair branch is governed by the desired-state fence because
      an earlier successful start legitimately advances the generation.
    *   On attachment insert in both LightVM create/update: a unique violation
      explicitly `tx.Rollback()` then loads the existing attachment through
      `s.GetDisk` and returns `DiskInUseError`; a foreign-key violation does the
      same (missing disk → `ErrDiskNotFound`, otherwise `DiskInUseError`).
      Alternatively, query the owner through `tx` before rollback. Never call an
      `s.db` helper while the transaction is open. Add timeout tests for both
      paths.
    *   Rely on the `lightvms` foreign-key cascade to release attachments. No
      new owner or delete triggers are created for this feature; the existing
      `entity_labels_lightvm_delete` trigger is unrelated.
*   **Rationale**: Validation must run before the transaction to avoid the
    single-connection deadlock. Delete-before-insert avoids transient
    unique-position conflicts. The pure normalizer, explicit preserve/grow
    budget rule, and foreign-key cascade leave no implementation choices.

#### 4. `internal/store/migration_test.go` (Modify)

*   **Type / Scope**: Migration tests.
*   **Proposed Changes**: Start at schema version 52. Insert:
    *   VM1 with disk A at position 5 in `vm_disks` and `vms.disk_id = F`,
      where F is unattached. `vm_disks` is authoritative, so F must not be
      added.
    *   VM2 with disk B at position 0 in `vm_disks` and no legacy pointer.
    *   VM3 with distinct disks C and D at positions `-1` and `0` in
      `vm_disks`, to exercise negative-position renumbering.
    *   VM4 with `vms.disk_id = B`, where B is already attached to VM2, to
      exercise the skipped-conflict path.
    *   VM5 with only `vms.disk_id = E`, where E is unattached, to exercise the
      pointer-only backfill.
    *   VM6 with `vms.disk_id = G` where no disk G exists, inserted with
      `PRAGMA foreign_keys=OFF`, to exercise the dangling-pointer skip.
    *   VM7 with distinct disks H and I both at position 0 in `vm_disks`, to
      exercise duplicate-position renumbering by disk ID.
  Open the store and assert:
    *   `workload_disks` has VM1/A at position 0, VM2/B at position 0, and
      VM5/E at position 0.
    *   VM1/F is not attached; F remains unattached in `ListDisks`.
    *   VM3 positions are renumbered to 0 and 1 in old-position, disk-ID order.
    *   VM7 positions are renumbered to 0 and 1 by disk-ID order.
    *   VM4 has no attachment, the migration succeeds, and B remains owned by
      VM2. VM6 has no attachment and the migration still succeeds. `GetVM` for
      VM4/VM6 returns nil `DiskID`/`Disk`; the gateway test in item 19 asserts
      `StartVM` returns `Aborted` “vm has no disk.”
    *   `vm_disks` is gone and `vms.disk_id` is NULL for every VM.
    *   `lightvms.lifecycle_generation` exists and defaults to 0.
    *   VM1, VM2, VM3, VM5, and VM7 load their disks and the disks report the
      correct owner.
    *   Deleting VM1 removes its workload row but leaves the disk row.
    *   An unattached disk remains in `ListDisks`.
    *   Inserting a `workload_disks` row with both owners or neither owner
      fails the `CHECK`; deleting a LightVM cascades its attachment rows;
      deleting an attached disk is restricted.
    *   A manual non-NULL write to `vms.disk_id` after migration does not change
      `GetVM`/`ListDisks` ownership; the loader ignores the column.
*   **Rationale**: The migration rewrites existing ownership data and is
  irreversible. The tests cover the copy, renumbering, skipped conflict, inert
  column, exact-owner CHECK, cascade, restrict, and unattached visibility.

#### 5. `internal/store/workload_disks_test.go` (Create)

*   **Type / Scope**: Store behavior tests for the unified attachment table.
*   **Proposed Changes**: Cover VM-to-LightVM and LightVM-to-VM conflicts,
  attachment order, `SetVMDisks` replacement and `updated_at` bump, LightVM
  create/update/delete release, zero-disk create, empty replacement clears,
  `GetDisk` owner fields, exact-owner CHECK rejection for both/neither owner,
  cascade deletion for VMs and LightVMs, restrict deletion for an attached
  disk, template unmark/provisioning/delete on an unattached template, unmark
  clearing template-only provisioning settings, rejection of
  marking/provisioning a disk in `provisioning`/`deleting`/`delete_failed`,
  rejection of `provisioning`/`deleting`/`delete_failed` attach candidates, and
  `MarkDiskDeleting` refusal (including a concurrent attach). Add a short
  context timeout around create/update paths to catch the single-connection
  deadlock, and a concurrent attach/`DeleteDisk` test that runs both in
  separate goroutines and asserts exactly one succeeds (not a `-race` detector
  test).
*   **Rationale**: These tests exercise the single-owner rule, the template
  lifecycle, the deletion-state guard, the foreign-key/CHECK integrity, and the
  two concurrency failure modes from the Risks table.

#### 6. `internal/store/lightvms_test.go` (Modify)

*   **Type / Scope**: LightVM store tests.
*   **Proposed Changes**: Extend the LightVM CRUD tests to load ordered disks,
    verify `DiskIDs` and `Disks`, verify that an omitted update preserves
    attachments and an explicit replacement clears or changes them, and verify
    that a disk with a throttle class is returned with resolved limits and
    class name. Add budget tests: a create with 16 disks and 8 interfaces is
    accepted; create with 17 disks and 8 interfaces, or 16 disks and 9
    interfaces, is rejected. A LightVM with 16 disks and 8 interfaces (24
    total) updated with `disk_ids` omitted and 9 requested interfaces is
    rejected at 25. A directly inserted grandfathered (over-budget) definition with 16 disks and 9
    interfaces (25) can be renamed with the same 25, but adding a disk (26) is
    rejected. Verify `ErrLightVMDeviceLimit` is wrapped with both counts.
    Verify `lifecycle_generation` increments on status, desired-state, and
    update writes and does not change on a read. Add a rename with a preserved
    disk in `deleting` or template state; it must succeed because attachment
    availability is not revalidated for preserved disks.
*   **Rationale**: The unified table tests prove ownership; these tests prove
    the LightVM-facing model, throttle integration, and the grandfather rule.

### Protobuf and generated API

#### 7. `design/proto/rookery/v1/lightvm.proto` (Modify)

*   **Type / Scope**: Public protobuf API.
*   **Proposed Changes**: Import `rookery/v1/disk.proto`. Add `repeated int64
    disk_ids = 25`, `repeated VMDisk disks = 26`, and `optional string
    run_state = 27` to `LightVM`. Add `repeated
    int64 disk_ids = 16` to `CreateLightVMRequest` with
    `max_items: 32` and `items: {int64: {gt: 0}}`, matching the existing
    `interface_ids` validation. Add `repeated int64 disk_ids = 17` with the same
    validation and `optional bool replace_disk_ids = 18` to
    `UpdateLightVMRequest`. The proto maximum stays 32 because the effective
    combined disk-plus-interface limit of 24 is a store/gateway rule; the proto
    only bounds the list and rejects non-positive IDs. The gateway normalizer
    and `validateLightVMDeviceBudget` are the authoritative second checks. VM
    disk IDs remain gateway-normalized rather than proto-validated.
*   **Rationale**: This gives clients ordered attachments, explicit
    replacement, and full disk details. Field numbers are fixed API decisions
    and are listed here so the implementer does not choose them.

#### 8. `design/proto/rookery/v1/disk.proto` (Modify)

*   **Type / Scope**: Public protobuf API.
*   **Proposed Changes**: Add `optional string attached_lightvm_id = 28` and
    `string attached_lightvm_name = 29` to `Disk`. Add
    `optional string attached_lightvm_id = 8` and
    `string attached_lightvm_name = 9` to `ImportableDisk`.
*   **Rationale**: The disk inventory and the import dialog must distinguish a
    disk attached to a LightVM from an unattached disk. Only one ownership pair
    is set at a time. The field numbers avoid the reserved `Disk` field
    numbers 24/25.

#### 9. Generated protobuf and OpenAPI outputs (Modify)

*   **Files**: `design/gen/rookery/v1/lightvm.pb.go`,
    `design/gen/rookery/v1/lightvm.pb.validate.go`,
    `design/gen/rookery/v1/disk.pb.go`, and
    `design/openapi/rookery.swagger.json`.
*   **Type / Scope**: Generated API code and documentation.
*   **Proposed Changes**: Regenerate with `make proto-generate`. All files
    under `design/gen/rookery/v1` and `design/openapi` are regenerated; only the
    files that carry the new messages and validation fields are expected to
    differ. Do not hand-edit generated files.
*   **Rationale**: The repository commits generated code and CI runs
    `make proto-check`, so the generated output must match the proto source.

### Gateway

#### 10. LightVM lifecycle and service wiring: `internal/gateway/lightvm_instances.go`, `internal/gateway/gateway.go`, `internal/api/server.go`, `internal/api/reconcile.go`, `internal/api/start_planner_internal_test.go`, and `internal/api/lightvm_console_test.go` (Modify)

*   **Type / Scope**: LightVM gateway lifecycle, service wiring, and API autostart.
*   **Proposed Changes**:
    *   Add a `LightVMDiskPlanner` interface with
      `PlanDisks(ctx context.Context, disks []store.VMDisk) (qemu.StartPlan, error)`
      and a `StartPlanner` interface containing both `Plan(ctx, store.VM)` and
      `PlanDisks`. Change `options.startPlanner`, `vmService.planner`, and
      `WithStartPlanner` to accept `StartPlanner`, not the concrete
      `*vmstart.Planner`, so gateway tests can inject a fake. Set
      `lightVMService.planner` to the narrow `LightVMDiskPlanner` interface.
    *   Add `planner`, `resourceMu`, and `startCoordinator` to `lightVMService`; wire
      `planner` to the `StartPlanner` interface, `resourceMu` to
      `options.diskMutex`, and `startCoordinator` to `options.lightvmStartCoordinator` in
      `gateway.New`. Add `options.lightvmStartCoordinator *lightvm.StartCoordinator` and
      `WithLightVMStartCoordinator`; default to `lightvm.NewStartCoordinator()` when nil.
      `CreateLightVM` takes `resourceMu` only. `UpdateLightVM`, `StopLightVM`,
      and `DeleteLightVM` call `release := startCoordinator.Reserve(id)` then
      `defer release()`, `Cancel(id)`, then take their mutexes.
      `StartLightVM` uses `startCoordinator.Begin`, then `mu` only for short
      lifecycle transitions (intent/status) and the final QEMU start.
      Retain `runtimeUnavailable()` in Start/Stop before `Begin` or any runtime
      call. Fail closed for Update/Delete when the runtime is nil unless both
      `status == stopped` and `desired_state == stopped`: return `Unavailable`
      with a message that liveness cannot be verified, and preserve the row and
      its attachments. A `status=error`/`desired_state=stopped` row may still
      have a live QEMU after a failed stop, so it is not safe. A nil-runtime
      Update/Delete is allowed only when both persisted states are stopped;
      Delete then removes the inventory record, skips QEMU/virtiofsd cleanup,
      and logs that runtime cleanup is unavailable via package-level
      `zerolog/log`. All failure re-reads and lifecycle
      intent/status writes (including Start failure restore, Stop, and Delete)
      use a fresh short context or `context.WithoutCancel(ctx)`; QEMU stops and
      cleanup calls likewise use their own bounded contexts, so a client cancel
      cannot leave `desired_state=running`. Add nil-runtime Start/Stop/Delete
      and Update tests (including a marked-running deletion/update that must
      preserve the row) and a client-canceled Start/Stop test.
    *   Add `PrepareLightVM`, `StartPreparedLightVM`,
      `CleanupPreparedLightVM`, `RunState`, `StopQEMULightVM`, and
      `CleanupStoppedLightVM` to `LightVMRuntime`; use
      `lightvm.PreparedLightVM` (LightVM ID, attempt token, attempt-specific
      container/rootfs and virtiofsd socket/PID, and the virtiofsd/QEMU-start
      ownership flags) as the handle. Change
      `toProto(vm store.LightVM)` to `toProto(ctx context.Context, vm
      store.LightVM)` and update every List/Get/Create/Update/Start/Stop call
      site; it populates `run_state` from `RunState` with a 2-second monitor
      timeout when the status is running, and logs and leaves it empty on
      error via package-level `zerolog/log`. If `s.runtime == nil`, it never
      calls `RunState`; it logs once and leaves `run_state` empty. The gateway
      `StartLightVM` flow is:
        1.  Load the LightVM; a missing row returns `NotFound`. If
            `len(vm.Disks)+len(vm.Interfaces) > MaxLightVMDevices`, return
            `FailedPrecondition` with the counts and remediation before
            `Begin`, image preparation, or any runtime call. Capture
            `requestGeneration := vm.LifecycleGeneration` and
            `loadedDesired := vm.DesiredState`.
        2.  Call `startCoordinator.Begin(ctx, vm.ID)` for a cancellable
            `startCtx`, a deferred `done`, and an error. A canceled wait returns
            `Aborted` without a store write or preparation. Different LightVMs
            proceed concurrently; a second start of this LightVM waits here.
        3.  Use a short `mu` scope with `defer Unlock`: re-read as `current`. A
            missing row returns `NotFound`. If QEMU is running: when
            `current.DesiredState == stopped`, a stop won (possibly a failed
            QEMU kill), so return `Aborted` without changing intent; otherwise
            call `RunState` and return `FailedPrecondition` for a `paused` value
            or monitor error, or repair status/desired state and return
            `AlreadyExists`. If QEMU is not running, return `Aborted` when
            `current.DesiredState != loadedDesired` or the generation differs
            from `requestGeneration` (a stop/update/delete landed while queued).
            Recheck the device budget; if it now exceeds
            `MaxLightVMDevices`, return `FailedPrecondition` before setting the
            start intent or calling the runtime. Otherwise record
            `previousDesired`, set `desired_state=running`, and capture the
            returned `lifecycle_generation` as `startGeneration`; assign it
            back to `current.LifecycleGeneration` before step 4 so the attempt
            token is monotonic across starts.
        4.  Call `runtime.PrepareLightVM(startCtx, current)` outside `mu`, then
            build the plan with `planner.PlanDisks(startCtx, current.Disks)`
            (empty plan when planner is nil).
        5.  On a preparation or planning failure, use a short `mu` scope with
            `defer Unlock` and re-read. A missing row returns `NotFound`. If
            QEMU is running, apply the step-3 QEMU-running branch:
            `desired_state=stopped` returns `Aborted`; a `paused` value or
            monitor error returns `FailedPrecondition`; otherwise repair
            status/desired state and return `AlreadyExists` without an error
            status. This also covers `qemu.ErrAlreadyRunning` while QEMU is
            alive. If QEMU is not running and the generation differs,
            `desired_state` is no longer `running`, or the error is
            `context.Canceled` (including `startCtx.Err()`), restore
            `previousDesired` only when it was `stopped` and the current desired
            state is still `running`, then return `Aborted` without a status
            write; never overwrite a stop’s `stopped`. If the error is
            `qemu.ErrAlreadyRunning` but QEMU is not running, set
            `status=error`, restore `previousDesired` when it was `stopped`,
            and return `Aborted` with “start raced an exiting QEMU; retry”
            instead of the `AlreadyExists` mapper. Only when the row, QEMU
            state (not running), generation, and desired state are unchanged
            and the error is not a cancellation, set `status=error`, restore
            `previousDesired` when it was `stopped`, and return
            `lightVMStartStatusError(err)`. Clean up the prepared container with
            a fresh bounded context after the scope.
        6.  Use one `mu` scope with `defer Unlock` for the final recheck and
            runtime start. Re-read; a missing row returns `NotFound` without a
            status write. If QEMU is running and `current.DesiredState ==
            stopped`, a stop won (possibly a failed QEMU kill): return `Aborted`
            without repairing status. If QEMU is running, call `RunState`; if it
            begins with `paused`, return `FailedPrecondition` with “LightVM is
            paused by an I/O error; stop and start to recover.” If `RunState`
            errors, return `FailedPrecondition` with “cannot read LightVM run
            state; stop and start to recover.” Otherwise repair status/desired
            state and return `AlreadyExists`. If the generation differs, restore
            `previousDesired` only when the current desired state is still
            `running` and return `Aborted`. If `desired_state` is no longer
            `running`, a stop happened: do not restore it and return `Aborted`.
            A changed disk set also increments the generation, so the generation
            branch covers it. Immediately before calling
            `runtime.StartPreparedLightVM`, check `startCtx.Err()`; if it is
            canceled, restore `previousDesired` when it was `stopped` and the
            current desired state is still `running`, return `Aborted` without
            a status write, and let step 7 clean up the prepared attempt.
            Otherwise call `runtime.StartPreparedLightVM` with the re-read
            LightVM, `&prepared`, and plan. If it returns nil, record success
            even when `startCtx` was canceled after launch: set
            `status=running`/`desired_state=running` with a fresh context. If
            it returns an error, first re-read QEMU. If QEMU is running and
            `prepared.QEMUStarted` is false, it belongs to another attempt:
            apply the step-3 QEMU-running branch (`desired_state=stopped` →
            `Aborted`; paused/monitor error → `FailedPrecondition`; otherwise
            repair status/desired → `AlreadyExists`) without an error-status
            write or cleanup of that QEMU. Otherwise, if the error is
            `context.Canceled` or `startCtx.Err() != nil`, restore
            `previousDesired` when it was `stopped` and the current desired
            state is still `running`, and return `Aborted` without a status
            write. Otherwise, if it returns `qemu.ErrAlreadyRunning`, re-read;
            if the row is gone, return `NotFound`; if QEMU is no longer
            running, set `status=error`, restore `previousDesired` when it was
            `stopped`, and return `Aborted` with “start raced an exiting QEMU;
            retry” instead of the `AlreadyExists` mapper. If QEMU is running
            and desired is stopped, return `Aborted`; if QEMU is running, call
            `RunState` and return `FailedPrecondition` for a paused value or
            monitor error; otherwise repair status/desired and return
            `AlreadyExists` without an error-status write or QEMU stop. On any
            other failure, set `status=error`, restore `previousDesired` when
            it was `stopped`, and return `lightVMStartStatusError(err)`.
        7.  After the final `mu` scope returns, clean up on every non-success
            path with a fresh bounded context. Any return after
            `PrepareLightVM` that does not reach a successful start or a
            `QEMUStarted` stop path must call `CleanupPreparedLightVM`,
            including pre-call cancellation, a QEMU found before
            `StartPreparedLightVM`, generation/desired-state skips, and
            over-budget. Track `startErr` and `startSucceeded` from
            `StartPreparedLightVM`. When `startErr` is
            non-nil and is not `qemu.ErrAlreadyRunning` and
            `prepared.QEMUStarted` is true, call `StopQEMULightVM` and wait for
            confirmation; only after QEMU is gone call
            `CleanupPreparedLightVM`. If the stop fails or QEMU remains, retain
            the exact container/virtiofsd, keep any non-canceled error status,
            and return the stop error. When `startErr` is non-nil but QEMU
            never started (including a virtiofsd failure), skip
            `StopQEMULightVM` and still call `CleanupPreparedLightVM` to remove
            the attempt container/virtiofsd. On pre-start aborts (AlreadyExists,
            paused, generation change, missing row, over-budget), and on a real
            `qemu.ErrAlreadyRunning` failure, call only
            `CleanupPreparedLightVM`; it removes this attempt's exact container
            and stops only this attempt's exact virtiofsd PID when
            `VirtiofsdStarted`, and never stops QEMU. If `StartPreparedLightVM`
            succeeded but a subsequent status write failed, do not stop or clean
            up the now-running QEMU; return the store error. Cleanup runs
            outside `mu` but before the deferred `done`, so a subsequent start
            of the same LightVM cannot reuse a container being removed; cleanup
            must be idempotent.
    *   Change `StopLightVM` to call
      `release := startCoordinator.Reserve(id)`, `defer release()`, then
      `Cancel(id)` before waiting on `mu`. Under `mu`, re-read, set
      `desired_state=stopped` (bumps the generation), call
      `runtime.StopQEMULightVM` with a fresh bounded context, and set
      `status=stopped` only after QEMU is confirmed gone. If the QEMU stop
      fails, keep `status=running`/`error`, leave desired stopped, release
      `mu`, return the error, and do not call `CleanupStoppedLightVM`. On
      success release `mu`, call `runtime.CleanupStoppedLightVM` with a fresh
      bounded context to stop virtiofsd and remove the container. The deferred
      release is the only release. A cleanup error is returned, but
      status/desired remain stopped.
    *   `UpdateLightVM` uses the same `release := Reserve; defer release();
      Cancel` sequence before `resourceMu`/`mu`. Because updates are allowed
      only while stopped, its transaction always sets
      `desired_state='stopped'` (updates are only valid for stopped LightVMs),
      so a canceled start’s `running` intent cannot survive the edit.
    *   `DeleteLightVM` uses the same reservation sequence. Under `mu`, set
      `desired_state=stopped`, call `StopQEMULightVM`, and confirm
      `RunningLightVM` is false; a stop error or a still-live process aborts
      the delete and leaves the row. Only after QEMU is confirmed gone set
      `status=stopped`, release `mu`, and call `CleanupStoppedLightVM` outside
      `mu`; if cleanup fails, abort the delete, keep the stopped row, and
      return the cleanup error so it can be retried. Only after cleanup
      succeeds reacquire `mu` and delete the row. `Begin` waits while the
      reservation is held, so cleanup cannot race a new start. Add
      running-delete, delete-cleanup-failure, two-concurrent-stop, and
      client-canceled Start/Stop tests.
    *   Add `lightVMStartStatusError` that maps `kouja.ErrSessionActive` to
      `Unavailable`, `context.Canceled` to `Aborted` with “start was canceled;
      start again,” `qemu.ErrAlreadyRunning` to `AlreadyExists` only when QEMU
      is confirmed alive, and everything else to `Internal`. Pass preparation,
      planning, and final `StartPreparedLightVM` errors through this mapper;
      missing-row, generation-change, disk-set-change, and QEMU-dead
      `ErrAlreadyRunning` branches return `NotFound`/`Aborted` directly without
      it. Do not reuse the VM-specific `vmStartStatusError` message.
    *   In `lightVMFromCreateRequest` and `lightVMFromUpdateRequest`, set
      `vm.DiskIDs = request.GetDiskIds()`; in the update helper set
      `vm.ReplaceDiskIDs = request.GetReplaceDiskIds()`. Reuse the existing
      `normalizeGatewayIDs(ids, store.MaxWorkloadDisks, "disk_ids")` helper and
      set `gatewayMaxVMDisks = store.MaxWorkloadDisks`; do not add a second
      normalizer or a third 32 limit. Leave the effective device-budget
      calculation to `store.UpdateLightVM` (which loads existing counts). The
      gateway returns `InvalidArgument` for `ErrLightVMDeviceLimit`.
    *   Keep the existing "stop the LightVM before editing it" rule.
    *   Return `disk_ids` and `disks` in every LightVM response with
      `vmDiskToProto`.
    *   Extend `lightVMError` with `ErrDiskNotFound`, `ErrInvalidDiskIDs`, and
      `ErrLightVMDeviceLimit` → `InvalidArgument`; `ErrDiskIsTemplate`,
      `ErrDiskDeleting`, and `ErrDiskInUse` → `Aborted`. Use `errors.As` on
      `store.DiskInUseError` to name the owning workload.
    *   In `internal/api/server.go`, extend `StartPlanner` with
      `PlanDisks(ctx, disks []store.VMDisk) (qemu.StartPlan, error)` and replace
      `LightVMController.StartLightVM` with `PrepareLightVM`,
      `StartPreparedLightVM`, `CleanupPreparedLightVM`, `StopQEMULightVM`, and
      `RunState`; add all five to the API fake controller in
      `internal/api/lightvm_console_test.go`. `LightVMController` is a
      deliberate subset of `LightVMRuntime` (it does not need
      `CleanupStoppedLightVM`); keep each fake aligned with its own interface
      and add separate compile-time assertions for `*lightvm.Runtime` against
      both interfaces and for the API adapter. Add
      `ConfigureLightVMStartCoordinator(*lightvm.StartCoordinator)` and a
      `lightVMStartCoordinator` field; if it is nil, autostart uses
      `lightvm.NewStartCoordinator()` (tests only, because production wires the shared
      one). Remove the current top-level
      `runMu.Lock`/`defer Unlock` from `autostartLightVMs`; list without a lock
      and lock per LightVM in short scopes. Autostart processes items
      sequentially to bound startup load; gateway starts for different
      LightVMs remain concurrent. For each desired-running item, run a helper
      closure so defers are per item:
        1.  List LightVMs and select `desired_state == running`.
        2.  For each item, load the LightVM and capture
            `requestGeneration := vm.LifecycleGeneration`; skip if the row is
            gone. Call `startCoordinator.Begin(ctx, vm.ID)` for a cancellable
            `startCtx`, a deferred `done`, and an error; a canceled wait skips
            without a store write or preparation.
        3.  In a short `runMu` scope with `defer Unlock`, re-read. Skip if the
            row is missing, its generation differs from `requestGeneration`
            (a stop/update/delete landed while queued), or `desired_state` is
            no longer `running`. If QEMU is running, repair
            `status=running` and `desired_state=running` and skip. If
            `len(current.Disks)+len(current.Interfaces) > MaxLightVMDevices`,
            set `status=error` and `desired_state=stopped`, log the counts, and
            skip before any preparation; a grandfathered over-budget definition
            is not retried on every restart. Otherwise set
            `desired_state=running`, capture the returned
            `lifecycle_generation` as `startGeneration`, and assign it back to
            `current.LifecycleGeneration` before preparation so attempt tokens
            are monotonic.
        4.  Call `runtime.PrepareLightVM(startCtx, current)` outside `runMu`,
            then build the plan with
            `s.startPlanner.PlanDisks(startCtx, current.Disks)` (empty plan when
            nil).
        5.  On a preparation or planning failure, use a short `runMu` scope to
            re-read. Skip without a status write if the row is missing, the
            generation differs, `desired_state` is no longer `running`, or
            `startCtx.Err() != nil`; otherwise set `status=error` only when QEMU
            is not running. Clean up the prepared container outside `runMu`
            with a fresh bounded context.
        6.  In a short `runMu` scope with `defer Unlock`, re-read and skip if
            the LightVM is missing, the generation differs, `desired_state` is
            no longer `running`, QEMU is running, or `startCtx.Err() != nil`.
            Otherwise call `runtime.StartPreparedLightVM` with `&prepared`
            directly (the API autostart has no gateway service). If it returns
            nil, set `status=running`/`desired_state=running` with a fresh
            context even when `startCtx` was canceled after launch. If it
            returns an error, re-read QEMU. If QEMU is running and
            `prepared.QEMUStarted` is false, skip without an error-status
            write; if its run state is paused or unreadable, also skip any
            repair, otherwise repair status/desired and skip. If the error is
            `context.Canceled` or `startCtx.Err() != nil`, skip without a
            status write. Otherwise, if it returns `qemu.ErrAlreadyRunning`,
            skip when the row is gone or `desired_state=stopped`; set
            `status=error` when QEMU is no longer running; if QEMU is running,
            call `RunState`, skip without repair when paused/error, otherwise
            repair status/desired and skip. On any other failure set
            `status=error`.
        7.  Clean up outside `runMu` on every non-success path with a fresh
            bounded context. Any API autostart return after `PrepareLightVM`
            that skips the final start (row/generation/desired change, QEMU
            found, or `startCtx` canceled) or otherwise does not start
            successfully must call `CleanupPreparedLightVM`. Track `startErr`
            and `prepared.QEMUStarted`. Only
            when `startErr` is non-nil, is not `qemu.ErrAlreadyRunning`, and
            `QEMUStarted` is true, call `StopQEMULightVM`; only after it
            confirms QEMU is gone call `CleanupPreparedLightVM`. If the stop
            fails or the process remains, retain the exact
            container/virtiofsd and log the error. When `startErr` is non-nil
            but QEMU never started (including a virtiofsd failure), skip
            `StopQEMULightVM` and still call `CleanupPreparedLightVM` for the
            attempt container/virtiofsd. On pre-start aborts (AlreadyExists,
            paused, generation change, missing row, over-budget) and on
            `qemu.ErrAlreadyRunning`, call only `CleanupPreparedLightVM`; it
            removes this attempt's exact container and stops only this
            attempt's virtiofsd PID when `VirtiofsdStarted`. It never stops
            QEMU. If a start succeeded but the subsequent status write failed,
            leave the running QEMU and its resources alone. Cleanup must be
            idempotent. `startCoordinator` is released by the deferred `done`,
            not explicitly.
      The reconcile worker’s 2-minute context must not bound image pulls.
      `RunInfrastructureReconciler(ctx)` stores its lifetime context in the
      server (for example `lightVMAutostartCtx` guarded by
      `lightVMAutostartMu`); `reconcileInfrastructure` receives only the
      2-minute work context and schedules a detached goroutine with a
      **30-minute** `context.WithTimeout` over the stored lifetime context
      (greater than the buildah pull timeout), canceled on shutdown. The
      reconcile worker does not wait for it; the slow-pull test asserts this
      configured bound rather than an implicit default. Add single-flight
      state (`lightVMAutostartRunning`, `lightVMAutostartDirty`): a second VPP
      restart sets dirty and returns while a pass is running; when the pass
      finishes, a dirty flag launches exactly one more pass. Tests inject the
      lifetime context or call `RunInfrastructureReconciler`; production never
      falls back to `context.Background`. A nil planner uses an empty plan.
      Update the fakes in `internal/api/start_planner_internal_test.go` and
      `internal/api/lightvm_console_test.go` for the new methods, including
      `StopQEMULightVM`. Add tests for a changed disk set, a deleted LightVM, a
      stop during preparation, a slow-pull autostart that is not canceled by the
      reconcile timeout, a VPP-restart-while-autostarting dirty rerun, an
      autostart shutdown cancellation that writes no error status, a pre-call
      skip that still cleans the prepared container, and a successful launch
      whose `startCtx` is canceled afterward (assert
      `running`/`desired_state=running` persists).
*   **Rationale**: Two-phase start keeps image preparation and planning outside
    `mu`, so a slow registry blocks only another LightVM start, not disk,
    network, VM-create, or LightVM-create writes. The gateway/API own status;
    the runtime remains store-free. The planner and startCoordinator interfaces
    give tests seams without constructing a real `vmstart.Planner` or real
    buildah.

#### 11. `internal/gateway/vm_create.go` (Modify)

*   **Type / Scope**: VM create and update validation and error mapping.
*   **Proposed Changes**: In VM create and update disk validation, reject a
    disk attached to a LightVM as well as a disk attached to another VM. Update
    `vmStoreError` for both create and update so `DiskInUseError` names the
    owning VM or LightVM, `ErrDiskIsTemplate` says “template disks cannot be
    attached to a workload,” and `ErrDiskDeleting` maps to `Aborted` for any
    non-empty `DeletionState`. Add create-path tests for a LightVM-owned disk
    and for `provisioning`, `deleting`, and `delete_failed` disks.
*   **Rationale**: The store is authoritative, but the gateway pre-check gives
    the correct client-facing error and avoids learning about the conflict only
    after a store write attempt.

#### 12. Disk deletion paths: `internal/gateway/disk_writes.go`, `internal/store/disk_deletion.go`, `internal/storage/reclaimer.go`, and `internal/storage/reclaimer_test.go` (Modify)

*   **Type / Scope**: Disk inventory deletion, deletion-state transitions, and
    background reclaimer. Phase 1 lands the store/reclaimer guards; phase 3
    lands the gateway error mapping and `ListImportableDisks` ownership fields.
*   **Proposed Changes**:
    *   In `disk_writes.go`, keep the exported
      `store.DiskAttachedToAnyWorkload` for read-only pre-checks and template
      clone validation. Replace the `DeleteDisk` pre-check
      `if disk.AttachedVMID != nil` with
      `if store.DiskAttachedToAnyWorkload(disk)` and name the owner before any
      thin-volume removal. The gateway must not call the unexported
      `checkDiskOwnership`/`checkDiskNotDeleting`/`checkDiskAvailableForAttach`
      helpers. Call the store operations `SetDiskTemplate`,
      `SetDiskProvisioning`, and `DeleteDisk`, which perform those checks
      internally, and map their errors. Marking a template uses
      attach-availability; provisioning uses ownership plus not-deleting;
      unmarking uses ownership and clears `provisioning_script`/
      `provisioning_timeout`. Hold `diskService.mu` (the shared `resourceMu`)
      for the entire synchronous LVM delete; `store.DeleteDisk` performs the
      ownership check before the row delete, and the gateway removes the thin
      volume inside that lock. Update `diskUpdateError`, the `DeleteDisk`
      store-error branches, and the `CreateDisk` template pre-check so a
      rejected disk names the owning workload instead of saying “attached to a
      VM.” Add `ErrDiskDeleting` → `Aborted` with the existing deletion-state
      message, and retain `DiskInUseError` owner naming via `errors.As`. Map
      `ErrDiskInUse`/`ErrDiskNotFound` from the Kouja `MarkDiskDeleting` path
      to `Aborted`/`NotFound` instead of wrapping every error as `Internal`.
    *   In `disk_deletion.go`, make `MarkDiskDeleting` one conditional
      statement with the existing SET list:
      `deletion_state = ?`, `deletion_error = ''`, `deletion_attempts = 0`,
      `deletion_next_attempt_at = ?`, `deletion_claimed_at = 0`,
      `throttle_class_id = NULL`, and `updated_at = ?`, guarded by
      `WHERE id = ? AND NOT EXISTS (SELECT 1 FROM workload_disks WHERE disk_id = ?)`.
      After a zero-row result, re-read the disk outside any transaction and
      return `ErrDiskNotFound` or `ErrDiskInUse`. Do not call
      `checkDiskOwnership` or any other pre-check from `MarkDiskDeleting`.
    *   In `reclaimer.go`, use `store.DiskAttachedToAnyWorkload` and skip any
      disk with an owner. Extend the existing
      `internal/storage/reclaimer_test.go` (Modify, not Create): attach a disk
      to a LightVM through the store, then use raw SQL to set
      `deletion_state='deleting'` and a past `deletion_next_attempt_at`, and
      assert the reclaimer skips it.
    *   Include the LightVM ownership fields in `ListImportableDisks`
      responses.
*   **Rationale**: Every disk mutation that currently trusts `AttachedVMID`
    would otherwise allow a LightVM-owned disk to be deleted or converted to a
    template. The foreign key only protects the final row delete; synchronous
    local volume removal must hold `resourceMu` and re-check ownership before
    removing the volume. The conditional update closes the check-then-act race
    for the reclaimer and Kouja paths.

#### 13. `internal/gateway/readonly.go` (Modify)

*   **Type / Scope**: Disk read API.
*   **Proposed Changes**: Map `AttachedLightVMID` and `AttachedLightVMName` into
    the generated `Disk` response.
*   **Rationale**: The inventory list and disk detail view must show LightVM
    ownership.

#### 14. `internal/gateway/host_interfaces.go` (Modify)

*   **Type / Scope**: Attachable host block-device listing.
*   **Proposed Changes**: When a registered host-device disk is attached to a
    LightVM, set the unavailable reason to name that LightVM. Keep the existing
    VM behavior. Do not add new `HostAttachableBlockDevice` proto fields; the
    picker uses the reason text, not structured owner fields.
*   **Rationale**: The host-device picker must explain why a device is
    unavailable; an empty or VM-only reason would be misleading. The existing
    structured VM owner fields stay for compatibility; LightVM ownership is
    intentionally reported through the reason text. A future proto revision can
    add structured LightVM fields if clients need them.

#### 15. `internal/gateway/lightvm_disks_test.go` (Create) and `internal/gateway/lightvm_lifecycle_test.go` (Modify)

*   **Type / Scope**: Gateway tests for LightVM disk API behavior.
*   **Proposed Changes**: In the new `lightvm_disks_test.go`, cover create with
    disks, response `disk_ids` and `disks`, the four-case replacement matrix
    (`true` + empty clears; `true` + non-empty replaces; `false`/omitted +
    empty preserves; `false`/omitted + non-empty replaces), direct API rejection
    of a disk owned by a VM or another LightVM, template and
    `provisioning`/`deleting`/`delete_failed` disk rejection, delete releases
    disks, running update rejection with a fake runtime, and the 24-device
    budget/grandfather errors. Extend the existing
    `internal/gateway/lightvm_lifecycle_test.go` with a fake runtime that
    returns `kouja.ErrSessionActive`, asserting `Unavailable` and persisted
    `error` status; a fake planner that returns a plan; a fake
    `PrepareLightVM` that blocks while a concurrent update changes the disk set
    (assert `startCoordinator.Cancel` aborts the prepare, `Aborted`, no status
    write, and the update leaves `desired_state=stopped`); a blocking prepare followed by a stop (assert cancellation, the
    start aborts, and stopped status is not overwritten); a blocking prepare
    followed by deletion (assert cancellation, `NotFound`, and no status
    write); a grandfathered LightVM above 24 devices (assert Start returns
    `FailedPrecondition` with counts/remediation and does not call the runtime);
    a fake runtime that reports running while the store says
    `desired_state=running` and `status=error` (assert status repair), a fake
    runtime that reports running while the store says `desired_state=stopped`
    (assert `Aborted` and that the stopped intent is not overwritten), a fake
    runtime that reports `paused` (assert Start returns
    `FailedPrecondition` with stop/start guidance), a QEMU with existing
    attempt container/rootfs and virtiofsd socket that is live before
    preparation's reaping/live guard (assert Start returns AlreadyExists,
    QEMU remains alive, and the existing resources are untouched after the
    second attempt's cleanup), a QEMU whose pidfile is lost but whose attempt
    socket is referenced (assert authoritative `RunningLightVM` is true,
    `PrepareLightVM`/`StartPreparedLightVM` return `ErrAlreadyRunning`, Stop
    does not clean up while it remains, Delete keeps the row/attachments, and
    no second QEMU starts), a second attempt that starts its own attempt-scoped
    virtiofsd and then fails (assert only its exact PID and container are
    removed), a preparation failure after QEMU appears
    (assert AlreadyExists, no error status, and QEMU remains), a fake
    `PrepareLightVM` returning `qemu.ErrAlreadyRunning` with QEMU already gone
    (assert `Aborted`, status `error`, not `AlreadyExists`), a fake
    `StartPreparedLightVM` returning
    `qemu.ErrAlreadyRunning` with a paused QEMU (assert `FailedPrecondition`
    and no repair), with a running QEMU and `desired_state=stopped` (assert
    `Aborted` and no repair), with a running QEMU (assert AlreadyExists, no
    error status, QEMU remains), with a QEMU that dies before the re-read
    (assert `Aborted`, status `error`, not `AlreadyExists`), and with a
    canceled `startCtx` plus a QEMU that dies before the re-read (assert no
    error-status write), a generic
    `StartPreparedLightVM` failure whose
    `StopQEMULightVM` reports success but leaves the process alive (assert no
    container/virtiofsd cleanup), a `StartPreparedLightVM` failure with
    `QEMUStarted=false` (assert `StopQEMULightVM` is not called and the attempt
    container/virtiofsd is cleaned up), a generic `StartPreparedLightVM`
    failure with `QEMUStarted=false` while another QEMU is alive (assert
    AlreadyExists/repair, no error status, and the existing QEMU/resources are
    untouched), a delete whose QEMU stop fails or leaves the process alive
    (assert the row and attachments remain), nil-runtime
    Start/Stop/Delete and Update behavior (including a
    marked-running Update/Delete that must preserve the row and attachments,
    a `status=error`/`desired_state=stopped` Update/Delete that must preserve
    the row, and nil-runtime List/Get of a marked-running row that must not
    panic), a
    planner failure followed by deletion (assert no status write for the
    missing row); a stop whose runtime cleanup fails after QEMU exits (assert
    `desired_state=stopped` and `status=stopped` persist even though the RPC
    returns an error); a stop whose QEMU stop fails while a start is queued
    (assert the queued start returns `Aborted` and does not repair the stopped
    intent); a delete whose cleanup fails, including an attempt-resource
    enumeration failure (assert the row remains with `status=stopped` and
    `desired_state=stopped`, and a retry succeeds); a stop
    whose cleanup is still running while a new start is
    requested (assert the reservation makes the new start wait, then abort);
    and client-canceled Start/Stop, including a canceled prepare, cancellation
    immediately before the final `StartPreparedLightVM` (assert `Aborted`, no
    runtime start, and `CleanupPreparedLightVM` removes the attempt container),
    a canceled `startCtx` after a successful launch (assert
    `status=running`/`desired_state=running` with a fresh write, not
    `Aborted`), and `qemu.ErrAlreadyRunning` against a live QEMU with a
    canceled `startCtx` (assert repair/AlreadyExists and no stopped-intent
    restore). Assert the start coordinator and `mu` are released on every
    path.
*   **Rationale**: The API contract is the part clients depend on; these tests
    exercise it end to end through the gateway and pin the status, planner, and
    lock boundaries.

#### 16. `internal/gateway/lightvm_instances_test.go` (Modify)

*   **Type / Scope**: Existing LightVM route tests.
*   **Proposed Changes**: Add disk IDs to the create/update route fixtures and
    assert the response shape and ownership fields on the disk endpoint.
*   **Rationale**: Keeps the existing route coverage representative of the new
    payload.

#### 17. `internal/gateway/disk_writes_test.go` (Modify)

*   **Type / Scope**: Disk write and import tests.
*   **Proposed Changes**: Add cases for deleting, templating, and provisioning
    a disk attached to a LightVM, for importable-disk responses carrying
    LightVM ownership, for `diskUpdateError` mapping `ErrDiskDeleting` to
    `Aborted` and owner errors to named `Aborted` messages, for
    `MarkDiskDeleting` mapping `ErrDiskInUse` and `ErrDiskNotFound` on the
    Kouja path, for changing a throttle class on an attached VM-owned and
    LightVM-owned disk (must remain allowed), and for synchronous LVM deletion
    holding `resourceMu` before volume removal. The LVM test should block the
    delete while a concurrent attach holds the mutex and assert the volume is
    not removed. This phase-1 gate test uses direct store attachment, because
    gateway LightVM disk writes do not exist until phase 3; the phase-3 version
    repeats the check through the gateway attach path. Land the phase-1 test
    with item 12, before the rest of this item.
*   **Rationale**: These are the paths most likely to bypass the unified
    ownership check because they use disk IDs and pre-reads rather than VM
    records. The LVM path is destructive before the row foreign key can fire.

#### 18. `internal/gateway/host_devices_test.go` (Modify)

*   **Type / Scope**: Attachable host-device tests.
*   **Proposed Changes**: Add a registered host device attached to a LightVM and
    assert the unavailable reason names it.
*   **Rationale**: Guards the user-facing explanation in the disk creation form.

#### 19. `internal/gateway/vm_create_test.go` (Modify)

*   **Type / Scope**: VM create and update tests.
*   **Proposed Changes**: Add an update case where a VM tries to take a disk
    attached to a LightVM and assert the rejection. Add a diskless legacy VM
    fixture and assert `StartVM` returns `Aborted` “vm has no disk.”
*   **Rationale**: Protects the reverse direction of the single-owner rule and
    pins the repair signal for a skipped legacy pointer.

### Runtime and QEMU

#### 20. `internal/vmstart/planner.go` (Modify)

*   **Type / Scope**: Remote attachment planner.
*   **Proposed Changes**: Add
    `PlanDisks(ctx context.Context, disks []store.VMDisk) (qemu.StartPlan, error)`
    containing the existing remote-disk preparation loop. Make
    `Plan(ctx context.Context, vm store.VM) (qemu.StartPlan, error)` call
    `PlanDisks(ctx, vmDisks(vm))` so the legacy `vm.Disk` fallback still
    prepares remote attachments. Keep the existing nil-store/nil-registry
    empty-plan behavior.
*   **Rationale**: LightVM start needs the same Kouja credential and attachment
    preparation without requiring a `store.VM`.

#### 21. `internal/vmstart/planner_test.go` (Modify)

*   **Type / Scope**: Planner tests.
*   **Proposed Changes**: Add tests that call `PlanDisks` directly: a remote
    disk succeeds with a prepared attachment, a local-only disk set succeeds
    with an empty plan, and a disk whose `storage_backend_id` does not resolve
    returns the planner error.
*   **Rationale**: Proves the extracted entry point preserves VM behavior and
    serves LightVM callers.

#### 22. `internal/qemu/qemu.go`, `internal/qemu/metrics.go`, `internal/qemu/plan_test.go`, and `internal/qemu/orphan_test.go` (Modify)

*   **Type / Scope**: QEMU start helpers, process matching, and process metrics.
*   **Proposed Changes**: Update the `qemu.go` call sites for the matcher
    contract in item 23: `Start`/`StartWithPlan` (through `Running`),
    `processIDs`, `ApplyCgroup`, `Stop`, `Running`, and `Metrics` use the new
    exact/basename predicates and pass the VM or LightVM pidfile. Update
    `orphan_test.go`'s helper process to pass both `-uuid <id>` and
    `-pidfile <run-dir>/<id>.pid` (leaving the pidfile absent for the orphan
    case), and update `startMonitorProcess` in `plan_test.go` to the same
    QEMU-shaped argv so the exact matcher still detects the fixture. Replace
    the VM-only secret helper with a
    `*QEMU` method:
    `prepareDiskSecrets(secretPrefix string, disks []store.VMDisk, plan StartPlan) (map[int64]string, error)`.
    It uses `q.writeSecret`/`q.secretPath(secretPrefix, diskID, protocol)`.
    Update the VM start call to pass `vm.ID`; LightVM calls pass
    `lightvm-<id>`. Update `internal/qemu/plan_test.go` to call the new method
    with a prefix. `prepareDiskSecrets` removes any files it already wrote when
    a later disk fails.
*   **Rationale**: Both workload types need the same 0600 CHAP secret handling,
    and the prefix keeps VM and LightVM secret paths distinct. One helper
    avoids two cleanup paths that can drift.

#### 23. `internal/qemu/lightvm.go` (Modify)

*   **Type / Scope**: LightVM QEMU lifecycle.
*   **Proposed Changes**:
    *   Add
      `StartLightVMWithVirtioFSAndPlan(ctx context.Context, vm store.LightVM, rootfs LightVMVirtioFS, plan StartPlan) (started bool, err error)`
      as the only non-deprecated LightVM QEMU entry point. `started` is true
      after this call launched a process; `started=false` is a guarantee that
      no QEMU process from this call remains. If launch was attempted and the
      final liveness check cannot prove absence, return `true` with the error
      so the caller stops/confirms before removing the rootfs. In particular,
      on a `q.run`/post-launch error, check `RunningLightVM` before any
      `cleanupLightVM`; return `true` if a process is alive. Validation and
      `qemu.ErrAlreadyRunning` failures return false. Remove
      `StartLightVM` and `StartLightVMWithVirtioFS` in phase 3; keep them as
      deprecated wrappers over the new plan-aware path through phase 2 so
      `go build ./...` passes before the gateway/API interfaces move. Update the
      QEMU integration test to pass an empty `StartPlan`.
    *   Change
      `buildLightVMArgs(vm store.LightVM, consoles []lightVMConsoleArg, pidfile, monitor string, rootfs LightVMVirtioFS, plan StartPlan, secretPaths map[int64]string) ([]string, error)`.
      It propagates `buildDiskArgs` errors.
    *   Add `lightvmSecretPrefix(id string) string { return "lightvm-" + id }`.
      Change `lightVMBase(id)` to return
      `filepath.Join(q.runDir, lightvmSecretPrefix(parsed.String()))`. Update
      `cleanupLightVM` to remove the prefixed console, pidfile, monitor, and
      console children, and also remove the legacy unprefixed LightVM
      artifacts if they still exist. Replace `processMatches(pid, id)` with
      `processMatches(pid int, expectedUUID, expectedPidfile string) bool`,
      which verifies both the `-uuid` value and the exact `-pidfile` path. Use
      exact `processMatches` only for the pidfile fast path; update
      `internal/qemu/metrics.go` to resolve the VM pidfile through `q.paths`
      and call the new signature. A stale `<uuid>.pid` that points at a LightVM
      process cannot make a VM stop it. Extract an unfiltered
      `matchingProcessIDs(id)` scan that matches `-uuid <id>` and returns the
      PIDs plus their arguments. `matchingVMPIDs(id)` filters that scan to a
      `-pidfile` whose basename is `<uuid>.pid`; `matchingLightVMPIDs(id)`
      filters to a basename of `lightvm-<uuid>.lightvm.pid` or, until the next
      major release, the legacy `<uuid>.lightvm.pid`; remove the legacy
      fallback in that release. The fallback helpers do not
      revalidate the exact current run-dir path, so orphan discovery still
      works if `-run-dir` changes. Neither helper is derived from the other
      after filtering. Add `lightVMConsolePath(id, consoleID)` as the prefixed-only
      path builder used by `lightVMConsoleArgs`; `LightVMConsoleSocket` keeps
      the legacy read fallback per console ID: the prefixed path first, then
      `<uuid>.console` for ID 0 and `<uuid>.console.<id>` for ID > 0 when the
      prefixed socket is absent until the next major release; remove this
      fallback in that release. Do not use the read fallback when building QEMU
      arguments.
    *   Add `LightVMRunState(ctx, id) (string, error)` using the LightVM monitor
      socket (`info status`) and parse QEMU’s running/paused result. The runtime
      delegates to it; `toProto(ctx, vm)` maps it into `LightVM.run_state` with
      a 2-second monitor timeout, leaving it empty and logging when the monitor
      read fails. `LightVMRunState` checks
      `RunningLightVM` first and tries the prefixed monitor path, then the
      legacy `<uuid>.lightvm.monitor` until the next major release; remove the
      legacy monitor fallback in that release.
    *   Change `processIDs(vmID, pidfile string, matcher func(string) []int)`
      to accept the kind-specific matcher. VM cgroup placement uses
      `matchingVMPIDs`; LightVM placement in
      `StartLightVMWithVirtioFSAndPlan` uses `matchingLightVMPIDs`, so a
      LightVM whose pidfile was lost can still be found and placed in its
      cgroup. Change `StopLightVM` and `RunningLightVM` fallback discovery from
      `matchingVMPIDs` to `matchingLightVMPIDs`; `QEMU.Stop`, `QEMU.Running`,
      and `QEMU.Metrics` keep `matchingVMPIDs`, so neither workload can stop
      the other.
    *   `processMatches(pid, expectedUUID, expectedPidfile)` is used for the
      pidfile fast paths in `QEMU.Start`/`StartWithPlan` (through
      `QEMU.Running`), `QEMU.processIDs` (also reached by
      `QEMU.ApplyCgroup`), `QEMU.Stop`, `QEMU.Running`, `QEMU.Metrics`,
      `StartLightVMWithVirtioFSAndPlan` (through `RunningLightVM`),
      `StopLightVM`, and `RunningLightVM`. Add
      `processMatchesBasename(pid int, expectedUUID string, pidfileBases
      ...string) bool` and use it for every target discovered by the fallback
      matcher and for the post-signal liveness loops in both stop paths; a
      LightVM call passes both the prefixed and legacy pidfile basenames, while
      a VM call passes only `<uuid>.pid`. Exact `processMatches` is only the
      pidfile fast-path selector. Basename matching does not require the
      current absolute run-dir path, or an old-run-dir process is treated as
      gone. The `matchingProcessIDs` scan returns the PIDs plus their
      arguments; the fallback helpers return `[]int`.
    *   Make `RunningLightVM` the single authoritative liveness predicate. Add
      an unexported `lightVMQEMUProcesses(id) []int` that scans QEMU command
      lines only for LightVM-specific evidence: a `-pidfile` basename of
      `lightvm-<uuid>.lightvm.pid` or the legacy `<uuid>.lightvm.pid`, or a
      `-chardev`/virtiofs path containing any of the attempt, pre-token, or
      pre-prefix LightVM sockets. Do not match a bare `-uuid <id>`, because a
      VM may share the UUID. Exclude virtiofsd by executable
      name/arguments. `RunningLightVM` returns true when either the
      pidfile/matcher path or this socket scan finds a process;
      `StopLightVM` target discovery uses the union, so a QEMU whose pidfile is
      lost but whose socket is referenced is still stopped. Use the same
      predicate in gateway/API steps 3/5/6, Stop/Delete confirmation,
      `StopQEMULightVM`, `CleanupStoppedLightVM`, `RunState`, and `toProto`.
      `PrepareLightVM`/`StartPreparedLightVM` call `RunningLightVM`; no separate
      interface method is added. A hit returns `qemu.ErrAlreadyRunning` rather
      than starting a second guest.
    *   Change `StopLightVM` target discovery to a slice of target structs
      carrying the PID and match mode (pidfile fast path, fallback pidfile
      basename, or socket-only). For socket-only targets, the post-signal
      liveness loop uses the LightVM socket reference predicate, not a pidfile
      basename. Poll until every target is gone and `RunningLightVM` is false;
      if a process remains after the bounded wait, return an error and do not
      remove console/pid/monitor or secret files. The runtime's
      `StopQEMULightVM` relies on this confirmation.
    *   Add `cleanupSecrets(lightvmSecretPrefix(id))` to `StopLightVM`, called
      after the QEMU process is confirmed stopped (or was already absent), so
      stop and delete remove stale CHAP files. Do not make `cleanupLightVM`
      remove secret files; it runs before preparation.
    *   Validate `vm.Disks` with the existing disk validation path, including
      LVM activation and host-device revalidation.
    *   Run `cleanupLightVM` for stale console/pid/monitor artifacts before
      preparing secrets. Prepare secrets with
      `prepareDiskSecrets(lightvmSecretPrefix(vm.ID), vm.Disks, plan)`, defer
      `removeSecretFiles` and `cleanupSecrets(lightvmSecretPrefix(vm.ID))`, and
      include the pid and process name of any image-lock holder in start
      errors.
    *   Insert disk arguments after the conditional `memory-backend-memfd` and
      `-numa` block and before `-display none` and the console chardevs.
    *   Preserve `lightVMAppend`'s `APP_HOSTNAME` suffix behavior; do not
      rewrite `root=`. Keep the virtiofs rootfs device in place.
*   **Rationale**: QEMU receives the disk arguments here. Reusing
    `buildDiskArgs` keeps local, Kouja, throttle, and error behavior aligned
    with VMs. The `lightvm-` prefix and pidfile-based matching prevent a VM and
    LightVM with the same UUID from stopping each other. The cleanup order
    ensures stale cleanup cannot delete freshly prepared secrets, and deferred
    removal covers validation, argument-build, start, stop, and delete
    failures.

#### 24. `internal/qemu/lightvm_test.go` and `internal/qemu/lightvm_integration_test.go` (Modify)

*   **Type / Scope**: LightVM argument tests.
*   **Proposed Changes**: Update calls to the new argument builder signature.
    Add tests for zero disks, multiple local disks in order, a combined
    24-device layout (16 disks plus 8 interfaces), a disk with a throttle
    class, a remote iSCSI disk with a plan and secret path under the
    `lightvm-<uuid>` prefix, console arguments containing the prefixed
    `lightvm-<uuid>.console...` paths (not legacy `<uuid>.console...`),
    preserved `root=` with the existing
    `APP_HOSTNAME` suffix behavior, secret-file removal on success, partial
    preparation, start failure, stop/delete cleanup, a stop whose target never
    exits after SIGKILL using an injectable process-control seam (assert an
    error and that artifacts/secrets are not removed; do not depend on a real
    unkillable QEMU), a legacy-basename/old-run-dir fallback target found by
    `matchingLightVMPIDs` that survives longer than one poll and is stopped via
    basename matching (assert SIGKILL and both legacy/prefixed basenames are
    checked), a socket-only QEMU with no `-pidfile` argument but a `-chardev`
    referencing an attempt socket (assert `RunningLightVM` is true,
    `StopLightVM` target discovery uses the socket-scan union, the post-signal
    loop uses the socket predicate, SIGKILL is reached, and artifacts are not
    removed while it remains), and that no `-machine` option is emitted. Add package-level function variables for the
    process-alive/matcher/kill primitives with production defaults unchanged so
    the timeout branch is deterministic; the gateway fake covers cleanup
    retention through `StopQEMULightVM`. Add a non-skippable unit test that builds the arguments
    for 16 disks and 8 interfaces, asserts the rootfs device, serial controller,
    16 virtio drives, and 8 vhost-user net devices, and checks the documented
    `pc-i440fx` slot arithmetic; this test does not need QEMU. Add a separate
    QEMU/KVM-gated integration test that actually starts the 16+8 layout under
    the default `pc-i440fx` machine, using a real virtiofsd rootfs and real
    VPP/vhost-user net peers (or a complete vhost-user responder that finishes
    the handshake). Skip that integration test with an explicit reason when
    QEMU/KVM/virtiofsd is unavailable; do not claim the unit test proves QEMU
    slot allocation. The store’s 25-device
    rejection and the gateway’s `StartLightVM` `FailedPrecondition` for a
    directly inserted 25+ definition are asserted in item 6 and the gateway
    start test in item 15, not in this
    package. Add a process-matching
    test with a VM and LightVM using the same UUID, asserting a VM-only QEMU
    (VM pidfile, no LightVM socket) leaves `RunningLightVM` false and
    `StopLightVM` with no target, `RunningLightVM`/`StopLightVM` do not match
    the VM pidfile, and VM `Running` does not match the LightVM pidfile; also
    assert a LightVM QEMU with a lost pidfile but an attempt socket is still
    found. A stale `<uuid>.pid` pointing at a live
    LightVM cannot make VM `Running`/`Stop` target it, and `Metrics` still
    resolves the VM pidfile through `q.paths` with the new matcher. Add a cgroup-enabled
    LightVM start with the pidfile removed before cgroup placement, asserting
    `matchingLightVMPIDs` finds the process. Add a pre-upgrade fixture with two
    consoles (`<uuid>.console.1` and `<uuid>.console.2`) and assert
    `LightVMConsoleSocket` falls back to both legacy paths. Test cleanup by calling
    `prepareDiskSecrets` and the deferred `removeSecretFiles`/`cleanupSecrets`
    helpers directly; the QEMU start path remains covered by the KVM-gated
    integration test. Also update `internal/qemu/lightvm_integration_test.go`
    to call the plan-aware start with an empty plan. Keep the existing console,
    interface, memory-backend, and virtiofs assertions.
*   **Rationale**: Argument construction is deterministic and covers the
    remote-secret path, the tested device budget, and the workload-ID namespace.
    The KVM-gated start is the only verification of the QEMU slot assumption.

#### 25. `internal/lightvm/runtime.go` (Modify) and `internal/lightvm/start_coordinator.go` (Create)

*   **Type / Scope**: LightVM runtime orchestration and start coordination.
*   **Proposed Changes**:
    *   Add `StartCoordinator` with per-LightVM slots and a refcounted lifecycle
      reservation. Signatures:
      `Begin(ctx, id) (startCtx context.Context, done func(), err error)` and
      `Reserve(id) (release func())`; `Cancel(id)` aborts the current prepare.
      `Begin` takes the slot for that ID (different IDs proceed concurrently; a
      second start of the same ID waits, selecting on `ctx.Done()`); if the
      caller context is canceled while queued it returns `context.Canceled`
      without any store write or preparation, releasing the slot and cancel
      entry it acquired and releasing the semaphore only if it had acquired
      one. `done` cancels/removes the entry
      and releases the slot and preparation semaphore. `Reserve` increments a
      per-ID count and returns an idempotent release; `Begin` waits while the
      count is non-zero, so two concurrent stop/update/delete operations both
      hold the reservation until their own release. Stop/update/delete call
      `release := Reserve(id); defer release(); Cancel(id)`. A small global
      preparation semaphore (default 2) bounds concurrent `PrepareLightVM`
      calls; `Begin` registers the cancel entry before waiting for the
      semaphore, waits on it, and selects on `ctx.Done()` for every wait (slot,
      reservation count, and semaphore). A canceled wait releases the slot, the
      cancel entry, and any semaphore it acquired.
      Queued starts capture the generation before `Begin`; when QEMU is not
      running, a changed generation aborts before any intent or status write.
      When QEMU is running, `desired_state=stopped` aborts; otherwise the start
      repairs status/desired and returns `AlreadyExists`. Generation is not a
      fence in that branch because a successful earlier start advances it; the
      desired state is the intent fence that prevents overwriting a newer stop.
    *   Add `PreparedLightVM{LightVMID, AttemptToken, Container, Rootfs,
      VirtiofsdSocket string; VirtiofsdPID int; VirtiofsdStarted,
      QEMUStarted bool}` and:
        *   `PrepareLightVM(ctx context.Context, vm store.LightVM) (PreparedLightVM, error)`:
            check the authoritative `RunningLightVM` (pidfile plus socket scan)
            first; if it finds a live QEMU referencing this LightVM, return
            `qemu.ErrAlreadyRunning` before touching anything; re-check
            immediately before reaping.
            Generate the fixed-width monotonic attempt token
            `fmt.Sprintf("%016x%08x", vm.LifecycleGeneration, random)` (24
            characters max; the gateway/autostart copy
            `vm.LifecycleGeneration` from the post-intent `startGeneration`
            before calling prepare). Parse the generation numerically for
            older/newer comparison, never lexicographically, and reject the
            attempt with a clear error when
            `len(runDir/lightvm-<uuid>-<token>.virtiofs.sock)` exceeds the Unix
            socket limit (107 bytes plus NUL). Use a shared
            `enumerateLightVMAttempts(ctx, id)` helper that lists buildah
            containers and run-directory socket/pid files by the LightVM
            prefix, parses token/age, and sorts them; buildah has no wildcard
            remove. If enumeration fails, log it and treat the stale set as
            empty; the authoritative `RunningLightVM` guard is still evaluated,
            and the new unique attempt proceeds. With no QEMU alive, reap only
            resources with an older token
            and only when no live QEMU command line references that socket:
            stop the matching virtiofsd PID and remove its attempt container.
            Reaping is best-effort for stale resources: log a stop/remove
            failure and continue preparing the new unique attempt; only a live
            QEMU reference aborts with `qemu.ErrAlreadyRunning`. Also inspect
            the pre-token fixed
            `lightvm-<uuid>` and pre-prefix `<uuid>` sockets, plus the legacy
            `rookery-lightvm-<uuid>` container, with the same live-process
            guard; never touch the current or a newer token's resources. Pull
            the image, create and mount a uniquely named
            buildah container (`rookery-lightvm-<uuid>-<token>`), write `.env`
            and `.entrypoint`, and return the ID, token, container, rootfs,
            attempt-specific virtiofsd socket, and both flags false. On error,
            remove only that partial container with a fresh bounded context and
            join the removal error with the preparation error.
        *   `StartPreparedLightVM(ctx context.Context, prepared *PreparedLightVM, vm store.LightVM, plan qemu.StartPlan) error`:
            first re-check the authoritative `RunningLightVM`; if a QEMU is
            alive or references any of this LightVM's sockets, return
            `qemu.ErrAlreadyRunning` without touching any socket or container.
            Start virtiofsd on `prepared.VirtiofsdSocket`, record the exact PID,
            socket, and `VirtiofsdStarted=true`, and pass
            `prepared.VirtiofsdSocket` to QEMU. Set `prepared.QEMUStarted` from
            the plan-aware QEMU call's `started` result, which is true only
            after QEMU is confirmed launched. If virtiofsd itself fails to
            start, remove only the partial socket/pid it created. On failure,
            return the error without stopping QEMU or removing the container;
            the caller performs those steps outside `mu`.
        *   `CleanupPreparedLightVM(ctx context.Context, prepared PreparedLightVM) error`:
            stop only `prepared.VirtiofsdPID`, after verifying its command line
            still references `prepared.VirtiofsdSocket`, when
            `VirtiofsdStarted`; then remove only `prepared.Container`. It never
            stops QEMU and never uses the ID-wide virtiofsd or container
            cleanup. A pre-start abort has both ownership flags false, so an
            existing QEMU's container/rootfs/virtiofsd/socket is untouched. It
            is a no-op on a zero-value handle. Callers invoke this with a fresh
            bounded context outside `mu` on every non-success path. Only after
            a real `StartPreparedLightVM` failure other than
            `qemu.ErrAlreadyRunning`, with `QEMUStarted=true`, does the caller
            additionally call `StopQEMULightVM` before this cleanup, and only
            after QEMU is confirmed gone. If QEMU never started, cleanup still
            removes the attempt container/virtiofsd.
        *   `StopQEMULightVM(ctx, id) error`: stop only the QEMU process and
            poll `RunningLightVM` until it is false or a bounded timeout
            expires; return an error when the process is still alive.
        *   `CleanupStoppedLightVM(ctx, id) error`: refuse to run when
            `RunningLightVM` is still true; otherwise use
            `enumerateLightVMAttempts` to stop every virtiofsd process for all
            three socket layers (attempt
            `lightvm-<uuid>-<token>`, pre-token fixed `lightvm-<uuid>`, and
            pre-prefix `<uuid>`), remove every attempt container
            (`rookery-lightvm-<uuid>-*`) plus the legacy fixed container
            (`rookery-lightvm-<uuid>`), and propagate real removal errors.
            Unlike preparation's best-effort stale sweep, an
            `enumerateLightVMAttempts` failure here is a cleanup failure: return
            it without reporting success so Stop returns the error and Delete
            keeps the stopped row for retry. Callers invoke this outside `mu`
            with a fresh bounded context after QEMU is confirmed stopped.
        *   `cleanupContainer` treats `buildah umount` "not mounted",
            `buildah rm` "not known", and a missing container as success for
            idempotency; it attempts `umount` and then `rm`, and propagates only
            real `rm` failures for an existing container.
            `PrepareLightVM`'s partial-cleanup path uses the same helper and
            joins a real removal failure with the preparation error.
      Keep `Runtime.StartLightVM` as a deprecated phase-2 wrapper over
      `PrepareLightVM`+`StartPreparedLightVM` with an empty plan. It checks
      `ctx.Err()` before `StartPreparedLightVM` and cleans up the prepared
      attempt instead of launching when canceled. On a
      `StartPreparedLightVM` failure other than `qemu.ErrAlreadyRunning` with
      `QEMUStarted`, the wrapper stops QEMU, confirms it is gone, and cleans up
      the attempt container/virtiofsd exactly as the gateway does; when QEMU
      never started it still cleans up the attempt container/virtiofsd, but
      never touches QEMU. In phase 3 remove
      `Runtime.StartLightVM`, `QEMURunner.StartLightVMWithVirtioFS`,
      `LightVMRuntime.StartLightVM`, and `LightVMController.StartLightVM`;
      rewrite `runtime_test.go` calls to the new methods. Redefine
      `Runtime.StopLightVM` as a deprecated wrapper over
      `StopQEMULightVM` -> confirm `RunningLightVM == false` ->
      `CleanupStoppedLightVM`; it must not clean up while QEMU remains. Remove
      `Runtime.StopLightVM` from the gateway/API interfaces and fakes in phase
      3; the `StopLightVM` RPC uses the split methods. Keep the QEMU-level
      `StopLightVM`, `RunningLightVM`, and `LightVMConsoleSocket`. Do not add a
      planner field or `StartPlanner` interface.
    *   Normalize `runCommand` cancellation: when `ctx.Err() != nil`, return
      `ctx.Err()` rather than the wrapped `exec.ExitError`/`signal: killed`.
      Start treats `context.Canceled` as `Aborted` without a status write.
    *   Attempt-scoped runtime paths: QEMU console/pid/monitor stay per
      LightVM (`lightvm-<uuid>...`), while virtiofsd socket/pid/log and the
      buildah container include a fixed-width monotonic attempt token plus 8
      random hex characters
      (`lightvm-<uuid>-<token>.virtiofs.sock`, `rookery-lightvm-<uuid>-<token>`).
      Validate the complete socket path against the Unix `sun_path` limit
      (107 bytes plus NUL) before creating the container. ID-wide cleanup
      (`Stop`/`Delete`) enumerates all three socket layers (attempt,
      pre-token fixed `lightvm-<uuid>`, pre-prefix `<uuid>`) and both container
      layers until the next major release; prepared cleanup stops only the
      exact PID/socket and container in its handle. Remove the legacy
      fallback in that release.
    *   Keep the existing constructor signature; do not add a planner argument.
    *   Add `RunState(ctx, id) (string, error)` delegating to
      `QEMURunner.LightVMRunState`. `QEMURunner` exposes
      `LightVMRunState(ctx, id)` and
      `StartLightVMWithVirtioFSAndPlan(ctx, vm, rootfs, plan) (started bool,
      err error)`. The runtime does not write status; callers own
      `status`/`desired_state`.
*   **Rationale**: Splitting preparation from QEMU start lets the gateway/API
    run the slow image pull under `startCoordinator` only, not `mu` or `resourceMu`.
    The runtime remains store-free and coordinates buildah, virtiofsd, and
    QEMU.

#### 26. `internal/lightvm/runtime_test.go` (Modify)

*   **Type / Scope**: LightVM runtime tests.
*   **Proposed Changes**: Keep the existing constructor call; there is no
    planner argument. Replace the existing `runtime.StartLightVM` calls with
    the new prepare/start methods. Update the fake QEMU and add tests for
    `PrepareLightVM` (unique attempt container/socket names, fixed-width token,
    socket-path-limit rejection, image/container/env/entrypoint,
    older-attempt stale resource reaping that leaves current/newer tokens and
    live-QEMU-referenced sockets alone (numeric generation ordering; test
    generation 9 vs 10 so a newer token is never reaped), logs and continues
    on a stale-resource or enumeration failure, and partial-preparation
    cleanup),
    `StartPreparedLightVM` (QEMU recheck before virtiofsd, exact
    socket/PID/flags, then QEMU with plan handoff, and virtiofsd-start failure
    that leaves QEMU untouched while the caller still cleans up the container),
    `CleanupPreparedLightVM`
    (stops only the exact PID/socket when `VirtiofsdStarted`, removes only the
    exact attempt container, leaves another attempt's resources, propagates
    real `rm` errors, and treats an unmounted/already-absent container as
    success), `RunState`, `StopQEMULightVM`/`CleanupStoppedLightVM` split,
    `StartPreparedLightVM` returning `qemu.ErrAlreadyRunning`, a QEMU failure
    returning without cleanup and the caller stopping QEMU only after it
    confirms the process is gone, a partially launched QEMU where the QEMU
    method returns `started=true` with an error (assert the caller
    stops/confirms before cleanup), a QEMU method returning `started=false`
    only when no process remains, a still-running QEMU stop error that leaves
    the exact container/virtiofsd intact, `CleanupStoppedLightVM` removing all
    three socket layers and both container layers while refusing a live QEMU,
    an enumeration failure during `CleanupStoppedLightVM` returning an error
    (and, in the gateway test, keeping the Delete row), the phase-2
    `Runtime.StartLightVM` wrapper cleanup on failure, the deprecated
    `Runtime.StopLightVM` wrapper refusing cleanup while QEMU remains,
    prefixed and legacy virtiofsd paths, and that the runtime does not write
    status. The Delete-row-retention assertion stays in item 15. Add
    `StartCoordinator` tests for same-ID serialization, different-ID
    concurrency, `Cancel` aborting a blocked `PrepareLightVM`, a queued start
    aborting after caller-context cancellation, refcounted reservations with
    two concurrent stops and a canceled reservation wait, the global
    preparation limit (two blocked prepares, a third waits), `done` releasing
    the slot and semaphore and removing the cancel entry, and no double-cancel
    panic. Generation-change behavior is asserted in the gateway/API caller
    tests, not in `StartCoordinator`.
*   **Rationale**: The runtime coordinates buildah, virtiofsd, and QEMU; the
    two-phase handoff and cleanup order must be tested.

#### 27. `cmd/rookery/serve.go` (Modify)

*   **Type / Scope**: Server wiring.
*   **Proposed Changes**: Create `lightvmStartCoordinator := lightvm.NewStartCoordinator()`
    after the single `lightvm.NewRuntime` call. Call
    `apiServer.ConfigureLightVMStartCoordinator(lightvmStartCoordinator)` and add
    `gateway.WithLightVMStartCoordinator(lightvmStartCoordinator)` to `gatewayOptions`.
    Keep the existing planner and runtime wiring; the runtime no longer takes a
    planner.
*   **Rationale**: The gateway and API autostart must share the same start
    coordinator and runtime instance so two starts of the same LightVM cannot
    prepare its container concurrently. Different LightVMs remain concurrent.

### UI

#### 28. `ui/src/inventory/types.ts` (Modify)

*   **Type / Scope**: TypeScript inventory types.
*   **Proposed Changes**: Add `disk_ids`, `disks`, and `run_state` to
    `LightVM`; add
    `attached_lightvm_id` and `attached_lightvm_name` to `Disk` and
    `ImportableDisk`.
*   **Rationale**: The UI must type the new API fields before any component can
    use them.

#### 29. `ui/src/inventory/shared/disk-predicates.ts` (Move/Modify)

*   **Type / Scope**: Shared disk predicates.
*   **Proposed Changes**: Add
    `diskAttachedToAnyWorkload(disk: Disk): boolean`,
    `diskOwner(disk: Disk): { kind: "vm" | "lightvm"; name: string } | null`,
    and `diskAvailableTo(disk: Disk, kind: "vm" | "lightvm", id?: string): boolean`.
    `diskAvailableTo` returns false for `is_template`, any non-empty
    `deletion_state`, a different VM owner, or a different LightVM owner; it
    returns true only for an unattached disk or the same workload. Do not
    hardcode the three deletion-state strings; use a non-empty check. Reuse the
    existing `diskIsDeleting` helper where a single predicate is enough.
    Move the file from `ui/src/inventory/vms/disk-predicates.ts` to the shared
    directory and update imports in `VMDialog`, `VMsTab`, `DisksTab`,
    `ImportDisksDialog`, and the LightVM components. Replace direct
    `attached_vm_id` checks in the disk pickers and Disks tab with these
    helpers.
*   **Rationale**: One helper prevents the VM, LightVM, and Disks tab from
    disagreeing about whether a disk is available to a workload. A neutral
    directory reflects that LightVM and Disks code now imports it.

#### 30. `ui/src/inventory/lightvm/LightVMDialog.tsx` (Modify)

*   **Type / Scope**: LightVM create/edit dialog.
*   **Proposed Changes**: Accept a `disks: Disk[]` prop. Extract the ordered
    add/remove picker from `VMDialog` into a shared `DiskPicker` (props for
    boot-disk labeling and allow-empty) and reuse it here with filtering for
    templates, every disk with a non-empty deletion state, VM-owned disks, and
    disks owned by another LightVM. Selected rows use the same `VMDisk[]`
    representation as `VMDialog`; available rows are `Disk[]` from
    `/api/disks`. Submit `disk_ids` and, on edit, `replace_disk_ids: true`.
    Show a hint that the guest initramfs is responsible for mounting and that
    the caller controls any `root=` selector in `append`; Rookery does not
    rewrite it.
*   **Rationale**: This is the operator-facing attachment path. Ordering must
    be visible because it determines QEMU `-drive` order; the guest kernel
    assigns `/dev/vdX` names.

#### 31. `ui/src/inventory/lightvm/LightVMTab.tsx` (Modify)

*   **Type / Scope**: LightVM tab shell.
*   **Proposed Changes**: Load `/api/disks`, listen for
    `rookery:disks-changed` and `rookery:disks-data`, pass disks to the dialog,
    add a Disks column, and reload disks after save or delete. The listeners
    consume `event.detail` from `rookery:disks-changed` and
    `rookery:disks-data` without refetching or dispatching. Only the
    save/delete handler reloads `/api/disks` and dispatches both events with
    the fresh list.
    Initialize the dialog selection from the LightVM response's `disks` field.
    Update the delete confirmation to say disks are released, not deleted.
    Show a `run_state` beginning with `paused` (including
    `paused (io-error)`) as a distinct status with “stop and start to recover”
    guidance.
*   **Rationale**: The tab must show ownership changes immediately and keep the
    other inventory tabs consistent without an event/request loop.

#### 32. `ui/src/inventory/vms/VMDialog.tsx` (Modify)

*   **Type / Scope**: VM create/edit dialog.
*   **Proposed Changes**: Replace the `attached_vm_id` filter with the shared
    availability helper so a LightVM-owned disk is never offered to a VM.
*   **Rationale**: Prevents a user from selecting a disk that the API will
    reject.

#### 33. `ui/src/inventory/vms/VMsTab.tsx` (Modify)

*   **Type / Scope**: VM tab shell.
*   **Proposed Changes**: Compute disks available to a VM with
    `diskAvailableTo`. When none are available, show “No disk is available to a
    VM. Create a disk on the Disks tab or edit the owning VM or LightVM to
    detach one.” and disable the create button. After a VM save/delete, reload
    `/api/disks` and dispatch `rookery:disks-changed` and `rookery:disks-data`
    so the LightVM tab and disk inventory stay current.
*   **Rationale**: The VM create button must stay disabled when no disk is
    available to a VM, including when every existing disk is owned by a
    LightVM.

#### 34. `ui/src/inventory/disks/DisksTab.tsx` (Modify)

*   **Type / Scope**: Disk inventory tab.
*   **Proposed Changes**: Use the shared attachment helper for the attached
    count, delete guard, template guard, action titles, and ownership cell.
    Display the owning VM or LightVM name.
*   **Rationale**: A LightVM-owned disk must not appear deletable or convertible
    to a template, and its owner must be visible.

#### 35. `ui/src/inventory/disks/ImportDisksDialog.tsx` (Modify)

*   **Type / Scope**: Disk import dialog.
*   **Proposed Changes**: Show a LightVM owner name alongside the existing VM
    owner name when an imported volume is already registered and attached.
*   **Rationale**: The import list must not imply that a LightVM-owned volume is
    free.

#### 36. `internal/webui/static/build/app.js` (Modify, generated)

*   **Type / Scope**: Embedded UI bundle.
*   **Proposed Changes**: Rebuild with `make ui-build`. Do not hand-edit.
*   **Rationale**: The Go binary embeds this bundle, and CI checks generated
    bundle drift.

### Terraform provider

#### 37. `cmd/terraform-provider-rookery/internal/provider/resource_disk.go` (Modify)

*   **Type / Scope**: Terraform disk resource model and schema.
*   **Proposed Changes**: Add computed `attached_lightvm_id` and
    `attached_lightvm_name` attributes and map them in `setDiskState`.
*   **Rationale**: Terraform users reading a disk must see the actual owner,
    even though the provider has no LightVM resource.

#### 38. `cmd/terraform-provider-rookery/internal/provider/data_source_disk.go` (Modify)

*   **Type / Scope**: Terraform disk data source schema.
*   **Proposed Changes**: Add the same two computed attributes.
*   **Rationale**: Keeps the data source consistent with the resource and the
    API.

#### 39. `cmd/terraform-provider-rookery/internal/provider/resource_disk_test.go` (Modify)

*   **Type / Scope**: Provider state-mapping test.
*   **Proposed Changes**: Assert that LightVM ownership fields populate and that
    they are null when absent.
*   **Rationale**: Prevents a silent regression in the computed attributes.

### Documentation

#### 40. `AGENTS.md` (Modify)

*   **Type / Scope**: Repository agent instructions.
*   **Proposed Changes**: Update these four bullets by exact leading text:
    `- \`design/\` is the protobuf source of truth` (Go API),
    `- \`POST /api/vms\` creates the VM` (VM/QEMU),
    `- Disks are first-class LVM-backed resources` (Disks), and
    `- LightVM inventory starts with kernels` (LightVM). In the Go API bullet,
    record that LightVM start is two-phase: image preparation and planning run
    under the shared `startCoordinator`, `mu` is held only for short
    intent/status transitions and the final QEMU start, and
    `lifecycle_generation` aborts a stale start. State that the gateway/API own
    status. Replace `vm_disks` with `workload_disks` in the VM/QEMU and Disks
    bullets; state that a disk belongs to one VM or LightVM through nullable
    owner foreign keys; state that `workload_disks` is the only owner record
    and `vms.disk_id` is inert and NULL. Update the LightVM bullet to include
    ordered disk attachments, the gateway/API planner, `run_state` paused forms,
    no per-disk boot flag or disk-root boot mode, the `APP_HOSTNAME` behavior,
    the required image, the virtiofsd `lightvm-<uuid>` paths, and the device
    budget with grandfathering.
*   **Rationale**: `AGENTS.md` is the operating guide for future changes and
    must not describe the old table, treat `vms.disk_id` as an owner, or retain
    the old "no block device" behavior. Do not commit the root
    `design_draft.md`; it is a scratch copy, not a second source of truth.

#### 41. `README.md` (Modify)

*   **Type / Scope**: High-level project description.
*   **Proposed Changes**: Add disk attachments to the LightVM feature bullet.
*   **Rationale**: The high-level feature list should reflect that LightVMs can
    use block storage.

#### 42. `docs/api.md` (Modify)

*   **Type / Scope**: API reference.
*   **Proposed Changes**: Document LightVM `disk_ids`/`disks` on every
    response; the full `replace_disk_ids` truth table; LightVM ownership on
    disk and importable-disk responses; and the updated delete behavior.
    Replace "no block device" in the start description with the actual disk
    behavior. State that the image remains required, Rookery does not implement
    a disk-root boot mode, `root=` is caller-controlled and not validated,
    `APP_HOSTNAME` may be appended, attachment order is QEMU `-drive` order
    only, and a `run_state` beginning with `paused` means QEMU is paused; an
    I/O-error pause is `paused (io-error)`. The operator must stop and start to
    recover. Do not document the internal
    `vms.disk_id` column.
*   **Rationale**: Clients need the exact field semantics and the boot
    responsibility split. The old "no block device" text would contradict the
    implementation.

#### 43. `docs/operations.md` (Modify)

*   **Type / Scope**: Operations guide.
*   **Proposed Changes**: Describe attaching disks in the LightVM tab, the guest
    mounting responsibility, throttle-class inheritance, and attachment release
    on delete. Replace the "no block device" statement in the start description
    with the disk behavior. Document the image/buildah/virtiofsd dependency,
    the `APP_HOSTNAME` behavior, that `/dev/vdX` names are guest-assigned and
    not a Rookery contract, the 24-device budget with grandfathering, and the
    pre-upgrade query for conflicting `vms.disk_id` pointers. State that
    `StopLightVM`/delete remove stale CHAP secret files, and that a LightVM
    paused by a remote-disk I/O error shows `run_state=paused (io-error)` and
    is recovered by stop/start until a resume action exists. Replace the documented
    `<run-dir>/<uuid>.virtiofs.sock`, pidfile, and log paths with the
    `lightvm-<uuid>` prefixed paths. Include the exact query:

    ```sql
    SELECT v.id, v.name, v.disk_id, dsk.name AS disk_name,
           CASE
             WHEN dsk.id IS NULL
               THEN 'skipped: disk missing'
             WHEN EXISTS (SELECT 1 FROM vm_disks x WHERE x.vm_id = v.id)
               THEN 'ignored: vm_disks has rows'
             ELSE 'skipped: disk owned by another VM'
           END AS reason
    FROM vms AS v
    LEFT JOIN vm_disks AS vd ON vd.disk_id = v.disk_id
    LEFT JOIN disks AS dsk ON dsk.id = v.disk_id
    WHERE v.disk_id IS NOT NULL
      AND (
        dsk.id IS NULL
        OR EXISTS (SELECT 1 FROM vm_disks x WHERE x.vm_id = v.id)
        OR (vd.vm_id IS NOT NULL AND vd.vm_id <> v.id)
      );
    ```

    If the query returns rows, attach a replacement disk to each affected VM
    before relying on it; a VM left with no attachment will fail start with
    “vm has no disk.” The migration logs the same rows.
*   **Rationale**: Operators need to know that Rookery exposes the device but
    does not format, mount, or support a disk-root boot mode, and that every
    LightVM still depends on the container image and runtime tools.

#### 44. `docs/architecture.md` (Modify)

*   **Type / Scope**: Architecture reference.
*   **Proposed Changes**: Update the “Storage and data model” section to
    replace `vm_disks` with `workload_disks`, describe the nullable
    `vm_id`/`lightvm_id` owner columns, the exact-one-owner CHECK, the unique
    disk constraint, and the owner/disk foreign keys. Describe `vms.disk_id` as
    an inert NULL compatibility column rather than an owner, and update the
    ownership paragraph to say that a disk belongs to one VM or LightVM.
    Record `lightvms.lifecycle_generation` as the start/stop/update token used
    by the two-phase start. Add this design to the design index in
    `docs/README.md`.
*   **Rationale**: The architecture document must show one ownership table and
    must not describe the legacy column as a second owner or retain the VM-only
    ownership statement.

### Verification Plan

*   **Protobuf generation and drift**: Run `make proto-generate` and
    `make proto-check`. Confirm only the expected generated files changed and
    that `design/openapi/rookery.swagger.json` includes the new fields.
*   **Build**: Run `make build`, which builds the UI bundle and the Go binary.
    `make build` already runs the UI typecheck; use `make ui-typecheck` only
    for a standalone typecheck without a full build.
*   **Unit and integration tests**: Run `go test ./...`. The new tests must
    cover the migration copy, legacy backfill, rows-plus-stale-pointer
    skipping, dangling-pointer skipping, position renumbering, duplicate and
    negative positions, a skipped conflicting pointer, `vm_disks` drop,
    `vms.disk_id` inert NULL, exact-owner CHECK, owner cascade, attached-disk
    restrict, unattached disks in `ListDisks`, VM/LightVM conflicts, shared-UUID
    runtime isolation, zero-disk create, the four-case replacement matrix,
    ordered attachments, template unmark/provisioning/delete on an unattached
    template including unmark clearing provisioning settings,
    `MarkDiskDeleting` (conditional update, no pre-check) and
    reclaimer refusal, `provisioning`/`deleting`/`delete_failed` attach
    rejection, throttle-class resolution, local and remote disk arguments,
    secret cleanup on success, partial preparation, start failure, stop, and
    delete, runtime status ownership, two-phase preparation outside `mu`,
    blocking prepare followed by stop/delete/update with `startCoordinator.Cancel`
    aborting the pull and `lifecycle_generation` rejecting the stale start, a
    a second start of the same LightVM waiting on `startCoordinator` while a
    different LightVM start and disk/network/VM-create writes proceed, and a
    third preparation waiting at the global preparation limit,
    autostart revalidation, status repair for an already-running LightVM, the
    24-device budget including grandfathering, preserve updates, and high-count
    start remediation, deadlock timeouts, LVM delete mutual exclusion, and the
    gateway/API error mappings.
*   **Static analysis**: Run `go vet ./...`, `golangci-lint run ./...`, and
    `gofmt -l .`. The repository CI also enforces a 20% global coverage floor
    and 80% coverage on changed Go lines.
*   **Test setup**: Use the server database passed to `serve -db` (for example
    `/tmp/rookery.db`). Upload a kernel and initramfs from the LightVM tab,
    create an LVM thin disk from the Disks tab, and create a Kouja backend per
    `docs/operations.md`. For direct database steps, use
    `sqlite3 <db> "<SQL>"`. For a pre-53 database, check out the previous
    release tag in a scratch worktree, run that binary with a fresh
    `/tmp/pre53.db`, create a VM with disks, stop it, and keep the database.
    For the conflict case, stop the old server and use SQL to attach disk B to
    VM2 in `vm_disks` (with `vms.disk_id` NULL) and set VM4's `vms.disk_id = B`;
    keep only one legacy pointer to B because the old column is unique.
*   **Manual lifecycle checks**:
    1.  Create an LVM thin disk, create a LightVM with that disk, start it, open
        `/lightvm-console.html?vm=<uuid>` (see the console procedure in
        `docs/operations.md`), and confirm QEMU has one `-drive` for the disk
        and the guest sees a virtio block device.
    2.  Put `root=` in the LightVM `append`, start, and confirm `root=` remains
        exactly as supplied and the existing `APP_HOSTNAME` behavior is
        unchanged. This checks command-line preservation, not a supported
        disk-root boot mode.
    3.  Attach a Kouja disk, start, confirm the iSCSI drive appears and
        `<run-dir>/lightvm-<uuid>.disk*.secret` does not exist after start.
        Stop the LightVM and confirm no secret file remains.
    4.  Stop the server for direct database edits. For throttle checks: (a) set
        `sqlite3 <db> "PRAGMA foreign_keys=OFF; UPDATE disks SET throttle_class_id = 9999 WHERE id = <id>;"`
        and start; confirm the class error and no QEMU/virtiofsd; (b) clear the
        class and confirm the start succeeds unthrottled; (c) assign a valid
        class and confirm the new `throttling.*` options appear.
    5.  Try to attach a LightVM-owned disk to a VM through the API and UI;
        confirm both reject it. Try to attach a VM-owned disk to a LightVM;
        confirm both reject it. The original owner keeps the disk.
    6.  Delete the LightVM and confirm every disk still exists and is
        attachable.
    7.  Open the pre-53 database and run the conflict query from
        `docs/operations.md`. Start the API and confirm the attachment is
        backfilled, positions are renumbered, `vm_disks` is gone,
        `vms.disk_id` is NULL, a conflicting pointer was skipped, a
        rows-plus-stale-pointer was ignored, and a dangling pointer was skipped,
        all with warnings rather than aborting startup.
    8.  Stop the server, attach a Kouja disk to a LightVM through the store/API
        before stopping, then run
        `sqlite3 <db> "UPDATE disks SET deletion_state='deleting', deletion_next_attempt_at=0 WHERE id=<id>;"`.
        Restart the server and confirm the reclaimer skips the attached disk and
        the volume is not reclaimed.
    9.  Create a LightVM with zero disks. Update it with
        `replace_disk_ids: true` and an empty `disk_ids`. Update again with a
        non-empty list and `replace_disk_ids: false`. Confirm all three
        operations match the truth table.
    10. Create a LightVM with 16 disks and 8 interfaces, start it, and confirm
        the combined device budget is accepted. Try 17 disks with 8 interfaces,
        or 16 disks with 9 interfaces, and confirm create/update rejects it.
        Directly insert a 30-interface grandfathered definition and confirm a rename
        succeeds, adding a disk (31) is rejected, and removing one interface
        while adding one disk (30) is allowed. Start the 30-interface definition
        and confirm `FailedPrecondition` reports the disk/interface counts and
        reduction guidance; reduce it to 24 devices and confirm it starts.
    11. Start a LightVM with a deliberately slow image pull (use a local
        registry with a delayed manifest, or a test image with a large layer)
        and confirm a disk create/update, network write, storage-backend write,
        and VM create proceed while the pull is in progress. A different LightVM
        start also proceeds; only a second start of the same LightVM waits on
        `startCoordinator`. Stop the preparing LightVM and confirm the pull is
        canceled and the next start does not wait for the full image-pull
        timeout.
    12. With a remote disk, induce a QEMU I/O pause (for example by logging out
        the iSCSI session or stopping the Kouja backend), confirm
        `run_state=paused (io-error)` and that Start returns `FailedPrecondition` with
        stop/start guidance, then stop and start the LightVM and confirm
        recovery. This is the documented restart-only recovery path until a
        LightVM resume action exists.
*   **Rollback preparation**: Stop the server, copy the database to a backup
    file, and keep it. A code rollback requires stopping the server, restoring
    that backup over the database, and starting the previous binary. The
    migration drops `vm_disks`; the inert `vms.disk_id` column does not make an
    older binary compatible.
