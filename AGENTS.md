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

The web reader has a browser check (`TestReaderOpensInARealBrowser`)
that drives Chromium over CDP. It skips when no Chromium is found,
including in CI. On a desktop run, set `LISEUR_CHROME=/path/to/chrome`
to opt in explicitly; CI likewise runs it only when that variable is set.
Set `LISEUR_READER_SCREENSHOT=/tmp/reader.png` to keep what it saw. Run it
after touching `static/reader*.js`, `reader.templ` or the reader's CSP:
a `srcdoc` document inherits the framing page's policy, so a page CSP
that is slightly too strict renders a blank frame with no error
anywhere.

The README's screenshots are made by `make screenshots`
(`scripts/screenshots.sh`): it fetches real books from Standard Ebooks,
seeds a throwaway server with them and photographs four pages through
the same CDP walk the UI tests use
(`internal/webui/testdata/uishots.mjs`). It needs the network, a
browser, node and jq, so it is never part of a test run. Retake them
when a page in a shot changes shape.

## Architecture in one paragraph

One binary, two subcommands (`serve`, `admin`). Storage goes through the
`store.Store` interface with SQLite (`modernc.org/sqlite`) and Postgres
(`pgx`) backends; per-user monotonic `ops.seq` comes from
`seq_counters`. The native API (`internal/api`) is the only
full-fidelity surface; legacy protocols are edge adapters
(`internal/adapter/kosync`, `internal/adapter/koplugin`) that write
native records only. The web UI (`internal/webui`) is templ + vendored
htmx, no CDN, no build pipeline beyond `go tool templ generate`.

Books come from **watched folders**
([ADR-0017](docs/adr/0017-folders-not-pipelines.md)). A folder is a row
(`id, name, root_path, kind`); `internal/content` walks it, reads
metadata and calls one store method, `ReconcileFolder`, which is the
single write path into the catalog — including for an upload. A folder
an administrator marked `accepts_uploads` can be written into by
`POST /v1/folders/{folder}/books`
([ADR-0023](docs/adr/0023-uploads-land-in-a-folder.md)), and that route
creates a file (or, in a Calibre folder, a Calibre book) and then runs
a pass. It never touches a catalog table itself, so an uploaded book
and a book somebody copied in by hand are the same kind of thing. (It
does write one non-catalog row: the uploader's `user_book_work` link, so
a book whose position was already syncing does not appear twice.)
`DELETE /v1/books/{id}` is its counterpart
([ADR-0025](docs/adr/0025-deleting-a-book.md)), bounded by the same
flag: the file goes and then the row, and only in a folder that accepts
uploads. One watcher goroutine
(`internal/content/watch_linux.go`) triggers a pass at startup, on a
debounced fsnotify event, and on a slow safety timer. There is no ingest
job, no content-addressed store, no quota, no trash and no review queue:
an upload is a file written into a folder and nothing more, and the
only other directory the server writes to is the cover cache.

The API contract is [docs/openapi.yaml](docs/openapi.yaml) — update it
in the same commit as any route/shape change, and keep
[docs/integrating.md](docs/integrating.md) consistent with it.

The store test suite is shared across backends in
`internal/store/storetest`. PostgreSQL tests run when
`LISEUR_PG_TEST_DSN` is set (a dev database lives on civuole; DSN is in
the gitignored `.env`).

This project has **no released version**, so a page, route or shape may
be replaced outright: no deprecation shims, no compatibility redirects,
no migration UI. It does have users running published images, and their
databases are real. Issue #13 is what that distinction costs when it is
forgotten: migration 4 was correct and emptied working libraries. So a
shipped migration is never edited, an upgrade path across published
images is tested, and a migration that changes what an existing
deployment can see says so in `docs/deployment.md` before it ships.
(The compatibility that *does* matter is with other people's software:
the kosync and koplugin wire protocols, and the EPUB and OPDS formats.
Those are not ours to change.)

## Rules that must not be broken

These come straight from the design and its review; tests enforce most
of them.

- **Every route is authenticated** except `/healthz`, `/v1/login`,
  `/v1/register` (invite), and the adapter pairing endpoints. Add new
  routes to the scope table in `internal/api/routes.go`.
- **Reading state is scoped by `user_id`; catalog access is scoped by folder
  grants.** Ops, sessions, insights, devices and `user_book_works` are
  per-user and must never be readable across users. Books, folders and
  catalog entities remain shared rows, but a real viewer ID sees only books
  supported by an explicit `user_folders` grant; administrator status never
  implies a grant. Creating a folder writes exactly one grant, for the
  account named as its grantee, in the same transaction (ADR-0029); a
  route that creates a folder and grants nobody is issue #13 again. Empty viewer IDs are reserved for reconciliation, watchers
  and trusted administration. **Series,
  contributors and tags are library-wide, not folder-scoped**
  (ADR-0019): they are keyed by normalized name alone, one series held
  in two folders is one row, and an entity nothing names any more is
  garbage-collected — unless a reader's claim still names it. The few global
  lookups outside the catalog — token/auth hashes, `UserIDs`,
  `ListUsers` — exist for auth and background jobs and are documented as
  such. The one bounded exception is **series claims** (ADR-0018):
  `book_series_overrides` and `book_series_override_items` are keyed by
  `scope_user`, so a series membership resolves per reader — personal,
  else shared, else what the folder said. It is bounded on purpose:
  claims change only which series a book is in and where, never whether
  a book, folder or entity exists. A personal claim must never be
  readable by another user, and only an admin writes the shared layer.
  Every store read that yields a series therefore takes a `userID`.
  **A series rename (ADR-0020) is a second layer of the same shape**, in
  `series_name_overrides`, and it carries one hard rule: a rename never
  touches `series.normalized_name`. That column is the fold key a pass
  resolves an observed name against, so moving it would make the next
  scan undo the rename and split the shelf. Renaming onto a name already
  visible in that scope is `ErrConflict`, never a merge.
- **An observed name resolves through `series_bindings` first**
  (ADR-0021): this folder's binding, then the global one, then
  `series.normalized_name`. That table is what a merge and a split
  write, and it is why neither is a plain edit — a shelf rearranged only
  in the database is rearranged back by the next pass that observes the
  old name. **A merge is never a plain delete**: it repoints
  memberships, claims and any bindings that named the absorbed series,
  binds the absorbed name to the survivor, and only then drops the row.
  It never renumbers positions and never touches reading state. Merge
  and split are admin-written and shared, always: only a shared decision
  could ever be written back to a library, and a per-reader version
  would put a redirect in every series-bearing read.
- **`root_path` never reaches a non-admin.** It is a filesystem oracle;
  a `library-read` response names books, not paths.
- **The server writes under a watched folder only where an
  administrator asked it to.** Everywhere else: rooted, read-only opens;
  symlinks refused; no temp files, no cover extracted beside the book,
  no `metadata.db` writes. In a folder marked `accepts_uploads` an
  upload creates a file — and in a Calibre folder a book directory and a
  `metadata.db` row
  ([ADR-0023](docs/adr/0023-uploads-land-in-a-folder.md)) — and a delete
  takes one back out
  ([ADR-0025](docs/adr/0025-deleting-a-book.md)). It still never
  modifies or renames anything, and the delete refuses a file that has
  changed since the last pass: there is no trash behind it, so it
  deletes the book the catalog described or nothing.
- **A pass that did not fully succeed, or that observed nothing, never
  marks anything missing, and never purges.** Both rules are enforced by
  `ReconcileFolder`'s signature rather than by care.
- **A plain folder is keyed by relative path; a Calibre folder is keyed
  by `calibre_id`, never by path.** Calibre rewrites a book's directory
  on every title or author edit, and path-keying would lose the reading
  position each time. Calibre metadata is re-read on every pass.
- **In a Calibre folder, `metadata.db` is authoritative** (ADR-0022): a
  book a complete, non-empty pass did not find there is *deleted*, not
  flagged missing, and every non-pending work with no book mapping, ops,
  sessions or rollups is collected after the pass. A plain folder still
  keeps a missing book, because there absence is only evidence about a
  disk. A book Calibre still lists but whose file this server cannot
  serve is observed as `Unservable`: marked missing, kept, exempt from
  the purge, and it never makes the pass incomplete. A Calibre book
  whose bytes changed keeps its readers — the new `sha256` and `ta`
  fingerprint are registered *additively* against the work already
  mapped to it, and a value another work already claims is left alone,
  never merged by a scan.
- **The op log and sessions are append-only within their retention
  windows.** Same id with a different payload is a conflict, never an
  overwrite. Aged immutable sessions become daily rollup totals plus
  compact idempotency tombstones; koplugin revisions remain raw and
  become new rows in `session_supersessions`, never updates. The one
  exception is a reader deleting a whole work (ADR-0024): the unit is
  the entire work graph, never one op or one session, and only a work no
  catalog book maps any more — a missing book keeps its reading, because
  absence is evidence about a disk. An admin may delete a *missing*
  catalog book (never an active one, never in a Calibre folder), which
  leaves each reader's work behind as theirs to remove.
- **`seq` is never renumbered**, including by compaction. Heads (newest
  op per work+device) are never compacted.
- **Adapters write native records only.** No legacy wire shape is stored
  (`foreign_pos` is stored verbatim but is a payload, not a shape).
- **Never fabricate page numbers.** Statistics come from progression
  fractions; pages derive from edition page counts when known.
- **No redirects on API routes.** `301`s exist only under `/ui`.
- **Content change is not identity transfer.** A file whose bytes
  changed at a path is a new catalog book: delete the old row and its
  cascade, insert the new one, one transaction, enforced by
  `UNIQUE (folder_id, relative_path)`.
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
  backend's `schema.go` — never edit a shipped migration, including the
  baseline at index 0 (`TestBaselineIsFrozen` pins its hash). ADR-0017
  squashed 22 migrations into that baseline before anything was
  published; that was a one-off and not a precedent. A migration that
  changes what an existing deployment can read is a decision, not a
  detail: state it in an ADR and warn about it in `docs/deployment.md`
  (ADR-0029, migration 6).
- Web UI mutations require the per-session CSRF token.
- Error responses are JSON `{"error": "..."}` with a precise 4xx;
  malformed input never produces a 5xx.

## Testing expectations

- Property test `TestConcurrentAppendGapFreeSeq` must keep passing
  (gap-free per-user seq under concurrent pushes).
- Named regression tests for legacy bugs: falsy-zero percentage,
  xpointer round-tripping, open-route access.
- The reconcile suite in `internal/store/storetest` covers idempotency,
  incomplete and zero-observation passes, replacement without inherited
  work mappings, and a Calibre book whose directory moved. Extend it
  rather than testing a pass through the API.
- Tenant isolation and scope matrix tests live in `internal/api` and
  `internal/webui`; extend them when adding routes.
- Adapter changes must not break the conformance tests in
  `internal/adapter/*/`.
- **A test binary hashes passwords at a reduced argon2id cost.**
  `internal/auth` selects its cost with `testing.Testing()`: 64 MiB /
  t=3 / p=2 in a real build, 8 MiB / t=1 / p=1 under `go test`. The
  parameters travel inside every encoded hash, so a password an existing
  deployment stored still verifies at the cost it was written with, and
  nothing about production changes. `TestProductionPasswordParamsPinned`
  holds the production baseline in place. Use `auth.HashPassword` in
  fixtures rather than a hardcoded encoding, so the saving applies.
