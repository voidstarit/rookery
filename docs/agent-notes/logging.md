# Logging notes

Structured logging conventions for both binaries.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Logging

Both binaries log through `zerolog` via `internal/logging.Setup`. `-log-level` (`ROOKERY_LOG_LEVEL`,
default `info`) and `-log-format` (`ROOKERY_LOG_FORMAT`, `console` or `json`) configure the global
logger, and every line is tagged `service=rookery` or `service=vnfs`. Components hold a
`zerolog.Logger` value (tests use `zerolog.Nop()`); package-level events in `internal/host` and
`internal/api` use `github.com/rs/zerolog/log`. Use typed builders (`.Str`, `.Int64`, `.Uint32`,
`.Bool`, `.Dur`, `.Err`) rather than pre-formatting values into strings. At trace level,
`internal/vnfd` logs every daemon RPC (via `TraceInterceptor`), client availability changes,
supervision decisions, DHCPv4/v6 request/reply details, DNS queries/records/forwarding results, and
BGP route installs/withdrawals. Lease allocation and release log at info; renewals at debug.
