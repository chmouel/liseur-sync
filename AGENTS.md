# AGENTS.md

Guide for AI coding agents working in this repository.

## What this is

liseur-sync is a self-hostable reading-position sync and
reading-statistics server in Go, companion to
[Liseur](https://github.com/chmouel/liseur). Read
[docs/DESIGN.md](docs/DESIGN.md) before changing behavior; the design
doc is the authority on protocol semantics, and the implementation plan
(how each milestone maps to code) is in that document's milestones
section.

## Build, test, verify

```
go build ./...          # build everything
go vet ./...            # vet
golangci-lint run ./... # lint (or `make lint` for all linters including openapi)
go test -race ./...     # full suite, always run with -race
go tool templ generate ./internal/webui/    # regenerate after editing .templ files
```

CI (`.github/workflows/`): `test.yaml` runs vet + the full race-enabled
suite on PRs and pushes, against SQLite and a Postgres 17 service
container, checks templ output is committed-fresh, and lints
`docs/openapi.yaml` with redocly. `build.yaml` builds static
linux/amd64+arm64 binaries and a multi-arch image on push, with a
container smoke test.

The store test suite runs against SQLite by default. Set
`LISEUR_PG_TEST_DSN=postgres://...` to also exercise the PostgreSQL
backend (throwaway database).

## Architecture in one paragraph

One binary, two subcommands (`serve`, `admin`). Storage goes through the
`store.Store` interface with SQLite (`modernc.org/sqlite`) and Postgres
(`pgx`) backends; per-user monotonic `ops.seq` comes from
`seq_counters`. The native API (`internal/api`) is the only
full-fidelity surface; legacy protocols are edge adapters
(`internal/adapter/kosync`, `internal/adapter/koplugin`) that write
native records only. The web UI (`internal/webui`) is templ + vendored
htmx, no CDN, no build pipeline beyond `go tool templ generate`.

The API contract is [docs/openapi.yaml](docs/openapi.yaml) — update it
in the same commit as any route/shape change, and keep
[docs/integrating.md](docs/integrating.md) consistent with it.

The store test suite is shared across backends in
`internal/store/storetest`. PostgreSQL tests run when
`LISEUR_PG_TEST_DSN` is set (a dev database lives on civuole; DSN is in
the gitignored `.env`).

## Rules that must not be broken

These come straight from the design and its review; tests enforce most
of them.

- **Every route is authenticated** except `/healthz`, `/v1/login`,
  `/v1/register` (invite), and the adapter pairing endpoints. Add new
  routes to the scope table in `internal/api/routes.go`.
- **All queries are scoped by `user_id`.** Never write a store method
  that can read across users (the few global lookups — token/auth
  hashes, `UserIDs`, `ListUsers` — exist for auth and background jobs
  and are documented as such).
- **The op log and sessions are append-only within their retention
  windows.** Same id with a different payload is a conflict, never an
  overwrite. Aged immutable sessions become daily rollup totals plus
  compact idempotency tombstones; koplugin revisions remain raw and
  become new rows in `session_supersessions`, never updates.
- **`seq` is never renumbered**, including by compaction. Heads (newest
  op per work+device) are never compacted.
- **Adapters write native records only.** No legacy wire shape is stored
  (`foreign_pos` is stored verbatim but is a payload, not a shape).
- **Never fabricate page numbers.** Statistics come from progression
  fractions; pages derive from edition page counts when known.
- **No redirects on API routes.** `301`s exist only under `/ui`.
- **Secrets are stored hashed** (SHA-256) and shown to the user exactly
  once. Passwords are argon2id. kosync's MD5-derived key is a pairing
  credential bound to one device slot, never the account password.
- **Credential traffic requires HTTPS** unless `insecure_http: true`;
  `X-Forwarded-Proto` is trusted only from `trusted_proxies` CIDRs.

## Conventions

- Validation lives at the handler edge; the store trusts its inputs but
  enforces invariants with composite FKs (SQLite: `PRAGMA
  foreign_keys=ON`; both backends: deferrable FKs for split/merge).
- Timestamps are UTC; SQLite encodes them as RFC3339Nano text.
- New migrations are appended to the `migrations` slice in each
  backend's `schema.go` — never edit a shipped migration.
- Web UI mutations require the per-session CSRF token.
- Error responses are JSON `{"error": "..."}` with a precise 4xx;
  malformed input never produces a 5xx.

## Testing expectations

- Property test `TestConcurrentAppendGapFreeSeq` must keep passing
  (gap-free per-user seq under concurrent pushes).
- Named regression tests for legacy bugs: falsy-zero percentage,
  xpointer round-tripping, open-route access.
- Tenant isolation and scope matrix tests live in `internal/api` and
  `internal/webui`; extend them when adding routes.
- Adapter changes must not break the conformance tests in
  `internal/adapter/*/`.
