# Read-Only Shared Disks

*   **Author:** Rookery maintainers
*   **Status:** Accepted
*   **Date:** 2026-11-16

--------------------------------------------------------------------------------

## Overview

Today a disk can belong to exactly one workload: `workload_disks` enforces
`UNIQUE (disk_id)`, the store's attach guard rejects any second owner, and the
start planner hands each disk to one QEMU process with read/write access.

This design adds a per-disk `read_only` flag (the `READONLY` mode requested for
disks). When the flag is set:

*   Every QEMU process that attaches the disk opens it read-only, so the guest
    sees a write-protected virtio-blk device.
*   The disk may be attached to any number of VMs and LightVMs, because
    read-only QEMU opens take shared image locks and cannot corrupt the image.
*   The disk still cannot be deleted, templated, or have its read-only mode
    changed while any workload is attached.

The flag lives on the disk, not on an individual attachment. A disk is therefore
either a normal single-writer disk or a shared read-only disk; a disk is never
read-write for one guest and read-only for another at the same time. That
invariant is what makes the read-only guarantee meaningful and keeps QEMU's
image locking simple.

The change covers both workload types because they share `workload_disks` and
`internal/qemu.buildDiskArgs`: VMs and LightVMs get the behavior together.

--------------------------------------------------------------------------------

## Goals

*   Add `read_only` to the disk inventory (create, update, get, list) with a
    default of `false`, preserving today's behavior for every existing disk.
*   Allow a read-only disk to be attached to multiple VMs and LightVMs.
    Read-write disks keep the current one-owner rule.
*   Emit `readonly=on` for every QEMU drive built from a read-only disk,
    including local LVM thin volumes, host passthrough devices, and remote
    iSCSI disks, for both `internal/qemu.StartWithPlan` and
    `StartLightVMWithVirtioFSAndPlan`.
*   Keep the invariant enforced in SQLite (not only in Go), so a second
    read-write attachment fails even under concurrent or out-of-band writes.
*   Report all attachments on disk responses so the UI and API can show that a
    disk is shared and by whom.
*   Let the UI create a shared disk, show a read-only badge and the list of
    owning workloads, and offer read-only disks in the VM and LightVM disk
    pickers even when they already have owners.
*   Expose the flag in the Terraform provider for the `rookery_disk` resource
    and data source.

--------------------------------------------------------------------------------

## Non-goals

*   No per-attachment read-only override. A read-only attachment to one guest
    and a read-write attachment to another is explicitly unsupported.
*   No writable sharing (`share-rw`/cluster filesystems) and no distributed
    locking between hosts.
*   No hot-plug. A disk set is replaced only while the workload is stopped, as
    today.
*   No automatic conversion of existing disks. Existing disks migrate with
    `read_only = false` and keep the one-owner rule.
*   No change to boot media, templates, media records, or ephemeral LightVM
    rootfs.
*   No new endpoints. The flag is a field on the existing disk RPCs.

--------------------------------------------------------------------------------

## Semantics

| State | Attach to second workload | Guest access | Delete / template |
| --- | --- | --- | --- |
| `read_only = false`, no owner | allowed | read/write | delete allowed |
| `read_only = false`, one owner | rejected (`ErrDiskInUse`) | read/write | delete rejected until detached |
| `read_only = true`, any owners | allowed | read-only | delete/template rejected until detached |

Rules:

1.  `read_only` may be set at disk creation for LVM thin, host-device, Kouja,
    and template-clone disks.
2.  `read_only` may be changed through `PUT /api/disks/{id}` only while the disk
    has no attachments. Requiring a detach avoids converting a disk that a
    running QEMU process still has open read-write.
3.  A template disk cannot be `read_only`; templates are clone sources and are
    never attached.
4.  `read_only` applies to the attachment as a snapshot. `workload_disks`
    stores a copy so the single-writer unique index can be partial.
5.  A read-only disk can be the boot disk or a data disk, for either workload.
    Rookery does not decide how the guest mounts it.
6.  All attached workloads must be detached before the flag can be cleared or
    the disk deleted. Deleting the last workload releases the attachment rows
    through the existing cascade.

### Remote (Kouja) disks

The local QEMU side is identical (`readonly=on`). Two extra points apply:

*   `internal/kouja.Preparer.validateLiveExport` currently rejects a start when
    a fresh observation reports an active session. For a read-only disk this
    gate must be skipped: another read-only QEMU session is expected. The
    target's own session limit still applies, and the start error must surface
    it.
*   The configured iSCSI initiator/ACL must permit multiple sessions for that
    initiator. The plan keeps the existing backend config and documents the
    requirement; a per-disk capability or multi-initiator config is out of
    scope.

If multi-session support cannot be guaranteed for a given backend, the
follow-up is a driver capability flag that rejects a second read-only
attachment at attach time with a clear `FailedPrecondition`, rather than a
start-time failure.

--------------------------------------------------------------------------------

## Database changes

New migration `internal/store/migrations/0058_readonly_shared_disks.sql`:

1.  `ALTER TABLE disks ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0;`
2.  Rebuild `workload_disks` to remove `UNIQUE (disk_id)` (SQLite cannot drop an
    inline constraint) and add the attachment snapshot:

    ```sql
    CREATE TABLE workload_disks_new (
        vm_id      TEXT REFERENCES vms(id) ON DELETE CASCADE,
        lightvm_id TEXT REFERENCES lightvms(id) ON DELETE CASCADE,
        disk_id    INTEGER NOT NULL REFERENCES disks(id) ON DELETE RESTRICT,
        position   INTEGER NOT NULL CHECK (position >= 0),
        serial     TEXT NOT NULL DEFAULT '',
        read_only  INTEGER NOT NULL DEFAULT 0,
        CHECK ((vm_id IS NOT NULL) <> (lightvm_id IS NOT NULL)),
        UNIQUE (vm_id, position),
        UNIQUE (lightvm_id, position)
    );
    INSERT INTO workload_disks_new (vm_id, lightvm_id, disk_id, position, serial, read_only)
    SELECT vm_id, lightvm_id, disk_id, position, serial, 0 FROM workload_disks;
    DROP TABLE workload_disks;
    ALTER TABLE workload_disks_new RENAME TO workload_disks;
    ```

3.  Replace the old single-owner constraint with a partial unique index. Only
    read-write attachments compete for the disk:

    ```sql
    CREATE UNIQUE INDEX idx_workload_disks_single_writer
        ON workload_disks(disk_id) WHERE read_only = 0;
    CREATE INDEX idx_workload_disks_disk
        ON workload_disks(disk_id);
    ```

4.  Add a guard trigger as defense in depth. Application inserts read the flag
    from `disks` in the same statement, but the trigger prevents a mismatch if
    another writer appears:

    ```sql
    CREATE TRIGGER workload_disks_read_only_matches_disk
    BEFORE INSERT ON workload_disks
    WHEN NEW.read_only <> (SELECT read_only FROM disks WHERE id = NEW.disk_id)
    BEGIN
        SELECT RAISE(ABORT, 'workload disk read_only does not match disk');
    END;
    ```

5.  Add a trigger that rejects demoting a shared disk if attachment rows ever
    get out of sync (the API restricts the change to unattached disks, so this
    is a backstop):

    ```sql
    CREATE TRIGGER disks_read_only_single_writer
    BEFORE UPDATE OF read_only ON disks
    WHEN NEW.read_only = 0 AND OLD.read_only = 1
      AND (SELECT COUNT(*) FROM workload_disks WHERE disk_id = NEW.id) > 1
    BEGIN
        SELECT RAISE(ABORT, 'disk is attached to multiple workloads');
    END;
    ```

`isUniqueViolation` continues to classify the partial unique index, so
`workloadDiskAttachError` keeps returning `DiskInUseError` for a second
read-write attachment. The trigger errors need a store mapping to
`ErrDiskInUse`/a validation error in `workloadDiskAttachError`.

The migration test fixture (`internal/store/migration_test.go`) must be updated:
raw `INSERT` statements now need the `read_only` column, the same-disk
read-write insert still fails, and a read-only disk fixture can insert twice.

--------------------------------------------------------------------------------

## Store changes (`internal/store`)

`store.Disk` and `store.VMDisk`:

*   Add `ReadOnly bool` to both.
*   Add `Attachments []DiskAttachment` to `Disk` (kind, id, name, position,
    read_only); keep `AttachedVMID`/`AttachedLightVMID` populated only when
    exactly one attachment exists, for older clients.
*   `DiskAttachedToAnyWorkload` becomes `len(disk.Attachments) > 0` (falling
    back to the legacy fields when `Attachments` is empty).

Queries:

*   `diskSelect` currently joins `workload_disks` → `vms`/`lightvms`. That join
    returns one row per attachment and would duplicate disks. Remove the join
    and the four attachment columns from `scanDisk`.
*   Add `loadDiskAttachments(ctx, diskIDs)` with one query over
    `workload_disks` joined to `vms`/`lightvms`, ordered by `disk_id,
    position`. Call it from `ListDisks` (one query for the whole list),
    `GetDisk`, and `GetDiskByName` (one disk).
*   `loadWorkloadDisks` selects `d.read_only` and sets `VMDisk.ReadOnly`.

Write paths:

*   `CreateDisk` persists `read_only` and rejects `read_only && is_template`.
*   Add `SetDiskReadOnly(ctx, id, readOnly)`: reject templates, reject while
    `workload_disks` has any row, then update `disks.read_only`. Implement in a
    transaction so the count and update cannot interleave (the pool already has
    one connection).
*   Attachment inserts (`CreateVM`, `SetVMDisksWithSerials`, `CreateLightVM`,
    `UpdateLightVM`) insert `read_only` by selecting it from `disks` in the
    same statement, e.g.
    `INSERT INTO workload_disks (...) SELECT ?, NULL, id, ?, ?, read_only FROM disks WHERE id = ?`,
    and return `ErrDiskNotFound` when no row was inserted.
*   `checkDiskAvailableForAttach` returns nil for a read-only disk after the
    template/deleting checks; for read-write disks it returns `ErrDiskInUse`
    when `len(Attachments) > 0`.
*   `checkDiskOwnership` (used by delete, template, provisioning, and
    `MarkDiskDeleting`) treats any attachment as in use; the
    `DiskInUseError` names the first owner and should include the attachment
    count when greater than one.
*   `DeleteDisk`, `MarkDiskDeleting`, and `internal/storage/reclaimer` keep
    requiring zero attachments; update them to use `Attachments` unless the
    helper already covers them.

--------------------------------------------------------------------------------

## QEMU changes (`internal/qemu`)

`buildDiskArgs` is the single place that builds local and remote drives, and it
is used by both VM and LightVM starts:

*   Local branch: append `,readonly=on` to the `-drive` options when
    `disk.ReadOnly`.
*   iSCSI branch: append `,readonly=on` to the `file.driver=iscsi,...` string.
*   Keep `if=virtio` for serial-less disks and the explicit
    `virtio-blk-pci,serial=` form for disks with a serial; `readonly=on` is a
    block-backend option and works with both.
*   Do not add `share-rw=on`. That option declares writable sharing and is not
    needed for read-only opens; QEMU's read-only image opens take compatible
    shared locks.
*   `validateVMDisks` and `ensureDiskDevice` stay unchanged: LVM activation and
    host-device validation still operate on the host device, while QEMU opens
    it read-only.
*   `imageLockHolders` should ignore read-only disks so a read-only start
    failure is not reported as a lock conflict with another guest.
*   `prepareDiskSecrets` is already per-VM (`<vm-id>.disk<id>.<protocol>.secret`),
    so shared Kouja disks do not collide.

Verification: the QEMU manual says conflicting access modes cause subsequent
opens to fail, meaning multiple read-only opens are compatible
([Disk image file locking](https://www.qemu.org/docs/master/system/images.html)).
Add an integration test that starts two VMs against the same read-only raw file
when `/dev/kvm` is available, and a unit test asserting `readonly=on` in both
drive forms.

--------------------------------------------------------------------------------

## API changes (`design/proto/rookery/v1/disk.proto`)

*   `Disk`: add `bool read_only = 30;` and
    `repeated DiskAttachment attachments = 31;`.
*   New message:

    ```proto
    message DiskAttachment {
      string workload_kind = 1;   // "vm" or "lightvm"
      string workload_id = 2;
      string workload_name = 3;
      int32 position = 4;
      bool read_only = 5;
    }
    ```

*   `VMDisk`: add `bool read_only = 18;` (returned by VM and LightVM reads and
    used by the UI).
*   `CreateDiskRequest`: add `optional bool read_only = 13;`.
*   `UpdateDiskRequest`: add `optional bool read_only = 8;`.
*   Keep the existing `attached_vm_*` / `attached_lightvm_*` fields for
    compatibility; document that they are set only for a single attachment and
    that `attachments` is authoritative.

Run `make proto-generate` / `make proto-check` to regenerate `design/gen` and
`design/openapi`.

--------------------------------------------------------------------------------

## Gateway changes (`internal/gateway`)

*   `disk_writes.go`
    *   `CreateDisk` / `createHostDeviceDisk` / `createKoujaDisk`: pass
        `request.GetReadOnly()` into `store.Disk`, reject `read_only` with
        `is_template`, and reject read-only on partitions/devices that cannot
        be opened read-only if the host validator already forbids them.
    *   `UpdateDisk`: handle `request.ReadOnly` by calling `SetDiskReadOnly`
        before the other mutable settings; map "attached" to
        `codes.FailedPrecondition`/`Aborted` with a message that says to detach
        the disk first.
*   `readonly.go`: `diskToProto` maps `ReadOnly` and `Attachments`.
*   `gateway.go`: `vmDiskToProto` maps `ReadOnly`.
*   `vm_create.go` (create and update pre-checks) and
    `lightvm_instances.go`: allow an already-attached disk when
    `disk.ReadOnly`; otherwise keep the current in-use error. The store remains
    authoritative.
*   `host_interfaces.go`: a registered read-only host-device disk that is
    attached should remain registrable state but the reason text should say
    "read-only, shared". It is still one registration; no duplicate record.
*   `diskInUseError` already formats the owner; extend it for multiple
    attachments if `DiskInUseError` gains a count.

--------------------------------------------------------------------------------

## Remote storage changes (`internal/kouja` / `internal/storage`)

*   `Preparer.validateLiveExport`: skip the `errSessionActive` check when the
    disk is read-only, or take a `readOnly` argument so the gate is explicit.
*   `KoujaDriver.PrepareStart` should not wait/retry on `ErrSessionActive` for a
    read-only disk.
*   Document the requirement that the export's initiator/ACL allows more than
    one login for that initiator. If the driver cannot, return a clear error at
    start and add the capability flag as a follow-up.

--------------------------------------------------------------------------------

## UI changes

*   `ui/src/inventory/types.ts`: add `read_only?: boolean` to `Disk` and
    `VMDisk`; add `attachments?: DiskAttachment[]`.
*   `ui/src/inventory/disks/DiskDialog.tsx`: add a "Read-only / shareable"
    checkbox on create (disabled for templates) with a hint that QEMU attaches
    it read-only and it can be shared across VMs/LightVMs.
*   `ui/src/inventory/disks/DisksTab.tsx`: add a `read-only` badge and an owner
    column/cell that renders `attachments` (e.g. "shared: vm-a, lightvm-b"),
    not just the singular legacy fields. The delete guard already blocks
    attached disks.
*   `ui/src/inventory/vms/VMDialog.tsx`: in `availableDisks`, keep a disk when
    `disk.read_only` is true even if it has owners; keep the current rule for
    read-write disks. Show a lock/read-only marker on selected rows.
*   `ui/src/inventory/lightvm/LightVMDialog.tsx`: same filter change; reuse the
    existing read-only share concept visually but label it "disk is read-only"
    to avoid confusion with virtiofs shares.
*   Optionally add a `Read-only` action/toggle on unattached disks; otherwise
    the Disk dialog covers create and the API covers update.
*   Rebuild with `make ui-build`; do not edit `internal/webui/static/build`.

--------------------------------------------------------------------------------

## Terraform provider

*   `resource_disk.go`: add `read_only` (optional/computed bool) to `diskModel`
    and schema, send it on create and update, and read it in `setDiskState`.
    Add computed `attachments` if the nested list is cheap to model; otherwise
    at minimum expose `read_only` and keep the legacy attached fields.
*   `data_source_disk.go`: add `read_only` and the attachment summary.
*   Update the provider README/examples if it documents disk attributes.

--------------------------------------------------------------------------------

## Documentation

The current single-owner wording appears in several places and must be updated
with the implementation:

*   `docs/api.md` (~line 361 "A disk has exactly one workload owner", ~line 436
    host passthrough, ~line 939 LightVM update): disk response fields,
    `read_only` on create/update, the shared-disk rule, and the changed `PUT`
    semantics.
*   `docs/architecture.md` (~line 220 "a disk can belong to only one VM at a
    time").
*   `docs/operations.md` (~line 282 "same single-owner rule").
*   `AGENTS.md` disk bullet ("A disk can attach to only one VM or LightVM").
*   `docs/designs/lightvm-disk-attachments.md` is the historical design for the
    attachment table; leave it as the record of that change, but note in this
    doc that the `UNIQUE (disk_id)` rule it introduced is replaced by the
    partial unique index.
*   This design doc.

--------------------------------------------------------------------------------

## Test plan

Store tests (`internal/store`):

*   Read-only disk attaches to two VMs and a LightVM; `GetDisk` reports three
    attachments in position order.
*   Read-write disk still rejects the second owner, including across VM and
    LightVM.
*   `SetDiskReadOnly(true)` while attached fails; succeeds while unattached;
    `SetDiskReadOnly(false)` with multiple attachments fails.
*   Read-only + template is rejected.
*   Delete and `MarkDiskDeleting` are blocked until every attachment is gone,
    then succeed.
*   Migration: existing rows keep `read_only = 0`; the partial unique index
    still rejects two read-write rows; two read-only rows succeed.
*   `ListDisks` returns each disk once when it has multiple attachments.

QEMU tests:

*   `TestBuildDiskArgs...` asserts `readonly=on` for local (with and without
    serial) and iSCSI remote drives, and its absence for read-write disks.
*   LightVM args include the same option.
*   Optional integration test with two read-only QEMU starts against one image.

Gateway tests:

*   Create a read-only disk, attach it to two VMs; `ListDisks`/`GetDisk`
    expose `read_only` and both attachments.
*   `PUT /api/disks/{id}` with `read_only` while attached returns a
    precondition error; while unattached it succeeds.
*   VM/LightVM create/update error paths still return `Aborted` for a
    read-write disk owned by another workload.

UI verification: `make ui-build` (tsc + esbuild) and a manual pass over create,
edit, share, and delete flows.

Final: `go test ./...`, `go vet ./...`, `golangci-lint run ./...`,
`gofmt -l .`, `make proto-check`, and the generated UI drift check.

--------------------------------------------------------------------------------

## Risks and edge cases

*   **QEMU image locking.** Read-only opens must take shared locks. The design
    relies on documented QEMU behavior; the integration test is the gate. If a
    QEMU build still takes an exclusive lock, add `file.locking=off` only for
    read-only drives (never for read-write ones) or reject multi-attach at
    start.
*   **Converting a disk while running.** Prevented by requiring zero
    attachments for `read_only` changes.
*   **Kouja multi-session.** The session-active gate must be conditional, and
    the target must allow multiple logins. Add the capability flag if a backend
    cannot.
*   **Legacy API consumers.** `attached_vm_id`/`attached_lightvm_id` become
    ambiguous for shared disks. They stay populated for the single-attachment
    case; new clients should use `attachments`.
*   **Duplicate disk rows.** Removing the join from `diskSelect` is mandatory;
    without it every shared disk appears once per attachment.
*   **Deletion races.** The partial unique index plus the "no attachments"
    delete guard keep disk deletion safe while a shared read-only disk is in
    use.

--------------------------------------------------------------------------------

## Suggested implementation order

1.  Migration + store model/queries/guards + store tests.
2.  Proto changes and regeneration; gateway mapping and write paths.
3.  QEMU `readonly=on` + unit tests (the smallest user-visible behavior).
4.  Kouja session gate.
5.  UI create/edit/list/pickers.
6.  Terraform provider.
7.  Docs and full verification.

The migration and store layer should land first because every other layer
depends on the attachment model; the QEMU flag can be reviewed independently
afterward.

--------------------------------------------------------------------------------

## Open decision

This plan models `READONLY` as a **disk** attribute, so a disk is uniformly
read-only for every guest and the SQL invariant is a partial unique index over
read-write attachments. The alternative is a **per-attachment** flag: one disk
could be read-only for one VM and read-write for another, with rules preventing
mixed attachments. That is more flexible but needs a per-attachment API, UI
toggles, and a much harder invariant (no read-write attachment may coexist with
any read-only attachment). The disk-level flag is recommended unless the
per-attachment workflow is a requirement.
