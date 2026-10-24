# Vendored proto dependencies

These files are vendored so `protoc` generation does not depend on a schema
registry or network access.

## google/api

Source: https://github.com/googleapis/googleapis
Commit: 9415ba048aa587b1b2df2b96fc00aa009c831597
Files:
- google/api/annotations.proto
- google/api/http.proto

License: Apache License 2.0.

## validate

Source: https://github.com/envoyproxy/protoc-gen-validate
Version: v1.3.3
File: validate/validate.proto

License: Apache License 2.0.

Google protobuf well-known types are provided by the local protoc installation
under `PROTO_INCLUDE` (default `/usr/include`).

## protoc-gen-openapiv2 options

Source: https://github.com/grpc-ecosystem/grpc-gateway
Version: v2.28.0
Files:
- protoc-gen-openapiv2/options/annotations.proto
- protoc-gen-openapiv2/options/openapiv2.proto

License: BSD-3-Clause.
