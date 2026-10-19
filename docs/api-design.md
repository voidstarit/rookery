# Rookery API design

This document describes the Rookery protobuf API design. The `design/` directory is the protobuf source of truth; implementation lives outside it.

## Layout

```text
design/
  proto/rookery/v1/       proto definitions and HTTP annotations
  third_party/            vendored google/api and validate protos
  gen/rookery/v1/         generated Go, gRPC, gateway, and validation code
  openapi/                generated OpenAPI (Swagger 2.0) manifest

internal/gateway/         implementation: in-process gRPC server + grpc-gateway
```

Generated manifests are committed. `make proto-check` compiles the protos and
fails when regeneration changes tracked generated files.

## Toolchain

No `buf` is used. The build calls `protoc` directly.

Requirements:

- `protoc` 3.21 or newer
- protobuf well-known protos available under `PROTO_INCLUDE` (default `/usr/include`)
- generator binaries in `$(go env GOPATH)/bin`:
  - `protoc-gen-go` v1.36.12
  - `protoc-gen-go-grpc` v1.6.2
  - `protoc-gen-grpc-gateway` v2.28.0
  - `protoc-gen-openapiv2` v2.28.0
  - `protoc-gen-validate` v1.3.3

Install them with:

```bash
make proto-tools
```

## Generation

```bash
make proto-compile   # compile all protos to a descriptor set
make proto-generate  # regenerate Go, gRPC, gateway, validation, and OpenAPI
make proto-check     # regenerate and verify committed output is current
```

The generated OpenAPI file is:

```text
design/openapi/rookery.swagger.json
```

Swagger 2.0 is what `protoc-gen-openapiv2` emits. Add an OpenAPI 3 conversion
step only if a consumer requires it.

## API conventions

- Proto package: `rookery.v1`
- Go package: `github.com/voidstarit/rookery/design/gen/rookery/v1`
- HTTP routes preserve the existing `/api/...` paths via `google.api.http`.
- The future gateway must enable `UseProtoNames: true` for snake_case JSON.
- List RPCs use `response_body` so gateway responses remain arrays.
- Timestamps use `google.protobuf.Timestamp`.
- Nullable IDs use proto3 `optional`.
- Error handling is not generated: the gateway will map gRPC errors back to the
  existing `{"error":"..."}` response shape.
- WebSocket VNC, WebShark SSE/static assets, and pcap downloads stay on native
  HTTP handlers because grpc-gateway does not proxy those protocols.

## Validation

Request messages use `protoc-gen-validate` rules. The generated `Validate()`
methods are intended to run in a gRPC unary interceptor, which also covers
grpc-gateway traffic.

Validation covers field-level constraints such as required names, UUIDs,
ranges, enums, MAC addresses, and capture limits. Cross-field and conditional
rules (for example, DHCP versus NAT44 VNF config requirements) remain in a
small domain validator until they are moved into proto-level CEL rules.

## Native exceptions

The following remain outside the unary gRPC/gateway surface initially:

- `GET /ws/vms/{id}/vnc` — WebSocket
- `/webshark/*` — vendored WebShark UI and SSE
- `GET /api/troubleshoot/capture/files/{name}` — streamed pcap download
- static UI assets

## Migration status

Phase 0 (design package and generation) and Phase 1 (all current JSON resource
schemas and HTTP annotations) are complete.

The first gateway slice exists under `internal/gateway`: it runs an in-process
gRPC server, applies generated validation through a unary interceptor, exposes
`SystemService`, read-only list/get adapters for VMs, disks, media, networks,
interfaces, ACLs, VNFs, storage backends, and host interfaces, plus
VM/disk/media/network/interface/ACL/storage writes, VM lifecycle actions and
metrics, interface ACL assignment, storage disk import, and troubleshoot/capture
services through grpc-gateway. It preserves the `{"error":"..."}` response
shape, emits 64-bit integers as JSON numbers through a custom marshaler, and
returns `201` for creates and `204` for deletes. VM lifecycle and disk
provisioning share the legacy API's mutexes so both listeners remain serialized. The main `serve` listener now routes `/api` through this gateway; the legacy
handler remains for native VNC/WebShark/pcap-download/static paths and as the
temporary VNF-write bridge. `-gateway-addr` adds an optional second gateway HTTP
listener and `-grpc-addr` exposes direct gRPC.

## Known gaps before full cutover

- `UpdateVMRequest` uses repeated `disk_ids`/`interface_ids`; proto3 has no
  presence bit for repeated fields. Add a presence wrapper or field mask before
  replacing the existing update semantics.
- VNF DHCP/NAT44 conditional requirements remain in a domain validator until a
  CEL-based validation layer is added.
- grpc-gateway returns `200` for `google.protobuf.Empty` deletes; map successful
  deletes to the existing `204` behavior in the gateway forwarder.
- The gateway must reproduce the existing security headers, CSP, request
  logging, and readiness gating.
- The gateway's numeric JSON marshaler now matches the legacy REST API's
  number encoding for int64/uint64 fields; verify it with UI contract tests
  before route cutover.
- VNF create/update/delete/restart are implemented by a temporary in-process
  bridge to the legacy VNF HTTP handler. Native activation/NAT lifecycle
  extraction is required before removing the legacy handlers.
- Gateway network deletion requires VNFs and any BVI to be removed first; it
  does not yet replicate the legacy handler's automatic VNF shutdown.
- VM update uses `replace_interface_ids`/`replace_disk_ids` to distinguish an
  omitted list from an explicit replacement. Interfaces can be cleared with
  `replace_interface_ids`; disks cannot be cleared because a VM must keep at
  least one disk.
- Disk creation/deletion, network/interface/ACL writes, VM create/update/delete
  and actions, storage disk import, and troubleshoot/capture are implemented.
- VNF status/status_detail/lease_count and lease/session RPCs still need the VNF
  runtime adapter.
- Troubleshoot and capture gateway services are generated but not registered yet.
- Route cutover is complete for the main HTTP listener; UI contract tests and
  removal of the legacy API handlers (blocked on native VNF lifecycle
  extraction) are still pending.
