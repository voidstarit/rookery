# API notes

Protobuf/gateway design, listeners, identifiers, labels, and concurrency.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Protobuf source of truth and gateway

`design/` is the protobuf source of truth for the API. It uses direct `protoc` via the root
Makefile, not `buf`. Third-party protos are vendored under `design/third_party`; generated
Go/gRPC/gateway/validation code and OpenAPI are committed under `design/gen` and `design/openapi`.
Use `make proto-tools`, `make proto-generate`, `make proto-compile`, and `make proto-check`. Request
validation uses `protoc-gen-validate`; the gateway under `internal/gateway` exposes all generated
services for system, VMs, disks, disk throttle classes, media, networks, interfaces, ACLs, VNFs,
storage backends, host interfaces, troubleshoot, and capture through an in-process gRPC server and
grpc-gateway. VM/disk/media/network/interface/ACL/storage writes, VM and LightVM lifecycle actions
and metrics, interface ACL assignment, storage disk import, VNF status/lease/session views,
troubleshoot/capture, and VNF writes are implemented. VNF writes and network deletion are
implemented in `internal/lifecycle`, backed by the VPP/NAT/DHCP runtime exposed by `internal/api`;
the old JSON handlers have been removed. The gateway emits 64-bit JSON fields as numbers and maps
create/delete responses to `201`/`204`. Network deletion uses a native lifecycle method that stops
VNFs, removes any BVI and bridge domain, then deletes the record. VM lifecycle and disk provisioning
share the server's mutexes so gateway and native handlers cannot race.

## HTTP listeners and native routes

The main `serve` HTTP listener routes `/api` through the grpc-gateway and mounts
`api.Server.NativeHandler` for WebSockets (VNC, LightVM console, and token-based console access),
pcap downloads, PKI, uploads, and static UI. `api.New` now builds only this native handler. `serve
-gateway-addr` adds an optional second gateway HTTP listener and `serve -grpc-addr` exposes direct
gRPC. Keep WebSocket VNC and streamed pcap downloads on native HTTP handlers because gateway does
not proxy those protocols.

## Identifiers

VM IDs are UUIDs (`internal/store/migrations/0004_vm_uuid.sql`). Use UUID paths/validation
consistently; disk and media IDs remain integers.

## Labels

Inventory entities support free-form labels stored in `entity_labels` (`0041_entity_labels.sql`).
The `LabelService` exposes `GET /api/labels/{entity_type}`, `GET
/api/labels/{entity_type}/{entity_id}`, and `PUT /api/labels/{entity_type}/{entity_id}`; setting
labels replaces the full set. Supported entity types are `vm`, `disk`, `media`, `network`,
`interface`, `vnf`, `acl`, `route_table`, `route`, `network_pipe`, `storage_backend`, `host_tap`,
and `host_interface`. Labels are trimmed, deduplicated, sorted, limited to 64 per entity and 64
characters each, and may not contain commas or control characters. Entity list payloads do not embed
labels; the UI loads them per type and shows label chips plus a reusable labels dialog. Terraform
resources and data sources expose a `labels` set and apply it through the label API, including
label-only updates for otherwise immutable resources.

## Concurrency

`internal/api.Server.resourceMu` serializes disk provisioning with VM attachment; preserve it or
replace it with equivalent concurrency control.

## Additional gRPC listeners

`gateway.New` supports extra gRPC listeners via `gateway.WithExtraGRPCListener`; `serve
-grpc-mtls-addr` (default `:9443`, `ROOKERY_GRPC_MTLS_ADDR`) wraps a listener with
`pki.Manager.TLSConfig()` and serves the same generated services alongside the optional plaintext
`-grpc-addr` listener. The PKI TLS config must advertise ALPN `h2`; without it, grpc-go clients fail
with `missing selected ALPN property`.
