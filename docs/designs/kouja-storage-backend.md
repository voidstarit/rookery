# Kouja Storage Backend for Rookery

*   **Author:** Rookery maintainers
*   **Status:** Proposed
*   **Date:** 2026-11-07

--------------------------------------------------------------------------------

## Overview

Rookery provisions disks from LVM thin pools and supports host block-device
passthrough. A storage backend record names a driver and driver-specific
configuration, but `lvm-thin` is the only provisioned driver. This design adds a
`kouja` driver. kouja is a per-host block-storage agent that creates thick LVM
logical volumes, selects a volume group through a named storage class, and
exports a volume over iSCSI to one initiator with CHAP.

The data path differs from the existing storage model. Rookery does not create a
host kernel iSCSI session and does not register a `/dev/sdX` path. Each QEMU
process opens the iSCSI LUN directly through QEMU's libiscsi block driver when
the VM starts. QEMU owns the session for the lifetime of the VM.

The kouja project now ships a generated client SDK (`gen/client`), typed stubs
(`gen/kouja/v1`), an OpenAPI document, request-ID propagation, and retry
helpers. Rookery uses that SDK instead of maintaining a hand-copied kouja proto
or a second client implementation.

The first implementation is iSCSI only. NVMe/TCP, snapshots, clones, and volume
migration are not part of this design.

## The Problem

Rookery's disk model assumes a local block device that exists before QEMU
starts. Host-device passthrough follows the same assumption. That model does not
fit a remote storage agent:

*   kouja owns the volume and its export. The target IQN, portal, LUN, and CHAP
    credential are produced by kouja, not by the Rookery host.
*   A QEMU process can open an iSCSI target directly, so a host kernel session
    would add a second owner for the same LUN and a second failure path.
*   `disks.device` has no path meaning for a LUN that QEMU reaches over TCP.
*   A VM restart needs the current credential and export details, not a stale
    device path copied into the database.

Without an explicit driver, an operator would create volumes outside Rookery,
run `iscsiadm` by hand, and register a transient `/dev/sdX` device. That path can
resolve to a different volume after a reboot, so a later delete can target the
wrong device.

## Goals

*   Register a storage backend with driver `kouja` and a configuration that
    names the kouja endpoint, mTLS client material, the kouja storage class,
    the iSCSI/NVMe initiator IQN/NQN, and an optional protocol portal override.
*   Keep the disk model protocol-neutral: the same persisted metadata and
    attachment types can represent a future kouja NVMe/TCP export or a Ceph
    RBD disk without changing the disk table, API envelope, or QEMU start
    contract.
*   Create a disk through Rookery and have Rookery call kouja to create a thick
    LVM volume of the requested size in the configured storage class, export it
    over iSCSI, and persist the returned volume, export, and owner identity.
*   Start a VM with a kouja-backed disk by giving QEMU a libiscsi block device
    that uses the live export target, portal, LUN, initiator, and a CHAP
    credential fetched at start time. No `iscsiadm` call and no host kernel
    iSCSI session are required.
*   Stop a VM without deleting its volume. The next start fetches the current
    export and credential. If the session drops while the VM is running, QEMU
    retries the connection; if QEMU exhausts its retry budget and returns an
    I/O error, the VM pauses, and an operator can resume it after the target
    returns.
*   Delete a kouja-backed disk through one background worker. The disk enters
    `deleting`; on success its record is removed; on a permanent failure it
    enters `delete_failed` with the error. A thin-LVM delete failure becomes
    `delete_failed` immediately. A `delete_failed` disk can be retried.
*   Report per-backend health. A control-plane check covers the kouja endpoint,
    TLS, storage class, iSCSI advertisement, and QEMU capability. An optional
    deep check creates and exports a small volume and confirms through QEMU's
    monitor that the block device opened.
*   Preserve existing behaviour for `lvm-thin` and host-device disks. No
    existing VM or disk record changes meaning.
*   Expose the driver through the protobuf API, gateway validation, the
    inventory UI, the Terraform provider, and the operator documentation.

## Non-Goals

*   Implementing the NVMe/TCP or Ceph drivers in this cut. The shared types,
    metadata, API fields, and QEMU start plan are designed to add them without
    reshaping the disk record.
*   A host kernel iSCSI or NVMe/TCP session managed with `open-iscsi`,
    `nvme-cli`, or `iscsiadm`.
*   Mounting, formatting, or otherwise touching the guest filesystem from the
    Rookery host.
*   kouja templates, snapshots, or clones. kouja does not implement them.
*   Importing volumes that were created outside Rookery.
*   Multi-initiator or read/write-many access. Rookery attaches a disk to at
    most one VM, and kouja exports to one initiator per export.
*   Automatic credential rotation, volume resize, or online expansion.
*   Cross-host placement, migration, or high availability.
*   Replacing kouja's reconciliation, drift handling, or admin actions.

## Resolved Decisions

*   **Kouja client SDK:** depend on `github.com/voidstarit/kouja` at the latest
    release, or the current `main` commit when no release exists. Record the
    exact version in `go.mod`. Use `gen/client` and `gen/kouja/v1` from that
    module. Do not vendor a second copy or maintain a hand-copied API appendix.
*   **Kouja error and retry handling:** use `client.ErrorInfo`,
    `client.IsRetryable`, and `client.WithRequestID` from the generated client.
    Reuse the same `request_id` when retrying a mutation.
*   **Layer separation:** `source`/driver (`lvm-thin`, `host-device`, `kouja`,
    future `ceph`) is separate from the attachment protocol (`local`,
    `iscsi`, `nvme-tcp`, `rbd`). A kouja backend can export iSCSI now and
    NVMe/TCP later without changing the disk record shape.
*   **QEMU dependency direction:** QEMU uses a protocol-neutral
    `internal/blockdev.Attachment`. The package has typed protocol variants
    for iSCSI, NVMe/TCP, and Ceph RBD. Neither `internal/qemu` nor
    `internal/blockdev` imports `internal/kouja` or any driver. A driver maps
    its backend export to the neutral attachment.
*   **Persisted versus runtime attachment:** `disk.backend_metadata` stores a
    versioned, secret-free `blockdev.Reference`. The live `blockdev.Attachment`
    is produced by `StartPreparer` at VM start and carries the CHAP/PSK
    credential. Credentials are never written to SQLite.
*   **Disk device:** `disks.device` stays empty for every remote attachment.
    The API returns a display-only, protocol-aware `locator` derived from
    `backend_metadata`; `device` remains a path only for local LVM and
    host-device disks.
*   **Disk protocol:** `disks.attachment_protocol` and the API `protocol` field
    distinguish `local`, `iscsi`, `nvme-tcp`, `rbd`, and future protocols.
    `source` remains the backend/driver identity. Existing rows with an empty
    protocol are treated as `local`.
*   **Disk size:** `disks.size_gb` is the requested size. `disks.size_bytes` is
    the actual size returned by kouja, or the exact host device size for
    passthrough disks. There is no `desired_size_bytes` field.
*   **Backend selection:** explicit `storage_backend_id`; then a backend named
    `default`; then the first `lvm-thin` backend for legacy compatibility; then
    the only registered backend; then the readiness-bootstrapped LVM default
    when no backend records exist; otherwise an error. There is no
    cross-driver fallback.
*   **Disk states:** `disks.deletion_state` is empty for a ready disk, or
    `provisioning`, `deleting`, or `delete_failed`. `provisioning` covers a
    remote create that has not finalized. `deleting` is a queued kouja delete.
    `delete_failed` is terminal until the operator retries.
*   **Deletion:** one worker with an atomic claim/lease. Thin-LVM deletes stay
    synchronous in the gateway; a failure sets `delete_failed` immediately.
    Kouja deletes are asynchronous and retry with 30s/1m/5m/15m/30m/1h backoff
    up to 8 attempts. A future Ceph driver uses the same queued contract. A
    `delete_failed` disk can be reset to `deleting`.
*   **Readiness:** the global `/api/status` `ready` flag is VPP ready and, when
    a thin pool is configured, thin-pool ready. It does not depend on kouja.
    Kouja readiness is per-backend health. There is no `alternate_storage`.
*   **Health:** save-time validation is local only (syntax, paths, key
    permissions, SAN). Network, class, QEMU, and data-path checks run through
    the health endpoint. An unreachable agent does not block saving a backend.
*   **HTTP timeouts:** keep the normal 30-second write timeout. `PathDeadline`
    sets both a 15-minute request context deadline and the HTTP write deadline
    for disk create, disk delete, VM create/start/reset, storage-backend
    health/test, and the deep data-path test. The context deadline is what
    stops a wedged RPC.
*   **Resume:** the VM response carries a `run_state` from QEMU's monitor. The
    UI shows Resume only when the state starts with `paused`; the API checks the
    state again and re-queries after `cont`.
*   **iSCSI timeout:** `file.timeout=30` is the per-request libiscsi timeout,
    not a promise that the VM pauses after 30 seconds. QEMU retries timed-out
    commands; the real pause bound is established by a fault-injection test.
*   **Owner:** the owner is the full client URI SAN,
    `spiffe://kouja/caller/<name>`. It is parsed and validated when the backend
    is saved, stored in disk metadata, and compared at health time. Certificate
    rotation must keep the same URI SAN.

## Open Questions

None. The remaining choices are implementation details with one defensible
answer.

## Critical User Journeys (CUJs)

### CUJ 1: Create a kouja-backed disk

*   **Given**: A kouja agent is reachable over mTLS, the configured storage
    class maps to a volume group with enough allocatable capacity, and a
    storage backend with driver `kouja` exists.
*   **When**: An operator creates a disk with a name and size and selects that
    backend.
*   **Then**: Rookery creates a `provisioning` disk record, calls kouja with
    `export=true`, verifies the returned volume ID, resource UUID, owner, and
    `EXPORTED` export state, updates the record to ready, and returns a disk
    whose source is `kouja`. No host block device is required.

### CUJ 2: Start a VM with a kouja-backed disk

*   **Given**: A stopped VM has one or more kouja-backed disks attached, and the
    kouja agent is reachable.
*   **When**: The operator starts the VM.
*   **Then**: Rookery prepares the iSCSI attachments and CHAP credentials before
    taking the VM run lock. The `internal/qemu` wrapper writes a short-lived
    secret file, and QEMU starts with an iSCSI block device that names the live
    target, portal, LUN, initiator, and secret. The guest sees a virtio block
    device. No host kernel iSCSI session exists.

### CUJ 3: Stop and restart a VM

*   **Given**: A VM is running with a kouja-backed disk.
*   **When**: The operator stops and later starts the VM.
*   **Then**: Stopping QEMU closes the iSCSI session and removes its secret
    files. Starting again fetches the current export and credential.

### CUJ 4: Delete a kouja-backed disk

*   **Given**: A kouja-backed disk is attached to no VM and no QEMU process uses
    it.
*   **When**: The operator deletes the disk.
*   **Then**: Rookery marks the disk `deleting` and returns `202 Accepted` with
    the disk state. The worker unexports and deletes the kouja volume, then
    removes the disk record. On a retryable failure it retries with backoff; on
    the retry cap or a permanent error it sets `delete_failed` with the error.
    Rookery never forces the operation.

### CUJ 5: kouja is unavailable

*   **Given**: A kouja backend exists but the agent is unreachable or its
    storage class is missing.
*   **When**: Rookery starts, an operator creates a disk, or a VM using a
    kouja-backed disk starts.
*   **Then**: Rookery starts and reports the backend as unhealthy. Disk create
    and VM start fail with a message naming the backend and the kouja error.
    Existing VMs that do not use kouja continue to work. Rookery does not create
    a local disk as a fallback.

### CUJ 6: Recover after a storage outage

*   **Given**: A VM is running with a kouja-backed disk over iSCSI.
*   **When**: The kouja agent or the network path becomes unavailable long
    enough for QEMU's iSCSI retries to return an error.
*   **Then**: QEMU's configured `werror=stop,rerror=stop` pauses the VM instead
    of delivering the error to the guest. The VM response reports a paused
    `run_state`; an operator resumes it only after the target returns. The
    pause bound is measured by a fault-injection test, not assumed from
    `file.timeout`.

### CUJ 7: Test a kouja backend data path

*   **Given**: A kouja backend is saved and healthy at the control plane.
*   **When**: The operator runs the deep data-path test.
*   **Then**: Rookery creates a temporary volume, exports it, fetches the CHAP
    credential, starts QEMU with the production iSCSI arguments, confirms
    through QEMU's monitor that the block device opened, then removes the
    temporary volume and export. The result names the failed stage when it does
    not work.

## Technical Plan (Architecture)

### Component map

```text
+-------------------+        mTLS gRPC         +----------------------+
| Rookery API       | -----------------------> | kouja-agent          |
| storage driver    |  Create/Export/Get/Delete | (per storage host)   |
| cleanup worker    |                          | LVM + LIO + SQLite   |
+---------+---------+                          +----------+-----------+
          |                                               |
          | QEMU libiscsi                                  | iSCSI target
          +---------------------------------------------->+
          |                                               |
   +------+------+                                        |
   | qemu-system |  -drive file.driver=iscsi,...           |
   +-------------+                                        |
                                                          |
   SQLite: storage_backends.config, disks.backend_metadata,
           disks.deletion_state
```

Rookery is the control plane. kouja is the storage-host agent. QEMU opens the
data path directly. Rookery does not create a host kernel session or a `/dev`
node for remote disks. The diagram shows the iSCSI path delivered now; the same
`blockdev.Attachment`/`StartPlan`/protocol-builder boundary carries NVMe/TCP
and Ceph RBD later.

### Kouja client SDK and dependency

Rookery depends on `github.com/voidstarit/kouja` at the latest release, or the
current `main` commit when no release exists. The exact version is pinned in
`go.mod` and recorded in `THIRD_PARTY_NOTICES.md`. Import:

*   `github.com/voidstarit/kouja/gen/client` for `NewGRPC`,
    `LoadTLSConfig`, `WithRequestID`, `ErrorInfo`, and `IsRetryable`.
*   `github.com/voidstarit/kouja/gen/kouja/v1` for request and response types.

The wrapper in `internal/kouja/client.go`:

1. Calls `client.LoadTLSConfig(certFile, keyFile, caFile)` in that exact order.
2. Sets `tls.Config.ServerName` when `server_name` is configured.
3. Creates the client with `client.NewGRPC(target, tlsConfig)`.
4. Propagates request IDs with `client.WithRequestID`.
5. Classifies errors with `client.ErrorInfo` and `client.IsRetryable`.

The upstream client documentation is
<https://github.com/voidstarit/kouja/blob/main/docs/clients.md> and
<https://github.com/voidstarit/kouja/blob/main/docs/client-errors.md>. The
generated package is the contract; the design does not copy its messages.

`internal/kouja/errors.go` maps errors to `Success`, `Retry`, or `Terminal` for
Rookery's cleanup policy. It starts from `client.IsRetryable`, but cleanup has
two explicit overrides because the kouja SDK marks them non-retryable:

*   `NOT_FOUND` is success for `UnexportVolume`, `DeleteVolume`, and orphan
    cleanup; the desired end state already exists.
*   `SESSION_ACTIVE` and `SESSION_UNKNOWN` are `Retry` for disk deletion while
    the disk is unattached. Rookery waits for the target session to clear; it
    never logs out the initiator and never sends `force=true`.

All other non-retryable reasons are `Terminal`. Start does not retry create or
export; a failure is returned to the operator.

### Protocol-neutral attachment model

`internal/blockdev` is the leaf package shared by store, drivers, storage, and
QEMU. It separates a persisted reference from a live attachment:

```go
type Protocol string

const (
    ProtocolLocal   Protocol = "local"
    ProtocolISCSI   Protocol = "iscsi"
    ProtocolNVMeTCP Protocol = "nvme-tcp"
    ProtocolRBD     Protocol = "rbd"
)

type Reference struct {
    Protocol Protocol          `json:"protocol"`
    ISCSI    *ISCSIReference   `json:"iscsi,omitempty"`
    NVMeTCP  *NVMeTCPReference `json:"nvme_tcp,omitempty"`
    RBD      *RBDReference     `json:"rbd,omitempty"`
}

type ISCSIReference struct {
    Portal       string `json:"portal"`
    ExportPortal string `json:"export_portal,omitempty"`
    Target       string `json:"target"`
    LUN          int    `json:"lun"`
    Initiator    string `json:"initiator,omitempty"`
}

type NVMeTCPReference struct {
    Address       string `json:"address"`
    ExportAddress string `json:"export_address,omitempty"`
    NQN           string `json:"nqn"`
    HostNQN       string `json:"host_nqn,omitempty"`
    NSID          int    `json:"nsid"`
}

type RBDReference struct {
    Cluster  string   `json:"cluster,omitempty"`
    Monitors []string `json:"monitors,omitempty"`
    Pool     string   `json:"pool"`
    Image    string   `json:"image"`
    Snapshot string   `json:"snapshot,omitempty"`
    User     string   `json:"user,omitempty"`
    Conf     string   `json:"conf,omitempty"`
    Keyring  string   `json:"keyring,omitempty"`
}

type Attachment struct {
    Protocol Protocol            `json:"protocol"`
    ISCSI    *ISCSIAttachment    `json:"iscsi,omitempty"`
    NVMeTCP  *NVMeTCPAttachment  `json:"nvme_tcp,omitempty"`
    RBD      *RBDAttachment      `json:"rbd,omitempty"`
}

type ISCSIAttachment struct {
    ISCSIReference
    Username   string `json:"-"`
    Credential string `json:"-"`
}

type NVMeTCPAttachment struct {
    NVMeTCPReference
    PSK string `json:"-"`
}

type RBDAttachment struct {
    RBDReference
}
```

`Reference` is secret-free and is what `disk.backend_metadata` persists.
`Attachment` is ephemeral and may carry CHAP or PSK material. `Reference` and
`Attachment` have explicit JSON shapes; an unknown protocol is an error, not an
empty attachment. `Reference.Validate()` accepts a protocol-only reference for
a `provisioning` row; `Reference.ValidateComplete()` requires the matching
protocol variant for a ready disk. `Reference.Locator()` is a display-only,
protocol-aware string; no locator is parsed as a URI. An empty protocol is
normalized to `ProtocolLocal` on read for legacy LVM/host-device rows.

The kouja driver maps a live `Export` and credential to `Attachment`:
`PROTOCOL_ISCSI` populates `ISCSI`; `PROTOCOL_NVME_TCP` populates `NVMeTCP` with
the export target/NQN, portal address, NSID, and initiator as host NQN. A future
Ceph driver returns `RBD` with pool/image/snapshot and the config/keyring paths.
Neither `internal/qemu` nor `internal/blockdev` imports a driver or `internal/kouja`.

### Driver boundary

`Driver.Validate` is local structural/config validation. Network reachability,
class capacity, QEMU capability, and the data path are `Health`/`TestDataPath`
checks. The core driver interface is small; capabilities are optional
interfaces:

```go
type Driver interface {
    Kind() string
    Validate(ctx context.Context, backend store.StorageBackend) error
    CreateDisk(ctx context.Context, backend store.StorageBackend,
        request CreateDiskRequest) (CreatedDisk, error)
    DeleteDisk(ctx context.Context, backend store.StorageBackend,
        disk store.Disk) error
    Health(ctx context.Context, backend store.StorageBackend) (Health, error)
}

type StartPreparer interface {
    PrepareStart(ctx context.Context, vm store.VM,
        disk store.VMDisk) (blockdev.Attachment, error)
}

type DataPathTester interface {
    TestDataPath(ctx context.Context, backend store.StorageBackend) (Health, error)
}

type Importer interface {
    Importable(ctx context.Context, backend store.StorageBackend) ([]ImportableDisk, error)
    Import(ctx context.Context, backend store.StorageBackend,
        names []string) ([]store.Disk, error)
}

type OrphanCleaner interface {
    DeleteOrphanVolume(ctx context.Context, backend store.StorageBackend,
        volumeID string) error
}

type CreateDiskRequest struct {
    DiskID              int64
    Name                string
    SizeGB              int
    VolumeID            string
    RequestID           string
    TemplateDiskID      *int64
    IsTemplate          bool
    ProvisioningScript  string
    ProvisioningTimeout string
    Provisioning        map[string]any
}

type CreatedDisk struct {
    Metadata         store.DiskBackendMetadata
    Device           string // local device path; empty for remote attachments
    SizeBytes        uint64
    PreparedByScript bool
}

type Health struct {
    Reachable    bool
    Ready        bool
    Detail       string
    StorageClass string
    Protocols     []string
    QEMUReady     bool
    CheckedAt     time.Time
    Classes       []ClassCapacity
}

type ClassCapacity struct {
    Name             string
    VGName           string
    State            string
    TotalBytes       int64
    FreeBytes        int64
    AllocatableBytes int64
    MaxVolumeBytes   int64
}

type ImportableDisk struct {
    Name    string
    SizeGB  int
    Device  string
    InUseBy string
}
```

`internal/storage/lvmthin.go` implements the core driver and `Importer`. It
receives the store, an injectable thin-volume provisioner, a
`readiness.Inspector`, and a logger. It owns the existing template preparation,
provisioning scripts, clone deletion, and import registration. The LVM create,
delete, template, provisioning-script, and import code currently in
`internal/gateway/disk_writes.go` and `internal/gateway/disk_script.go` moves
into this driver and `internal/storage/script.go`; the gateway keeps only
request validation, backend resolution, host-device handling, and driver
dispatch. The low-level `DiskProvisioner` interface moves to `internal/storage`;
`gateway.DiskProvisioner` remains a type alias so existing tests and
`WithDiskProvisioner` callers keep compiling. The driver does not insert a second disk row: the gateway's `provisioning` row
ID is passed in `CreateDiskRequest`. The move must preserve the default-backend
thin-pool readiness gate, template clone-count and
`allow_template_delete_with_clones` guard, script-created clone backend
fallback, no-properties provisioning-script early return, and import name
limits (`normalizeImportNames`).

`internal/storage/kouja.go` implements the core driver, `StartPreparer`,
`DataPathTester`, and `OrphanCleaner`. It rejects import and template requests.
It receives the store, a client factory, the QEMU binary, and the run directory.
The client factory constructs a client per backend from `StorageBackend.Config`;
there is no single global client for different endpoints.

Host-device disks stay in the gateway as a first-class special case. They are
not a storage backend and do not implement `Driver`. `WithDiskProvisioner`
remains the LVM provisioner injection for tests and is wrapped by the LVM
driver; it is not a second dispatch path. The default LVM driver built by
`gateway.New` uses the readiness manager when it implements
`readiness.Inspector`; otherwise it falls back to `Status().ThinPool` for the
bootstrap backend and reports per-backend inspection as unavailable. Tests
inject a fake inspector.

### Extension checklist for the next protocol/driver

*   **NVMe/TCP through kouja:** add/populate `blockdev.NVMeTCPReference` and
    `blockdev.NVMeTCPAttachment`, map kouja `Export.Nsid`/target NQN/portal,
    add a QEMU NVMe/TCP builder and probe, and set
    `KoujaConfig.Protocol="nvme-tcp"`. No disk-table or metadata-envelope
    change.
*   **Ceph RBD:** add a `ceph` driver with `CephConfig`, return
    `blockdev.RBDReference` from create, add the QEMU RBD builder and
    protocol probe, and register the driver by `Kind()`. `source="ceph"`,
    `protocol="rbd"`, and the same `StartPlan` are used.
*   **Both:** add protocol-specific outage/fault tests and a deep-test branch;
    add the UI/Terraform enum value; keep credentials out of SQLite and
    process arguments.

### Storage backend configuration

A kouja backend is one kouja endpoint plus one storage class. Multiple storage
classes on one agent are multiple Rookery backends that share an endpoint and
certificate paths.

The backend `config` JSON is driver-scoped. The kouja block is:

```json
{
  "kouja": {
    "endpoint": "10.10.0.1:9443",
    "protocol": "iscsi",
    "storage_class": "nvme",
    "initiator": "iqn.2026-11.io.example:rookery",
    "host_nqn": "nqn.2026-11.io.example:rookery",
    "ca_file": "/etc/rookery/kouja/ca.crt",
    "client_cert_file": "/etc/rookery/kouja/rookery.crt",
    "client_key_file": "/etc/rookery/kouja/rookery.key",
    "server_name": "kouja-storage-1",
    "portal": "10.10.0.1:3260"
  }
}
```

LVM keeps `vg`, `thin_pool`, template, and script settings at the top level. A
future Ceph backend adds a sibling `ceph` block (`monitors`, `pool`, `user`,
`conf_file`, `keyring_file`) without changing the disk or attachment schema.
`internal/store.StorageBackendConfig` becomes:

```go
type StorageBackendConfig struct {
    VG                  string       `json:"vg,omitempty"`
    ThinPool            string       `json:"thin_pool,omitempty"`
    SupportsTemplates   bool         `json:"supports_templates,omitempty"`
    PreparationScript   string       `json:"preparation_script,omitempty"`
    PreparationTimeout  string       `json:"preparation_timeout,omitempty"`
    AllowTemplateDeleteWithClones bool `json:"allow_template_delete_with_clones,omitempty"`
    Kouja               *KoujaConfig `json:"kouja,omitempty"`
    // Ceph             *CephConfig  `json:"ceph,omitempty"` // future
}

type KoujaConfig struct {
    Endpoint       string `json:"endpoint"`
    Protocol       string `json:"protocol,omitempty"` // iscsi, nvme-tcp
    StorageClass   string `json:"storage_class"`
    Initiator      string `json:"initiator,omitempty"`
    HostNQN        string `json:"host_nqn,omitempty"`
    CAFile         string `json:"ca_file"`
    ClientCertFile string `json:"client_cert_file"`
    ClientKeyFile  string `json:"client_key_file"`
    ServerName     string `json:"server_name,omitempty"`
    Portal         string `json:"portal,omitempty"`
}
```

`endpoint` is a `host:port` or `https://host:port` gRPC target. `protocol`
defaults to `iscsi` when empty. `protocol=iscsi` requires `initiator` as an
IQN; `protocol=nvme-tcp` requires `host_nqn` as an NQN. This cut accepts only
the iSCSI protocol at backend save; `nvme-tcp` is rejected with a clear
`FailedPrecondition` until the NVMe/TCP builder is implemented, so no kouja
mutation or orphan is possible for an unsupported protocol. Certificate files
are paths, not inline PEM. `client_key_file` must not be world-readable
(`mode & 0o004 == 0`); group-read (`0640`) is allowed. `server_name` is optional
and overrides TLS verification and SNI. `portal` is optional; when set it overrides the portal/address returned by kouja for the QEMU connection and must not be
`0.0.0.0`, loopback, or an unspecified address. When empty, the portal returned
by kouja is used and is validated the same way.

Protobuf additions use a nested driver block so future drivers do not extend
the kouja field list:

```text
message StorageBackendConfig {
  string vg = 1 [...];
  string thin_pool = 2 [...];
  bool supports_templates = 3;
  string preparation_script = 4 [...];
  string preparation_timeout = 5 [...];
  bool allow_template_delete_with_clones = 6;
  oneof driver_config {
    KoujaConfig kouja = 7;
    // CephConfig ceph = 8; // future
  }
}

message KoujaConfig {
  string endpoint = 1 [(validate.rules).string = {max_len: 512}];
  string protocol = 2 [(validate.rules).string = {ignore_empty: true, in: ["iscsi", "nvme-tcp"]}];
  string storage_class = 3 [(validate.rules).string = {max_len: 127}];
  string initiator = 4 [(validate.rules).string = {max_len: 223}];
  string ca_file = 5 [(validate.rules).string = {max_len: 1024}];
  string client_cert_file = 6 [(validate.rules).string = {max_len: 1024}];
  string client_key_file = 7 [(validate.rules).string = {max_len: 1024}];
  string server_name = 8 [(validate.rules).string = {max_len: 255}];
  string portal = 9 [(validate.rules).string = {max_len: 255}];
  string host_nqn = 10 [(validate.rules).string = {max_len: 223}];
}
```

`CreateStorageBackendRequest.driver` and `UpdateStorageBackendRequest.driver`
allow `lvm-thin` and `kouja` now; adding `ceph` later is a PGV enum change plus
a `CephConfig` block, not a disk-schema change. PGV checks the driver enum and
the field lengths above; endpoint grammar, iSCSI-name grammar, absolute paths,
key permissions, SAN, and portal rules are runtime checks. The kouja driver in
this cut implements `protocol="iscsi"`; `nvme-tcp` is accepted by the schema
but returns a clear `Unimplemented`/`FailedPrecondition` until its QEMU builder
is added.

Save-time validation is local only. It checks syntax, absolute paths, key
permissions, a single `spiffe://kouja/caller/<name>` URI SAN, and the optional
portal. It does not dial kouja, so an operator can pre-register a backend or fix
a certificate while the agent is down.

Identity fields are `driver`, `config.kouja.protocol`,
`config.kouja.storage_class`, `config.kouja.initiator` (iSCSI) or
`config.kouja.host_nqn` (NVMe/TCP), and the certificate URI SAN/owner. They
cannot change while the backend has disks: existing exports would point at the
old driver/protocol/class/initiator/owner. Connection fields are
`config.kouja.endpoint`, `config.kouja.ca_file`,
`config.kouja.client_cert_file`, `config.kouja.client_key_file`,
`config.kouja.server_name`, and `config.kouja.portal`. They may change
while disks exist, including certificate files whose SAN is unchanged; health
and the next start use the new values. This lets an operator repair a moved
endpoint or rotated certificate without deleting every disk. A future driver
uses its own identity/connection field list.

### Disk record and states

`disks.source` is the backend driver identity: `lvm-thin`, `host-device`,
`kouja`, and future `ceph`. It equals `Driver.Kind()` for backend disks.
`disks.attachment_protocol` stores the attachment protocol. The backend
metadata is a versioned envelope with a secret-free attachment reference:

```json
{
  "version": 1,
  "driver": "kouja",
  "volume": {
    "volume_id": "0e3b0b0e-...",
    "request_id": "create-0e3b0b0e-...",
    "resource_id": "9f0a...",
    "owner": "spiffe://kouja/caller/rookery",
    "storage_class": "nvme"
  },
  "attachment": {
    "protocol": "iscsi",
    "iscsi": {
      "portal": "10.10.0.2:3260",
      "export_portal": "10.10.0.1:3260",
      "target": "iqn.2026-11.io.kouja:host-uuid:resource-uuid",
      "lun": 0,
      "initiator": "iqn.2026-11.io.example:rookery"
    }
  }
}
```

The same envelope represents future transports without a schema change:

```json
{
  "version": 1,
  "driver": "kouja",
  "volume": {"volume_id": "...", "owner": "...", "storage_class": "..."},
  "attachment": {
    "protocol": "nvme-tcp",
    "nvme_tcp": {
      "address": "10.10.0.1:4420",
      "export_address": "10.10.0.1:4420",
      "nqn": "nqn.2026-11.io.kouja:...",
      "host_nqn": "nqn.2026-11.io.example:rookery",
      "nsid": 1
    }
  }
}
```

A Ceph disk uses `"protocol": "rbd"` with `pool`, `image`, optional `snapshot`,
`user`, `conf`, and `keyring`; the keyring is a path, not key material.

`volume.volume_id` is the gateway-generated request identity. `resource_id` is
the driver's durable volume identity (kouja's `resource_uuid`). The CHAP username is derived as
`kouja-<resource_uuid>`. The metadata stores `owner` for diagnosis and
certificate-rotation checks. The persisted type is:

```go
type DiskBackendMetadata struct {
    Version    int                `json:"version"`
    Driver     string             `json:"driver"`
    Volume     VolumeMetadata     `json:"volume"`
    Attachment blockdev.Reference `json:"attachment"`
}

type VolumeMetadata struct {
    VolumeID     string `json:"volume_id,omitempty"`
    RequestID    string `json:"request_id,omitempty"`
    ResourceID   string `json:"resource_id,omitempty"`
    Owner        string `json:"owner,omitempty"`
    StorageClass string `json:"storage_class,omitempty"`
}
```

`ISCSIReference.Portal` is the effective portal used by QEMU: the configured
`portal` override when set, otherwise `ExportPortal`. `ExportPortal` is
the raw portal returned by kouja; start compares the live raw portal against
it. `locator` is a display-only string built by `blockdev.Reference.Locator()`;
it is not parsed as a URI.

`disks.device` stays empty for every remote attachment. The API returns a
separate display-only `locator` field derived from the attachment. `disks.vg`
and `disks.thin_pool` stay empty for remote drivers. `disks.size_gb` is the
requested size; `disks.size_bytes` is the actual size.

The Rookery disk protobuf fields are:

```text
Disk:
  string locator = 20;
  string deletion_state = 21;
  string deletion_error = 22;
  string protocol = 23;

VMDisk:
  string locator = 11;
  string protocol = 12;

DeleteDiskResponse:
  bool accepted = 1;
  string state = 2;
```

`DeleteDisk` returns `accepted=true, state="deleting"` for a queued kouja
delete and for a `delete_failed` kouja retry reset; the gateway maps both to
`202 Accepted`. A completed LVM delete returns `accepted=false`; the gateway
maps it to `204 No Content`.

Store methods:

```text
MarkDiskDeleting(ctx, id) error
SetDiskDeleteFailed(ctx, id, message) error
RecordDiskDeletionFailure(ctx, id, attempts int, next time.Time, message string) error
ClaimDueDiskDeletions(ctx, now time.Time, limit int) ([]Disk, error)
ReleaseDiskDeletionClaim(ctx, id) error
FinalizeProvisioning(ctx, id, metadata DiskBackendMetadata, sizeBytes uint64, device string, preparedByScript bool) (Disk, bool, error)
ResetDiskDeletion(ctx, id) error
```

`FinalizeProvisioning` returns `false` when the row is no longer
`provisioning`; the caller must clean the created volume and must not overwrite
the newer state.

Deletion/state columns:

```sql
ALTER TABLE disks ADD COLUMN backend_metadata TEXT NOT NULL DEFAULT '{}';
ALTER TABLE disks ADD COLUMN attachment_protocol TEXT NOT NULL DEFAULT '';
ALTER TABLE disks ADD COLUMN deletion_state TEXT NOT NULL DEFAULT '';
ALTER TABLE disks ADD COLUMN deletion_error TEXT NOT NULL DEFAULT '';
ALTER TABLE disks ADD COLUMN deletion_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE disks ADD COLUMN deletion_next_attempt_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE disks ADD COLUMN deletion_claimed_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_disks_deletion_due
    ON disks(deletion_state, deletion_next_attempt_at);
```

Timestamps are Unix milliseconds in UTC. `backend_metadata` empty or NULL is
treated as `{}`. `deletion_state` is empty, `provisioning`, `deleting`, or
`delete_failed`.

### Create flow

1. Resolve the backend. Reject templates, clones, provisioning, and host-device
   fields for kouja.
2. Check the requested size against `allocatable_bytes` and `max_volume_bytes`
   when those values are non-zero: `size_bytes <= allocatable_bytes` and
   `size_bytes <= max_volume_bytes`. If a value is zero or absent, skip that
   check and let kouja enforce capacity.
3. Under the disk mutex, check the name and insert a disk row with the selected
   `source`, `storage_backend_id`, backend VG/thin-pool fields,
   `deletion_state='provisioning'`, `deletion_next_attempt_at = now + 20
   minutes`, `prepared_by_script = (template_disk_id != nil)`, and a versioned
   `backend_metadata` envelope with the generated `driver`,
   `volume.volume_id`, `volume.request_id`, `volume.owner`,
   `volume.storage_class`, and `attachment.protocol` (`local` for
   LVM/host-device, `iscsi` for this kouja
   cut). The protocol-specific attachment reference is filled at finalize; the
   provisioning row may carry a protocol-only reference. The row ID is passed
   to the driver; this changes the
   preparation-script payload from `id: 0` to the real row ID, so the script
   contract test is updated. The generated IDs are persisted before the remote
   call so a crash can identify the volume.
4. Release the disk mutex. Call `driver.CreateDisk`. The gateway does not
   automatically retry a failed create; it marks the row for cleanup so a
   retry cannot create two live volumes. If a retry is added later, it must
   reuse the same volume ID and request ID. The kouja driver:
   * creates the volume with `export=true`, the configured protocol
     (`PROTOCOL_ISCSI` in this cut), `include_credential=false`, `wipe=false`,
     the configured storage class and the protocol's initiator (iSCSI IQN or
     NVMe host NQN), the request ID, and `size_bytes = size_gb * 1<<30`;
   * requires `VolumeID` equality, a non-empty `resource_uuid`,
     `VolumeState` in `AVAILABLE`/`EXPORTED`, `ExportState=EXPORTED`, a
     non-empty target and portal, and `LUN >= 0`;
   * derives the CHAP username from `resource_uuid` and returns a
     `blockdev.Reference` with protocol `iscsi`. A future NVMe/TCP export
     returns `blockdev.NVMeTCPReference` from the same driver.
   The LVM driver performs its existing create/template/provisioning flow and
   writes no remote attachment.
5. Call `FinalizeProvisioning` with a compare-and-swap on
   `deletion_state='provisioning' AND deletion_claimed_at=0`. It receives the
   driver's `CreatedDisk` metadata, `Device`, `SizeBytes`, and
   `PreparedByScript`. If it updates the row, clear the state, store those
   fields, and set `attachment_protocol` from the reference (`local` for
   LVM/host-device). If it affects zero rows, do not
   start a second cleanup: re-read the row. If it is `provisioning`/`deleting`,
   the worker owns it; if it is gone, cleanup already happened. Return an error
   without overwriting `deleting`/`delete_failed`. The row keeps its name
   reserved until the worker removes it, so a failed create cannot collide with
   a new disk of the same name.
   On a kouja or LVM failure, set `deletion_state='deleting'`, reset
   `deletion_next_attempt_at=now`, record the error, and return the error. The
   worker runs the idempotent kouja orphan delete or LVM delete. Normal LVM
   deletes remain synchronous and do not use this queued path.
6. A `provisioning` row older than 20 minutes is treated as `deleting` by the
   worker, for both drivers. This covers a crash after the row insert but before
   the remote call or local create. The worker uses the persisted volume ID and
   request ID.

The remote call runs outside the disk mutex. Name uniqueness is protected by the
unique `disks.name` index and the `provisioning` row. The mutex is held only for
the short name check and row insert/finalize.

### QEMU start flow

The gateway prepares attachments outside the disk and VM locks. It holds
`resourceMu` only for the revalidation window before taking `s.mu`; it releases
`resourceMu` before the local QEMU start and never holds either lock across a
kouja RPC. The lock order is `resourceMu` (disk mutex) → `s.mu` (VM run mutex);
`resourceMu` is released after `s.mu` is held.

1. Load the VM and its disk set. If the VM is already running, repair the stored
   status and return the existing idempotent already-running result without
   calling kouja.
2. For each remote disk (kouja now, a future Ceph driver later), call
   `StartPreparer.PrepareStart` outside both locks:
   * call `GetVolume` and require `ExportState=EXPORTED` and `VolumeState` in
     `AVAILABLE`/`EXPORTED`;
   * if `Observation.ObservedAt` is at most two minutes old and
     `TargetPresent=false` or `ACLPresent=false`, fail before starting. If the
     observation is older, proceed and let QEMU's connection error be
     authoritative; do not assume a fixed 10-second reconcile window;
   * reject `SessionState=ACTIVE`. Allow `NONE`; allow `UNKNOWN` or
     `UNSPECIFIED` only when `ActiveSessions=0`, and log it;
   * call `GetCredential` with no owner and derive the attachment from the live
     export;
   * compare the live export's raw `export_portal` plus
     target/LUN/initiator with stored metadata. If they differ, fail with a
     precondition error that tells the operator to refresh or re-export the
     disk; do not silently adopt a different LUN. The effective `portal` may
     differ only through the configured `portal` override.
3. Acquire `resourceMu`, re-read the VM, and confirm the disk set is unchanged.
   If it changed, release `resourceMu`, rebuild the plan, and retry at most
   twice. If the VM was deleted, return `NotFound`.
4. Acquire `s.mu`, re-read the VM, release `resourceMu`, and call
   `runtime.StartWithPlan(ctx, vm, plan)` when the runtime implements the
   optional `PreparedStarter` interface. Otherwise call the existing
   `runtime.Start(ctx, vm)` only for a VM with no kouja disks; a VM with a
   kouja disk whose runtime does not implement `PreparedStarter` fails with a
   clear configuration error. `qemu.Start` remains the local-only entry point
   and delegates to `StartWithPlan` with an empty plan.

`ResetVM` normally sends HMP `system_reset` and does not touch storage. Only
when the monitor is unavailable does it fall back to a power cycle: stop under
`s.mu`, release it, build a fresh plan outside both locks, then acquire
`resourceMu`/`s.mu`, revalidate the desired state and disk set, release
`resourceMu`, and start through the same optional `PreparedStarter` path.
`CreateVM` inserts the VM under
`resourceMu`, releases it, builds the plan, then reacquires
`resourceMu`/`s.mu`, revalidates, releases `resourceMu`, and calls
`startCreatedVMLocked`, which assumes `s.mu` is held and does not lock again;
on plan or start failure it rolls back the created VM. Autostart must release
the outer `runMu` (the same mutex as `s.mu` through `WithVMMutex`) before
planning each VM: plan outside the locks, acquire `resourceMu`, re-read, acquire
`s.mu`, release `resourceMu`, re-read `desired_state` (skip if a concurrent Stop
changed it), start, and release `s.mu`. It makes one attempt per VM under the
existing 2-minute reconciliation work context; on failure it logs and leaves
`desired_state=running` for the next startup/VPP-restart reconciliation. All
paths treat `ErrAlreadyRunning` as idempotent and do not repeat storage
preparation.

`qemu.StartPlan` is:

```go
type StartPlan struct {
    Attachments map[int64]blockdev.Attachment
}

type PreparedStarter interface {
    StartWithPlan(ctx context.Context, vm store.VM, plan StartPlan) error
}
```

`qemu.Start(ctx, vm)` remains for local-only VMs and calls
`StartWithPlan(ctx, vm, StartPlan{})`. `buildArgs` adds VM-level flags and
iterates `vmDisks(vm)` in order, calling `buildDiskArgs` for each disk. A
remote disk requires a plan entry; a local disk emits the existing `-drive`; a
missing or unknown attachment is an error. `buildDiskArgs` switches on
`attachment.Protocol` and calls a protocol builder:

*   `iscsi` builds the iSCSI `-object secret` and `-drive` below.
*   `nvme-tcp` is reserved for the next driver; the builder validates QEMU's
    NVMe/TCP support and otherwise returns a clear unsupported-protocol error.
*   `rbd` is reserved for the Ceph driver; the builder uses the RBD block
    driver and the configured cluster/pool/image/conf/keyring.
*   any other protocol is rejected before QEMU is launched.

Adding a protocol is therefore a new `blockdev` variant plus a QEMU builder,
not a change to `StartPlan`, `PreparedStarter`, or the disk table.

The `internal/qemu` wrapper writes each protocol secret (CHAP now, NVMe PSK
later) as raw bytes with no trailing newline to
`<run-dir>/<vm-id>.disk<disk-id>.<protocol>.secret` with `O_CREAT|O_EXCL` and
mode `0600`. The run directory is created with mode `0700`. If the exact file
already exists from an earlier crash, the wrapper removes that file before the
exclusive create; it never follows a symlink. qemu-system only reads it. The
wrapper keeps the file until `Start`/`StartWithPlan` returns success, after
QEMU has created its pid file, then removes it. It removes all prepared secrets
on a failed start, already-running early return, Stop, and VM deletion.
`qemu.New` sweeps stale secret files older than one hour.

The iSCSI QEMU arguments for one kouja disk are:

```text
-object secret,id=rookery-iscsi-<disk-id>,file=<run-dir>/<vm-id>.disk<disk-id>.iscsi.secret,format=raw

-drive file.driver=iscsi,file.transport=tcp,file.portal=<portal>,file.target=<target>,file.lun=<lun>,file.user=kouja-<resource_uuid>,file.password-secret=rookery-iscsi-<disk-id>,file.initiator-name=<initiator>,file.timeout=30,if=virtio,format=raw,cache=none,werror=stop,rerror=stop
```

The secret object ID and file name are protocol-scoped
(`rookery-<protocol>-<disk-id>`) so an NVMe PSK or a future protocol can use
the same wrapper lifecycle without colliding with the iSCSI secret.
`aio=native` is not used for iSCSI; QEMU's iSCSI driver is coroutine-based and
does not gain the local native-AIO path. The qemu command-line drive order is
preserved; guest `/dev/vdX` names are assigned by the guest kernel and are not
a contract. A future RBD attachment uses the keyring path, not a copied key
material file; a future NVMe/TCP attachment uses the PSK object supported by
the installed QEMU, and the capability probe reports it unsupported rather than
silently dropping authentication.

### Outage and Resume

This section describes the iSCSI path delivered first. QEMU's libiscsi driver
sends NOPs and retries timed-out commands with exponential backoff before
returning an I/O error. `file.timeout=30` bounds a request, not the pause. A
fault-injection test measures the actual pause bound. If a bounded pause is
required, the design adds a QEMU monitor watchdog; it does not claim a
30-second pause from `file.timeout` alone. NVMe/TCP and RBD builders must add
their own outage tests rather than inherit these assumptions.

Production uses the existing HMP monitor (`-monitor unix:<path>,server,nowait`).
`RunState` sends `info status` and parses `running`, `paused`, or
`paused (io-error)`. `werror=stop,rerror=stop` pauses the VM when an I/O error
reaches the block layer. All VM responses set `run_state` through the same
helper; list/get and create/start/reset/update use it for running VMs. A
missing monitor or unparsable reply returns an empty `run_state` and logs a
warning, not a request failure. Stopped VMs return an empty state. The UI shows
Resume only for `paused`/`paused (io-error)`. `ResumeVM` re-queries the HMP
monitor, requires a paused state, sends `cont`, and re-queries. It returns
`404` for an unknown VM, `FailedPrecondition` for a non-paused VM or missing
monitor, and `Internal` for other failures. It does not change stored status or
desired state.

```text
rpc ResumeVM(ResumeVMRequest) returns (ResumeVMResponse) {
  option (google.api.http) = { post: "/api/vms/{id}/resume" body: "*" };
}
message ResumeVMRequest {
  string id = 1 [(validate.rules).string.uuid = true];
}
message ResumeVMResponse {
  VM vm = 1;
}
```

### Delete flow

1. `DELETE /api/disks/{id}` takes `resourceMu`, rejects a disk attached to a VM,
   and performs the state transition. Attach/update paths take the same mutex,
   so a disk cannot be attached between the check and the transition. DELETE on
   an already-`deleting` disk is idempotent and returns `202`.
2. For a thin-LVM disk, the gateway calls the driver delete synchronously. On
   success it removes the row and returns `204`. On failure it sets
   `delete_failed`, records the error, returns the error, and does not retry.
   Normal LVM deletes never set `deleting`; a crash mid-delete leaves the row
   unchanged, and the next DELETE retries the idempotent LVM delete. An LVM
   create failure does set `deleting` for worker cleanup so the name stays
   reserved until the partial LV is gone.
3. For a kouja disk, Rookery sets `deletion_state='deleting'`, clears the error,
   resets attempts, sets `deletion_next_attempt_at=now`, and returns `202` with
   the disk state. The worker uses the deterministic request IDs
   `unexport-<volume_id>` and `delete-<volume_id>` so a retry replays the same
   journaled kouja operation. It calls `UnexportVolume` and `DeleteVolume` with
   `force=false`. If kouja returns `SESSION_ACTIVE` or `SESSION_UNKNOWN`, the
   worker applies its explicit retry policy (those reasons are non-retryable by
   `client.IsRetryable` but are transient here), records the error, and retries
   with the session backoff. It never logs out the initiator and never forces
   the operation. `NOT_FOUND` is success for unexport/delete/orphan cleanup.
4. The worker polls every 30 seconds and claims at most 20 due rows with
   `UPDATE ... SET deletion_claimed_at=now WHERE id=? AND
   deletion_state IN ('provisioning','deleting') AND
   deletion_next_attempt_at<=now AND (deletion_claimed_at=0 OR
   deletion_claimed_at<now-10m)`, processes a small bounded concurrency, and
   never holds a SQLite transaction across a remote call. Each kouja RPC has a
   2-minute timeout; the 10-minute claim lease is longer than the maximum call.
   An unexpected LVM `deleting` row is executed as the idempotent LVM delete,
   not skipped.
5. Before a remote delete, re-read the disk and confirm `attached_vm_id` is
   empty, the state is still `provisioning`/`deleting`, and
   `deletion_claimed_at` still equals this worker's claim. If any changed,
   release the claim and retry later; a create that finalized the row wins.
6. For a due `provisioning` row, call `DeleteOrphanVolume` for kouja or the
   idempotent `DeleteDisk` for LVM (missing LV is success). For a `deleting`
   row (kouja, or LVM create-failure cleanup), call `DeleteDisk`.
7. On success, delete the disk row. On a retryable error, increment attempts,
   set the next backoff, clear the claim, and keep `deleting`. On the 8th
   attempt or a permanent error, set `delete_failed`, record the error, and
   clear the claim. `NOT_FOUND` counts as success.
8. `DELETE` on a `delete_failed` kouja disk resets it to `deleting` and
   schedules a retry. `DELETE` on a `delete_failed` LVM disk runs the
   synchronous LVM delete again; it stays `delete_failed` on another failure.
   The UI shows the error and offers Retry.

Backoff: 30s, 1m, 5m, 15m, 30m, then 1h, capped at 8 attempts. The worker
exposes backlog count and oldest due age for health/metrics.

### Readiness

```text
Ready = VPP.Ready && (thin pool not configured || ThinPool.Ready)
```

A kouja-only host with no thin pool reports ready when VPP is ready. A host with
a configured but broken thin pool reports not ready even if kouja works.
`checkVMCreatePrerequisites` requires the thin pool only for `lvm-thin` and
legacy empty-source disks. Kouja disks are validated at QEMU start.

### Health and data-path test

`GET /api/storage-backends/{id}/health` returns `200` with the health message;
a missing backend is `404`. Control-plane failures are reported in the message,
not as a 500.

```text
rpc GetStorageBackendHealth(GetStorageBackendHealthRequest)
    returns (StorageBackendHealth) {
  option (google.api.http) = { get: "/api/storage-backends/{id}/health" };
}
rpc TestStorageBackendDataPath(TestStorageBackendDataPathRequest)
    returns (StorageBackendHealth) {
  option (google.api.http) = { post: "/api/storage-backends/{id}/test" };
}
message GetStorageBackendHealthRequest {
  int64 id = 1 [(validate.rules).int64 = {gt: 0}];
}
message TestStorageBackendDataPathRequest {
  int64 id = 1 [(validate.rules).int64 = {gt: 0}];
}
message StorageBackendHealth {
  int64 id = 1;
  bool reachable = 2;
  bool ready = 3;
  string driver = 4;
  string storage_class = 5;
  repeated string protocols = 6;
  bool qemu_ready = 7;
  string detail = 8;
  google.protobuf.Timestamp checked_at = 9;
  repeated StorageClassCapacity classes = 10;
  int64 pending_deletions = 11;
  int64 oldest_pending_deletion_age_seconds = 12;
}
message StorageClassCapacity {
  string name = 1;
  string vg_name = 2;
  string state = 3;
  int64 total_bytes = 4;
  int64 free_bytes = 5;
  int64 allocatable_bytes = 6;
  int64 max_volume_bytes = 7;
}
```

The normal check:

*   dials kouja with the backend's mTLS material and the generated client;
*   calls `GetInfo` and requires `ready=true` and the protocol requested by the
    backend (`PROTOCOL_ISCSI` in this cut);
*   calls `ListStorageClasses` and requires the configured class;
*   checks that `qemu-system-x86_64 -object help` lists `secret`, reports the
    QEMU version, and records whether the binary/secret object prerequisites
    are present; the deep test proves the selected protocol's full option set.
    There is no reliable QEMU command that enumerates block protocols, so the
    normal check does not claim to discover them;
*   samples at least one existing kouja disk's `GetVolume`/`GetCredential` to
    detect an owner/SAN rotation that makes existing volumes inaccessible;
*   returns capacities, class names, and the deletion backlog count/oldest age
    from the store.

The QEMU capability probe runs `qemu-system-x86_64 -object help` with a
5-second timeout. It does not claim that `-drive ...help` proves
`file.password-secret`, `file.initiator-name`, or `file.timeout`; those are
proven by the deep test.

`POST /api/storage-backends/{id}/test` runs the deep test:

1. Insert a temporary `provisioning` disk row named
   `rookery-health-<uuid>`, then create a temporary 1 GiB volume in the
   configured class. If `max_volume_bytes` is below 1 GiB, use
   `max_volume_bytes`. A crash leaves the row for the worker.
2. Export it to the configured initiator and fetch the credential. Use the
   configured `portal` override when set; otherwise use the export portal.
3. Create the runtime `blockdev.Attachment` for the selected protocol and
   write any credential/PSK to a temporary 0600 secret file. RBD uses the
   configured keyring path.
4. Start QEMU with `-nodefaults -display none -S`, the production
   protocol-specific arguments from `qemu.buildDiskArgs`, a probe-only
   `id=rookery-health` drive ID, and a temporary HMP monitor socket.
5. Send HMP `info block` to identify the device, then
   `qemu-io rookery-health "read 0 512"`. The read must succeed; insertion
   alone is not success. Do not treat a live process after 5 seconds as
   success. `ProbeAttachment` kills QEMU and removes the secret before
   returning.
6. The caller unexports and deletes the temporary volume, then deletes the
   temporary disk row.
7. Return the failed stage and the kouja/QEMU error when any step fails.

```go
type ProbeResult struct {
    Version      string
    SecretObject bool
}
```

`qemu.ProbeBlockDriver(ctx, binary string) (qemu.ProbeResult, error)` reports
the binary version and whether `-object help` includes `secret`. It does not
claim to enumerate block protocols. `qemu.ProbeAttachment(ctx, binary, runDir
string, attachment blockdev.Attachment) error` owns steps 3–5 and shares a new
per-disk `buildDiskArgs` builder with `StartWithPlan`; it returns after the
read. `internal/storage/kouja_data_path.go` owns the kouja
create/export/credential steps and all cleanup (unexport, delete, temporary
row). The deep test is protocol-generic: iSCSI uses the CHAP secret and
`qemu-io` read now; a future NVMe/TCP or RBD attachment reuses the same probe
with its protocol-specific builder.

For `lvm-thin`, health inspects the requested backend's own `vg`/`thin_pool`
through `readiness.Inspector`; `TestDataPath` returns `FailedPrecondition`.

### Security

*   Control-plane mTLS with TLS 1.3 minimum. The full URI SAN is the owner.
*   The client private key stays in a file, is not stored in SQLite or returned
    by the API, and must not be world-readable.
*   CHAP secrets are written by the Rookery QEMU wrapper to a 0600 file and
    removed after start, failure, or Stop. They never appear in process
    arguments or the database.
*   iSCSI payload is plaintext. CHAP authenticates the initiator but does not
    encrypt data. The storage network must be isolated/dedicated or wrapped in
    IPsec; this is a deployment requirement and a residual risk.
*   Cleanup never forces an unexport or delete while a session may be active.
*   The backend API returns certificate paths, not certificate or key contents.

### Compatibility and rollback

| Operation | Old binary with a kouja disk | New binary |
|---|---|---|
| List disks | Shows `source=kouja` but cannot interpret metadata | Full support |
| Start VM | Stats the empty device and fails | Direct iSCSI start |
| Stop VM | Stops QEMU normally | Stops QEMU normally |
| Delete disk | Treats it as LVM and fails | Queued kouja unexport/delete |
| Attach to VM | Store accepts; QEMU start fails | Full support |

Rollback requires stopping all kouja VMs and deleting all kouja disks (including
`provisioning`/`deleting`/`delete_failed` rows), then running the old binary.
The old binary ignores the new columns but cannot operate on a kouja disk. The
migration is additive; existing LVM and host-device records keep their meaning.

## Alternatives Considered

### Alternative A: Host kernel iSCSI session with `iscsiadm`

*   **Pros**: The existing disk model works unchanged; the kernel handles
    reconnect and multipath.
*   **Cons**: Adds `open-iscsi`/`iscsid`; the host and QEMU both own the LUN;
    device names are transient; a stale session can outlive Rookery.
*   **Reason for Rejection**: It duplicates session ownership and adds a second
    failure domain.

### Alternative B: kouja HTTP/JSON client instead of gRPC

*   **Pros**: No protobuf/gRPC stack for the client.
*   **Cons**: Duplicates the wire contract and error mapping; the agent's
    primary contract is gRPC; the generated client already supports both.
*   **Reason for Rejection**: Rookery uses the generated gRPC client; the HTTP
    client remains available for tests.

### Alternative C: Separate `disk_exports` table

*   **Pros**: Queryable columns.
*   **Cons**: More joins and CRUD for a record read as a unit.
*   **Reason for Rejection**: JSON metadata on the disk is sufficient.

### Alternative D: Re-export automatically when the export is missing

*   **Pros**: Recovers from an operator unexport or a kouja rebuild.
*   **Cons**: Changes target/portal/LUN/credential without an explicit action;
    a missing export can mean deletion or drift.
*   **Reason for Rejection**: Fail fast, matching local-disk behavior.

### Alternative E: Force unexport or delete when a session is active

*   **Pros**: Deletion eventually completes without operator action.
*   **Cons**: Can remove a volume that QEMU is still writing.
*   **Reason for Rejection**: Retry-only cleanup; never force.

### Alternative F: Derive the CHAP username versus extend kouja's API

*   **Pros of deriving**: No kouja release required; the CSI controller already
    relies on the same rule.
*   **Cons of deriving**: Couples Rookery to a kouja naming rule.
*   **Pros of extending**: The API becomes self-describing.
*   **Cons of extending**: Requires a kouja proto change and release.
*   **Reason for Rejection**: The rule is a de facto contract; switch to an
    explicit field later without changing stored data.

### Alternative G: QEMU `-blockdev` instead of `-drive file.driver=iscsi`

*   **Pros**: Modern block graph with explicit node names.
*   **Cons**: More arguments; does not match the existing local-disk `-drive`
    shape; the installed QEMU rejects `aio=native` on the raw node.
*   **Reason for Rejection**: `-drive file.driver=iscsi` supports the full
    option set and preserves argument order.

### Alternative H: Inline certificate and key PEM in the backend record

*   **Pros**: One self-contained record.
*   **Cons**: The backend config is returned by the API and written to
    Terraform state; the private key would be exposed.
*   **Reason for Rejection**: File paths keep the key out of the API/database.

### Alternative I: Synchronous delete in the request handler

*   **Pros**: The response reflects the final state.
*   **Cons**: A slow/unreachable kouja hangs or fails the request; no durable
    retry record.
*   **Reason for Rejection**: Durable asynchronous retries are required.

### Alternative J: Vendored proto or hand-copied client instead of the kouja module

*   **Pros**: Rookery's build does not depend on kouja's release or transitive
    dependencies; the client surface can be trimmed.
*   **Cons**: Rookery must regenerate or copy the client, track kouja proto
    drift, and maintain request-ID/error/retry behavior separately.
*   **Reason for Rejection**: kouja now ships a maintained generated client,
    OpenAPI document, request-ID propagation, and retry helpers. Pinning the
    module is the smaller maintenance risk.

### Alternative K: Deliver NVMe/TCP in the first cut

*   **Pros**: Encrypted data path, modern transport, kouja support.
*   **Cons**: Requires `tlshd`/NVMe/TCP support, PSK distribution on both agent
    and QEMU host, and a second QEMU argument builder before the first backend
    is proven; less operational familiarity than iSCSI.
*   **Reason for Deferral**: The first cut uses iSCSI because QEMU's libiscsi
    path is simpler and already tested. The attachment model, metadata, and
    start plan already carry an `nvme-tcp` variant, so NVMe/TCP is the next
    transport rather than a schema change. The iSCSI data path still requires
    an isolated network or IPsec.

### Alternative L: Add a Ceph RBD driver in the first cut

*   **Pros**: No per-VM kouja agent for the Ceph pool; RBD is a mature QEMU
    block driver; Ceph provides replication and snapshots.
*   **Cons**: A second control plane (monitors, auth, pools, keyrings), a new
    driver, and a different capacity/ownership model. It would delay the kouja
    path and require a second health/deep-test implementation.
*   **Reason for Deferral**: The data model is ready for it: a `ceph` driver,
    `CephConfig`, and an `RBD` attachment reference fit the same disk envelope
    and `StartPlan`. It is the next driver after kouja iSCSI, not part of this
    cut.

## Risks & Mitigations

*   **Kouja API drift:** kouja is an early-stage external project. Mitigation:
    pin the Go module version, use the generated client SDK, and run contract
    tests against a real agent on upgrade.
*   **Owner rotation:** a new certificate with a different URI SAN loses volume
    ownership. Mitigation: validate the SAN at save/health, store the owner, and
    block SAN-changing certificate updates while disks exist.
*   **Capacity:** thick LVM volumes consume the class. Mitigation: preflight
    `allocatable_bytes`/`max_volume_bytes`, document headroom, expose class
    capacity and near-full health.
*   **Remote create latency:** Mitigation: provisioning row + short mutex,
    remote call outside the lock, 15-minute targeted deadline.
*   **Cleanup wedging:** a stale session or rotated certificate can block a
    delete. Mitigation: claim/lease, retry/backoff, terminal `delete_failed`,
    Retry action, backlog metrics.
*   **Outage behavior:** libiscsi/NVMe retry and pause timing are
    version-dependent. Mitigation: per-protocol fault-injection tests, explicit
    QEMU capability probes, HMP paused-state reporting, Resume only when paused.
*   **Rollback:** the old binary cannot resolve remote disks. Mitigation:
    compatibility table and pre-rollback drain.
*   **Plaintext iSCSI:** Mitigation: isolated storage network or IPsec; NVMe/TCP
    with TLS PSK and Ceph RBD are future transports.

## Non-Functional Requirements

### Security

*   Control-plane mTLS TLS 1.3; data-plane iSCSI plaintext with CHAP; isolated
    network or IPsec required. NVMe/TCP uses TLS PSK; RBD uses the configured
    Ceph keyring path.
*   Private keys, CHAP secrets, and PSKs are never stored in SQLite or process
    arguments. RBD keyring paths, not key material, are stored.
*   Owner is the full URI SAN; rotation must keep it stable.
*   Cleanup never forces an active session.

### Performance

*   Remote create runs outside the disk mutex; the mutex is held only for name
    reservation and row finalization.
*   QEMU opens the LUN directly with `cache=none`; `aio=native` is not used for
    iSCSI.
*   Health is on demand; the deep test is explicit and creates a temporary
    volume.
*   Worker uses bounded concurrency and short transactions.

### Availability

*   Rookery starts when kouja is unreachable.
*   Deletion state and claims survive API restarts.
*   QEMU reconnects after short outages and pauses after the measured retry
    bound; Resume is gated on the paused state.
*   kouja remains a single-host failure domain.

### Rollout and Rollback

*   Additive migration; existing LVM/host-device records keep their meaning.
*   A future Ceph driver adds a `ceph` source, `CephConfig`, and an RBD
    attachment variant; it does not reshape the disk table.
*   Rollback requires removing remote disks and draining
    provisioning/deleting/delete_failed rows.
*   Old binaries cannot start or delete remote attachment disks.

--------------------------------------------------------------------------------

## Additional Resources

*   kouja README: <https://github.com/voidstarit/kouja/blob/main/README.md>
*   kouja client SDK: <https://github.com/voidstarit/kouja/blob/main/docs/clients.md>
*   kouja client errors and retries: <https://github.com/voidstarit/kouja/blob/main/docs/client-errors.md>
*   kouja volume proto: <https://github.com/voidstarit/kouja/blob/main/proto/kouja/v1/volume.proto>
*   kouja common proto: <https://github.com/voidstarit/kouja/blob/main/proto/kouja/v1/common.proto>
*   QEMU `block/iscsi.c` and QAPI `BlockdevOptionsIscsi`.
*   QEMU `block/nvme.c`, `block/rbd.c`, and their QAPI block options for the
    future NVMe/TCP and Ceph builders.
*   QEMU `-object secret` documentation.
*   Rookery `design/proto/rookery/v1/storage_backend.proto`,
    `design/proto/rookery/v1/disk.proto`, `design/proto/rookery/v1/vm.proto`,
    `internal/gateway/disk_writes.go`, `internal/qemu/qemu.go`,
    `internal/store/store.go`, `docs/api.md`, `docs/operations.md`, and
    `docs/architecture.md`.

## Future Improvements

*   **NVMe/TCP**: a kouja `protocol="nvme-tcp"` export, an NVMe/TCP
    `blockdev` variant, and the QEMU NVMe/TCP builder.
*   **Ceph RBD**: a `ceph` driver/source, a `CephConfig` backend block, an
    `RBD` attachment variant, and the QEMU RBD builder.
*   **Import existing kouja volumes**: with explicit owner/export checks.
*   **Credential rotation**: through kouja's `RotateCredential`.
*   **Online resize**: extend a running volume and rescan it.
*   **Paused-state history**: retain run-state transitions for diagnosis.
*   **Multipath and queue tuning**: evaluate multiple portals and depth.
*   **Templates/snapshots**: when kouja supports them.
*   **Asynchronous provisioning UI**: show provisioning progress directly.

--------------------------------------------------------------------------------

## Detailed Implementation Plan

Phase order is protocol/shared-types/store → dependency/client → QEMU
plan/probe → storage driver/planner → gateway/API/serve → UI/Terraform/docs.
Each phase ends with `go test ./...` green; protocol changes run
`make proto-check`, UI runs `make ui-build`, final phase runs lint and
Terraform validation. Additive message/field changes may land in the first
phase. New RPCs (`ResumeVM`, storage health/test) and the `DeleteDisk` return
shape change are generated and wired with their gateway implementations in the
gateway/API phase, so intermediate phases still compile. Signature changes and
all callers/fakes land in the same phase.

### Protocol

| File | Action | Notes |
|---|---|---|
| `design/proto/rookery/v1/storage_backend.proto` | Modify | Add `oneof driver_config { KoujaConfig kouja = 7; }` with `KoujaConfig` fields 1–10; reserve 8 for future `CephConfig`; driver PGV `lvm-thin|kouja`; add health/test RPCs with the gateway implementation. |
| `design/proto/rookery/v1/disk.proto` | Modify | Add `locator`, `deletion_state`, `deletion_error`, and `protocol` in phase 1; change `DeleteDisk` to `DeleteDiskResponse` with the gateway implementation. |
| `design/proto/rookery/v1/vm.proto` | Modify | Add `run_state` (field 18) in phase 1; add `ResumeVM` with the gateway implementation. |
| `design/gen/rookery/v1/*` | Regenerate | Regenerate with each protocol change; the gateway phase wires new RPCs and the `DeleteDisk` return shape. |
| `internal/blockdev/attachment.go` | Create | Protocol-neutral `Reference`/`Attachment` with `iscsi`, `nvme-tcp`, and `rbd` variants; `Locator()`, validation, and JSON round-trip. This lands before store because `DiskBackendMetadata` embeds `blockdev.Reference`. |
| `internal/blockdev/attachment_test.go` | Create | Protocol validation, secret-free reference JSON, locator strings, unknown-protocol rejection. |

### Store

| File | Action | Notes |
|---|---|---|
| `internal/store/migrations/0049_kouja_storage.sql` | Create | Add the seven disk columns (`backend_metadata`, `attachment_protocol`, and five deletion fields) and due index from this design. |
| `internal/store/store.go` | Modify | Add metadata/state/protocol fields to `Disk`/`VMDisk`; select/scan/insert them in `diskSelect`, `scanDisk`, `CreateDisk`, `loadVMDisks`, `scanVM`; normalize an empty `attachment_protocol` to `local`; accept remote `source` (`kouja` now, `ceph` later) with empty device/VG/thin-pool; source-aware `/dev` fallback; reject non-empty deletion state in `checkDiskAvailable`/`CreateVM`/`SetVMDisks`; reject remote sources in `SetDiskTemplate`/`SetDiskProvisioning`; add `MarkDiskDeleting`, `SetDiskDeleteFailed`, `RecordDiskDeletionFailure`, `ClaimDueDiskDeletions`, `ReleaseDiskDeletionClaim`, `ResetDiskDeletion`, `FinalizeProvisioning` (CAS). |
| `internal/store/storage_backends.go` | Modify | Add nested `KoujaConfig`/future driver config; delete guard counts all disk states atomically with FK/transaction. |
| `internal/store/store_test.go` | Modify | Metadata/state round-trip, kouja source create, source-aware device, template/provisioning rejection, attach rejection, claim/backoff/CAS finalize. |
| `internal/store/migration_test.go` | Modify | Add `dropKoujaSchema` helper (columns + index) for every test that resets below 49; assert `user_version == 49`. |

### Dependency and client

| File | Action | Notes |
|---|---|---|
| `go.mod` | Modify | Add `github.com/voidstarit/kouja` at the exact release or `main` commit; document the transitive gRPC bump. |
| `go.sum` | Modify | Record checksums. |
| `internal/kouja/client.go` | Create | Wrap `client.NewGRPC`, `client.LoadTLSConfig(certFile, keyFile, caFile)`, `client.WithRequestID`, `client.ErrorInfo`, `client.IsRetryable`; set `ServerName`; expose a client factory per backend and a test constructor. |
| `internal/kouja/config.go` | Create | Validate endpoint, protocol, class, iSCSI IQN or NVMe host NQN, absolute paths, key mode, portal, and single caller SAN; return parsed config and owner. |
| `internal/kouja/errors.go` | Create | Thin classification over `client.ErrorInfo`/`client.IsRetryable`; map to Rookery retry/terminal decisions. |
| `internal/kouja/provisioner.go` | Create | Create/export volume, validate ID/resource UUID/state/export, derive protocol-neutral `blockdev.Reference` (iSCSI now; NVMe/TCP mapping reserved). |
| `internal/kouja/preparer.go` | Create | Live export/credential fetch, observation freshness, stored-vs-live comparison, `blockdev.Attachment` for the configured protocol. |
| `internal/kouja/health.go` | Create | Control-plane health, requested protocols, class capacity, and existing-volume owner sample. |
| `internal/kouja/client_test.go` | Create | TLS test server or wrapper around `koujav1.VolumeServiceClient`; certificate/key/CA argument order; server-name; request ID; error helpers. |
| `internal/kouja/config_test.go` | Create | Endpoint/protocol/class/IQN-or-host-NQN/path/key/portal/SAN validation. |
| `internal/kouja/provisioner_test.go` | Create | Volume ID, resource UUID, state/export/LUN, capacity, username. |
| `internal/kouja/preparer_test.go` | Create | Stale observation, active session, export mismatch, credential, neutral mapping. |
| `internal/kouja/health_test.go` | Create | Control-plane, missing class, and owner sample. |
| `internal/kouja/errors_test.go` | Create | `ErrorInfo`/`IsRetryable` mapping, `NOT_FOUND` success, session retry override. |

### QEMU

| File | Action | Notes |
|---|---|---|
| `internal/qemu/qemu.go` | Modify | `StartPlan{Attachments map[int64]blockdev.Attachment}`; keep `Start(ctx, vm)` and add `StartWithPlan(ctx, vm, plan)`; source-aware `validateVMDisks`/`imageLockHolders`/`ensureDiskDevice`; `buildArgs` protocol switch (iSCSI now; nvme-tcp/rbd builders reserved); secret wrapper lifecycle; `CleanupStaleSecrets` called from `New`; `RunState`/`Paused`/`Resume` via HMP `info status`/`cont`, empty state on missing monitor; chmod run dir 0700. |
| `internal/qemu/probe.go` | Create | `ProbeBlockDriver` (version, `secret`) and `ProbeAttachment` using `buildDiskArgs` + HMP `info block`/`qemu-io`, timeout, QEMU kill, and secret cleanup; caller owns kouja cleanup. |
| `internal/qemu/qemu_test.go` | Modify | `Start`/`StartWithPlan`, buildArgs/mixed order; source-aware validation. |
| `internal/qemu/qemu_devices_test.go` | Modify | Remote-source disk skips `ensureDiskDevice`; local LVM/host-device still validates. |
| `internal/qemu/iscsi_test.go` | Create | Secret raw bytes/mode/path/cleanup/stale sweep; exact drive options. |
| `internal/qemu/probe_test.go` | Create | `-object help` capability; HMP info block/`qemu-io` read success/failure. |
| `internal/qemu/qemu_integration_test.go` | Modify | `TestISCSIIntegration` gated by `ROOKERY_TEST_ISCSI_PORTAL`, `..._TARGET`, `..._LUN`, `..._INITIATOR`, `..._USER`, `..._PASSWORD`. |
| `internal/qemu/iscsi_outage_test.go` | Create | Gated by `ROOKERY_TEST_ISCSI_OUTAGE=1`; measures the real pause bound and verifies `run_state`/Resume. |

### Storage driver and planner

| File | Action | Notes |
|---|---|---|
| `internal/storage/driver.go` | Create | Core `Driver`, `CreateDiskRequest`, `CreatedDisk`, protocol-neutral `Health`, optional capability interfaces. |
| `internal/storage/registry.go` | Create | Construct LVM driver with store/injectable provisioner/inspector/logger; kouja driver with store/client factory/qemu binary/run dir; register by `Driver.Kind()` so a future `ceph` driver is a constructor entry, not a new dispatch branch. |
| `internal/storage/lvmthin.go` | Create | Move LVM create/delete/import/template/provisioning/clone deletion from the gateway; synchronous delete failure sets `delete_failed`; idempotent `DeleteDisk` also cleans a `provisioning` row; `DeleteOrphanVolume` unsupported. |
| `internal/storage/script.go` | Create | Move `disk_script.go` payloads, template preparation, and provisioning-script execution; keep `ROOKERY_DISK_JSON`/`ROOKERY_PROVISION_JSON` contracts. |
| `internal/storage/kouja.go` | Create | Kouja create/delete/orphan delete/start/health; protocol mapping; add QEMU capability probe; reject import/templates. |
| `internal/storage/kouja_data_path.go` | Create | Deep test: temporary row/volume, export, `blockdev.Attachment`, `qemu.ProbeAttachment` read, cleanup. |
| `internal/storage/reclaimer.go` | Create | Claim/lease worker (10m lease, 2m RPC), 30s tick, batch 20, bounded concurrency, recheck attachment, NOT_FOUND success, session retry, short transactions, backoff/cap, terminal state, backlog metrics. |
| `internal/vmstart/planner.go` | Create | Build `qemu.StartPlan` from VM disks and drivers; shared by gateway/API autostart. |
| `internal/storage/driver_test.go` | Create | Driver contract, capability dispatch, host-device separation. |
| `internal/storage/lvmthin_test.go` | Create | LVM templates/provisioning/import/per-backend health. |
| `internal/storage/script_test.go` | Create | Move and preserve `ROOKERY_DISK_JSON`/`ROOKERY_PROVISION_JSON` payloads, timeout, and failure handling. |
| `internal/storage/kouja_test.go` | Create | Kouja create/delete/start/health/import rejection. |
| `internal/storage/kouja_data_path_test.go` | Create | Protocol-generic probe; iSCSI read success, portal failure, CHAP failure, cleanup. |
| `internal/storage/reclaimer_test.go` | Create | Claim, LVM unexpected-row delete, kouja retry/cap, NOT_FOUND success, session retry, create/finalize race, attachment recheck, restart. |
| `internal/vmstart/planner_test.go` | Create | Mixed disks, missing metadata, driver errors. |

### Gateway and API

| File | Action | Notes |
|---|---|---|
| `internal/gateway/gateway.go` | Modify | Add `storage.Registry`/planner options; when no registry is injected, build a default LVM driver from `WithDiskProvisioner` and a `readiness.Inspector` assertion/fallback; construct `diskService`, `storageBackendService`, and `vmService` with the registry/planner; `vmDiskToProto` protocol/locator; populate `VM.run_state` for all VM responses via `runtime.RunState`, empty on missing monitor; delete response mapping; update `gateway.New` callers. |
| `internal/gateway/readonly.go` | Modify | Nested kouja config mapping; `protocol`/`locator`; `deletion_state`/`error` only on `Disk`; health protocols; `storageBackendService.registry` and `diskService.registry`; `storageBackendService` constructor. |
| `internal/gateway/writes.go` | Modify | Full replacement except empty driver preserves; local-only save validation; block only identity changes (storage class, initiator, owner SAN) while disks exist; allow connection-field repair; per-backend health. |
| `internal/gateway/disk_writes.go` | Modify | Reduce to validation, driver dispatch, host-device special case, `UpdateDisk` (reject kouja templates/provisioning), `DeleteDiskResponse` 202/204, and `resolveStorageBackend`; move LVM create/delete/import bodies to the driver. |
| `internal/gateway/disk_script.go` | Delete/Move | Move payloads and script execution to `internal/storage/script.go`; update tests. |
| `internal/gateway/storage_backend_health.go` | Create | `GetStorageBackendHealth` for both drivers; `TestStorageBackendDataPath` kouja-only, LVM returns FailedPrecondition. |
| `internal/gateway/vm_actions.go` | Modify | Add `PreparedStarter`; already-running check before storage; plan outside locks; revalidate under `resourceMu`/`s.mu`; `ResetVM` plan after stop; `Paused`/`Resume`; `ResumeVM` status codes. |
| `internal/gateway/vm_create.go` | Modify | Plan before start; revalidate under `resourceMu`/`s.mu`; call `startCreatedVMLocked` (no internal lock); use `PreparedStarter`; rollback on plan failure; thin-pool only for LVM/empty; deleting-disk errors. |
| `internal/gateway/http_status.go` | Modify | Map `DeleteDiskResponse` accepted to 202, completed to 204; remove `DeleteDisk` from no-content map. |
| `internal/api/long_request.go` | Create | Shared `PathDeadline` for disk create/delete, VM create/start/reset, backend health/test; sets a 15-minute context deadline and HTTP write deadline. |
| `internal/api/lightvm.go` | Modify | Build `LightVMStartDeadline` on the shared `PathDeadline` helper. |
| `internal/api/server.go` | Modify | Keep `VMController.Start(ctx, vm)`; add a variadic `Option` and `WithStartPlanner` so existing positional `api.New` callers keep compiling; use an optional `PreparedStarter` assertion for autostart. |
| `internal/api/reconcile.go` | Modify | Release the outer `runMu` (same as `s.mu`) before each plan; per-VM plan → `resourceMu` → re-read → `s.mu` → release `resourceMu` → re-read desired state → start → release `s.mu`; use `PreparedStarter`; one attempt under the 2m reconciliation context. |
| `internal/api/api_test.go` | Modify | Add `StartWithPlan` to the fake controller; exercise `WithStartPlanner`; host-device fake. Existing `api.New` calls remain valid. |
| `cmd/rookery/serve.go` | Modify | Build registry/planner/reclaimer; pass `gateway.WithStorageRegistry` and `api.WithStartPlanner`; wire `PathDeadline`; start worker; no global timeout change. |
| `internal/readiness/readiness.go` | Modify | `Ready = VPP && (thin pool not configured || ThinPool.Ready)`; no alternate flag. |

### Gateway/API tests

| File | Action | Notes |
|---|---|---|
| `internal/gateway/disk_writes_test.go` | Modify | Backend selection, kouja create/delete, 202, LVM retry, host-device; fake driver. |
| `internal/gateway/host_devices_test.go` | Modify | Use the fake driver/`WithDiskProvisioner` injection; keep host-device regression coverage. |
| `internal/gateway/disk_template_test.go` | Modify | Kouja template rejection; LVM regression. |
| `internal/gateway/disk_kouja_test.go` | Create | Provisioning row, metadata, state, volume mismatch. |
| `internal/gateway/storage_backend_health_test.go` | Create | Control-plane, deep test, LVM rejection, owner sample. |
| `internal/gateway/vm_actions_test.go` | Modify | `StartWithPlan`, plan/lock, Reset, Resume, Paused. |
| `internal/gateway/vm_resume_test.go` | Create | Status codes and monitor errors. |
| `internal/gateway/vm_create_test.go` | Modify | Plan failure rollback, kouja without thin pool, deleting disk. |
| `internal/gateway/gateway_test.go` | Modify | Backend CRUD, full replacement, health. |
| `internal/gateway/ui_contract_test.go` | Modify | Locator, deletion state, run_state, health. |
| `internal/gateway/validation_test.go` | Modify | Driver/config validation. |
| `internal/api/long_request_test.go` | Create | Path/method matching and deadline. |

### UI

| File | Action | Notes |
|---|---|---|
| `ui/src/inventory/types.ts` | Modify | Nested kouja config, `protocol`, `locator`, `deletion_state`/`error`, `run_state`, health protocols; no `in_use`. |
| `ui/src/inventory/disks/StorageBackendDialog.tsx` | Modify | Kouja fields; Test connection for saved backend; Test data path kouja-only. |
| `ui/src/inventory/disks/DisksTab.tsx` | Modify | Kouja/class/endpoint, locator, provisioning/deleting/delete_failed badges; template/import disabled for kouja; delete_failed Retry; refresh on kouja delete. |
| `ui/src/inventory/disks/DiskDialog.tsx` | Modify | Label "Provisioned storage (LVM thin or kouja)"; hide templates/provisioning for kouja. |
| `ui/src/inventory/disks/ImportDisksDialog.tsx` | Modify | Reject kouja. |
| `ui/src/inventory/vms/disk-predicates.ts` | Create | Shared `diskNeedsThinPool`/`diskIsDeleting`. |
| `ui/src/inventory/vms/VMsTab.tsx` | Modify | Global gate uses `diskNeedsThinPool`; Resume only paused; deleting filter. |
| `ui/src/inventory/vms/VMDialog.tsx` | Modify | Shared predicate; deleting filter; kouja label. |
| `internal/webui/static/build/app.js` | Regenerate | `make ui-build`. |

### Terraform and docs

| File | Action | Notes |
|---|---|---|
| `cmd/terraform-provider-rookery/internal/provider/resource_storage_backend.go` | Modify | Nested `config.kouja`; `vg`/`thin_pool` optional; full replacement semantics. |
| `cmd/terraform-provider-rookery/internal/provider/data_source_storage_backend.go` | Modify | Computed nested kouja fields and protocols. |
| `cmd/terraform-provider-rookery/internal/provider/resource_disk.go` | Modify | Add computed `source`, `protocol`, `locator`, `deletion_state`, `deletion_error` to the shared `diskModel` and schema; use fixed create context 20m and delete context 4h (not `client.Timeout()`); delete poll 2s until 404/`delete_failed`; `setDiskState` copies new fields. |
| `cmd/terraform-provider-rookery/internal/provider/data_source_disk.go` | Modify | Add the same computed `source`/`protocol`/`locator`/deletion fields to its schema so the shared `diskModel` stays consistent; keep existing fields. |
| `cmd/terraform-provider-rookery/internal/provider/resource_storage_backend_test.go` | Create | Kouja config, full replacement. |
| `cmd/terraform-provider-rookery/internal/provider/resource_disk_test.go` | Modify | 202/204 delete polling, long timeout, `delete_failed`, locator/source state. |
| `cmd/terraform-provider-rookery/examples/complete/main.tf` | Modify | Kouja example and provider timeout. |
| `cmd/terraform-provider-rookery/internal/provider/schema_test.go` | Modify | Both driver shapes. |
| `docs/api.md` | Modify | Nested config, source/protocol/locator, delete 202, health/test protocols, run_state, resume. |
| `docs/operations.md` | Modify | Prerequisites, stable SAN, portals, plaintext iSCSI, secret, retries, deep test, timeouts, rollback drain. |
| `docs/architecture.md` | Modify | Client SDK, driver/attachment separation, protocol variants, single state machine. |
| `docs/development.md` | Modify | Packages, client SDK, tests. |
| `AGENTS.md` | Modify | Drivers, attachment protocols, direct QEMU path, states, readiness. |
| `README.md` | Modify | Mention kouja. |
| `THIRD_PARTY_NOTICES.md` | Modify | Kouja module and Apache 2.0/commercial dual license. |

### Verification

*   `make proto-generate`, `make proto-check`.
*   `make ui-build` and bundle drift check.
*   `go test ./...`, `go vet ./...`, `golangci-lint run ./...`, `gofmt -l .`.
*   `go build ./cmd/terraform-provider-rookery`; `terraform validate` for the
    complete example with `dev_overrides`.
*   80% changed-line coverage.

Manual checks:

1. Save a kouja backend while the agent is down; the record saves and health
   reports unreachable. Start the agent, health passes, deep test passes.
2. Create a disk; inspect `disks.backend_metadata` for the versioned volume
   envelope and iSCSI reference; confirm `source=kouja`, `protocol=iscsi`,
   `locator`, and empty `device`. Confirm no credential is stored.
3. Start a VM; confirm QEMU uses `file.driver=iscsi`, the guest sees the disk,
   and stop/start works.
4. Delete a running VM's disk; reject. Stop, delete; confirm 202, `deleting`,
   then the row and volume disappear.
5. Delete with an active session; confirm retries, `deletion_error`, no force.
6. Cut the network; measure the real pause bound and verify `run_state`/Resume.
7. Start with the agent down; confirm a specific error, no half-created VM.
8. Remove the export; start; confirm failure and no re-export.
9. Rollback: remove kouja disks and all non-empty state rows; old binary works
   for LVM/host-device.
10. LVM thin and host-device regression.

--------------------------------------------------------------------------------

<!-- Provenance: Indicates initial structural drafting via EGM. -->

*Drafted with EGM*
