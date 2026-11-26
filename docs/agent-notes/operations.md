# Operations notes

Readiness, PKI/mTLS, local-listener security, and rate limits.

Part of the Rookery agent notes; the index and working rules are in
[`AGENTS.md`](../../AGENTS.md).

## Readiness

`serve` must start even when prerequisites (or the VNF daemon) are missing. It reports state via
`GET /api/status`, blocks default-backend disk creation when the bootstrap thin pool is not ready,
and blocks `POST /api/vms` until VPP and at least one unattached disk are ready; the thin pool is
required only when a selected disk is LVM-backed, so a VM using only host passthrough disks can
start while the thin pool is missing. Additional storage backends are validated by their driver when
a disk is created. Media registration has no readiness gate. Do not turn readiness into a startup
failure; QEMU/KVM availability is checked when a VM is created or started.

## mTLS listener and PKI modes

Public mTLS API: `serve -mtls-addr` (default `:8443`, `ROOKERY_MTLS_ADDR`; empty disables) starts a
second HTTPS listener using `internal/pki`. It serves the grpc-gateway handler plus the
token-authenticated LightVM console WebSocket (`/ws/lightvm-console/{token}`) and requires a client
certificate signed by the server CA (`tls.RequireAndVerifyClientCert`, TLS 1.3, session tickets
disabled). The CA/server certificate live under `-pki-dir` (`ROOKERY_PKI_DIR`, default
`/var/lib/rookery/pki`); in the default `managed` mode the server certificate SANs include
localhost, hostname, host IPs, and `-mtls-sans`/`ROOKERY_MTLS_SANS`. Set `-pki-mode existing` (env
`ROOKERY_PKI_MODE`) to load provisioned `ca.pem`/`ca-key.pem`/`server.pem`/`server-key.pem` without
writing or renewing them; `-pki-ca-cert`, `-pki-ca-key`, `-pki-server-cert`, and `-pki-server-key`
override the individual paths, the CA key is optional for serving but required to issue clients, and
the server certificate must chain to the CA and be valid for server authentication. Set
`-pki-client-dir` (env `ROOKERY_PKI_CLIENT_DIR`, YAML `api.pki_client_dir`) to accept only client
certificates whose public PEM is in that directory; it is scanned recursively for
`.crt`/`.pem`/`.cer`, ignores CA certs, still verifies the chain and revocation, writes newly issued
public certs there, and reloads when the directory mtime changes. Empty keeps the historical CA-wide
client trust.

## Client certificates and the allowlist

Client certificates are issued by the native-only endpoints `GET /api/pki/ca`, `GET/POST
/api/pki/clients`, and `DELETE /api/pki/clients/{id}`; these are routed to the native handler by
`CutoverHandler` and are **not** exposed on the public mTLS listener (which serves the gateway
handler plus the token-authenticated console WebSocket). Issued private keys are returned once and
not stored; metadata and revocation live in `client_certificates` (`0025_client_certificates.sql`),
and `VerifyPeerCertificate` rejects revoked serials. The UI sidebar **Client certificates** item
issues, downloads, lists, and revokes them. Administrators can issue the same certificates from the
host with `rookery create-client-cert <name>` (alias `issue-client-cert`), using the server's `-db`
and `-pki-dir`; it writes `<name>.crt`, `<name>.key`, and `<name>-ca.crt`. `pki.NewClientIssuer`
loads an existing CA only and must never create a new one.

## Local listener security

The local HTTP API has no authentication or authorization and binds to `127.0.0.1:8080` by default;
do not expose it without an access-controlled proxy. The public mTLS listener is the authenticated
API entry point.

## Rate limits

`serve` applies a per-path token-bucket rate limit to every request on both HTTP listeners
(`-rate-limit` / `ROOKERY_RATE_LIMIT`, default 3 RPS per URL path, `0` disables); excess requests
get `429` with `Retry-After: 1`.
