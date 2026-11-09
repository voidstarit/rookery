# Reusable QEMU Disk Throttling Classes

*   **Author:** Rookery maintainers
*   **Status:** Proposed
*   **Date:** 2026-11-08

--------------------------------------------------------------------------------

## Overview

Rookery currently stores QEMU block-throttling limits as raw `read_bps` and
`write_bps` numbers on each disk. The same numbers are entered again for every
disk that needs the same policy, and there is no single place to see or change
that policy.

This design introduces a named, reusable **disk throttling class**: a record
that holds a set of QEMU block-throttling limits. A disk may reference one
class. When QEMU starts a VM, Rookery resolves each attached disk's class into
the corresponding `throttling.*` arguments. Disks without a class are
unlimited. The raw per-disk fields are removed entirely; a disk's limits exist
only through its assigned class.

The feature applies to the QEMU block layer only. It is separate from the
existing cgroup v2 per-device `io.max`/`io.weight` limits, which remain a VM
cgroup concern.

--------------------------------------------------------------------------------

## Problem Statement

Today the only way to limit a disk's throughput is to type byte-per-second
values into that disk's own record. This has three costs:

1.  **No shared definition.** An operator who wants every "standard" disk to
    read at 500 MiB/s must repeat the same two numbers on every disk. A policy
    change means editing every disk individually, with no guarantee that all of
    them end up identical.
2.  **No policy inventory.** There is no list of the throttling policies in use.
    Auditing which disks are limited, and to what, requires inspecting each
    disk. The UI's per-disk **Limits** dialog has no name, description, or
    reusable object.
3.  **No policy object at input.** The UI and API expose two independent
    integers per disk. Values are entered independently with no policy to
    validate against, so inconsistent values are not detected. The Terraform
    provider has no per-disk bandwidth attributes today; adding class selection
    there is new capability, not a removal.

Replacing per-disk numbers with a class inventory gives operators one place to
define a policy, one place to change it, and a stable name to attach to disks.

--------------------------------------------------------------------------------

## Goals

*   Provide a single CRUD inventory of named QEMU disk throttling classes, with
    a name, an optional description, and the limits QEMU will receive.
*   Model every QEMU block-throttling option except `throttling.group`:
    total/read/write BPS and IOPS, burst maximums, burst lengths, and IOPS size.
    The design owner confirmed QEMU parity is an explicit product requirement
    for this iteration, and the design validates QEMU's own rules. Deferring
    those fields would not satisfy the requirement. `throttling.group` is the
    explicit exception: it is a shared-quota feature with group lifecycle and
    reference management,
    not a per-drive limit.
*   Allow disk creation and disk update to select an optional throttling class.
    Omitting the class means unlimited.
*   Remove the raw per-disk `read_bps`/`write_bps` fields from the API,
    database schema, and UI. The Terraform provider gains class selection; it
    had no per-disk bandwidth fields to remove.
*   Resolve the assigned class to QEMU `throttling.*` arguments at VM start,
    using the class's current values, so a class edit affects every assigned
    disk on its next start. This applies to manual start, API autostart, and
    planned remote-storage starts.
*   Show the assigned class on disk and VM responses, and show the configured
    limits in the Disks UI. Replace the per-disk **Limits** dialog with class
    selection.
*   Prevent a template disk from carrying a throttling class; converting a disk
    to a template clears its class.
*   Block deleting a class that is still assigned to a disk, and name the
    assigned disks in the conflict.
*   Keep the API, protobuf definitions, generated gateway, Terraform provider,
    docs, and tests consistent with the new model.

--------------------------------------------------------------------------------

## Non-Goals

*   Replacing or changing cgroup v2 `io.max`/`io.weight` limits or cgroup
    classes. Those limit kernel-level I/O per device and are configured through
    `cgroup_settings`; this feature is QEMU block throttling only.
*   Changing limits on a running VM. QEMU applies these arguments at start, so
    edits take effect on the next VM start. Live QMP `block_set_io_throttle` is
    out of scope.
*   Network, CPU, memory, or whole-VM throttling.
*   Fair-share scheduling, per-VM aggregate quotas, or cross-disk burst
    borrowing. Each disk's limit stands alone. A VM with N disks assigned to the
    same class can consume N times that class's limit.
*   Historical throttling metrics or alerts.
*   Automatic class selection based on disk size, backend, or labels.
*   Backward compatibility with the removed raw fields or migration of their
    values. The code stops using the raw columns in this release; a later
    release drops them after the cutover, with a pre-upgrade backup. There is
    no backfill, compatibility alias, or dual-write period.
*   A QEMU capability probe at startup. The design records a tested QEMU
    version and maps start failures to the disk and class.

--------------------------------------------------------------------------------

## Critical User Journeys (CUJs)

### CUJ 1: Define a reusable throttling class

*   **Given**: an operator is on the Disks tab and no class named
    `web-standard` exists.
*   **When**: the operator creates `web-standard` with directional BPS limits
    (500 MiB/s read, 524,288,000 bytes/s; 300 MiB/s write, 314,572,800
    bytes/s) and a total IOPS limit of 2,000.
*   **Then**: the class appears in the throttling class panel with its limits
    summarized, reports zero assigned disks, and is selectable in the disk
    creation dialog.

### CUJ 2: Create a disk with a throttling class

*   **Given**: the `web-standard` class exists and a storage backend is ready.
*   **When**: the operator creates a disk and selects `web-standard`.
*   **Then**: the disk response reports the class id and name; the class list
    provides the configured limits; the VM start path emits the matching QEMU
    `throttling.*` arguments; no raw `read_bps` or `write_bps` value is sent or
    stored.

### CUJ 3: Change a class used by running VMs

*   **Given**: `web-standard` is assigned to disks attached to running VMs.
*   **When**: the operator changes the class's read limit.
*   **Then**: the running QEMU processes keep their current limits; each
    affected VM receives the new limits the next time it starts. The UI labels
    the value as configured and applies on the next start.

### CUJ 4: Delete a class that is still assigned

*   **Given**: `web-standard` is assigned to at least one disk.
*   **When**: the operator tries to delete the class.
*   **Then**: the API rejects the delete with a conflict that names the number
    of assigned disks and up to five disk names; after every disk is reassigned
    or set to unlimited, the delete succeeds.

### CUJ 5: Convert a throttled disk to a template

*   **Given**: an unattached disk has `web-standard` assigned.
*   **When**: the operator marks the disk as a template.
*   **Then**: an explicit class in the same request is rejected, an omitted
    class is cleared, the template has no throttling limits, and a clone created
    from it may choose its own class.

### CUJ 6: Create an unthrottled disk

*   **Given**: the class inventory has entries.
*   **When**: the operator creates a disk without selecting a class.
*   **Then**: the disk is stored with no class reference and QEMU receives no
    `throttling.*` arguments for it.

### CUJ 7: Cap burst and IOPS for a database disk

*   **Given**: a database VM needs a sustained bandwidth cap with temporary
    bursts and an IOPS ceiling.
*   **When**: the operator creates a class with `bps_total`,
    `bps_total_max`, `bps_total_max_length`, `iops_total`, and `iops_size`.
*   **Then**: the class validates, the disk uses the class, and the QEMU start
    arguments include the total BPS, burst, burst length, total IOPS, and IOPS
    size options.

--------------------------------------------------------------------------------

## Technical Plan (Architecture)

### 1. Design principles

1.  **One inventory.** A throttling class is the only place where QEMU block
    limits are defined. Disks hold a reference, never a copy.
2.  **Resolve at start.** The class limits are read when a VM starts. Editing a
    class changes every assigned disk on its next start; a running VM is not
    modified. "Live" is reserved for QMP changes to a running process.
3.  **QEMU fidelity.** The class model accepts every QEMU throttling option
    except `throttling.group`, and validates QEMU's positive-base, burst, cap,
    and overflow rules before a class is saved.
4.  **Zero means unset.** Every numeric limit is optional. Absent or zero means
    unset; negative values are rejected. The UI's total/read-write selector is
    local dialog state, and the API derives the mode from the positive values.
5.  **No raw disk fields.** The `disks.read_bps` and `disks.write_bps` columns
    and their API and UI inputs are removed. There is no second way to set a
    disk limit.

### 1a. Terminology and presence

*   **Disk sources:** LVM thin (a Rookery-managed thin volume), kouja (a remote
    iSCSI or NVMe/TCP backend), and host-device (a passed-through host block
    device). A storage backend is the record that supplies one of these
    sources.
*   **Disk lifecycle:** `provisioning` (being created), ready (usable),
    `deleting` (kouja export cleanup), and `delete_failed` (the reclaimer has
    not completed or will retry). The reclaimer advances deletion states.
*   **Start paths:** manual start, API autostart (API startup or VPP restart
    reconciliation), and planned start (`vmstart.Planner` prepares remote
    attachments).
*   **Gateway and store:** the generated gRPC gateway exposes the API; the
    store is the SQLite persistence layer.
*   **cgroup class vs throttling class:** a cgroup class is a kernel cgroup v2
    policy in `cgroup_settings`; a throttling class is the QEMU block policy in
    this feature.
*   **Presence:** `CreateDiskRequest.throttle_class_id` is plain `int64`
    (zero = unlimited). `UpdateDiskRequest.throttle_class_id` is `optional
    int64` (absent preserves, zero clears). The 19 `DiskThrottleLimits` fields
    are plain `int64` with zero meaning unset. Class `name`, `description`, and
    `limits` use proto3 presence; an absent `limits` preserves the current
    limits and is validated, while a present `limits` replaces them.

### 2. Component flow

```text
 Disks UI / Terraform / API client
              │
              ▼
   DiskThrottleService (gateway)
              │  trim name/description, validate limits
              ▼
   store.disk_throttle_classes ──────────────┐
              │                              │
              │ throttle_class_id            │ limits JSON
              ▼                              │
        disks.throttle_class_id ─────────────┘
              │
              │ GetVM / ListVMs join the class
              ▼
   qemu.buildDiskArgs / diskThrottleArgs
              │
              ▼
   -drive ...,throttling.bps-read=...,
            throttling.iops-total=...
```

The API server never caches class limits. Every VM start path already loads the
VM from the store immediately before launching QEMU, so the join returns the
current class values. Autostart uses `ListVMs`, which performs the same join.

### 3. Data model

A new table stores the class:

| Column | Type | Purpose |
| --- | --- | --- |
| `id` | `INTEGER PRIMARY KEY AUTOINCREMENT` | Stable class id. |
| `name` | `TEXT NOT NULL UNIQUE COLLATE NOCASE` | Human-facing inventory key. |
| `description` | `TEXT NOT NULL DEFAULT ''` | Free-form description; empty when not provided. |
| `limits` | `TEXT NOT NULL` | JSON-encoded `DiskThrottleLimits`; only set values are stored. |
| `created_at` | `INTEGER NOT NULL` | Creation time in Unix milliseconds. |
| `updated_at` | `INTEGER NOT NULL` | Last edit time in Unix milliseconds. |

`disks` changes:

*   Add `throttle_class_id INTEGER NULL REFERENCES disk_throttle_classes(id)
    ON DELETE RESTRICT`.
*   Add an index on `throttle_class_id`.
*   Drop `read_bps` and `write_bps` in a later migration, after the Go cutover.

JSON is chosen for `limits` because the repository already stores structured
configuration as JSON (`storage_backends.config`, `cgroup_settings.config`,
`acls.rules`), and because one validated object keeps the 19 values together.
The API remains fully typed, so JSON is not exposed as an unvalidated free-form
string. The table is global; it is not scoped to a storage backend.

The store is the only writer. `limits` has no default. On read, the store
decodes and validates the JSON. A malformed or manually corrupted row does not
abort the disk or VM list: `loadVMDisks` or `scanVM` sets the internal
`VMDisk.ThrottleError` and leaves `Throttle` nil. `qemu.Start` checks
`ThrottleError`, fails that VM's start with the disk and class, and logs the
error, so the disk is not silently run unthrottled. The class response exposes
`validation_error`, and the UI disables assignment to an invalid class. The
rest of the list remains usable. A non-NULL `throttle_class_id` with no
matching class row is also corrupt: set `ThrottleError` so the start fails
rather than running unlimited. This is a deliberate exception to the
repository's usual internal-error behavior for JSON config because this table
is on the VM autostart path; a single bad row must not block every VM. The
other JSON configs keep their existing read behavior.

### 4. Limits model and validation

`internal/diskthrottle.Limits` has 19 plain `int64` fields. The proto uses
plain `int64`; the Go struct uses `json:",omitempty"`. Zero means unset for
both the API and the stored JSON, and negative values are rejected. The only
optional fields are the disk's `throttle_class_id` and the class update's
`name`, `description`, and `limits`, where absent has distinct semantics.

The two families are BPS and IOPS. Each family has a total group, a read/write
group, and a burst length. The mode is inferred:

*   **none**: no positive base in the family.
*   **total**: `bps_total` or `iops_total` is positive; the family's read/write
    values must be zero.
*   **directional**: at least one of `bps_read`/`bps_write` or
    `iops_read`/`iops_write` is positive; the family's total values must be
    zero.

`internal/diskthrottle.Validate` enforces these rules before any write:

1.  At least one of the BPS or IOPS families has a positive base value.
2.  A family with a positive total base has all directional values unset;
    a family with positive directional bases has all total values unset.
3.  A family with no positive base has all of its values unset.
4.  Every `*_max` requires its corresponding positive base and must be greater
    than or equal to it.
5.  Every `*_max_length` requires the corresponding `*_max`, must be greater
    than zero, must not exceed `UINT_MAX` (4,294,967,295), and must satisfy
    QEMU's burst-length check: `*_max_length <= THROTTLE_VALUE_MAX / *_max`.
    The `UINT_MAX` bound keeps burst lengths within the 32-bit range QEMU's
    throttling configuration uses; Rookery enforces it before QEMU sees the
    value.
6.  Every base and `*_max` value must not exceed QEMU's
    `THROTTLE_VALUE_MAX` (1,000,000,000,000,000).
7.  `iops_size` is a byte count, must be greater than zero, and requires a
    positive IOPS base.
8.  When a `*_max` is set and its length is omitted, QEMU defaults the burst
    length to one second.

The rules match the QEMU 10.0.13 `throttle_is_valid` behavior for positive
base values, burst dependency, burst-length overflow, and `THROTTLE_VALUE_MAX`.
The store calls `Validate` as the authoritative check. The gateway calls it
before the store for a clear API error. The UI performs lightweight checks
(non-negative values, per-family exclusivity, required base before a burst) and
leaves the QEMU caps and overflow rules to the server; it shows the server
error when a rule still fails. The server remains authoritative.

Units:

*   BPS bases and `*_max`: bytes per second (BPS). A `*_max` uses the same unit
    as its base.
*   IOPS bases and `*_max`: I/O operations per second.
*   `*_max_length`: seconds.
*   `iops_size`: bytes.

### 5. QEMU mapping

`diskThrottleArgs` in `internal/qemu/qemu.go` renders the class limits. Each set
value becomes one `throttling.*` key inside the drive's `-drive` argument:

| Model value | QEMU option |
| --- | --- |
| `bps_total` | `throttling.bps-total` |
| `bps_read` | `throttling.bps-read` |
| `bps_write` | `throttling.bps-write` |
| `bps_total_max` | `throttling.bps-total-max` |
| `bps_read_max` | `throttling.bps-read-max` |
| `bps_write_max` | `throttling.bps-write-max` |
| `bps_total_max_length` | `throttling.bps-total-max-length` |
| `bps_read_max_length` | `throttling.bps-read-max-length` |
| `bps_write_max_length` | `throttling.bps-write-max-length` |
| `iops_total` | `throttling.iops-total` |
| `iops_read` | `throttling.iops-read` |
| `iops_write` | `throttling.iops-write` |
| `iops_total_max` | `throttling.iops-total-max` |
| `iops_read_max` | `throttling.iops-read-max` |
| `iops_write_max` | `throttling.iops-write-max` |
| `iops_total_max_length` | `throttling.iops-total-max-length` |
| `iops_read_max_length` | `throttling.iops-read-max-length` |
| `iops_write_max_length` | `throttling.iops-write-max-length` |
| `iops_size` | `throttling.iops-size` |

`Limits.QEMUOptions() string` returns a comma-prefixed fragment, for example
`,throttling.bps-read=524288000,throttling.iops-total=2000`. It returns an
empty string when no values are set. `diskThrottleArgs` returns that fragment
or an empty string, so the existing `%s` call sites for local and remote drives
remain unchanged. Unset values are omitted.

The arguments are appended to both local and remote `-drive` definitions, so
kouja, host-device, and LVM disks use the same path. The internal
`store.VMDisk` carries the resolved `Throttle *diskthrottle.Limits` populated
by `loadVMDisks`; the proto `VMDisk` exposes only the class id and name, not
the full payload. `throttling.group` is never emitted. The throttling options
are tested with QEMU 10.0.13; earlier versions are not verified. There is no
fallback for older QEMU versions and no startup capability
probe. A Rookery-side `ThrottleError` includes the disk name and class id. A
QEMU-side argument failure may only name the offending option in QEMU's stderr;
the error wrapper includes the rendered throttling fragment for the disk so the
operator can correlate it.

### 6. API surface

A new `DiskThrottleService` owns the inventory:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/disk-throttle-classes` | List classes with `disk_count`. |
| `GET` | `/api/disk-throttle-classes/{id}` | Get one class. |
| `POST` | `/api/disk-throttle-classes` | Create a class. Returns `201`. |
| `PUT` | `/api/disk-throttle-classes/{id}` | Update name, description, or limits. |
| `DELETE` | `/api/disk-throttle-classes/{id}` | Delete an unused class. Returns `204`. |

The protobuf contract is:

```text
message DiskThrottleLimits {
  // All values are >= 0. Base and *_max values are <= 1e15;
  // every *_max_length is in seconds, <= UINT_MAX (4,294,967,295),
  // and must satisfy QEMU's overflow check.
  int64 bps_total = 1;              // bytes per second
  int64 bps_total_max = 2;          // bytes per second
  int64 bps_total_max_length = 3;   // seconds
  int64 bps_read = 4;
  int64 bps_read_max = 5;
  int64 bps_read_max_length = 6;
  int64 bps_write = 7;
  int64 bps_write_max = 8;
  int64 bps_write_max_length = 9;
  int64 iops_total = 10;
  int64 iops_total_max = 11;
  int64 iops_total_max_length = 12;
  int64 iops_read = 13;
  int64 iops_read_max = 14;
  int64 iops_read_max_length = 15;
  int64 iops_write = 16;
  int64 iops_write_max = 17;
  int64 iops_write_max_length = 18;
  int64 iops_size = 19;             // bytes, > 0 when set
}

message DiskThrottleClass {
  int64 id = 1;
  string name = 2;
  string description = 3;
  DiskThrottleLimits limits = 4;
  int32 disk_count = 5;
  google.protobuf.Timestamp created_at = 6;
  google.protobuf.Timestamp updated_at = 7;
  string validation_error = 8;
}

service DiskThrottleService {
  rpc ListDiskThrottleClasses(ListDiskThrottleClassesRequest)
      returns (ListDiskThrottleClassesResponse) {
    option (google.api.http) = {
      get: "/api/disk-throttle-classes"
      response_body: "classes"
    };
  }
  rpc GetDiskThrottleClass(GetDiskThrottleClassRequest)
      returns (DiskThrottleClass) {
    option (google.api.http) = {get: "/api/disk-throttle-classes/{id}"};
  }
  rpc CreateDiskThrottleClass(CreateDiskThrottleClassRequest)
      returns (DiskThrottleClass) {
    option (google.api.http) = {post: "/api/disk-throttle-classes" body: "*"};
  }
  rpc UpdateDiskThrottleClass(UpdateDiskThrottleClassRequest)
      returns (DiskThrottleClass) {
    option (google.api.http) = {
      put: "/api/disk-throttle-classes/{id}"
      body: "*"
    };
  }
  rpc DeleteDiskThrottleClass(DeleteDiskThrottleClassRequest)
      returns (google.protobuf.Empty) {
    option (google.api.http) = {delete: "/api/disk-throttle-classes/{id}"};
  }
}

message ListDiskThrottleClassesRequest {}
message ListDiskThrottleClassesResponse {
  repeated DiskThrottleClass classes = 1;
}
message GetDiskThrottleClassRequest {
  int64 id = 1 [(validate.rules).int64 = {gt: 0}];
}
message CreateDiskThrottleClassRequest {
  string name = 1 [(validate.rules).string = {
    min_len: 1, max_len: 127, pattern: "^[A-Za-z0-9][A-Za-z0-9._-]*$"
  }];
  string description = 2 [(validate.rules).string = {max_len: 2000}];
  DiskThrottleLimits limits = 3;
}
message UpdateDiskThrottleClassRequest {
  int64 id = 1 [(validate.rules).int64 = {gt: 0}];
  optional string name = 2 [(validate.rules).string = {
    min_len: 1, max_len: 127, pattern: "^[A-Za-z0-9][A-Za-z0-9._-]*$"
  }];
  optional string description = 3 [(validate.rules).string = {max_len: 2000}];
  optional DiskThrottleLimits limits = 4;
}
message DeleteDiskThrottleClassRequest {
  int64 id = 1 [(validate.rules).int64 = {gt: 0}];
}
```

All numeric fields carry `gte: 0` and the appropriate `lte` cap. The proto
snippet above is illustrative for field numbering; every numeric field also
carries those validation rules. The cross-field rules are enforced by
`internal/diskthrottle.Validate`, not by `protoc-gen-validate`.

Update semantics: the gateway loads the existing class, overlays `name` and
`description` when those optional fields are present, and replaces the entire
`limits` object when it is present. The store writes the already-merged class
and validates it. An omitted `limits` preserves the current limits but still
validates them; an omitted name or description preserves the current value.
A corrupt class cannot be renamed without supplying valid limits: the update
returns `InvalidArgument` with "supply valid limits to repair." Full replacement
is intentional; concurrent edits are last-write-wins. The UI always sends the
complete limits object.

Disk requests and responses change as follows:

*   `CreateDiskRequest.throttle_class_id` is field 12, plain `int64` with
    `gte: 0`. Zero means unlimited. A template disk may not set it.
*   `UpdateDiskRequest.throttle_class_id` is field 7, optional `int64` with
    `gte: 0`. Absent preserves the current assignment, zero clears it, and a
    positive value assigns that class.
*   `VMDisk` fields 15 and 16 expose `throttle_class_id` and
    `throttle_class_name`; it does not carry the full limits payload or a
    throttle error.
*   `Disk` fields 26 and 27 expose `throttle_class_id` and
    `throttle_class_name`; it does not carry the limits payload. The UI
    resolves limits from the loaded class list.
*   `DiskThrottleClass.validation_error` (field 8) is empty for a valid class;
    otherwise it contains the decode or validation failure. The UI shows the
    error and disables assignment. Assigning a class with a non-empty
    `validation_error` returns `InvalidArgument` from the API and Terraform;
    the UI guard is advisory, and the server is authoritative.
*   When a class's stored `limits` cannot be decoded or validated, the disk or
    VM list still succeeds. `loadVMDisks` or `scanVM` sets the internal
    `VMDisk.ThrottleError`, and `qemu.Start` fails that VM's start with the
    disk and class in the error. The disk is not silently run unthrottled.
    Clients consult the class list's `validation_error` for the reason.
*   The removed `read_bps`/`write_bps` field numbers and names are reserved in
    `disk.proto`: `VMDisk` 13/14, `Disk` 24/25, `CreateDiskRequest` 10/11,
    `UpdateDiskRequest` 5/6. `disk.proto` no longer imports
    `disk_throttle.proto`; the class payload is a separate resource.
*   `disk_count` is returned by list, get, create, and update.
*   The gateway marshaler uses `EmitUnpopulated: false`, so zero-valued limit
    fields, `disk_count: 0`, and a zero class id are omitted from JSON. Clients
    treat an absent field as zero or unset.

Error mapping:

| Condition | gRPC code | HTTP |
| --- | --- | --- |
| Class name already exists | `AlreadyExists` | 409 |
| Class not found | `NotFound` | 404 |
| Class still assigned | `Aborted` | 409 |
| Template with a class | `InvalidArgument` | 400 |
| Invalid limits or name | `InvalidArgument` | 400 |
| Corrupt class limits | `OK` with `validation_error`; VM start fails | 200 |
| Unknown class id on disk create/update | `InvalidArgument` | 400 |
| Assignment while disk is deleting/delete_failed | `Aborted` | 409 |
| Class delete FK race | `Aborted` | 409 |
| Class assignment FK race | `InvalidArgument` | 400 |
| Missing store row or internal failure | `Internal` | 500 |

The in-use message is `class is assigned to N disk(s): name1, name2, ...`
(up to five names). The service is registered in the same in-process gRPC
server as the other services, so it is available on the local HTTP listener,
the mTLS listener, direct gRPC, and the Terraform provider. No native HTTP
handler is needed.

`internal/gateway/http_status.go` must add
`/rookery.v1.DiskThrottleService/CreateDiskThrottleClass` to `createdMethods`,
`/rookery.v1.DiskThrottleService/DeleteDiskThrottleClass` to
`noContentMethods`, and a
`*rookeryv1.DiskThrottleClass` case to `setLocationHeader` returning
`/api/disk-throttle-classes/{id}`.

### 7. Disk lifecycle rules

*   **Create**: before any host operation, resolve the optional class id,
    normalize zero to nil, reject a negative id, load the class by id, reject a
    class with `ValidationError`, and reject a class on a template. Then store
    the id for LVM thin, kouja, and host-device disks. A zero id is normalized
    to nil. Templates are clone sources and never start, so a class on a
    template has no runtime effect and would misleadingly suggest that a clone
    inherits it; this is the user-facing reason for the rule.
*   **Update**: the gateway acquires the existing disk mutex before loading the
    disk and holds it through the store calls. It adds
    `hasClass := request.ThrottleClassId != nil` to the no-op guard, computes
    `targetTemplate`, and applies the existing store methods in a defined order:
    reject a positive class when `targetTemplate` is true; call
    `SetDiskTemplate` first only when the template flag changes or a conversion
    is requested (it clears the class when marking a template); call
    `SetDiskProvisioning` when provisioning changes; call
    `SetDiskThrottleClass` whenever `hasClass` is true. The same class/template
    handling applies in the host-device branch. The mutex serializes the calls,
    and the store methods keep their existing LVM-thin, unattached, and
    template-only checks. A failure after an earlier call leaves that earlier
    change applied; the request returns the error and the operator can retry.
    Assignment changes are allowed while the disk is attached; the running QEMU
    process keeps its current arguments and the next start uses the new class.
*   **Clone**: a clone does not inherit a template class (templates cannot have
    one); the clone's create request may select any class.
*   **Import**: imported thin volumes start with no class; the operator can
    assign one after import.
*   **Delete disk**: LVM and host-device deletes remove the row immediately.
    Kouja deletes mark the row `deleting` and keep it until the reclaimer
    succeeds. `MarkDiskDeleting`, `RecordDiskDeletionFailure`, and
    `SetDiskDeleteFailed` clear `throttle_class_id` on every deletion-state
    transition. `SetDiskThrottleClass` rejects a positive assignment only while
    the disk is `deleting` or `delete_failed`; a `provisioning` disk may be
    assigned because it is unattached. A zero clear is allowed in every state.
    The positive-assignment check is a conditional `UPDATE ... WHERE
    is_template = 0 AND deletion_state NOT IN ('deleting','delete_failed')`, not
    a read-then-write, so a reclaimer transition cannot be overwritten. A
    `provisioning` row can pin a class until it becomes ready or the reclaimer
    cleans it up; the reclaimer is the recovery path.
*   **Delete class**: blocked while any disk references it. The store uses a
    conditional delete plus an FK check; the gateway returns the assigned count
    and names.
*   **Start/autostart**: resolve the current class through the normal VM load;
    no separate cache or reconciler is required.

### 8. UI

The Disks tab gains a third panel, **Throttling classes**, below **Disks**. It
lists name, description, BPS summary, IOPS summary, assigned disk count, and
updated time, with **Add class**, **Edit**, and **Delete** actions. Delete is
enabled; the confirmation resolves assigned disk names by filtering the loaded
disk list by `throttle_class_id` (no new endpoint) and the API returns `409` if
any remain. The panel shows **configured; applies on next start**. A class
with a `validation_error` shows the error and cannot be assigned. A VM whose
class limits cannot be decoded fails to start with the disk and class in the
error; it is not silently run unthrottled. The class dialog always sends the
full limits object. A corrupt class must be repaired by supplying valid limits;
it cannot be renamed without them. Alternatively, clear or reassign every disk
that uses it and delete/recreate the class.

The class dialog groups fields by metric. The mode selectors are local state:

*   A BPS selector (`Unlimited`, `Total`, `Read/Write`) and the matching fields.
*   An IOPS selector (`Unlimited`, `Total`, `Read/Write`) and the matching
    fields.
*   Shared `IOPS size` when IOPS is enabled.
*   Units: BPS fields are bytes per second; IOPS fields are operations per
    second; burst lengths are seconds; `iops_size` is bytes. The dialog inputs
    raw bytes per second; a MiB/s display conversion is a future improvement.
*   Inline validation covers non-negative values, per-family exclusivity, at
    least one positive BPS or IOPS base, and a required base before a burst;
    QEMU caps and overflow are left to the server, whose error is shown when a
    rule fails. `validateDraft` enforces these checks.

The dialog maps `Unlimited` to no values, `Total` to the total fields, and
`Read/Write` to the directional fields. On edit, the initial mode is inferred
from the stored positive values. Save is disabled when both selectors are
`Unlimited` because a class must set at least one positive base; a no-limit
disk is expressed by leaving the class unassigned, not by saving an empty
class.

The disk creation dialog replaces the two raw bandwidth inputs with a
**Throttling class** select whose default is **Unlimited**. The select is
disabled and cleared when the new disk is marked as a template. The disk row
action **Limits** becomes **Throttling** and opens an assignment dialog with
the same select; it is disabled for templates. Saving sends the class id or
zero. The Disks table shows the class name and, by resolving the class id
against the loaded class list, a short configured summary (for example,
`500 MiB/s read · 300 MiB/s write` or `2,000 IOPS total`) with the next-start
label. The disk response itself carries only the class id and name.

Deep links follow the existing router: `/disk-throttle-classes/new` and
`/disk-throttle-classes/{id}/edit` render the class dialog inside the Disks tab.
`ui/src/inventory/app.ts` maps the `disk-throttle-classes` path prefix to the
`disks` tab. `DisksTab` loads the class list, passes it to `DiskDialog`, and
refreshes both disks and classes after a class write or assignment. `DisksTab`
branches on `route.segments` (`disk-throttle-classes`, then `new` or `{id}`,
then `edit`); `app.ts` owns the path mapping. No regex helper is needed. No new
window CustomEvent is needed.

The UI helper module `ui/src/inventory/disks/throttle.ts` exports:

*   `ThrottleMode` (`none` | `total` | `directional`), used only for dialog
    state.
*   `inferMode(limits, family)`.
*   `validateDraft(draft): string | null`.
*   `limitsFromDraft(draft): DiskThrottleLimits`.
*   `formatThrottleSummary(limits): string`.

`formatBytesIEC` and `formatRateIEC` are added to `ui/src/shared/format.ts`
(the file already exists) and reused by `throttle.ts`. Use IEC labels (MiB/s,
GiB/s) for the class summary. Do not change `CgroupSettingsPanel` or the other
local formatters, including the exported helper in
`ui/src/inventory/vms/monitor.ts`; those keep their existing SI or local
labels.

### 9. Terraform provider

*   New `rookery_disk_throttle_class` resource and matching data source. The
    resource schema:
    *   `name`: required string, 1-127 characters, pattern
        `^[A-Za-z0-9][A-Za-z0-9._-]*$`.
    *   `description`: optional+computed string, max 2,000 characters, with
        `UseStateForUnknown`.
    *   The 19 numeric fields: optional+computed `int64` with
        `UseStateForUnknown`. Zero means unset. Removing a field preserves the
        prior state value; set `0` to clear it. The schema descriptions and
        README state this behavior.
    *   `validation_error`: computed string; empty for a valid class.
    *   `disk_count`: computed int64.
    *   Import state parses the integer id.
*   `rookery_disk` gains optional+computed `throttle_class_id` with
    `UseStateForUnknown` and computed `throttle_class_name`. `0` clears;
    omitting the attribute preserves the current state value. `setDiskState`
    maps no class to `types.Int64Value(0)` and a null name to
    `types.StringNull()`. Terraform plans an omitted optional+computed value as
    unknown on create, so the provider inspects the **configuration**, not only
    the planned value:
    *   Create: null config sends `0`; known config sends the id; unknown
        config with a known planned id sends the planned id (a same-apply
        reference resolved at apply); unknown config and unknown plan returns a
        diagnostic.
    *   Update: null config omits the field (preserve); known config sends the
        id (`0` clears); unknown config with a known plan sends the plan;
        otherwise returns a diagnostic.
    *   `ModifyPlan`: when `is_template = true`, a known positive config or an
        unknown config is a configuration error; null config is allowed and
        Create sends `0`. This catches an explicit class on a template without
        rejecting a new template that omits the attribute.
    Removing the attribute does not clear it because the optional+computed value
    is reused from state. The provider does not silently clear a positive
    class.
*   The `rookery_disk` data source gains computed `throttle_class_id` and
    `throttle_class_name`.
*   The provider client gains a `DiskThrottleClasses` service client.
*   The complete example creates a class named `standard` and assigns it to the
    root disk; the data-source example looks up `standard`. The provider README
    documents the resource, the data source, units, the `0`-clears behavior,
    and the template interaction.

### 10. Migration and compatibility

The schema change uses two migration files so every development phase builds:

*   `0051_disk_throttle_classes.sql` creates `disk_throttle_classes`, adds
    `disks.throttle_class_id` with the FK, and creates
    `idx_disks_throttle_class_id`. It keeps `read_bps` and `write_bps`.
*   `0052_drop_disk_bandwidth.sql` drops `read_bps` and `write_bps` after the
    Go cutover.

There is no backfill, compatibility alias, or dual-write period. `0050` is not
in any tagged release and the raw columns are unreleased. Both `0051` and
`0052` ship in **this** release; there is no later release that carries the
class feature with raw columns. Phase 4 applies `0052` after the Go cutover and
is a pre-release step, not a separate release. The two files are an
implementation split within one release, not a two-release model; the release
build embeds both and `Open` applies them before serving. If the release process
requires a single file, append `0052` to `0051` after Phase 4; the Phase 0
raw-column assertion is removed at that point. No code changes are needed. A pre-upgrade backup is
required. A downgrade to `v0.1.6` is unsupported because it does not know the
class table and the raw columns are gone. `docs/operations.md` documents the
backup. The stored `limits` JSON is unversioned; unknown future fields are
ignored by an older server, and downgrades are unsupported. A future QEMU
option must land with a schema/read change rather than relying on an older
server to enforce it.

API compatibility: JSON requests that send `read_bps` or `write_bps` are
rejected as unknown fields because the gateway sets `DiscardUnknown: false`.
Older gRPC clients silently ignore removed fields, so an old client's bandwidth
update becomes a no-op; this is documented. Removed field numbers and names are
reserved so an old value cannot be misread as a class id.

### 11. Operational notes

*   Class edits do not touch running QEMU processes. The UI labels limits as
    configured and applies on the next start.
*   Deleting a class is a two-step operation: reassign or clear every disk, then
    delete. The conflict names the assigned disks. There is no bulk clear or
    pagination; the inventory is assumed small, so deletion with many disks is
    a per-disk reassignment workflow.
*   Class updates are full replacement and last-write-wins. The API logs the
    class id, old limits, and new limits for audit; there is no ETag or field
    mask.
*   The class lookup adds one `LEFT JOIN` to disk and VM reads. The class table
    is small, and SQLite runs with a single connection, so no cache or
    invalidation layer is justified.
*   QEMU enforces throttling in its own I/O path, so very small limits can make
    guest I/O wait and cause guest-side timeouts. QEMU recommends at least
    2 MB/s (2,000,000 bytes/s) for disks; the UI hint and `docs/api.md` state
    this, and the API does not enforce it because it is not a hard QEMU limit.
*   A VM with N disks assigned to the same class can consume N times the class
    limit. The class is per disk, not a per-VM aggregate. The UI and docs state
    this.
*   Use a QEMU throttling class for per-disk throughput, IOPS, and burst
    limits. Use the cgroup `io.max`/`io.weight` settings for kernel-level
    per-device limits and VM resource policy. For local LVM and host-device
    disks both can apply; for kouja disks only the class works. The Disks tab
    owns the class inventory; Settings owns the cgroup policy.
*   The class feature is tested with QEMU 10.0.13; earlier versions are not
    verified. A Rookery-side `ThrottleError` names the disk and class; a
    QEMU-side failure may only name the option, and the wrapper includes the
    rendered throttling fragment. There is no capability probe.

--------------------------------------------------------------------------------

## Alternatives Considered

No prior design brief preceded this document. The alternatives below were
evaluated while settling the data model and scope.

### Alternative A: Reuse cgroup classes and `io.max` limits

*   **Pros**: The class inventory already exists in `cgroup_settings`, Rookery
    already applies per-device limits through cgroups, and there would be one
    resource model for all I/O policy.
*   **Cons**: cgroup limits are enforced by the kernel against a host block
    device's major:minor in the VM's cgroup. QEMU opens kouja iSCSI targets
    itself, so there is no host block device to reference; host passthrough has
    a device, but the limit would still be a kernel cgroup control, not QEMU's
    per-drive `throttling.*`. It would also couple CPU and memory policy to
    disk policy.
*   **Reason for Rejection**: Wrong enforcement layer and wrong scope. The
    requested behavior is QEMU block throttling, and the cgroup feature remains
    a separate VM resource control.

### Alternative B: Keep per-disk BPS fields and add a copy action

*   **Pros**: Smallest code change. No new table, service, or migration. The
    existing Limits dialog keeps working.
*   **Cons**: There is still no shared definition or reviewable inventory. A
    policy change still means editing every disk. The two numbers remain
    independent integers with no policy to validate against.
*   **Reason for Rejection**: It preserves the problem the feature is meant to
    remove.

### Alternative C: Snapshot class limits onto each disk at assignment

*   **Pros**: A disk's behavior is stable even if the class is edited later,
    and no lookup is needed at VM start.
*   **Cons**: Limits are duplicated on every disk, a class edit does not
    propagate, and the model needs either copied limit columns or versioned
    classes. It contradicts the decision to resolve the current class value at
    start time.
*   **Reason for Rejection**: The design uses a live reference; class edits
    must reach assigned disks on their next start.

### Alternative D: One total-versus-directional mode for the whole class

*   **Pros**: One mode selector in the UI and one validation rule. The schema
    and Terraform resource are smaller.
*   **Cons**: QEMU permits total BPS with directional IOPS and directional BPS
    with total IOPS. A single mode would reject those valid combinations, which
    conflicts with the full QEMU option requirement.
*   **Reason for Rejection**: QEMU's constraint is per metric family, so the
    class model needs independent BPS and IOPS groups.

### Alternative E: Generic key/value list of QEMU options

*   **Pros**: New QEMU options can be added without changing the protobuf,
    database, or UI. The server only needs an allowlist and value validation.
*   **Cons**: No typed fields for the UI or Terraform, poor discoverability,
    custom parsing for every client, and a higher risk of unsupported
    combinations. The option set is stable enough to model explicitly.
*   **Reason for Rejection**: The typed model gives better validation and a
    usable form, while still covering every option except `throttling.group`.

### Alternative F: BPS-only classes without IOPS or burst limits

*   **Pros**: A much smaller form and fewer validation rules. It matches the
    old per-disk surface.
*   **Cons**: Operators could not express IOPS caps or temporary bursts, even
    though QEMU supports them. The design treats the full QEMU option set
    except throttle groups as a requirement.
*   **Reason for Rejection**: The full option set is required for this
    iteration.

### Alternative G: Classes scoped to a storage backend

*   **Pros**: The same class name can mean different limits on different
    backends, and class management sits next to backend management.
*   **Cons**: Duplicate definitions, more CRUD paths, and no clear benefit on a
    single host. Disk assignment would need to validate backend compatibility.
*   **Reason for Rejection**: A global inventory is simpler and matches the
    "one place" requirement.

### Alternative H: Migrate existing raw limits into generated classes

*   **Pros**: No disk becomes unlimited after the upgrade. Existing policy can
    be reviewed and renamed later.
*   **Cons**: Extra migration logic, generated class names, and cleanup work
    for data that is not deployed in production. The design explicitly accepts
    dropping the old values.
*   **Reason for Rejection**: Pre-production data does not justify the
    migration complexity.

### Alternative I: Add a top-level Throttling tab

*   **Pros**: The inventory is prominent and has its own route.
*   **Cons**: Adds another top-level tab, splits storage-related inventory
    across two tabs, and duplicates the refresh and navigation shell.
*   **Reason for Rejection**: A third panel inside the existing Disks tab keeps
    backends, disks, and their throttling policy together.

### Alternative J: Support `throttling.group`

*   **Pros**: Multiple disks can share one quota, which prevents a guest from
    avoiding a limit by spreading I/O across many small disks.
*   **Cons**: Group names cross disk boundaries, require group lifecycle and
    reference management, and complicate deletion and reassignment.
*   **Reason for Rejection**: Out of scope for this iteration. It can be added
    later as a separate group resource or a class field with its own
    validation.

--------------------------------------------------------------------------------

## Non-Functional Requirements

*   **Security and privacy**: The local HTTP listener is localhost-bound and
    unauthenticated. The mTLS listener requires a client certificate signed by
    the server CA, but every client certificate is fully trusted and can change
    host-wide I/O limits; there is no per-resource authorization. The class
    object therefore has the same trust model as the rest of the API, and its
    scope is host-wide. Names are trimmed, length-limited, and pattern-validated;
    descriptions are trimmed and length-limited. QEMU arguments are built from
    integers and fixed option names, so user-controlled strings never reach the
    QEMU argument parser. No secrets are stored in a class.
*   **Performance**: Disk and VM reads gain one `LEFT JOIN` against a small
    table. There is no cache, background poll, or extra host operation. QEMU
    performs the throttling itself; the API only formats start arguments.
*   **Rollout and rollback**: This release adds `0051` and `0052`: the first
    adds the class inventory, the code cutover lands, and the second drops the
    raw columns in the pre-release Phase 4 step. A pre-upgrade backup is
    required. A downgrade to `v0.1.6` is unsupported because the older server
    does not know `disk_throttle_classes` and the raw columns are gone.
*   **Compatibility**: The disk API is intentionally breaking. Removed
    protobuf field numbers and names are reserved. JSON clients that send
    `read_bps` or `write_bps` receive an unknown-field error. Old gRPC clients
    silently ignore the removed fields; their bandwidth updates become no-ops.
*   **Reliability**: Class deletion is blocked by an application check and the
    foreign key. Disk template conversion and class assignment run under the
    disk mutex in a defined order, and the existing store invariants for
    LVM-thin, unattached, and template-only changes are preserved. Class edits
    apply only at the next start, so there is no partial runtime
    reconfiguration. A failure after an earlier store call leaves that earlier
    change applied; the request returns the error for retry.
*   **Observability**: Failures use the existing gRPC status and gateway error
    path; the UI shows the returned message. Rookery-side start errors name the
    disk and class; QEMU-side errors may only name the option, with the rendered
    throttling fragment in the wrapper. No new metrics or
    background workers are required.

--------------------------------------------------------------------------------

## Risks & Mitigations

| Risk | Impact | Mitigation |
| --- | --- | --- |
| A class passes Rookery validation but QEMU rejects it or installs no throttle. | Every VM assigned to the class can fail to start, or run unthrottled. | Validate positive bases, burst dependencies, the `UINT_MAX` burst-length cap, `THROTTLE_VALUE_MAX`, and burst-length overflow; add a documented representative QEMU acceptance matrix for the supported shape families. |
| Corrupt or manually edited `limits` JSON breaks disk/VM reads. | `ListVMs` and autostart can fail for the whole host. | Store-only writes; no invalid default; decode and validate on read; the class exposes `validation_error`, the affected VMDisk gets an internal `ThrottleError`, `qemu.Start` fails that VM with the disk and class, and the rest of the list stays usable. |
| One corrupt class is shared by many VMs. | Every VM using that class fails to start. | Repair the class by supplying a full valid limits object; or clear/reassign every disk and delete/recreate the class. The class list shows `validation_error`, and the start error names the class. |
| Template conversion and class assignment race. | A template can end up with a class, or a class can become dangling. | Take the disk mutex in `UpdateDisk`; apply `SetDiskTemplate`, `SetDiskProvisioning`, and `SetDiskThrottleClass` in that order; `SetDiskTemplate` clears the class; map constraint errors. |
| Class deletion races a disk assignment. | Delete fails with an unmapped `Internal` error, or a disk becomes dangling. | Conditional `DELETE` plus FK check; pre-check `disk_count`; map FK violations to `Aborted` with the assigned count. |
| Destructive migration in this release. | Existing limits are lost when `0052` runs. | `0052` runs in the pre-release Phase 4 after the code cutover; require a pre-upgrade backup and a preflight count of non-zero raw limits. |
| Downgrade to `v0.1.6`. | The older server does not know `disk_throttle_classes`, and the raw columns are gone. | Document the downgrade as unsupported; require a pre-upgrade backup; restore the backup if a rollback is unavoidable. |
| Kouja `deleting`/`delete_failed` row pins a class. | The class cannot be deleted for an unbounded time. | Clear `throttle_class_id` on every deletion-state transition (`MarkDiskDeleting`, `RecordDiskDeletionFailure`, `SetDiskDeleteFailed`); reject positive assignment while `deleting`/`delete_failed`. |
| Orphaned `provisioning` row pins a class. | The class cannot be deleted until the row is cleaned up. | The reclaimer resolves stale provisioning rows; document the recovery path and the small window in which a provisioning row still holds the class. |
| QEMU version mismatch. | Start fails with an opaque QEMU error. | Record the tested QEMU 10.0.13 version; Rookery-side errors name the disk and class, and QEMU-side errors include the rendered throttling fragment; no startup probe. |
| A class is per disk, not per VM. | A VM with N disks can consume N times the class limit. | State this in the UI and docs; do not present the class as a VM-wide quota. |
| Concurrent class edits clobber each other. | Last writer wins. | Document full replacement and last-write-wins; the UI always sends the complete limits object; no ETag/PATCH. |
| A running VM keeps stale limits after a class edit. | Operators may trust a value QEMU is not using. | Label limits as configured and applying on the next start; no per-VM stale-limit tracking. |

--------------------------------------------------------------------------------

## Additional Resources

*   QEMU system invocation documentation for `-drive` `throttling.*` options:
    <https://www.qemu.org/docs/master/system/invocation.html>.
*   QEMU QMP `BlockIOThrottle` documentation for `max-length` units and
    defaults: <https://www.qemu.org/docs/master/interop/qemu-qmp-ref.html>.
*   `AGENTS.md` storage, disk, gateway, and Terraform provider sections.
*   Existing `cgroup_settings` class implementation in
    `internal/cgroup`, `internal/gateway/cgroup.go`, and
    `ui/src/inventory/settings/CgroupSettingsPanel.tsx` as prior art for a
    reusable inventory.
*   `internal/store/migrations/0050_disk_bandwidth.sql` for the fields being
    removed.

--------------------------------------------------------------------------------

## Future Improvements

*   **Throttle groups**: Add `throttling.group` as a separate group resource or
    an optional class field once group lifecycle and reference management are
    designed.
*   **Live changes**: Use QMP `block_set_io_throttle` to apply class edits to
    running VMs without a restart.
*   **Operator-friendly units**: Accept MiB/s and GiB/s in the UI and Terraform
    while storing bytes per second.
*   **Class labels and search**: Reuse the entity label system and add filtering
    if the inventory grows beyond a small list.
*   **Migration tooling**: Provide an optional import path for installations
    that do have pre-release data worth preserving.
*   **Per-backend classes**: Revisit backend scoping only if operators need the
    same name to mean different limits on different storage backends.
*   **Throttling metrics**: Surface per-disk throttle wait or budget statistics
    if QEMU or the host exposes useful counters.

--------------------------------------------------------------------------------

## Detailed Implementation Plan

### Target Files to Create/Modify

#### 1. `design/proto/rookery/v1/disk_throttle.proto` (Create)

*   **Type / Scope**: Protobuf service and message definitions.
*   **Proposed Changes**: Define `DiskThrottleService` and the exact messages
    from §6: `DiskThrottleLimits` fields 1-19 as plain `int64` with `gte: 0`
    and the documented caps, `DiskThrottleClass` fields 1-8 including
    `validation_error`, and the list/get/create/update/delete request/response
    messages. Use `response_body: "classes"` on the list RPC, `body: "*"` on
    create/update, and `google.protobuf.Empty` for delete. Names are 1-127
    characters matching `^[A-Za-z0-9][A-Za-z0-9._-]*$`; descriptions are at
    most 2,000 characters. Follow the HTTP/PGV annotation style in `disk.proto`,
    `storage_backend.proto`, or `route_table.proto`; `cgroup.proto` is
    message-only and is not an annotation example.
*   **Rationale**: The API is protobuf-first; the exact contract prevents the
    implementer from inventing field numbers or response shapes.

#### 2. `design/proto/rookery/v1/disk.proto` (Modify)

*   **Type / Scope**: Disk protobuf definitions.
*   **Proposed Changes**: Do not import `disk_throttle.proto`; the class payload
    is a separate resource. Remove `read_bps`/`write_bps` from `VMDisk`,
    `Disk`, `CreateDiskRequest`, and `UpdateDiskRequest`. Reserve `VMDisk`
    13/14, `Disk` 24/25, `CreateDiskRequest` 10/11, and `UpdateDiskRequest`
    5/6, including the names. Add `int64 throttle_class_id = 15` and
    `string throttle_class_name = 16` to `VMDisk`; `VMDisk` does not carry the
    full limits. Add `int64 throttle_class_id = 26` and
    `string throttle_class_name = 27` to `Disk`. Add `int64
    throttle_class_id = 12` to `CreateDiskRequest` (zero = unlimited) and
    `optional int64 throttle_class_id = 7` to `UpdateDiskRequest` (absent
    preserves, zero clears), both with `gte: 0`. Update the `host_device`
    comment to list `throttle_class_id` as an allowed field.
*   **Rationale**: The disk API must stop advertising the removed fields and
    must carry the class reference on every disk response.

#### 3. Generated protobuf and OpenAPI manifests (Modify / Create)

*   **Type / Scope**: Generated code committed to the repository.
*   **Proposed Changes**: Run `make proto-generate`. Phase 0 generates the new
    `disk_throttle` files:
    `design/gen/rookery/v1/disk_throttle.pb.go`,
    `disk_throttle.pb.validate.go`, `disk_throttle_grpc.pb.go`, and
    `disk_throttle.pb.gw.go`, plus `design/openapi/rookery.swagger.json`
    (the generator merges every proto into that one file). Phase 1 regenerates
    `disk.pb.go` and `disk.pb.validate.go` after the `disk.proto` changes; the
    swagger may change again if `disk.proto` changes the disk schemas.
*   **Rationale**: Generated code and OpenAPI must match the source before code
    that references the new types compiles.

#### 4. `internal/diskthrottle/diskthrottle.go` (Create)

*   **Type / Scope**: Shared Go package.
*   **Proposed Changes**: Define `Limits` with 19 `int64` fields and explicit
    snake_case JSON tags with `omitempty` (for example
    `json:"bps_total,omitempty"` through `json:"iops_size,omitempty"`);
    `Validate() error` and `QEMUOptions() string`. `Validate` implements the
    positive-base, exclusivity, burst, cap, overflow, and `iops_size` rules.
    `QEMUOptions` returns a comma-prefixed fragment in table order, or an empty
    string. The package owns the `throttling.*` option names as the single QEMU
    formatter; it has no dependency on the generated proto or the store.
*   **Rationale**: This is the only code that validates and formats
    `throttling.*` values; a defect here affects every classed VM start.

#### 5. `internal/diskthrottle/diskthrottle_test.go` (Create)

*   **Type / Scope**: Unit tests.
*   **Proposed Changes**: Table-driven tests for every validation rule,
    accepted combinations (including total BPS with directional IOPS and the
    reverse), rejected combinations (mixed total/read-write, zero base with a
    burst, max below base, max without base, max-length without max,
    burst-length overflow, max-length above `UINT_MAX`, values above
    `THROTTLE_VALUE_MAX`, `iops_size` without IOPS), JSON round-trip,
    omitted-vs-explicit-zero, and exact
    `QEMUOptions` fragments for one and multiple options.
*   **Rationale**: The validator and formatter are the safety boundary for QEMU
    arguments.

#### 6. `internal/store/migrations/0051_disk_throttle_classes.sql` (Create)

*   **Type / Scope**: SQLite migration.
*   **Proposed Changes**: Create `disk_throttle_classes` with the columns from
    the Technical Plan (`limits TEXT NOT NULL`, no default); add
    `disks.throttle_class_id` with `REFERENCES disk_throttle_classes(id)
    ON DELETE RESTRICT`; create `idx_disks_throttle_class_id`. Keep
    `read_bps` and `write_bps`.
*   **Rationale**: This is the additive phase that keeps the tree buildable.

#### 7. `internal/store/migrations/0052_drop_disk_bandwidth.sql` (Create, Phase 4 / pre-release)

*   **Type / Scope**: SQLite migration.
*   **Proposed Changes**: Drop `disks.read_bps` and `disks.write_bps` after the
    Go cutover. No backfill. Both `0051` and `0052` ship in this release; this
    is the pre-release drop step after all code stops using the raw columns.
*   **Rationale**: The owner accepted dropping the old values; the separate
    file keeps the destructive step reviewable without creating a second
    release.

#### 8. `internal/store/disk_throttle.go` (Create)

*   **Type / Scope**: Store CRUD and disk assignment.
*   **Proposed Changes**: Define `DiskThrottleClass` (ID, name, description,
    `diskthrottle.Limits`, disk count, `ValidationError`, timestamps) and the
    errors `ErrDiskThrottleClassNotFound`, `ErrDiskThrottleClassNameTaken`,
    `ErrDiskThrottleClassInUse`, `ErrDiskThrottleClassOnTemplate`,
    `ErrDiskThrottleClassInvalid`, and `ErrDiskThrottleClassInDeletion`.
    Implement:
    *   `CreateDiskThrottleClass(ctx, class DiskThrottleClass) (DiskThrottleClass, error)`
    *   `GetDiskThrottleClass(ctx, id int64) (DiskThrottleClass, error)`
    *   `ListDiskThrottleClasses(ctx) ([]DiskThrottleClass, error)`
    *   `UpdateDiskThrottleClass(ctx, class DiskThrottleClass) (DiskThrottleClass, error)`
    *   `DeleteDiskThrottleClass(ctx, id int64) error`
    *   `SetDiskThrottleClass(ctx, diskID int64, classID *int64) (Disk, error)`
    *   `ListDiskNamesByThrottleClass(ctx, classID int64, limit int) ([]string, error)`
    The list query orders by `name COLLATE NOCASE, id` and computes
    `disk_count` with a correlated subquery. Create/update return the class via
    `Get` so `disk_count` is present. Get/list decode and validate the stored
    limits; an invalid row sets `ValidationError` instead of failing the list.
    The gateway merges optional name and description; the store writes the full
    class and validates it. `Delete` pre-checks the count, performs a
    conditional delete, and maps an FK violation to
    `ErrDiskThrottleClassInUse`. `SetDiskThrottleClass` treats a zero id as nil.
    For a nil/zero clear it runs `UPDATE disks SET throttle_class_id = NULL,
    updated_at = ? WHERE id = ?` in every state and returns the existing
    not-found error when no row matches. For a positive class it validates the
    class and limits, returning `ErrDiskThrottleClassInvalid` when
    `ValidationError` is non-empty, and then runs `UPDATE disks SET
    throttle_class_id = ?, updated_at = ? WHERE id = ? AND is_template = 0 AND
    deletion_state NOT IN ('deleting','delete_failed')`. Use
    `time.Now().UTC().UnixMilli()` for `updated_at` in both statements.
    When `RowsAffected()` is zero it re-reads the disk and returns
    `ErrDiskThrottleClassOnTemplate`, `ErrDiskThrottleClassInDeletion`, or the
    not-found error. After either update, re-read the disk and return it so
    `ThrottleClassID` and `UpdatedAt` reflect the write. The conditional update
    closes the read-then-write race with
    the reclaimer. Reuse `isForeignKeyViolation` so a concurrent class delete
    during assignment returns `ErrDiskThrottleClassNotFound`, not a generic
    internal error. Keep the existing `SetDiskTemplate` and
    `SetDiskProvisioning` methods and their LVM-thin, unattached, and
    template-only checks; `SetDiskTemplate` clears `throttle_class_id` when
    marking a template.
*   **Rationale**: Class persistence and assignment rules stay out of the
    already large `store.go`.

#### 9. `internal/store/store.go` (Modify)

*   **Type / Scope**: Disk and VM persistence.
*   **Proposed Changes**: **Phase 0:** add `ThrottleClassID *int64` and
    `ThrottleClassName string` to `Disk`; add
    those fields plus `ThrottleError string` and `Throttle
    *diskthrottle.Limits` to `VMDisk`. Keep `ReadBPS`/`WriteBPS` fields, scans,
    and writes so the unmodified proto and callers still build. **Phase 1:**
    remove `ReadBPS`/`WriteBPS` from `VMDisk` and `Disk` and stop scanning and
    writing the raw columns. The `VMDisk.Throttle`/`ThrottleError` are
    internal (`json:"-"`) and are not exposed in the proto. Add the class
    columns and `disk_throttle_classes` join to `diskSelect`, `loadVMDisks`,
    `vmSelect`, and `scanVM`; update `scanDisk`, `scanVM`, and `loadVMDisks`.
    `scanDisk` scans only the class id/name for the disk inventory. The
    `vmSelect`/`scanVM` join keeps the legacy `vm.Disk` fallback from silently
    losing the class when `vm_disks` rows are absent; `scanVM` also selects
    `dtc.limits` and runs the same decode/validate helper as `loadVMDisks`,
    setting `Throttle` and `ThrottleError`. Use one `decodeClassLimits` helper;
    the `scanVM` decode only matters for the legacy fallback, because
    `loadVMDisks` overwrites the primary disk when join rows exist. A non-NULL
    class id with no matching
    class row is a decode failure; set `ThrottleError` so the start fails
    instead of running unlimited. On a decode or validation failure, set
    `ThrottleError` and leave `Throttle` nil; do not abort the list. Update
    the `CreateDisk` insert to store `throttle_class_id`, validate class
    existence and limits, map a concurrent-delete FK violation to
    `ErrDiskThrottleClassNotFound`, and reject a class on a template.
    `ImportDisks` needs no column change; imported disks default to an
    unassigned class. Remove `SetDiskBandwidth`. `SetDiskTemplate` clears
    `throttle_class_id` when marking a template. Deletion-state clearing lives
    in `disk_deletion.go` (item 55); this file does not change those methods.
*   **Rationale**: Every disk read path must expose the class reference; the
    QEMU path reads the limits from the joined class.

#### 10. `internal/store/disk_throttle_test.go` (Create)

*   **Type / Scope**: Store tests.
*   **Proposed Changes**: Cover class create/get/list/update/delete, duplicate
    names, JSON limits round-trip, computed disk counts, in-use deletion with
    count and names, assignment and clearing (assert `UpdatedAt` advances on
    both), template rejection, template conversion clearing the class, disk
    creation with a class, imported disks starting unassigned, kouja delete
    clearing the class, live-reference behavior after a class update,
    corrupt-limit read behavior, a dangling `throttle_class_id` with no class
    row, a reclaimer transition between read and assignment (the conditional
    update rejects and leaves the class cleared), and a classed disk with no
    `vm_disks` row using the legacy `vm.Disk` fallback. Add state-guard cases:
    a `provisioning` disk accepts a class; `deleting` and `delete_failed` disks
    reject a positive assignment with `ErrDiskThrottleClassInDeletion`; a zero
    clear succeeds in both states.
*   **Rationale**: The store enforces the invariants even when the gateway is
    bypassed.

#### 11. `internal/store/store_test.go` (Modify)

*   **Type / Scope**: Existing store tests.
*   **Proposed Changes**: Replace `TestDiskBandwidthLimits` with a class-based
    test that creates a class, assigns it to a disk, reads the disk and VM, and
    verifies the live class values. Remove negative-bandwidth assertions.
*   **Rationale**: The old test targets removed columns.

#### 12. `internal/store/migration_test.go` (Modify)

*   **Type / Scope**: Migration tests.
*   **Proposed Changes**:
    *   Add `migrateToVersion(t *testing.T, db *sql.DB, version int)`. It sets
        `db.SetMaxOpenConns(1)` and `db.SetMaxIdleConns(1)`, applies
        `PRAGMA journal_mode = WAL`, `synchronous = NORMAL`,
        `busy_timeout = 5000`, and `foreign_keys = ON`, reads
        `PRAGMA user_version`, sorts the embedded `migrations` FS, and applies
        each migration with `version > current && version <= target` in a
        transaction that also sets `PRAGMA user_version`. Reuse
        `migrationVersion` and `migrations.ReadFile`; do not write a second
        filename parser. Each test opens the raw DB, calls the helper, inserts
        its fixtures, closes the DB, and calls `Open`.
    *   Replace the legacy-simulation setup in
        `TestMigrationConvertsNetworkDHCPToVNF`,
        `TestMigrationBackfillsVMDisks`,
        `TestMigrationAddsDefaultLightVMConsole`,
        `TestMigrationBackfillsVMPowerState`,
        `TestEnsureStartupScriptSchemaAfterTableDrop`, and
        `TestEnsureVPPInterfaceAddressesSchemaAfterTableDrop`. Also update
        `TestMigrationPreservesLegacyVMsWithoutDisks`.
    *   Target versions: `TestMigrationConvertsNetworkDHCPToVNF` 7,
        `TestMigrationBackfillsVMDisks` 11,
        `TestMigrationAddsDefaultLightVMConsole` 46,
        `TestMigrationBackfillsVMPowerState` 37,
        `TestEnsureStartupScriptSchemaAfterTableDrop` 37,
        `TestEnsureVPPInterfaceAddressesSchemaAfterTableDrop` 30, and
        `TestMigrationPreservesLegacyVMsWithoutDisks` 1.
    *   Use these exact fixture rows (raw SQL after `migrateToVersion`):

    ```text
    -- v1: TestMigrationPreservesLegacyVMsWithoutDisks
    INSERT INTO vms
      (name, description, status, vcpus, memory_mb, disk_gb, created_at, updated_at)
    VALUES ('legacy', 'old vm', 'stopped', 2, 1024, 42, 1000, 2000);
    
    -- v7: TestMigrationConvertsNetworkDHCPToVNF
    INSERT INTO networks
      (name, bridge_domain_id, dhcp_enabled, dhcp_gateway,
       dhcp_range_start, dhcp_range_end, dhcp_dns,
       dhcp_tap_sw_if_index, dhcp_tap_host_name, created_at, updated_at)
    VALUES ('legacy-net', 100, 1, '10.20.0.1/24', '10.20.0.100', '10.20.0.200',
        '1.1.1.1', 3, 'ndn1', 1000, 2000);
    
    -- v11: TestMigrationBackfillsVMDisks; v11 has no vm_disks table.
    INSERT INTO disks
      (id, name, size_gb, vg, thin_pool, created_at, updated_at,
       storage_backend_id)
    VALUES (1, 'legacy-root', 20, 'vg0', 'rookery', 1000, 1000, NULL);
    INSERT INTO vms
      (id, name, description, status, vcpus, memory_mb, disk_id, boot_media_id,
       created_at, updated_at, console_enabled)
    VALUES ('11111111-1111-4111-8111-111111111111', 'legacy', '', 'stopped', 1, 512,
        1, NULL, 1000, 1000, 1);
    
    -- v37: TestMigrationBackfillsVMPowerState; v37 has no desired_state column.
    INSERT INTO disks
      (id, name, size_gb, vg, thin_pool, created_at, updated_at,
       storage_backend_id, is_template, template_disk_id, prepared_by_script,
       provisioning_script, provisioning_timeout)
    VALUES (1, 'legacy-running', 20, 'vg0', 'rookery', 1000, 1000, NULL, 0, NULL,
        0, '', ''),
       (2, 'legacy-stopped', 20, 'vg0', 'rookery', 1000, 1000, NULL, 0, NULL,
        0, '', '');
    INSERT INTO vms
      (id, name, description, status, vcpus, memory_mb, disk_id, boot_media_id,
       created_at, updated_at, console_enabled)
    VALUES ('22222222-2222-4222-8222-222222222222', 'was-running', '', 'running',
        1, 512, 1, NULL, 1000, 1000, 1),
       ('33333333-3333-4333-8333-333333333333', 'was-stopped', '', 'stopped',
        1, 512, 2, NULL, 1000, 1000, 1);
    
    -- v46: TestMigrationAddsDefaultLightVMConsole; no lightvm_consoles table yet.
    INSERT INTO lightvm_kernels
      (id, name, description, path, file_name, size_bytes, managed, created_at,
       updated_at)
    VALUES (1, 'vmlinuz', '', '/tmp/vmlinuz', 'vmlinuz', 1, 0, 1000, 1000);
    INSERT INTO lightvm_initramfs
      (id, name, description, path, file_name, size_bytes, managed, created_at,
       updated_at)
    VALUES (1, 'initramfs', '', '/tmp/initramfs', 'initramfs', 1, 0, 1000, 1000);
    INSERT INTO lightvms
      (id, name, description, vcpus, memory_mb, console_enabled, cgroup_class,
       kernel_id, initramfs_id, image, env, executable_path, executable_args,
       append, status, desired_state, created_at, updated_at)
    VALUES ('44444444-4444-4444-8444-444444444444', 'app-01', '', 1, 128, 1, '',
        1, 1, 'alpine', '[]', '/bin/sh', '[]', 'console=hvc0 rdinit=/init',
        'stopped', 'stopped', 1000, 1000);
    
    -- v50: version-50 raw-bandwidth test; no disk_throttle_classes yet.
    INSERT INTO disks
      (id, name, size_gb, vg, thin_pool, created_at, updated_at,
       storage_backend_id, is_template, template_disk_id, prepared_by_script,
       provisioning_script, provisioning_timeout, source, device, size_bytes,
       backend_metadata, attachment_protocol, deletion_state, deletion_error,
       deletion_attempts, deletion_next_attempt_at, deletion_claimed_at,
       read_bps, write_bps)
    VALUES (1, 'legacy-raw', 20, 'vg0', 'rookery', 1000, 1000, NULL, 0, NULL, 0,
        '', '', 'lvm-thin', '/dev/vg0/legacy-raw', 21474836480, '', '', '', '',
        0, 0, 0, 1048576, 524288);
    ```

    *   `TestEnsureStartupScriptSchemaAfterTableDrop` and
        `TestEnsureVPPInterfaceAddressesSchemaAfterTableDrop` need no fixture
        rows: migrate, drop the table under test, close, then call `Open` and
        assert the repair path.
    *   `TestMigrationPreservesLegacyVMsWithoutDisks` derives its expected
        schema version from `migrationVersion` of the last embedded migration
        instead of hardcoding 50, 51, or 52.
    *   Add a `disksHasColumn` helper using `PRAGMA table_info('disks')`. In
        Phase 0, assert that `throttle_class_id` and `disk_throttle_classes`
        are present and that `read_bps`/`write_bps` still exist. In Phase 4,
        assert that `read_bps`/`write_bps` are absent after `0052`.
    *   Add the version-50 test using the fixture above: reopen the store,
        assert the disk remains and is unassigned, and assert the raw columns
        exist in Phase 0/1 and are absent after Phase 4.
    *   Do not add a `dropDiskThrottleSchema` helper; the migration fixture
        avoids dropping the foreign-key column. Delete
        `dropDiskSourceSchema`, `dropKoujaSchema`, `dropCgroupSchema`,
        `dropEntityLabelsSchema`, and `dropLightVMSchema`; the fixture move
        leaves no callers.
*   **Rationale**: The migration suite must model the two-step drop without
    relying on `ALTER TABLE ... DROP COLUMN` for the foreign-key column.

#### 13. `internal/gateway/disk_throttle.go` (Create)

*   **Type / Scope**: gRPC/gateway service implementation.
*   **Proposed Changes**: Implement `diskThrottleService` and its RPC methods.
    Trim and validate names/descriptions; convert the proto `int64` limit values
    directly; call `diskthrottle.Validate`; map errors to the table in the
    Technical Plan; return `disk_count` and `validation_error` from every class
    RPC. The list RPC returns the `classes` array and does not fail when one
    class is invalid. The update RPC loads the existing class, overlays optional
    name/description, and replaces `limits` when present. If `limits` is
    omitted, validate the existing limits and write them unchanged; if they are
    corrupt, return `InvalidArgument` with "supply valid limits to repair."
    If `limits` is present, validate the replacement before writing. The delete
    RPC checks the class count and, when in use, calls
    `ListDiskNamesByThrottleClass(ctx, id, 5)` to build an `Aborted` message
    with the assigned count and up to five disk names. If a concurrent
    assignment causes the conditional delete to fail, re-query the count and
    names before returning `Aborted` so the message is not stale.
*   **Rationale**: The generated gateway exposes the inventory on every
    listener without native handlers.

#### 14. `internal/gateway/gateway.go` (Modify)

*   **Type / Scope**: Gateway wiring.
*   **Proposed Changes**: Instantiate the class service in `gateway.New`, call
    `RegisterDiskThrottleServiceServer`, and add
    `RegisterDiskThrottleServiceHandler` to the HTTP mux list. Update
    `vmDiskToProto` for the new `VMDisk` class id/name fields.
*   **Rationale**: Services are reachable only when registered in both the gRPC
    server and the gateway mux.

#### 15. `internal/gateway/http_status.go` (Modify)

*   **Type / Scope**: HTTP status and Location mapping.
*   **Proposed Changes**: Add
    `/rookery.v1.DiskThrottleService/CreateDiskThrottleClass` to
    `createdMethods`,
    `/rookery.v1.DiskThrottleService/DeleteDiskThrottleClass` to
    `noContentMethods`, and a `*rookeryv1.DiskThrottleClass` case to
    `setLocationHeader` returning `/api/disk-throttle-classes/{id}`. Use the
    full gRPC method-name keys including the leading slash and package, matching
    the existing entries.
*   **Rationale**: Without these entries, create returns `200` without a
    `Location` and delete returns `200` with an empty body; the gateway tests
    expect `201`/`204`.

#### 16. `internal/gateway/readonly.go` (Modify)

*   **Type / Scope**: Disk response conversion.
*   **Proposed Changes**: Remove `ReadBps`/`WriteBps` from `diskToProto`; add
    only the class id and class name. The limits come from the class list, not
    the disk response.
*   **Rationale**: Disk responses must expose the class reference and must stop
    advertising removed fields.

#### 17. `internal/gateway/disk_writes.go` (Modify)

*   **Type / Scope**: Disk create/update writes.
*   **Proposed Changes**: Remove `requestDiskBandwidth` and
    `applyUpdateBandwidth`. At the start of `CreateDisk`, immediately after the
    name check and before the host-device branch, resolve
    `request.ThrottleClassId`: normalize zero to nil, reject a negative id, and
    for a non-nil id call `s.store.GetDiskThrottleClass`. Reject a class with
    `ValidationError` (`InvalidArgument`), map
    `ErrDiskThrottleClassNotFound` to `InvalidArgument`, and reject a class when
    `request.IsTemplate` is true. This preflight must run before
    `createHostDeviceDisk`, backend resolution, `CreateThinVolume`, or any
    preparation/provisioning script. Pass the resolved id into the LVM, kouja,
    and host-device create paths. In `UpdateDisk`:
    acquire `s.mu` before the initial `GetDisk` and hold it through the store
    calls, add `hasClass := request.ThrottleClassId != nil` to the no-op
    guard, compute `targetTemplate` from the request and the current disk, and
    apply the existing store methods in order: reject a positive class when
    `targetTemplate` is true; call `SetDiskTemplate` first only when the
    template flag changes or the disk is converted (it clears the class on
    conversion); call `SetDiskProvisioning` when provisioning changes; call
    `SetDiskThrottleClass` whenever `hasClass` is true. A zero clear is allowed
    even on a template, so it is not skipped when `targetTemplate` is true. When
    the request omits the class and converts the disk to a template,
    `SetDiskTemplate` already cleared it. Apply the
    same handling in the host-device branch. Map
    `ErrDiskThrottleClassInvalid` and `ErrDiskThrottleClassOnTemplate` to
    `InvalidArgument`, `ErrDiskThrottleClassNotFound` to `InvalidArgument`, and
    `ErrDiskThrottleClassInDeletion` to `Aborted`. Apply the same error switches
    in all four `store.CreateDisk` call sites (LVM clone, LVM, kouja, and
    host-device) and in the host-device update branch so class errors never
    fall through to `Internal`.
*   **Rationale**: This is the gateway/API write path; all disk sources must
    obey the same template and class rules.

#### 18. `internal/gateway/disk_kouja.go` (Modify)

*   **Type / Scope**: Kouja disk creation.
*   **Proposed Changes**: Replace the two bandwidth locals with the resolved
    class id. The preflight in item 17 has already validated the class; this
    path stores it in the `store.Disk` record and preserves rollback.
*   **Rationale**: Remote disks support the same class assignment as local
    disks.

#### 19. `internal/gateway/disk_writes_test.go` (Modify)

*   **Type / Scope**: Gateway disk tests.
*   **Proposed Changes**: Replace `TestDiskBandwidthRoutes` with tests that
    create a class, create a disk with it, verify the class id/name, update the
    class and verify the new limits through the class endpoint, clear the
    assignment with zero, reject unknown class ids, and verify a class-only
    `PUT` is not a no-op. Add class assignment/clearing and class-only `PUT`
    cases for a host-device disk and a kouja disk; assert the kouja update
    returns `200` and does not produce a template error.
*   **Rationale**: The old test sends removed fields and would fail; the
    host-device and kouja branches have separate write paths.

#### 20. `internal/gateway/disk_throttle_test.go` (Create)

*   **Type / Scope**: HTTP/gRPC gateway tests.
*   **Proposed Changes**: Test class CRUD over the gateway, every validation
    rule, duplicate names, delete-in-use returning 409 with count and names,
    delete after clearing returning 204, create returning 201 with Location,
    update merge semantics, empty-mode handling, and the JSON shape of
    `limits`, `disk_count`, and timestamps. Add a create-preflight test with an
    unknown class and with an invalid (`ValidationError`) class using a fake
    `DiskProvisioner`; assert `InvalidArgument` and that the
    `CreateThinVolume` counter stays zero. Add a positive assignment on a
    `deleting` disk returning 409 and on a `provisioning` disk succeeding.
*   **Rationale**: The cross-field rules and status mapping are the most
    failure-prone parts of the new API.

#### 21. `internal/gateway/disk_template_test.go` (Modify)

*   **Type / Scope**: Template lifecycle tests.
*   **Proposed Changes**: Add cases that create a class, assign it to a disk,
    mark the disk as a template, assert the response has no class, assert an
    explicit class plus `is_template=true` is rejected, assert a clone can
    select its own class, and assert creating a template with a class fails.
*   **Rationale**: The template rule is deliberate and needs regression
    coverage.

#### 22. `internal/gateway/validation_test.go` (Modify)

*   **Type / Scope**: Protobuf validation tests.
*   **Proposed Changes**: Add cases for negative limits, invalid names,
    over-limit values, and the `gte: 0` rules for the disk class ids.
*   **Rationale**: Generated validation rejects malformed requests before the
    handler runs.

#### 23. `internal/gateway/ui_contract_test.go` (Modify)

*   **Type / Scope**: UI/API contract test.
*   **Proposed Changes**: Seed one class and add `/api/disk-throttle-classes` to
    the list endpoint assertions. Assert that the response is a JSON array.
*   **Rationale**: Keeps the UI-facing JSON contract covered.

#### 24. `internal/qemu/qemu.go` (Modify)

*   **Type / Scope**: QEMU argument construction.
*   **Proposed Changes**: Replace `diskThrottleArgs`'s two `ReadBPS`/`WriteBPS`
    checks with `disk.Throttle.QEMUOptions()` when `disk.Throttle` is non-nil.
    Keep `diskThrottleArgs` string-only; it returns a comma-prefixed fragment or
    an empty string, and the existing `%s` call sites stay unchanged. In
    `buildDiskArgs`, before constructing the drive, return an error if
    `disk.ThrottleError` is set, or if `ThrottleClassID` is non-zero and
    `Throttle` is nil with an empty `ThrottleError`. This defensive check
    prevents a dangling class reference from silently running unlimited. Wrap
    start errors with the disk name, class id, and rendered throttling
    arguments.
*   **Rationale**: Local and remote drives use the same validated class limits.

#### 25. `internal/qemu/plan_test.go` (Modify)

*   **Type / Scope**: QEMU argument tests.
*   **Proposed Changes**: Replace the two-bandwidth assertions with class-based
    assertions. Add table-driven cases for total BPS, directional BPS, total
    IOPS, directional IOPS, burst values, burst lengths, IOPS size, mixed
    BPS-total with IOPS-directional, nil limits, and exact `-drive` strings for
    one and multiple options.
*   **Rationale**: The tests must prove the full mapping and the empty case.

#### 26. `ui/src/inventory/types.ts` (Modify)

*   **Type / Scope**: TypeScript API types.
*   **Proposed Changes**: Remove `read_bps`/`write_bps` from `Disk` and
    `VMDisk`; add `DiskThrottleLimits` and `DiskThrottleClass` types; add
    optional `throttle_class_id` and `throttle_class_name` on `Disk`; add
    optional `throttle_class_id` and `throttle_class_name` on `VMDisk`; add
    `validation_error` to `DiskThrottleClass`. The `Disk` response does not
    carry the limits; the UI resolves them from the loaded class list. Keep the
    cgroup types untouched. The disk fields and the numeric class limits are
    optional because the gateway omits unpopulated JSON fields; `disk_count: 0`
    is also omitted.
*   **Rationale**: The UI must typecheck against the new API.

#### 27. `ui/src/shared/format.ts` (Modify)

*   **Type / Scope**: Shared UI formatter.
*   **Proposed Changes**: The file already exists. Add and export
    `formatBytesIEC` and `formatRateIEC` for `throttle.ts`. Do not change
    `CgroupSettingsPanel`'s SI helpers; those labels remain `KB`/`MB`/`GB` and
    are not consolidated by this feature.
*   **Rationale**: The class summary needs IEC labels without changing the
    cgroup panel's units.

#### 28. `ui/src/inventory/disks/throttle.ts` (Create)

*   **Type / Scope**: UI helper module.
*   **Proposed Changes**: Export `ThrottleMode`, `inferMode`, `validateDraft`,
    `limitsFromDraft`, and `formatThrottleSummary`. Keep units explicit:
    bytes per second, IOPS, seconds, and bytes for `iops_size`.
*   **Rationale**: The class dialog, assignment dialog, and table summary need
    one set of rules and formatting.

#### 29. `ui/src/inventory/disks/DiskThrottleClassDialog.tsx` (Create)

*   **Type / Scope**: Preact dialog.
*   **Proposed Changes**: Build the create/edit form with name, description,
    BPS mode/fields, IOPS mode/fields, and IOPS size. Props:
    `{ classItem: DiskThrottleClass | null; onClose: () => void;
    onSaved: (item: DiskThrottleClass) => void }`. Prefill on edit, infer the
    initial modes, hide inactive groups, run `throttle.ts` validation, POST or
    PUT the class, and show server errors.
*   **Rationale**: This is the unified place where operators define policy.

#### 30. `ui/src/inventory/disks/DiskThrottleAssignmentDialog.tsx` (Create)

*   **Type / Scope**: Preact dialog.
*   **Proposed Changes**: Props: `{ disk: Disk; classes: DiskThrottleClass[];
    onClose: () => void; onSaved: (disk: Disk) => void }`. Offer **Unlimited**
    plus every class, show the selected class summary and assigned disk count,
    disable the form for templates, and PUT `{throttle_class_id: id}` or
    `{throttle_class_id: 0}`. Refresh both disks and classes on success.
*   **Rationale**: Replaces the raw Limits dialog with a class reference.

#### 31. `ui/src/inventory/disks/DiskBandwidthDialog.tsx` (Delete)

*   **Type / Scope**: Obsolete UI.
*   **Proposed Changes**: Remove the file and its import.
*   **Rationale**: A raw two-number editor would violate the one-policy-source
    goal.

#### 32. `ui/src/inventory/disks/DiskDialog.tsx` (Modify)

*   **Type / Scope**: Disk creation form.
*   **Proposed Changes**: Accept `classes: DiskThrottleClass[]`, replace the
    read/write BPS inputs with a **Throttling class** select defaulting to
    **Unlimited**, disable and clear the select when the new disk is marked as
    a template, and send `throttle_class_id` in both the LVM/kouja and
    host-device payloads. Remove the old bandwidth state.
*   **Rationale**: New disks choose an optional class at creation.

#### 33. `ui/src/inventory/disks/DisksTab.tsx` (Modify)

*   **Type / Scope**: Disks tab shell.
*   **Proposed Changes**: Load `GET /api/disk-throttle-classes` with the other
    data, add the **Throttling classes** panel and table, branch on
    `route.segments` for the class dialog (`disk-throttle-classes` + `new` or
    `{id}` + `edit`), pass `classes` to `DiskDialog`, add the assignment dialog
    state, replace **Limits** with **Throttling**, show the class name and the
    summary resolved from the loaded class list, disable the row action for
    templates, and refresh disks and classes after any class write or
    assignment. Delete the `DiskBandwidthDialog` import and render path. Remove
    the local `formatRate` helper; use the shared IEC formatter.
*   **Rationale**: The Disks tab is the unified place for storage backends,
    disks, and throttling policy.

#### 34. `ui/src/inventory/app.ts` (Modify)

*   **Type / Scope**: Inventory routing.
*   **Proposed Changes**: Map a top-level `disk-throttle-classes` path to the
    `disks` tab so the class deep links survive a refresh.
*   **Rationale**: The repository uses path-based inventory routing.

#### 35. `internal/webui/static/style.css` (Modify)

*   **Type / Scope**: UI styles.
*   **Proposed Changes**: Add selectors for the two throttling mode groups and
    the class summary line; reuse the existing panel, table, badge, dialog, and
    form classes.
*   **Rationale**: The mode groups need clear grouping without a new visual
    system.

#### 36. `internal/webui/static/build/app.js` (Modify, generated)

*   **Type / Scope**: Committed UI bundle.
*   **Proposed Changes**: Rebuild with `make ui-build`.
*   **Rationale**: `internal/webui` embeds this bundle.

#### 37. `cmd/terraform-provider-rookery/internal/provider/client.go` (Modify)

*   **Type / Scope**: Terraform provider gRPC client bundle.
*   **Proposed Changes**: Add `DiskThrottleClasses
    rookeryv1.DiskThrottleServiceClient` and initialize it.
*   **Rationale**: The provider needs the generated client.

#### 38. `cmd/terraform-provider-rookery/internal/provider/provider.go` (Modify)

*   **Type / Scope**: Provider registration.
*   **Proposed Changes**: Add `NewDiskThrottleClassResource` to `Resources` and
    `NewDiskThrottleClassDataSource` to `DataSources`.
*   **Rationale**: Unregistered resources are invisible to Terraform.

#### 39. `cmd/terraform-provider-rookery/internal/provider/resource_disk_throttle_class.go` (Create)

*   **Type / Scope**: Terraform resource.
*   **Proposed Changes**: Define `rookery_disk_throttle_class` with the schema
    from the Technical Plan: required name, optional+computed description, 19
    optional+computed numeric fields with `UseStateForUnknown`, computed
    `validation_error`, and computed `disk_count`. Implement Create, Read,
    Update, Delete, and ImportState. Convert the model to/from
    `DiskThrottleLimits`; let the API own cross-field validation. Use
    hand-written runtime checks in Create/Update for non-negative values and the
    name pattern, mirroring `cgroup.go`; do not add the
    `terraform-plugin-framework-validators` module. Import parses the integer
    id.
*   **Rationale**: Terraform users need the same inventory as UI users without a
    new dependency.

#### 40. `cmd/terraform-provider-rookery/internal/provider/data_source_disk_throttle_class.go` (Create)

*   **Type / Scope**: Terraform data source.
*   **Proposed Changes**: Look up a class by id or name, expose every limit,
    `validation_error`, and `disk_count`, and follow the existing
    `rookery_disk` data source's exactly-one-of-id-or-name behavior. There is no
    name lookup RPC; for a name, list all classes and filter by name, then
    return an error unless exactly one matches. Add
    `data_source_disk_throttle_class_test.go` with both paths and the
    zero/multiple-match errors.
*   **Rationale**: Data sources reference an existing operator-defined policy.

#### 41. `cmd/terraform-provider-rookery/internal/provider/resource_disk.go` (Modify)

*   **Type / Scope**: Existing disk resource.
*   **Proposed Changes**: Add optional+computed `throttle_class_id` with
    `UseStateForUnknown` and computed `throttle_class_name` to `diskModel` and
    the schema. Add two helpers:
    `createThrottleClassID(config, plan types.Int64) (int64, error)` and
    `updateThrottleClassID(config, plan types.Int64) (*int64, error)`. Create
    uses the first (null config -> 0, known config -> id, unknown config with
    known plan -> plan, otherwise diagnostic). Extract
    `buildDiskUpdateRequest(plan diskModel, configClass types.Int64)
    (*rookeryv1.UpdateDiskRequest, error)` and use it from `Update`; it calls
    `updateThrottleClassID` (null config -> nil/preserve, known config -> id,
    unknown config with known plan -> plan, otherwise diagnostic) and sets the
    id, template flag, provisioning fields, and `ThrottleClassId` as a proto
    optional. `setDiskState`
    maps no class to `types.Int64Value(0)` and a null name to
    `types.StringNull()`. Implement `ResourceWithModifyPlan` and, in
    `ModifyPlan`, return a configuration error when `is_template = true` and
    the configured `throttle_class_id` is known positive or unknown; a null
    config is allowed. Removing the attribute does not
    clear it because the optional+computed value is reused from state; the user
    must set `0`. When converting with no class, send `0`.
*   **Rationale**: Terraform-created disks choose a class and convert to
    templates without an API 400 caused by a reused positive state value.

#### 42. `cmd/terraform-provider-rookery/internal/provider/data_source_disk.go` (Modify)

*   **Type / Scope**: Existing disk data source.
*   **Proposed Changes**: Add computed `throttle_class_id` and
    `throttle_class_name` and populate them from the API.
*   **Rationale**: Data-source consumers see the assigned policy.

#### 43. `cmd/terraform-provider-rookery/internal/provider/resource_disk_throttle_class_test.go` (Create)

*   **Type / Scope**: Provider unit tests.
*   **Proposed Changes**: Test model-to-proto and proto-to-model conversion for
    both families, all numeric fields, `validation_error`, `disk_count`, failed
    create/update handling, import parsing, and the `0`/unset mapping. Add a
    case that removes a numeric field from configuration and asserts the
    documented preserve-vs-clear behavior. Add a fake
    `rookeryv1.DiskThrottleServiceClient` (all five methods, or a bufconn gRPC
    server) that returns a class with `ValidationError`; use it in a resource
    Read test and in `data_source_disk_throttle_class_test.go` to assert the
    computed `validation_error` is surfaced and not cleared or replaced.
*   **Rationale**: Provider conversion bugs are not caught by schema validation;
    corrupt-class recovery depends on the provider exposing the server error.

#### 44. `cmd/terraform-provider-rookery/internal/provider/resource_disk_test.go` and `kouja_state_test.go` (Modify)

*   **Type / Scope**: Existing provider tests.
*   **Proposed Changes**: Extend `TestSetDiskStateHostDevice` in
    `resource_disk_test.go` and `TestSetDiskStateRemoteFields` in
    `kouja_state_test.go` with class id/name assertions, a no-class `0` case,
    and a template-clears-class case. Add
    `TestCreateThrottleClassID`: null config with unknown plan returns `0`;
    unknown config with known plan returns the plan id; unknown config with
    unknown plan returns an error. Add
    `TestBuildDiskUpdateRequestKoujaClassOnly`: build a kouja `diskModel` whose
    only change is `ThrottleClassID = 7`, call
    `buildDiskUpdateRequest(plan, configClass)` with a known config, and
    assert the request carries `ThrottleClassId = 7`, `IsTemplate = false`, and
    no template error. Add `TestBuildDiskUpdateRequestNullConfig` (nil field,
    preserve) and `TestBuildDiskUpdateRequestUnknown` (error, not `0`). Add
    `TestModifyPlanTemplateClass`: null config is allowed; known positive config
    and unknown config are configuration errors when `is_template = true`.
*   **Rationale**: The provider always sends the template flag; the class-only
    kouja path must not regress into a template or 400 error, and the computed
    attributes must be set and cleared correctly.

#### 45. `cmd/terraform-provider-rookery/README.md` (Modify)

*   **Type / Scope**: Provider documentation.
*   **Proposed Changes**: Add the resource and data source to the lists,
    document every field and unit, the `0`-clears/omitted-preserves behavior,
    the template interaction, and a short example.
*   **Rationale**: The provider README is the published usage reference.

#### 46. `cmd/terraform-provider-rookery/examples/complete/main.tf` (Modify)

*   **Type / Scope**: Example configuration.
*   **Proposed Changes**: Add a `rookery_disk_throttle_class` named `standard`
    and assign it to `rookery_disk.root`. Leave the template disk without a
    class.
*   **Rationale**: The complete example exercises the resource and the template
    restriction.

#### 47. `cmd/terraform-provider-rookery/examples/data-sources/main.tf` (Modify)

*   **Type / Scope**: Example configuration.
*   **Proposed Changes**: Add a
    `data "rookery_disk_throttle_class" "standard"` lookup by name `standard`
    and a comment that the class must exist before planning.
*   **Rationale**: Demonstrates the data source and the name lookup.

#### 48. `docs/api.md` (Modify)

*   **Type / Scope**: API reference.
*   **Proposed Changes**: Add the five class endpoints; add
    `DiskThrottleService` to the gateway service enumeration; update the disk
    PUT description, including the `PUT /api/disks/{id}` table row that
    currently mentions `read_bps`/`write_bps`; replace the current paragraph
    with the class model, units, QEMU mapping, validation rules, live-reference
    behavior, template clearing, delete-in-use behavior, the QEMU 10.0.13
    tested version, the 2 MB/s (2,000,000 bytes/s) warning, and the
    per-disk-not-per-VM warning. Mention the Terraform resource and data source
    names.
*   **Rationale**: `docs/api.md` is the published API reference.

#### 49. `docs/architecture.md` (Modify)

*   **Type / Scope**: Architecture and data model.
*   **Proposed Changes**: Add `disk_throttle_classes` to the storage/data model
    table and describe the disk-to-class reference and start-time resolution.
*   **Rationale**: The architecture document lists the persistent stores.

#### 50. `docs/operations.md` (Modify)

*   **Type / Scope**: Operations guide.
*   **Proposed Changes**: Replace the per-disk read/write bandwidth paragraph
    with the class model and the next-start behavior; add the pre-upgrade
    backup step for this release, the downgrade warning (an older server does
    not know the class table and the raw columns are gone), the `0052` drop,
    and the QEMU 10.0.13 tested-version prerequisite.
*   **Rationale**: The operations guide currently describes removed controls.

#### 51. `docs/api-design.md` (Modify)

*   **Type / Scope**: API design notes.
*   **Proposed Changes**: Add `DiskThrottleService` to the generated service
    list and document the `201`/`204` status mapping and Location header.
*   **Rationale**: The API design notes list the generated services and status
    conventions.

#### 52. `AGENTS.md` (Modify)

*   **Type / Scope**: Repository guidance.
*   **Proposed Changes**: Replace the `read_bps`/`write_bps` sentence in the
    disk bullet with the class model, endpoints, validation rules, template
    clearing, delete blocking, and migrations `0051`/`0052`. Add
    `DiskThrottleService` to the gateway service sentence; do not rewrite the
    unrelated service enumeration in this change. Add the Terraform provider
    resource and the Disks tab panel/routes.
*   **Rationale**: `AGENTS.md` is the operational contract for future changes.

#### 53. `docs/imgs/disks.png` (Modify, manual post-merge)

*   **Type / Scope**: Documentation screenshot.
*   **Proposed Changes**: Refresh the Disks tab screenshot to show the
    **Throttling classes** panel, the class selector, and the **Throttling**
    row action after the UI lands.
*   **Rationale**: The released documentation image should not show removed
    controls. This is a manual documentation task, not a code step.

#### 54. `README.md` (Modify)

*   **Type / Scope**: Installation prerequisites.
*   **Proposed Changes**: Document that the class feature is tested with QEMU
    10.0.13 and that earlier versions are not verified; point to `docs/api.md`
    for the class model.
*   **Rationale**: The class feature has an explicit QEMU version contract; the
    main installation entry point should state it.

#### 55. `internal/store/disk_deletion.go` (Modify)

*   **Type / Scope**: Disk deletion and provisioning state.
*   **Proposed Changes**: Clear `throttle_class_id` in `MarkDiskDeleting`,
    `RecordDiskDeletionFailure`, and `SetDiskDeleteFailed`. `ResetDiskDeletion`
    inherits the clear through `MarkDiskDeleting`. `FinalizeProvisioning` keeps
    the class on a ready disk. The assignment guard is in `disk_throttle.go`
    (item 8); this file only clears on transitions.
*   **Rationale**: The class-pinning guarantee spans these methods; they are not
    in `store.go`.

### Phased Execution Plan

Each phase must leave the tree buildable and testable.

*   **Phase 0 — Additive API and model:** item 1 and the `disk_throttle`
    portion of item 3, including the regenerated OpenAPI; items 4, 5, 6, 8,
    item 12's Phase 0 migration-test changes, and item 9's class fields and
    joins while keeping `ReadBPS`/`WriteBPS` and the raw scans/writes. Gate:
    `go build ./...`, `go test ./internal/diskthrottle ./internal/store`, and
    `make proto-check`.
*   **Phase 1 — Disk cutover:** item 2 and the `disk.pb.go`/`disk.pb.validate.go`
    portion of item 3; item 9's removal of `ReadBPS`/`WriteBPS` and the disk/VM
    query cutover; items 10, 11, 13-25,
    and 55; and the UI changes that remove `read_bps`/`write_bps`: items 26, 27,
    28, 30, 31, 32, and item 33's disk table, form, row-action, class-list
    loading, and `classes` prop wiring. Deleting the old bandwidth dialog and
    adding the shared formatter and assignment dialog in this phase keeps
    `make ui-build` green. Keep the raw database columns. Gate:
    `go build ./...`, `go test ./...`, `make ui-build`. Any phase that changes
    UI sources rebuilds and commits `internal/webui/static/build/app.js`.
*   **Phase 2 — Class UI:** item 29 and item 33's class panel and class-dialog
    changes, items 34-36. Gate: `make ui-build` and the manual UI checks.
    Rebuild and commit the UI bundle again.
*   **Phase 3 — Terraform provider:** items 37-47. Gate:
    `go build ./cmd/terraform-provider-rookery`, provider tests, example
    validation.
*   **Phase 4 — Drop raw columns (pre-release):** item 7 and item 12's
    Phase 4 absent-column assertion and version-50 test. Gate:
    `go test ./internal/store` and the full migration suite. This phase runs
    after Phase 3 and before the release; it requires the pre-upgrade backup.
*   **Phase 5 — Documentation and final verification:** items 48, 49, 50, 51,
    52, and 54. Item 50 covers the class/`0051` backup, the downgrade note, and
    the `0052` drop. Run `make proto-check`, the full test/lint/format commands,
    the QEMU acceptance run, the manual API flow, UI checks, and regression
    checks. Item 53 is a post-merge manual documentation task.

### Verification Plan

*   **Protobuf**: Run `make proto-check`. It regenerates, compiles, and fails on
    any generated diff.
*   **Build and tests**: Run `make build`, `go test ./...`, `go vet ./...`,
    `golangci-lint run ./...`, and `gofmt -l .` (expect no output). The
    repository's 20% global and 80% changed-line coverage floors apply.
*   **Provider**: Run `go build ./cmd/terraform-provider-rookery` and
    `go test ./cmd/terraform-provider-rookery/...`. Run `terraform fmt -check`
    and `terraform validate` on `examples/complete` and
    `examples/data-sources` with the `dev_overrides` documented in
    `cmd/terraform-provider-rookery/README.md`.
*   **QEMU acceptance**:
    1.  Run `qemu-system-x86_64 --version` and record the local version. The
        acceptance matrix is verified with 10.0.13; earlier versions are not
        covered.
    2.  `truncate -s 1M /tmp/throttle-test.raw`.
    3.  Run `timeout 5s qemu-system-x86_64 -machine q35 -accel tcg -display none
        -S -drive
        file=/tmp/throttle-test.raw,format=raw,if=none,id=d0,throttling.bps-read=524288000,throttling.bps-write=314572800,throttling.iops-total=2000
        -device virtio-blk-pci,drive=d0`. This is the directional-BPS plus
        total-IOPS case. Expect exit 124 (the timeout killed a running, paused
        QEMU), not a QEMU argument error.
    4.  Repeat with a total-BPS plus total-IOPS class and a total-BPS plus
        directional-IOPS class.
    5.  At class create, verify that a mixed total/read-write class, a zero base
        with a burst, a burst length over `THROTTLE_VALUE_MAX / max`, a
        burst length above `UINT_MAX`, and `iops_size` without IOPS return
        `400`. These are API validation failures, not VM-start failures.
    *   The representative acceptance matrix covers: directional BPS
        (`bps-read`/`bps-write`); total BPS plus total IOPS; total BPS plus
        directional IOPS; directional BPS plus total IOPS; a base with
        `*_max`/`*_max_length`; and an IOPS base with `iops_size`. Each valid
        row is run with the generated options; each invalid counterpart is
        rejected at class create.
*   **Manual API flow**:
    1.  Start the server with a thin pool:
        `go run ./cmd/rookery serve -db /tmp/rookery-throttle.db -vg vg0
        -thin-pool pool` (substitute the host's names); alternatively register a
        host-device disk.
    2.  Create a class with `curl -sS -X POST
        http://127.0.0.1:8080/api/disk-throttle-classes -H 'Content-Type:
        application/json' -d '{"name":"standard","description":"test",
        "limits":{"bps_read":524288000,"bps_write":314572800}}'`; expect 201
        and a `Location` header.
    3.  Create a disk with `throttle_class_id` from the response; expect 201.
    4.  `GET` the disk and confirm the class id/name; `GET` the class and
        confirm the limits.
    5.  `PUT` the class with the complete `limits` object and one value
        changed; `GET` the class and confirm the changed value while all other
        values are unchanged.
    6.  `DELETE` the class while assigned; expect 409 with the disk count and
        names.
    7.  `PUT` the disk with `{"throttle_class_id":0}`; expect 200 and no class.
    8.  `DELETE` the class; expect 204.
    9.  Repeat with an invalid class (for example, a burst maximum with no
        base) and expect 400.
*   **UI checks**: Run `make ui-build`. Verify the Disks tab shows the class
    panel, the create dialog lists classes and clears the select for a
    template, the assignment dialog assigns and clears, a deep link to
    `/disk-throttle-classes/new` survives a refresh, and the **Throttling** row
    action is disabled for templates. Confirm the generated
    `internal/webui/static/build/app.js` diff is committed.
*   **Regression checks**:
    *   An unlimited disk produces no `throttling.*` arguments.
    *   A template cannot be created with a class; conversion clears it.
    *   A clone can choose its own class.
    *   A running VM's QEMU arguments do not change until it restarts. Inspect
        with `pgrep -af qemu-system` and confirm the expected
        `throttling.*` options on the process.
    *   A kouja disk that enters `deleting` no longer pins its class.
*   **Migration checks**:
    *   In this release, a version-50 database with non-zero raw limits migrates
        through `0051` and `0052`, keeps the disk, leaves it unassigned, and
        ends with no `read_bps`/`write_bps` columns.
    *   The seven legacy simulations use `migrateToVersion`; the expected schema
        version is derived from the embedded migration set. No test drops the
        foreign-key column.
