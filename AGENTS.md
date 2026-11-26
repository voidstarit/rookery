# AGENTS.md

Rookery is a self-hosted VM and network manager for Linux. The repository contains the Go
API with an embedded web UI, the `vnfs` DHCP/DNS/BGP daemon, QEMU/KVM lifecycle management,
LVM thin storage, and FD.io VPP networking.

This file is the entry point for agents and contributors working in this repository. The
detailed, hard-won subsystem notes live in [`docs/agent-notes/`](docs/agent-notes/); read the
relevant file before changing a subsystem.

Code review agents should also read [`docs/go-review-agent.md`](docs/go-review-agent.md) before
reviewing a change.

User-facing documentation lives in [`docs/`](docs/README.md), and [`README.md`](README.md) is the
high-level project entry point.

## Repository map

| Path | Purpose |
| --- | --- |
| `cmd/rookery` | API server: `init`, `serve`, and client-certificate issuance. |
| `cmd/vnfs` | DHCP/DNS/BGP daemon, exposed to the API over the internal typed `DHCPDaemon`, `DNSDaemon`, `BGPDaemon`, and `VNFRuntimeAdmin` gRPC services. |
| `cmd/terraform-provider-rookery` | Terraform provider, part of the main Go module. |
| `design/` | Protobuf source of truth, vendored third-party protos, and committed generated Go/OpenAPI. |
| `internal/` | API, gateway, store, host/VPP, QEMU, LightVM, PKI, cgroups, and UI embedding. |
| `ui/` | Preact/TypeScript sources; esbuild writes the committed bundle into `internal/webui/static/build`. |
| `extra/linux-kernel` | LightVM kernel (`virtio`, `storage`, `k8s`) and initramfs builders. |
| `extra/virtiofsd` | Download/package helper for the upstream static virtiofsd. |
| `docs/` | Architecture, operations, networking, API, development, and agent notes. |

The package-level map is in [`docs/agent-notes/architecture.md`](docs/agent-notes/architecture.md#package-map).

## Commands

Requirements: Go 1.26+ for the server; Node.js 24 and npm only when changing the UI; `protoc`
and the pinned plugins only when changing protos. Builds need no CGO.

```sh
make ui-install     # npm ci
make ui-typecheck   # tsc --noEmit
make ui-test        # bundle and run the VNF payload unit tests
make ui-build       # typecheck + esbuild into internal/webui/static/build
make ui-watch       # rebuild the bundle while developing
make build          # UI bundle + bin/{rookery,vnfs,terraform-provider-rookery}
make test           # UI build, then go test ./...
make vet            # go vet ./...
make fmt            # gofmt -w over non-UI Go files
golangci-lint run ./...
go test -short ./...    # skip the real QEMU/KVM integration test
make proto-check    # regenerate protos and fail if committed output drifted
make release VERSION=v0.1.0
go run ./cmd/rookery serve -db /tmp/rookery.db
```

CI (`.github/workflows/ci.yml`) runs the UI build, `go test` with coverage, changed-code
coverage, `go vet`, `gofmt -l`, `golangci-lint` (v2.15.0, config in `.golangci.yml`),
`make proto-check`, and a generated-UI-bundle drift check.

## Tooling for code navigation

Use symbol-aware tooling before text search. `rg`/grep/`sed` are for literals (config keys, error
strings, `rookery:*` event names, SQL, docs, generated output) and as a last resort — not for
answering "where is this defined?" or for editing source. Never use `sed -i`/`perl -pi` on Go or
TypeScript files. To find a symbol anywhere in the tree, run `scripts/glance --find NAME[|NAME...]
[path]`; for one symbol's declaration plus body, `scripts/glance --body N --match NAME <file>`. A
source `grep` is not the answer, in a file or across a directory.

```sh
# Go (from the repository root)
gopls workspace_symbol -matcher fuzzy 'CreateNetworkHostTap'
gopls symbols internal/api/host_taps.go
gopls definition internal/api/host_taps.go:70:20
gopls references internal/api/host_taps.go:70:20
gopls call_hierarchy internal/api/host_taps.go:70:20
gopls check internal/api/host_taps.go
go doc ./internal/api Server
```

If Go tooling reports a read-only cache in a sandbox (a gopls persistent-index error, or
`go doc`/`go build` failing under `~/.cache`), point both caches at a writable directory:
`XDG_CACHE_HOME=/tmp/rookery-gopls GOCACHE=/tmp/rookery-go-build gopls …`.
`gopls mcp -instructions` describes the MCP surface for agents that can attach it. References
reflect only the active build configuration, so repeat with the relevant build tags/`GOOS` when a
negative result matters.

```sh
# TypeScript / Preact
cd ui
npm run typecheck                     # tsc --noEmit, project config
./node_modules/.bin/tsc --noEmit --pretty false
```

TypeScript 7 ships a native compiler, not a standalone symbol-query CLI: use the TypeScript
language service from your editor/agent for definitions, references, and rename, and `tsc` for
diagnostics. If no language server is available, fall back to `rg -w` plus reading the file and
validate with `tsc`; do not claim symbol accuracy from text matches. Full recipes and fallback
rules are in the [tooling note](docs/agent-notes/tooling.md).

### Reading source files

Navigation answers *where*; reading answers *what it does*. Do not pay for the second when the
first is enough, and do not skip the outline before a large read.

- Run `scripts/glance <file>` before reading a source file longer than 200 lines. It prints the Go
  symbol map (`gopls symbols`, with line ranges) or the TypeScript top-level declarations, each
  followed by the first sentence of its Go doc / JSDoc comment.
- Read the ranges around the symbols the task names, plus about ten lines of context, rather than
  the whole file. Read the whole file when it is 200 lines or fewer, or when the outline shows the
  task really does touch most of it — say so in one line before doing it.
- "Who calls this" and "where is this defined" are `gopls references`/`definition` questions,
  never a full-file read.
- Do not use `cat`/`head`/`tail`/`sed -n` on Go or TypeScript source: `read` is the only reader
  that returns line numbers, honors `offset`/`limit`, and caps a single call at 2000 lines.

## Working standards

### Generated files and source-of-truth rules

- `design/` is the protobuf source of truth. It uses direct `protoc` via the root Makefile, not
  `buf`; third-party protos are vendored under `design/third_party`. Generated Go/gRPC/gateway/
  validation code and OpenAPI are committed under `design/gen` and `design/openapi`. Edit
  `.proto` files, run `make proto-tools` and `make proto-generate`, then commit the output.
- `ui/src` is the source of truth for the UI. `make ui-build` writes the committed, embedded
  bundle under `internal/webui/static/build`; edit sources, never the generated bundle.
- SQL migrations live in `internal/store/migrations` and are embedded. Add a new sequentially
  numbered migration instead of rewriting an applied one.
- Vendored runtime assets stay in-tree: noVNC under `internal/webui/static/novnc`, xterm.js and
  fonts in the UI bundle. Do not replace them with CDNs, and keep CSP exceptions minimal.

### Documentation comments

- Keep godoc current for every Go function, method, type, and package you add or change, including
  unexported helpers. Describe behavior and the contract, not the implementation line by line.
- Keep JSDoc current for every TypeScript/TSX function, type, and export you add or change, and
  keep a module-level header on new or substantially changed modules.
- Update the comment in the same change as the behavior it describes. Generated code and vendored
  third-party files are exempt.

### Go

- Format with `gofmt`; keep `go vet ./...` and `golangci-lint run ./...` clean. The lint config
  enables `govet`, `ineffassign`, `misspell`, `staticcheck`, and `unconvert`, with only the
  project's established naming/error-string style checks disabled.
- Wrap errors with context and `%w`; use `errors.Is`/`errors.As` for sentinel errors.
- Use the pure-Go `modernc.org/sqlite` driver; do not introduce CGO.
- Navigate Go symbols with `gopls`, not text search (see
  [Tooling for code navigation](#tooling-for-code-navigation)).
- Keep host operations and store records consistent. If a host operation succeeds and the DB
  write fails (or vice versa), roll the first side back explicitly.
- Prefer small, focused packages and typed APIs over JSON blobs except where a schema is
  deliberately extensible (storage backend `config`).

### Logging

Both binaries configure `zerolog` through `internal/logging.Setup`. Every line is tagged
`service=rookery` or `service=vnfs`. Hold a `zerolog.Logger` value in components (tests use
`zerolog.Nop()`); package-level events in `internal/host` and `internal/api` use
`github.com/rs/zerolog/log`. Use typed builders (`.Str`, `.Int64`, `.Uint32`, `.Bool`, `.Dur`,
`.Err`) rather than pre-formatting values into strings. Full conventions, including trace-level
events and lease log levels, are in [`docs/agent-notes/logging.md`](docs/agent-notes/logging.md).

### Tests

- Existing tests use the standard `testing` package; new behavioral specs use Ginkgo + Gomega
  (suites exist in `internal/gateway`, `internal/qemu`, and `internal/store`).
- The `internal/qemu` integration test launches a real QEMU/KVM process and skips automatically
  when `/dev/kvm` or `qemu-system-x86_64` is unavailable; `go test -short ./...` skips it
  explicitly.
- CI enforces a 20% total statement-coverage floor and 80% coverage on changed Go lines
  (`diff-cover`, generated protobuf packages excluded). Add tests for behavior changes.

### API and gateway

- Request validation uses `protoc-gen-validate`. 64-bit JSON fields are emitted as numbers;
  create/delete responses map to `201`/`204`.
- VNFs are four typed services (`DHCPVNFService`, `DNSVNFService`, `NAT44VNFService`,
  `BGPVNFService`); there is no common `/api/vnfs` route. Parent writes carry flat scalars, child
  rows have per-item routes, and a child write reloads the runtime while a parent
  scalar/transport/enabled change stops and starts it.
- grpc-gateway registers handlers in declaration order and tries the newest first, so literal
  paths must be declared after patterns they would otherwise shadow (for example
  `/api/interfaces/counters` must stay after `GetInterface`/`DeleteInterface`).
- The main `serve` listener routes `/api` through the grpc-gateway and mounts
  `api.Server.NativeHandler` for WebSockets (VNC, LightVM console, token console), pcap
  downloads, PKI endpoints, LightVM uploads, and the static UI. Keep WebSocket and streamed
  download protocols on native handlers; the gateway does not proxy them.
- The native `responseRecorder` in `internal/api/server.go` must keep implementing
  `http.Hijacker` (and `Flush`), or WebSocket upgrades break.
- VM and LightVM IDs are UUIDs; disk and media IDs remain integers.
- See [`docs/agent-notes/api.md`](docs/agent-notes/api.md) before changing listeners, labels,
  or gateway routing.

### UI

- The UI is fully Preact/TypeScript. `ui/src/inventory/app.ts` is the typed tab/readiness shell;
  tab islands communicate through `rookery:*` window CustomEvents. Keep those bridges in sync.
- Inventory routing is path-based (`/vms`, `/disks`, `/media`, `/networks`, `/vnfs`, plus dialog
  deep links). `internal/webui.Handler` must keep serving `index.html` for HTML navigations that
  do not match an embedded asset, or refreshing a deep link returns 404.
- All inventory HTTP calls go through `ui/src/shared/api.ts`: shared in-flight GETs, transient
  failures retried after 1s/2s/4s/10s, mutations retried only on `429`/`503`. Do not add
  unbounded retries or unguarded polling loops; gate polls on the active route and visibility.
- Use the TypeScript language service for symbol navigation and `tsc --noEmit` for diagnostics;
  `rg` is only for literal strings.
- The LightVM console page is the only route whose CSP allows `style-src 'unsafe-inline'`
  (xterm injects cursor/DOM styles).
- See [`docs/agent-notes/ui.md`](docs/agent-notes/ui.md).

### Database

- SQLite uses pure-Go `modernc.org/sqlite`; migrations are embedded and numbered sequentially
  (latest is `0061_per_type_vnfs.sql`). Foreign-key cascades and restriction are used
  deliberately; preserve them when adding tables.
- Store methods return typed/sentinel errors for cases the API maps to HTTP status codes; use
  transactions for multi-row invariants.
- Entity labels live in `entity_labels` and are loaded separately from list payloads.

### Security

- The local HTTP API has no authentication or authorization and binds to `127.0.0.1:8080` by
  default; never expose it without an access-controlled proxy. The public mTLS listener is the
  authenticated entry point.
- `console_enabled` is enforced server-side for VNC and LightVM consoles; token console access
  rechecks token expiry, access, and run state every second. Hiding UI buttons is not enough.
- Never log or persist secrets: Kouja CHAP secrets are transient `0600` files under the run
  directory and never appear in QEMU arguments or SQLite; issued client private keys are
  returned once and not stored.
- Do not commit local databases, Terraform state, credentials, private keys, or host-specific
  configuration. Report vulnerabilities through [`SECURITY.md`](SECURITY.md), not a public issue.

### Docs and Git

- `README.md` stays high-level; host, networking, API, operations, and development detail belongs
  under `docs/`, indexed by [`docs/README.md`](docs/README.md). Update the relevant page (and the
  agent note) when behavior changes.
- Use focused changes and conventional commit subjects (`feat(scope): …`, `fix(scope): …`,
  `docs: …`, `test(scope): …`, `build(deps): …`), matching the existing history; branch names
  follow `feat/…` and `fix/…`. Keep changes focused and include tests for behavior changes.
- Run the relevant checks before opening a PR (`make test`, `go vet ./...`, `gofmt -l .`,
  `golangci-lint run ./...`, plus `make proto-check` and the generated-bundle check when
  applicable). Contributions are Apache-2.0 licensed.

## Invariants that bite

1. **Two processes, one owner of state.** The API owns the database and every VPP operation;
   `vnfs` owns DHCP/DNS/BGP runtimes and only writes BGP-learned routes to VPP itself. `serve`
   starts without the daemon and reports VNFs as `degraded`.
2. **VPP state is in memory.** Startup and a 5-second identity check reconcile bridge domains,
   vhost-user interfaces, ACLs, NAT44, host interfaces/taps, routes, and VNF taps. Rebuild
   workers must never overlap (concurrent BVI creation aborts VPP). → [networking](docs/agent-notes/networking.md#infrastructure-reconciliation)
3. **Readiness is not a startup failure.** `serve` must start when prerequisites or the VNF
   daemon are missing; gates are applied per operation. The thin pool is required only for
   LVM-backed disks, not host passthrough. → [operations](docs/agent-notes/operations.md#readiness)
4. **Creates start immediately and roll back.** `POST /api/vms` and
   `POST /api/lightvm/instances` start the workload; a failed start stops any partial runtime
   and removes the record. `desired_state` drives autostart for desired-running workloads;
   ephemeral LightVMs are deleted after exit and never auto-start again. →
   [compute](docs/agent-notes/compute.md#vm-lifecycle-and-autostart),
   [lightvm](docs/agent-notes/lightvm.md#lifecycle-autostart-and-ephemeral-runs)
5. **Concurrency is explicit.** The shared `resourceMu` serializes disk provisioning, VM
   attachment, and typed VNF writes/reloads; a shared run mutex guards VM lifecycle with
   reconciliation; `lightvm.StartCoordinator` serializes starts per LightVM and limits concurrent
   preparation. Preserve or replace these with equivalent guarantees. →
   [api](docs/agent-notes/api.md#concurrency)
6. **The gateway/native split is deliberate.** WebSockets, pcap streams, PKI, uploads, and the
   UI stay on native handlers; the public mTLS listener serves the gateway plus the token
   console only, and PKI endpoints are not exposed there. → [api](docs/agent-notes/api.md#http-listeners-and-native-routes)
7. **Host and store must agree.** Roll back the LV/device or the record on partial failure;
   delete the LV before its record. Apply the same discipline to VPP objects, tap records, and
   attachments. → [storage](docs/agent-notes/storage.md#templates-and-clones)
8. **Addressing is reserved and scoped.** Each network gets its own VRF table
   (`100000 + network ID`); `.1` is the BVI, `.2` is the shared VNF stack, `.3`–`.5` are
   reserved. DHCP ranges, static assignments, and DNAT locals must avoid them. →
   [networking](docs/agent-notes/networking.md#cidrs-vrfs-and-reserved-addresses)
9. **DHCP/BGP transports have sharp edges.** Shared memif needs
   `host.NetworkVNFMemifID(networkID)`, and BGP must use `vnfMemifBGPMAC`
   (`02:4f:4d:…`), never `VNFMemifMAC` (`02:4d:4d:…`). → [vnfs](docs/agent-notes/vnfs.md)
10. **Console access is a server-side gate.** Disable it in the API, not just the UI, and close
    live sessions when access is disabled or the workload stops. →
    [compute](docs/agent-notes/compute.md#console-access-control),
    [lightvm](docs/agent-notes/lightvm.md#console-access)
11. **Validated host devices are revalidated.** A passthrough disk mounted after registration
    must not be handed to a guest; `internal/qemu.Start` rechecks it. →
    [storage](docs/agent-notes/storage.md#host-passthrough-devices)
12. **Generated artifacts stay in sync.** Protos, OpenAPI, UI bundles, and migrations are
    committed; CI fails on drift. Regenerate rather than hand-edit.

## Notes index

| Note | Contents |
| --- | --- |
| [Architecture](docs/agent-notes/architecture.md) | Commands, package map, configuration precedence, release packaging, database. |
| [API](docs/agent-notes/api.md) | Protobuf/gateway, listeners, native routes, identifiers, labels, concurrency. |
| [UI](docs/agent-notes/ui.md) | Preact sources, bundles, HTTP client, VM console. |
| [Compute](docs/agent-notes/compute.md) | VM lifecycle, QEMU launch/monitor, cgroups, metrics, media, console access. |
| [LightVM](docs/agent-notes/lightvm.md) | Inventory, runtimes, config drive, lifecycle, consoles, kernel variants. |
| [Storage](docs/agent-notes/storage.md) | Backends, Kouja, templates, attachments, passthrough, throttle classes, media. |
| [Networking](docs/agent-notes/networking.md) | Networks, interfaces, ACLs, routes, host NICs/taps, NAT44, reconciliation, counters. |
| [VNFs](docs/agent-notes/vnfs.md) | Typed records/tables, daemon RPCs, DHCP/DNS/BGP runtimes, transports, reload, leases, legacy cleanup. |
| [Troubleshooting](docs/agent-notes/troubleshooting.md) | VPP graph, packet trace, ACL counters, packet capture. |
| [Operations](docs/agent-notes/operations.md) | Readiness, mTLS/PKI, client allowlist, local-listener security, rate limits. |
| [Terraform](docs/agent-notes/terraform.md) | Provider scope, typed VNF resources and state migration, disk force delete. |
| [Logging](docs/agent-notes/logging.md) | zerolog setup, fields, levels, and trace-level events. |
| [Tooling](docs/agent-notes/tooling.md) | gopls/TypeScript navigation (incl. call hierarchy and caveats), read protocol, edit rules, sandbox cache workaround. |

## Keeping these notes current

When behavior changes, update the matching `docs/agent-notes/` file in the same change and keep
the index above accurate. Keep this file concise: it holds working rules and entry points, while
subsystem detail belongs in the notes. If a note grows past roughly 20 KB, split it by topic and
add it to the index rather than letting this entry point exceed the agent instruction budget.
