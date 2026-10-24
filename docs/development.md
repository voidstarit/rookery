# Development

## UI development

The management UI is Preact + TypeScript under `ui/`. Sources are typechecked
with `tsc` and bundled with esbuild into `internal/webui/static/build`, which is
committed and embedded in the Go binary with `go:embed`. Do not edit the
generated bundles directly.

```bash
make ui-install   # npm ci
make ui-typecheck
make ui-build     # typecheck + bundle
make ui-watch     # rebuild while developing
make build        # UI bundle + Go binaries in bin/
make test         # UI build + go test ./...
```

Node.js and npm are only needed when changing UI sources; plain `go build` and
`go test` use the committed bundle. The UI is fully Preact/TypeScript: the
console page and all inventory tabs are components, with
`ui/src/inventory/app.ts` providing the typed tab/readiness shell. Preact
islands communicate through `rookery:*` window events; keep those bridges in
sync when changing shared state.

Inventory URLs are path-based and survive refresh: `/vms`, `/disks`, `/media`,
`/networks`, and `/vnfs` select tabs; dialog routes include `/vms/new`,
`/vms/{uuid}/edit`, `/vms/{uuid}/monitor`, `/interfaces/{id}/acls`, and
`/vnfs/{id}/leases`. The server serves `index.html` for browser navigations that
do not match an embedded asset, so deep links work without hash routing.

Inventory HTTP calls go through `ui/src/shared/api.ts`. In-flight GET requests
are shared, and transient failures (network errors, `429`, and server errors)
are retried after 1s, 2s, 4s, and 10s. After the fifth failed attempt the call
stops and returns an error naming the method and path. Mutations only retry
`429`/`503` so a create is not duplicated; validation and other `4xx` errors
are returned immediately.

## Tests

```bash
go test ./...
go vet ./...
```

`internal/qemu` includes an integration test that launches a real QEMU/KVM
process and is skipped automatically when `/dev/kvm` or `qemu-system-x86_64`
is unavailable. Use `go test -short ./...` to skip it explicitly.

## Releases

Pushing a tag such as `v0.1.0` runs `.github/workflows/release.yml`, builds the
API server, VNF daemon, and Terraform provider for Linux x86_64/amd64, and
attaches a versioned archive, the standalone provider, and SHA-256 checksums
to the GitHub release. Publishing an existing
release also triggers the workflow, and it can be run manually from the Actions
tab with an existing tag (for example `v0.1.0`).

```text
rookery-v0.1.0-linux-amd64.tar.gz
  rookery
  vnfs
  terraform-provider-rookery_0.1.0_linux_amd64
  terraform-provider-rookery_0.1.0_linux_amd64.sha256
  config.example.yaml
  README.md
  docs/
  scripts/README.md
  scripts/lvm-thin-snapshot-disk.sh
```

The workflow runs the UI build before compiling so the API binary contains the
release UI. To build the same archive locally:

```sh
make release VERSION=v0.1.0
```

If a release already exists but only shows GitHub's generated source archives,
open **Actions → Release → Run workflow**, enter the existing tag, and run it.
That uploads the binary archive and the standalone Terraform provider to the
existing release.

Binaries are statically linked (`CGO_ENABLED=0`), stripped, and built with
`-trimpath`. The Terraform provider is built with its version set from the
release tag and is also uploaded as a standalone release asset.


## Protobuf generation

The generated API lives under `design/gen` and `design/openapi`; see
[api-design.md](api-design.md) for the toolchain and conventions.

```sh
make proto-tools     # install pinned protoc plugins
make proto-generate  # regenerate Go, gRPC, gateway, validation, OpenAPI
make proto-compile   # compile protos to a descriptor set
make proto-check     # verify committed generated output is current
```
