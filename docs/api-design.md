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

The gateway under `internal/gateway` runs an in-process gRPC server, applies
generated validation through a unary interceptor, and exposes all generated
services: system, VMs, disks, media, networks, interfaces, ACLs, VNFs, storage
backends, host interfaces, troubleshoot, and capture. It preserves the
`{"error":"..."}` response shape, emits 64-bit integers as JSON numbers through
a custom marshaler, keeps list responses as arrays, and returns `201` for
creates and `204` for deletes.

The main `serve` listener routes `/api` through the gateway. `api.Server.NativeHandler`
serves only native VNC WebSocket, WebShark, pcap-download, and static UI paths. VM lifecycle and disk provisioning share the legacy API's mutexes so both
listeners remain serialized. VNF writes and network deletion call native
lifecycle methods implemented in `internal/api`. `-gateway-addr` adds an
optional second gateway HTTP listener and `-grpc-addr` exposes direct gRPC.

## Known gaps

- UI contract tests against the cutover gateway are still needed.
- The legacy API route registrations and handlers remain in `internal/api` for
  tests but are no longer mounted by `serve`. They can be removed once the
  native VNF/network lifecycle methods are moved into a dedicated service and
  covered by focused unit tests.
- VNF DHCP/NAT44 conditional requirements remain in the domain validator rather
  than generated validation (a CEL layer would close this gap).
- VM update uses `replace_interface_ids`/`replace_disk_ids` to distinguish an
  omitted list from an explicit replacement. Interfaces can be cleared with
  `replace_interface_ids`; disks cannot be cleared because a VM must keep at
  least one disk.
