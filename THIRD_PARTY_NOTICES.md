# Third-party notices

Rookery is licensed under the Apache License 2.0. Third-party components
included in or used by the project remain under their own licenses.

## noVNC

The `internal/webui/static/novnc` directory contains noVNC v1.7.0, licensed
under the Mozilla Public License 2.0. The upstream license text is included at
`internal/webui/static/novnc/LICENSE.txt`.

Upstream: <https://github.com/novnc/noVNC>

## xterm.js

The LightVM console uses `@xterm/xterm` and `@xterm/addon-fit` (MIT license),
installed through npm and bundled into `internal/webui/static/build`.

Upstream: <https://github.com/xtermjs/xterm.js>

## JetBrains Mono

The LightVM console bundles JetBrains Mono (SIL Open Font License 1.1) from
`@fontsource/jetbrains-mono`.

Upstream: <https://github.com/JetBrains/JetBrainsMono>

## virtiofsd

Release archives include `virtiofsd` from the upstream virtio-fs project
(Apache License 2.0 and BSD 3-Clause). It is downloaded by `make virtiofsd`
from the project's published x86_64 Linux CI artifact.

Upstream: <https://gitlab.com/virtio-fs/virtiofsd>

## Protocol definitions

`design/third_party` contains vendored protobuf definitions and license texts
for Google APIs and grpc-gateway. See the license files in that directory.

## Go dependencies

Go module dependencies are listed in `go.mod` and `go.sum`; their licenses are
provided by their respective modules. Run `go list -m all` to inspect the
dependency set.
