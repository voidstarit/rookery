# Third-party notices

Rookery is licensed under the Apache License 2.0. Third-party components
included in or used by the project remain under their own licenses.

## noVNC

The `internal/webui/static/novnc` directory contains noVNC v1.7.0, licensed
under the Mozilla Public License 2.0. The upstream license text is included at
`internal/webui/static/novnc/LICENSE.txt`.

Upstream: <https://github.com/novnc/noVNC>

## Protocol definitions

`design/third_party` contains vendored protobuf definitions and license texts
for Google APIs and grpc-gateway. See the license files in that directory.

## Go dependencies

Go module dependencies are listed in `go.mod` and `go.sum`; their licenses are
provided by their respective modules. Run `go list -m all` to inspect the
dependency set.
