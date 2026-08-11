# Security policy

liseur-sync stores personal reading data and holds credentials for
every paired device, so security reports are welcome and taken
seriously.

## Reporting a vulnerability

**Do not open a public issue for a security problem.**

Report privately through GitHub's
[private vulnerability reporting](https://github.com/chmouel/liseur-sync/security/advisories/new).
If that is unavailable, email <chmouel@chmouel.com> with `liseur-sync
security` in the subject.

Useful reports include:

- affected version or commit, and the deployment shape (SQLite or
  PostgreSQL, direct TLS or behind a reverse proxy, which adapters are
  enabled)
- the relevant bits of the config, with secrets removed
- a request sequence or short script that reproduces the issue
- what an attacker gains

This is a spare-time project by one person. Expect an acknowledgement
within a week and a fix or an explanation of why it is not one within
30 days for anything exploitable. Please hold public disclosure until a
fix ships or 90 days pass, whichever comes first. Reporters are
credited in the advisory unless they ask not to be.

## Supported versions

Only the latest release and `main` are supported. Fixes land on `main`
and go out in the next release; there are no backports to older tags.

## In scope

- authentication and authorisation bypasses on any route
- **cross-user data access** — every query is meant to be scoped by
  `user_id`; anything that reads or writes another account's works,
  ops, sessions, or devices is a serious bug
- privilege escalation between the `sync`, `read-insights`, and `admin`
  scopes
- SQL injection, XSS in the web UI, CSRF on `/ui` mutations, SSRF, path
  traversal
- leaking credentials into logs or error responses (device tokens,
  kosync pairing keys, koplugin capability URLs, invite codes)
- weaknesses in credential handling: password hashing, secret
  generation, non-constant-time comparison of credentials
- bypassing the HTTPS requirement on credential-bearing routes, or
  making the server trust `X-Forwarded-Proto` from a peer outside
  `trusted_proxies`
- flaws in the legacy adapters (`kosync`, `koplugin`) that reach beyond
  the single device slot the credential is bound to

## Out of scope

- anything that requires `insecure_http = true`, which exists for
  LAN-only setups and disables the transport checks by design
- the kosync protocol's MD5-derived key being weak in itself. This is
  forced by stock KOReader and is contained deliberately: it is a
  pairing credential bound to one revocable device slot, never the
  account password (see
  [docs/DESIGN.md](docs/DESIGN.md) §8.3)
- missing rate limits with no demonstrated impact, volumetric denial of
  service, and load-generated resource exhaustion
- missing security headers with no exploitable consequence
- vulnerabilities in a dependency that liseur-sync does not actually
  reach — report those upstream, though a pointer here is appreciated
- findings from an automated scanner pasted without a working
  reproduction

## Hardening a deployment

- terminate TLS in front of the server and leave `insecure_http =
  false`. Set `trusted_proxies` to the proxy's address so
  `X-Forwarded-Proto` is honoured only from it.
- leave registration invite-only; hand out invite codes instead of
  sharing an account.
- give each device its own token so a lost e-reader costs one
  revocation, and enable only the adapters you use.
- keep backups of the database — it holds the only copy of the op log.

See [docs/deployment.md](docs/deployment.md) for the details.

## How the project defends itself

Documented in [docs/DESIGN.md](docs/DESIGN.md) §8, and enforced by
tests:

- passwords are argon2id; tokens, pairing keys, and capability secrets
  are random 256-bit values stored as SHA-256 hashes and shown exactly
  once
- every route is authenticated except `/healthz`, `/v1/login`,
  `/v1/register`, and the adapter pairing endpoints
- credential-bearing routes refuse plain HTTP unless `insecure_http` is
  set; web UI mutations require a per-session CSRF token
- tenant isolation and the route/scope matrix have dedicated tests; the
  CI suite runs `go vet` and the full race-enabled tests against both
  SQLite and PostgreSQL on every pull request
