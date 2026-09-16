# Security Policy

## Scope

netchaos is a dev-time test dependency: an in-process Go library that simulates
`net.Conn`/`net.Listener` for use inside `go test`. It is not designed to run in
production and does not accept network traffic from untrusted sources. The
relevant threat model is narrow — mainly:

- A bug in the fault-injection logic that silently masks a real bug in code
  under test (a correctness issue, reported as a regular issue rather than a
  security one, unless it has a security implication).
- Supply-chain concerns in the module itself (e.g. a compromised dependency
  or release).

## Supported versions

The API is stable but not frozen until `v1.0.0` (see [docs/07 — Contributing](docs/07-contributing.md)), so there is no formal support matrix yet: the latest tag and `main` are both in scope.

## Reporting a vulnerability

Please report security issues using GitHub's private vulnerability reporting:
[Report a vulnerability](https://github.com/jpgomesr/netchaos/security/advisories/new)
(Security tab → "Report a vulnerability"). Please do not open a public issue
for security reports.
