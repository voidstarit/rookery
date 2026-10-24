# Contributing

Contributions are welcome. Please open an issue for substantial changes before
starting work so the approach can be discussed.

## Development

Requirements are Go 1.26 or newer, Node.js 24, and npm.

```sh
make test        # UI build + go test ./...
make vet
make ui-typecheck
```

Keep generated UI bundles under `internal/webui/static/build` in sync with
`ui/src`; do not edit generated files directly.

## Pull requests

- Keep changes focused and include tests for behavior changes.
- Run `make test`, `go vet ./...`, and `gofmt -l .` before opening a PR.
- Do not commit local databases, Terraform state, credentials, private keys,
  or host-specific configuration.
- By contributing, you agree that your contribution is licensed under the
  project's Apache License 2.0.

Report security issues through the process in [SECURITY.md](SECURITY.md), not
through a public issue or pull request.
