# API notes

Protobuf/gateway design, the typed VNF API, listeners, identifiers, labels, and concurrency.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Protobuf source of truth and gateway

`design/` is the protobuf source of truth for the API. It uses direct `protoc` via the root
Makefile, not `buf`. Third-party protos are vendored under `design/third_party`; generated
Go/gRPC/gateway/validation code and OpenAPI are committed under `design/gen` and `design/openapi`.
Use `make proto-tools`, `make proto-generate`, `make proto-compile`, and `make proto-check`. Request
validation uses `protoc-gen-validate`; the gateway under `internal/gateway` exposes all generated
services for system, VMs, disks, disk throttle classes, media, networks, interfaces, ACLs, the four
typed VNF services (`DHCPVNFService`, `DNSVNFService`, `NAT44VNFService`, `BGPVNFService`),
storage backends, host interfaces, troubleshoot, and capture through an in-process gRPC server and
grpc-gateway. VM/disk/media/network/interface/ACL/storage writes, VM and LightVM lifecycle actions
and metrics, interface ACL assignment, storage disk import, typed VNF writes, troubleshoot/capture,
and VNF status/lease/session views are implemented. VNF writes and network deletion are
implemented in `internal/lifecycle`, backed by the VPP/NAT/DHCP runtime exposed by `internal/api`;
the old JSON handlers have been removed. The gateway emits 64-bit JSON fields as numbers and maps
create/delete responses to `201`/`204`. Network deletion uses a native lifecycle method that stops
VNFs, removes any BVI and bridge domain, then deletes the record. VM lifecycle and disk provisioning
share the server's mutexes so gateway and native handlers cannot race.

## VNF API

There is no `/api/vnfs` route. Parent paths are `/api/dhcp-vnfs`, `/api/dns-vnfs`,
`/api/nat44-vnfs`, and `/api/bgp-vnfs`; each has `GET`/`POST` on the collection,
`GET`/`PUT`/`DELETE` on `/{id}`, and `POST /{id}/restart`. Child collections are
`/{vnf_id}/static-assignments` and `/{vnf_id}/options` (DHCP), `/{vnf_id}/records` and
`/{vnf_id}/forwarders` (DNS), `/{vnf_id}/dnat-rules` (NAT44), and
`/{vnf_id}/advertised-prefixes` (BGP); each supports `GET`/`POST`, and each item under
`/{vnf_id}/.../{child_id}` supports `GET`/`PUT`/`DELETE`. Type-specific reads are
`GET /api/dhcp-vnfs/{id}/leases`, `GET /api/dns-vnfs/{id}/record-views`,
`GET /api/nat44-vnfs/{id}/sessions`, and `GET /api/bgp-vnfs/{id}/status`. The generated
definitions are in `design/proto/rookery/v1/{dhcp,dns,nat44,bgp}_vnf.proto`; there are no shared
`VNF`/`VNFConfig` messages.

The typed write path is `gateway` handler → `lifecycle.Service` (`VNFWriter`) → typed store
methods plus activation/reload. The gateway re-reads the typed record after a write and fills
`status`, `status_detail`, and counts from `VNFStatusProvider`. Parent create/update requests carry
scalars only; child requests carry one child's fields. A wrong-type ID maps to `404`, duplicate
child keys to `409`, invalid values to `400`, an unavailable VNF runtime write to `503`, and a
reload failure to `500`.

Child writes (`internal/lifecycle/vnf_children.go`) write the row, then call
`backend.ReloadVNF` when the parent is enabled. `vnfd.Client` selects `ReloadDHCP`, `ReloadDNS`,
or `ReloadBGP`; NAT44 uses `ApplyNAT44DNATRules` instead of the daemon. Reload keeps the process,
listener, lease file, and desired spec. On failure the lifecycle restores the previous row,
reloads the previous config, and disables the parent when that restore reload also fails. Parent
scalar/transport/enabled updates still use the stop/start activation path.

Type-specific reads: leases come from the daemon's `ListDHCPLeases` and are filtered to the VNF's
configured range; `record-views` merges typed static records with the daemon's dynamic DNS records
and reports a `dynamic_error` instead of hiding static rows; NAT44 sessions come from the live VPP
NAT44 dump through `api.Server.VNFSessions`; BGP status comes from the daemon's `GetBGPStatus`.
`VNFStatus` combines the stored enable flag with `Running`/`Healthy` to report `running`,
`disabled`, or `degraded`.

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

The shared `resourceMu` (owned by `api.Server` and passed to `lifecycle.Service`) serializes disk
provisioning, VM attachment, and typed VNF parent/child writes and reloads; preserve it or replace
it with equivalent concurrency control. A child reload is shorter than a restart but still holds
the lock.

## Additional gRPC listeners

`gateway.New` supports extra gRPC listeners via `gateway.WithExtraGRPCListener`; `serve
-grpc-mtls-addr` (default `:9443`, `ROOKERY_GRPC_MTLS_ADDR`) wraps a listener with
`pki.Manager.TLSConfig()` and serves the same generated services alongside the optional plaintext
`-grpc-addr` listener. The PKI TLS config must advertise ALPN `h2`; without it, grpc-go clients fail
with `missing selected ALPN property`.
