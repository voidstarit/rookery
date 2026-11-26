# Architecture notes

Repository layout, commands, configuration, packaging, and database engine.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Commands and packages

`cmd/rookery` has three commands: `init` prepares host prerequisites (idempotently creates/verifies
the thin pool and pings the VPP binary API), `serve` runs the generated gateway API plus a
Preact/TypeScript UI built from `ui/`, and `create-client-cert` (alias `issue-client-cert`) issues
an mTLS client certificate from the existing CA; the certificate command writes the client
certificate, private key, and CA files and shares the server's `-db`, `-pki-dir`, `-pki-mode`, and
optional PKI file flags. `internal/api` handles HTTP, `internal/store` owns SQLite and migrations,
`internal/host` wraps LVM/GoVPP and runs CoreDHCP VNFs, `internal/qemu` launches QEMU/KVM and owns
VNC Unix sockets, `internal/readiness` caches prerequisite status, and `internal/webui` embeds the
UI. `cmd/rookery`, `cmd/vnfs`, and `internal/config` support a YAML file through `-config` /
`ROOKERY_CONFIG`; one file may contain `api:` (serve/init/certificate issuance) and `vnf:` (daemon)
sections.

## Configuration precedence

Precedence is flags > environment > file > defaults, unknown fields are rejected, and
`config.example.yaml` documents every key.

## Release packaging

`.github/workflows/release.yml` runs on `v*` tags, published releases, or manual dispatch with a tag
and calls `make release VERSION=<tag>` to publish a static `linux/amd64` archive containing
`rookery`, `vnfs`, `virtiofsd`, `terraform-provider-rookery_<version>_linux_amd64`,
`config.example.yaml`, `README.md`, `docs/`, and scripts, plus SHA-256 files. `make virtiofsd`
(delegating to `extra/virtiofsd/Makefile`) downloads the upstream static x86_64 virtiofsd artifact
into `extra/virtiofsd/virtiofsd`; the binary is gitignored and `make release` includes it next to
`rookery`. The Terraform provider is also published as a standalone release asset. `make release`
builds the UI first and sets the provider version from `VERSION`.

## Database

SQLite uses the pure-Go `modernc.org/sqlite` driver, so builds need no CGO. The module targets Go
1.26 and pins `modernc.org/sqlite` v1.60.1.

## Package map

| Path | Purpose |
| --- | --- |
| `cmd/rookery` | `init`, `serve`, and `create-client-cert` entry points. |
| `cmd/vnfs` | DHCP/DNS/BGP daemon and its `VNFDaemon` gRPC service. |
| `cmd/terraform-provider-rookery` | Terraform provider (part of the main Go module). |
| `internal/api` | HTTP server, native handlers, lifecycle endpoints, autostart, reconciliation. |
| `internal/gateway` | grpc-gateway service implementations. |
| `internal/lifecycle` | VNF writes and network-deletion orchestration. |
| `internal/store` | SQLite schema, migrations, and persistence. |
| `internal/qemu` | QEMU/KVM launch, monitor socket, VNC sockets, disk arguments. |
| `internal/lightvm` | LightVM preparation/start/cleanup runtime and start coordinator. |
| `internal/vmstart` | Resolves runtime disk attachments (LVM/Kouja) before a start. |
| `internal/host` | LVM, GoVPP/VPP objects, NAT44, host interfaces/taps, pcap, VNF manager. |
| `internal/storage` | Driver-neutral storage-backend interfaces. |
| `internal/kouja` | Kouja remote-iSCSI driver. |
| `internal/cgroup` | cgroup v2 hierarchy, classes, and usage. |
| `internal/diskthrottle` | Validates and formats QEMU block-throttling limits. |
| `internal/blockdev` | Block-device attachment descriptions. |
| `internal/pki` | CA, server/client certificates, and mTLS configuration. |
| `internal/vnfd` | Client for the VNF daemon gRPC service. |
| `internal/readiness` | Cached host-prerequisite status. |
| `internal/config` | YAML/environment/flag configuration loading. |
| `internal/logging` | Process-wide zerolog setup. |
| `internal/netutil` | Shared IP-prefix helpers. |
| `internal/labels` | Label validation and normalization. |
| `internal/webui` | Embedded static UI assets and handler. |

