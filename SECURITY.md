# Security policy

Please do not report security vulnerabilities in public issues.

Use GitHub's **Security → Report a vulnerability** flow to open a private
security advisory for this repository. Include:

- The affected Rookery version or commit
- A description of the issue and its impact
- Reproduction steps or a minimal proof of concept
- Any relevant logs, configuration, or environment details
- Whether a fix or mitigation is already known

The latest release is supported. Older releases may receive fixes at the
maintainer's discretion.

Rookery's local HTTP API has no built-in authentication. Do not expose it
directly to an untrusted network; use the mTLS listener or an authenticated
reverse proxy for remote access.
