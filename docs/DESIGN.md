# liseur-sync: Design

**Status:** living design; sync core and watched-folder catalog are the
current architecture. Folder-based content is the accepted model in
[ADR-0017](adr/0017-folders-not-pipelines.md).

**Audience:** contributors to liseur-sync and client authors (Liseur
first, third parties later) **Purpose:** define the data model,
protocol, and architecture of a self-hostable server that syncs reading
positions, collects reading statistics, and reflects EPUB folders for
reader apps, including stock KOReader devices, without repeating the
mistakes of the servers that came before it.

---

## 1. Why another sync server

Liseur (an Android EPUB reader) already syncs positions against
calibre-web, Komga, and kosync-compatible servers. Building the
kosync client made the protocol's limits concrete. Together with
KoInsight (the only FOSS server with a reading-statistics write
API), the existing options define the problem by their flaws:

| Existing behaviour | Consequence |
|---|---|
| kosync's credential is the hex MD5 of the password, stored verbatim server-side | Password-equivalent secret on every request; no revocation, no per-device tokens |
| One `GET /syncs/progress/:document` per book, no bulk or delta endpoint | A shelf sweep costs one round trip per book; clients ration syncing |
| Positions are CRe xpointers, resolvable only by KOReader's rendering engine | Other clients can carry the string but not produce one; cross-app push is lossy |
| Book identity is `partialMD5` over roughly 12 KiB of sampled bytes | Collides by design; breaks whenever Calibre re-encodes on export |
| Last-write-wins on a single stored position | No history; a mis-tap on one device silently destroys the position everywhere |
| KoInsight authenticates only its kosync routes; stats and destructive routes are open, with `cors({origin:'*'})` | Unusable on the open internet without a reverse proxy bolted on |
| KoInsight stats rows require `page`/`total_pages` and silently drop rows where `total_pages <= 0` | Clients that track progression fractions, not pages, cannot report honestly |

liseur-sync exists to be the server those clients deserved: **positions
as an ordered, per-work operation log; sessions as append-only facts;
identity that survives re-encoding; auth that can be revoked; a kosync
adapter so stock KOReader devices participate without modification; and
a catalog that appears when an operator points at the folders they
already have.**

### 1.2 Non-goals

- **Formats other than EPUB.** PDF, comics, audiobooks, and fixed-layout
  formats require separate format-specific decisions.
- **Not a merge authority.** The server stores and orders operations; it
  never decides which position wins. Conflict resolution is a client
  concern (§5.4).
- **No proprietary dependencies.** Same constraint as Liseur: the server
  and any client code must be FOSS-compatible.
- **No E2E encryption in v1.** Positions and sessions are visible to the
  server; that is what enables server-side insights. An E2E mode with
  on-device-only insights is acknowledged as future work (§10), and the
  schema keeps payloads opaque enough not to preclude it.
- **No server custody of book files.** The catalog reflects watched
  folders. The server reads books where they are and never takes over the
  directory that holds them.

## 2. Goals

1. **Correct multi-device position sync** for the same user across
   Liseur instances, KOReader devices, and future clients, with
   history, not last-write-wins.
2. **Reading statistics done honestly**: sessions carry progression
   fractions measured at the time of reading; the server derives pages
   and aggregates. Never fabricate page numbers.
3. **Work identity that survives format shuffling**: the same book
   recognised across a Komga original, a Calibre re-encode, and the
   copy on a Kindle-jailbreak KOReader.
4. **First-class self-hosting**: one static Go binary, SQLite by default
   or PostgreSQL when desired, a disposable cover cache, and folders the
   operator already backs up. TLS is provided by a reverse proxy.
5. **Legacy interop as adapters**: stock KOReader syncs via a
   kosync-compatible endpoint; its statistics plugin sends data via a
   KoInsight-compatible endpoint. Neither contaminates the native model.
6. **Multi-user** from day one: a family or small community on one
   instance, reading state strictly scoped per user.
7. **First-class EPUB catalog**: every logged-in user can browse the
   watched folders; only administrators manage them, because that is the
   only place filesystem paths appear.
8. **Broad reader integration**: a native catalog API for Liseur clients,
   OPDS 1.2 for existing readers, and an isolated web reader using the
   same sync protocol.

## 3. Architecture overview

```
                    ┌──────────────────────────────────────────────┐
                    │                 liseur-sync                  │
                    │                     (Go)                     │
 Liseur (native) ──▶│  /v1/*              native sync/catalog API │
 OPDS readers ─────▶│  /opds/v1.2/*        catalog adapter        │
                    │                                              │
 KOReader kosync ──▶│  /adapter/kosync/*   translation layer      │
 KOReader stats  ──▶│  /adapter/koplugin/* translation layer      │
                    │                                              │
 Browser ──────────▶│  /ui/*               management + reader    │
                    │  watcher             reconcile folders      │
                    │                                              │
                    │  DB: SQLite or PostgreSQL                    │
                    │  files: watched roots + disposable covers    │
                    └──────────────────────────────────────────────┘
```

- **One process and one binary.** SQLite in WAL mode is the default for
  small self-hosted installations; PostgreSQL is supported for
  deployments that already operate it. The server's own writable content
  directory is the cover cache, and everything in it can be recreated.
- **Folders are the catalog source.** A folder is a row: `id`, `name`,
  `root_path`, and `kind` (`plain` or `calibre`). It has no owner, no
  access list, and no lease. Every logged-in account
  sees every folder's books; only administrators see or change folders.
- **Adapters translate at the edge.** The kosync and koplugin
  endpoints parse the legacy wire formats and write native records
  tagged with their origin. Nothing downstream knows or cares which
  protocol a record arrived on.
- **The native API is the only full-fidelity surface.** Adapters are
  deliberately lossy where the legacy protocol is, and the design never
  bends the native model to fit them.
- **Catalog storage and sync state have separate ownership.** Books
  and library-wide entities are shared. Works, operations, sessions and
  the `user_book_works` bridge remain per-user.
- **Reconciliation is stateless.** A pass enumerates a folder, compares
  it with the `books` rows, reads metadata for new or changed files, and
  upserts. Books not
  observed by a complete, non-empty pass are marked `missing` and kept;
  except in a Calibre folder, where the pass reads a curated catalog and
  a book absent from `metadata.db` is deleted (ADR-0022).

## 4. Identity: works, editions, aliases

The hardest problem, so it is solved structurally rather than by hashing
harder.

### 4.1 Three layers

- **Work**: the abstract book. Positions and statistics attach here.
  Server-assigned UUID, scoped to one user.
- **Edition**: one concrete file, a specific EPUB with a specific
  SHA-256. Editions belong to a work. Carries page-map metadata when a
  client knows it.
- **Alias**: an identifier observed in the wild that maps to a work:
  - `sha256:<hex>`: exact bytes, also the edition key
  - `partial-md5:<hex>`: KOReader's fingerprint, registered so kosync
    adapter traffic resolves
  - `source:<catalog-id>`: the catalog server's own id for the book
  - `dc:<urn>`: the EPUB's `dc:identifier` (ISBN, UUID, Calibre ID)
  - `ta:<fingerprint>`: normalised title+author hash, the fuzzy fallback

### 4.2 Resolution

A client asks `POST /v1/works/resolve` with every identifier it has for
a file. The server answers with the matching `work_id`, creating work,
edition and aliases as needed. Resolution order is `sha256` -> `partial-md5` -> `source` -> `dc` ->
`ta`, but every supplied alias that already matches must agree on one
work. Aliases spanning multiple works return 409 without mutation. After
one high-confidence match, supplied identifiers are registered as aliases
of that work, which is how the graph converges over time. A `ta:`-only
match stays low-confidence and does not promote stronger aliases until
the reader confirms it.

Lookup, work/edition creation, alias registration and any catalog
mapping are one store transaction. A uniqueness race is re-evaluated and
returns success on the one agreed work or 409; it never silently ignores
a conflicting alias or turns an identity conflict into a 500.

Fuzzy (`ta:`) matches are marked `confidence: low` in the response, and
clients may present a "same book?" confirmation before merging. Wrong
merges are repairable: `POST /v1/works/{id}/split` detaches an edition
and its records into a new work. The operation log makes this tractable
because every record knows its edition.

**Why not hash-only:** the Liseur kosync work established that Calibre
re-encodes on export, so byte identity fails exactly when a user mixes a
Calibre collection with anything else. **Why not metadata-only:** two
editions of the same ISBN can have different pagination, and anthologies
share titles. The layered scheme lets exact identity carry when it can
and metadata rescue it when it cannot.

### 4.3 Catalog identity

Catalog `book_id`s are stable folder identities. A
`user_book_works(user_id, book_id, work_id)` row maps a book into each
reader's existing work graph. Catalog browse and download do not mutate
sync identity. Before syncing or reporting sessions, a client with both
`library-read` and `sync` explicitly runs the normal alias resolver in
that user's namespace. Resolution, alias promotion and mapping insertion
commit in one per-user graph transaction, which also records the stable
`liseur-sync:<book_id>` source alias.

The same shared book can therefore have different work IDs for different
users without leaking either graph.

## 5. Position sync

### 5.1 The record

Positions are an **append-only operation log per work per user**:

```json
{
  "op_id":        "client-generated deterministic opaque id",
  "work_id":      "…",
  "edition_sha":  "sha256:…",
  "device_id":    "…",
  "client_ts":    "2026-08-10T12:58:04Z",
  "progression":  0.4137,
  "locator":      { "…Readium Locator JSON, opaque to server…" },
  "foreign_pos":  "/body/DocFragment[11]/body/div/p[3]/text()[1].0",
  "origin":       "native | kosync"
}
```

- `progression` (0..1) is the lingua franca; every client can produce
  and consume it. It is the only field the server itself reads.
- `locator` is Liseur's exact position, opaque to the server, replayed
  verbatim to other Liseur devices.
- `foreign_pos` carries a position
  string another engine owns. Stored verbatim, replayed verbatim to
  kosync pulls, never parsed.
- `op_id` is a client-generated opaque
  deterministic string. The same local fact must produce the same ID and
  payload on retry.

The server assigns each accepted op a **per-user monotonic `seq`**.

Append-only means history is never edited: no later write rewrites a
position that was reported or a session that happened. It does not mean
a reader cannot ask this server to forget a book. Deleting a work
removes its whole graph at once (ops, sessions, rollups, editions,
aliases and its book mapping) and only for a work no catalog book backs
any more ([ADR-0024](adr/0024-deleting-a-work.md)). There is no deleting
one op or one session, and no deletion record: a device still syncing
the book creates the work again.

### 5.2 Delta sync in one round trip

```
GET /v1/changes?since=<seq>&limit=500
```

returns every op accepted for this user after `seq`, plus the new
high-water mark. A whole-shelf sync is one request, the direct fix for
kosync's per-book polling. Pushing is symmetric: `POST /v1/ops` with a
batch; the response reports each op's assigned `seq` or that it was
already accepted.

### 5.3 History, not just heads

`GET /v1/works/{id}/positions?limit=50` returns recent ops. This enables
undo, session inference for legacy devices (§6.3), and trust: users can
see what each device claimed and when.

Retention: ops older than a configurable window (default 180 days) are
compacted to daily last-op-per-device snapshots.

### 5.4 Conflicts are resolved by clients

The server orders; it never merges. A client tracks the last `seq` it
reconciled and, on sync, performs the same three-way comparison Liseur
already implements in `domain/ReadingStateMerge.kt`: local position vs
newest remote op vs the recorded agreed baseline.

For non-interactive clients, the kosync adapter applies the only safe
default: `GET` returns the newest op's `foreign_pos`/`progression`,
which is exactly kosync's semantics today.

## 6. Reading statistics

### 6.1 Sessions are facts, not derived state

```json
{
  "session_id":        "client-generated deterministic opaque id",
  "work_id":           "…",
  "device_id":         "…",
  "started_at":        "…",
  "ended_at":          "…",
  "start_progression": 0.401,
  "end_progression":   0.413,
  "idle_ms":           30000,
  "origin":            "native | koplugin"
}
```

`POST /v1/sessions` accepts batches, idempotent on `session_id`. Rows
are never updated in place; a session is a historical fact. Immutable
native and inferred sessions older than the configurable retention
window are reduced to per-work, timezone-local daily totals, then their
raw rows are removed. A compact fingerprint preserves the original
idempotency and conflict behavior. Mutable koplugin supersession chains
stay raw because a later revision may replace them.

The design principle, learned from the Liseur/KoInsight investigation:
**never fabricate resolution you didn't measure.** liseur-sync requires
`start/end_progression` and derives everything else server-side:

- pages read = progression delta × edition page count, when known
- reading speed = progression delta / active duration, per work and
  rolling
- streaks, per-book time, calendar heatmaps: raw sessions plus
  daily rollups

### 6.2 Insight API

Read-only aggregation endpoints, all per-user:

```
GET /v1/insights/summary?range=30d      totals, streak, speed trend
GET /v1/insights/works                  all per-book aggregates
GET /v1/insights/works/{id}             per-book: time, sessions, pace, ETA
GET /v1/insights/calendar?year=2026     daily minutes for heatmaps
```

ETA = remaining progression / rolling speed, the same estimator Liseur
ships on-device, now computable across all devices' sessions combined.

### 6.3 Sessions inferred for position-only devices

A stock KOReader device syncing via the kosync adapter sends positions
but no sessions. The server can infer coarse sessions from its op log:
consecutive ops from one device with gaps below the configured threshold
form an inferred session with `origin: "inferred"`, always
distinguishable from measured ones and excluded from speed statistics by
default. Better than nothing, honest about being so. KOReader's
statistics plugin, where installed, reports measured sessions via the
koplugin adapter instead.

## 7. Legacy adapters

### 7.1 kosync (`/adapter/kosync/*`)

Implements the four calls KOReader's kosync plugin makes:

| kosync call | Translation |
|---|---|
| `POST /users/create` | Creates a device credential bound to an existing liseur-sync user via a valid, unexpired, single-use pairing code used as the kosync password. It never creates an account. |
| `GET /users/auth` | Validates the device credential. |
| `PUT /syncs/progress` | Resolves `document` (a partialMD5) via the alias table, then appends a native op with `origin: kosync`, `foreign_pos` = the xpointer, `progression` = percentage. |
| `GET /syncs/progress/:document` | Returns the newest op: `progress` = stored `foreign_pos` if the newest op has one, else the percentage string; `percentage`, `device`, `timestamp` filled natively. |

Unresolvable `document` digests create a pending work keyed on the
partialMD5 alias; when any client later resolves a file to that alias,
the history merges in. A KOReader-first book is not lost.

### 7.2 KOReader statistics plugin (`/adapter/koplugin/*`)

Accepts the KoInsight-shape request. Page-based rows are converted:
`progression = page / total_pages`, `origin: koplugin`. Rows the legacy
protocol would silently drop are rejected loudly with a 422 listing the
reason.

### 7.3 Adapter rules

- Adapters write native records; no legacy shape is ever stored.
- Adapters are feature-gated per instance and per user.
- Anything the legacy protocol cannot express is dropped at the edge,
  documented, never approximated silently.

## 8. Security and auth

Designed as the direct negation of the KoInsight findings.

### 8.1 Accounts and tokens

- Users authenticate with username + password, hashed with argon2id.
- Login yields nothing directly usable; it authorises creating
  per-device API tokens: random 256-bit, stored hashed (SHA-256), shown
  once, revocable individually, named by device.
- Tokens carry scope sets containing `sync`, `read-insights`,
  `library-read`, `library-manage`, or `admin`. `admin` implies all
  scopes. `library-manage` permits stating series claims (ADR-0018) and
  grants no catalog reads of its own. Existing singleton tokens remain
  wire-compatible, and explicit in-place scope updates preserve a token's
  device identity and secret. `admin` is never self-grantable.
- An account can be disabled rather than deleted. Every credential that
  resolves to a user checks the flag, so one write closes every door at
  once and enabling reopens exactly the same ones. The last enabled
  administrator can be neither demoted nor disabled.

### 8.2 Instance posture

- Every route is authenticated except the documented login, invite
  registration, health, and adapter-pairing routes. There is no
  anonymous catalog read; CORS is deny-by-default with an explicit
  origin allowlist.
- Registration is invite-only. An administrator can also create an account
  directly, and both account and invite creation in the panel re-verify the
  acting administrator's password before minting durable access.
- Per-token and per-IP rate limits protect auth endpoints; credential checks
  use constant-time compares.
- Reading state is scoped by `user_id` at
  the query layer. Catalog rows are deliberately shared across signed-in
  users, but positions, sessions, works, devices and `user_book_works`
  are not.
- `root_path` never reaches a non-admin response.

### 8.3 The kosync credential problem, contained

The kosync protocol forces a password-equivalent MD5 header; a stock
KOReader cannot do better. Containment:

- The kosync user/password pair is a dedicated pairing credential, never
  the account password.
- `POST /adapter/kosync/users/create` accepts only a one-time pairing code;
  supplying an account password creates neither an account nor a device.
- Compromise of that key exposes exactly one
  revocable device slot with `sync` scope, not the account.
- The kosync
  adapter refuses plain HTTP unless the instance is explicitly
  configured `insecure_http: true`.

### 8.4 Redirects and secrets

The server never redirects API routes. `301`s exist only on the web UI.

Credentials never reach a log. The koplugin adapter is the one place a
secret rides in the URL path, so any path the server logs goes through a
redactor first, and the deployment docs show the equivalent rule for the
proxy's access log.

### 8.5 Untrusted book content

EPUB files are untrusted ZIP and XML input. A reconcile pass applies
size, entry, decompression-ratio, path, symlink, XML, and
encryption-algorithm bounds before it trusts metadata. Covers are rasterized and
served with fixed MIME types and `nosniff`.

The browser reader unpacks publications in the page, not on the server.
No route serves publisher HTML, CSS or fonts as ordinary application
resources, and no script inside a book executes.

## 9. Folder catalog

### 9.1 The model

A folder is the only catalog root. It is a database row:

```
folders  id, name, root_path, kind, created_at, updated_at
books    id, folder_id, status, relative_path, calibre_id?, stat,
         content digest, filename, media type, metadata, timestamps
```

There is no owner on `folders`. Every logged-in account sees every
folder's books. Only administrators manage folders because adding one
names a path on the server. The admin panel and CLI share the same
rules: `add-folder <name> <root>`, `list-folders`, `remove-folder
<folder-id>` and `folder-uploads <folder-id> <on|off>`.

`root_path` is stored absolute and never appears in a non-admin API
response. A folder root is opened read-only; symlinks inside the tree
are refused. The server writes under a watched folder only where an
administrator set `accepts_uploads`: to create something that was not
there (ADR-0023), or to delete something it could have created
(ADR-0025). It never modifies or renames. The only unconditionally
writable directory in the content system is the cover cache.

There is one implementation of that write, reached two ways.
`api.ReceiveUpload` bounds the body, validates the EPUB, hashes it,
short-circuits on a digest the catalog already holds, and hands the
bytes to a pass. `POST /v1/folders/{folder}/books` calls it for an app
holding the `library-upload` scope; the library page's form calls it for
a signed-in browser, through the same kind of interface downloads and
covers already use. A browser has no scope, so its gate is the folder
flag plus the session, the decision about which folder may be written
to having already been made, once, by whoever marked it.

Deleting is the same shape in the other direction. `api.DeleteBook`
removes the file and then the row (that order, because a crash between
them leaves a book the next pass marks missing, a state the system
models, while the reverse leaves a file the next pass puts straight
back). `DELETE /v1/books/{id}` calls it for an app holding
`library-delete`; the book page's control calls it for a signed-in
administrator. The scope is separate from `library-upload` because
adding your own book and destroying everyone's are different questions,
and the browser's gate is the role rather than a scope because a
session carries none.

A Calibre folder inverts that order, and only it can: there the row is
the book, `metadata.db` is a transaction, and a failed directory
removal rolls the row back rather than leaving a library whose files
have no book.

### 9.2 Reconcile passes

A pass is stateless and idempotent. It enumerates one folder, compares
what it saw to the `books` rows for that folder, reads metadata for new
or changed files, and calls `ReconcileFolder` once. Running it twice is
running it once.

The four rules from ADR-0017 are load-bearing:

1. **A pass that did not fully succeed never concludes anything is
   absent.** Any per-file read or parse failure, and any hit against the
   file or depth bound, makes the pass incomplete. It may upsert what it
   saw; it may not mark anything missing, and it may not purge.
2. **A zero-observation pass never marks anything missing.** An unmounted
   mount point can be readable and empty, which otherwise hides the
   whole catalog. The same guard is what stands between an unreadable
   `metadata.db` and an emptied Calibre library.
3. **The server never writes under a watched folder.** No temporary
   files, no extracted covers beside a book, no Calibre writes, no
   renames.
4. **Content change is not identity transfer.** In a plain folder, bytes
   changing at a path delete the old catalog row and insert a new one in
   the same transaction. In a Calibre folder, the Calibre id is the
   identity, so a changed file is an update.

### 9.3 Plain folders

A plain folder is keyed by relative path. Size and modification time are
the cheap change gate. A changed stat causes a re-read; an unchanged
stat records that the book was seen without overwriting its metadata.

The directory tree is the organisation. A subdirectory containing EPUBs
is a series named after that directory, and the files in it are volumes.
A number read from the filename wins; otherwise sorted order is the
fallback. Files at the root are not in a series.

What the directory tree says is not the last word: a reader can restate
a book's series over it, for themselves or, as an admin, for everyone
(ADR-0018), and rename the series itself (ADR-0020). Neither reaches
the disk; §9.2 rule 3 stands.

### 9.4 Calibre folders

A Calibre folder is a root containing `metadata.db`. The server reads
the database read-only. A separate `calibre.Writer`, used only for an
upload into a folder that accepts them, is the one thing that does not.
It keys books by Calibre id, never by path.
Calibre renames a book's directory when title or author changes; path
identity would lose the reading-position mapping on every such edit.

A Calibre pass re-reads metadata every time. Series, tags, descriptions
and the chosen `cover.jpg` change in `metadata.db` without touching the
publication file. The server records a digest of `cover.jpg` so the
cover cache invalidates when the curator replaces it.

A book is served from the first of its formats that is actually on the
disk, EPUB before KEPUB. Both are the same zip container (a KEPUB is an
EPUB with Kobo's reading spans injected) so a book Calibre holds only
as a KEPUB is a book rather than a gap, and a format row left behind by
a deleted file falls through to the file next to it instead of making
the pass incomplete. EPUB is preferred because those injected spans
shift the document structure a reading position is expressed against.

Two-way Calibre synchronization is future work.

### 9.5 Watching

The watcher runs a pass at startup, on a debounced filesystem event, and
on a slow safety timer. Adding a folder registers it and reconciles it
immediately; removing one unregisters it. The debounce and safety
interval are constants, not configuration.

inotify is a speed path. If an instance cannot create a watcher, or a
particular root cannot be watched because a kernel limit was reached or
the mount does not support events, the server logs a warning and relies
on the safety pass. A stale catalog is better than refusing to start.

## 10. Implementation shape

### 10.1 Stack

- Go, stdlib HTTP mux; no framework.
- SQLite via `modernc.org/sqlite` and PostgreSQL via `pgx`.
- Single static binary; container image `FROM scratch`.
- Config: one TOML file + env overrides. Reverse-proxy TLS is the
  documented default posture.
- Content config: `cache_dir`, `folder_roots`, `scan_max_files`,
  `scan_max_depth`, and bounded EPUB parse limits.

### 10.2 Schema sketch

```
users              id, name, argon2_hash, timezone, is_admin, disabled_at
tokens             id, user_id, name, sha256, device_id, timestamps
token_scopes       token_id, user_id, scope
works              user_id, id
editions           user_id, sha256, work_id, page_count?, char_count?
aliases            user_id, kind, value, work_id
ops                user_id, seq, op_id, work_id, edition_sha?, device_id,
                   client_ts, progression, locator_json?, foreign_pos?, origin
sessions           user_id, session_id, work_id, device_id, started_at,
                   ended_at, start_prog, end_prog, idle_ms, origin
folders            id, name, root_path, kind
books              id, folder_id, status, relative_path, calibre_id?,
                   content_sha256, size_bytes, mtime, filename, media_type,
                   title, subtitle, description, publisher, published_date
book_identifiers   book_id, scheme, value
series             id, normalized_name, name
contributors       id, normalized_name, name
tags               id, normalized_name, name
book_series        book_id, series_id, position?
book_contributors  book_id, contributor_id, role, position
book_tags          book_id, tag_id
book_series_overrides       folder_id, book_id, scope_user, updated_at,
                            updated_by
book_series_override_items  folder_id, book_id, scope_user, series_id,
                            position?
series_name_overrides       series_id, scope_user, name, normalized_name,
                            updated_at, updated_by
series_bindings             id, folder_id?, name, normalized_name,
                            series_id, created_at, created_by
user_book_works    user_id, folder_id, book_id, work_id
```

`series`, `contributors` and `tags` are library-wide: they are keyed by
normalized name alone, so the same series held in two folders is one row
with one id, and its shelf spans both (ADR-0019). Only a *book* lives in
a folder, so the membership tables keep `folder_id` for the composite
foreign key that cascades a book's rows away with it. An entity nothing
names any more (no membership and no reader's claim) is collected at
the end of the pass that emptied it, and when a folder is removed.

Works are per-user in v1. The catalog is shared, but the
`user_book_works` bridge keeps the work graph private. Catalog metadata
requires `library-read`. Work mappings, positions and completion require
`sync`; aggregate statistics require `read-insights`. OPDS feeds remain
metadata-only regardless of extra token scopes and therefore cannot
inspect private reading state.

The two `book_series_override_*` tables are the one bounded exception to
"the catalog is not user-scoped" (ADR-0018). `book_series` still means
what the folder said on the last pass and is still rewritten wholesale by
every reconcile; a *claim* over it lives beside it, in a shared layer
(`scope_user = ''`, written by an admin) and a personal one
(`scope_user = <user id>`, written by anyone with `library-manage`).
Series are resolved at read time (personal, else shared, else folder)
so every series-bearing read takes the reader's id, and every series a
payload names carries the layer it came from. A claim speaks for the
whole book: its empty form is the statement "this book is in no series",
which is why the claim is a row of its own rather than a set of items.
A catalog response carries the effective claim layer per book even when
that claim is empty, plus that claim's server-assigned revision. A client
timestamp is an idempotency key only; a client protects an offline edit
with the last server revision it observed. A deleted claim retains its
revision and idempotency key so retries are safe and old writes cannot
resurrect it. This is catalog state, not an op log.
A series also carries a name layer, in the same two scopes
(ADR-0020). `series_name_overrides` says what a reader calls a shelf;
`series.name` and `series.normalized_name` keep meaning what the last
scan observed, and the normalized one stays the only thing a pass
resolves an observed name against. That separation is the whole design:
a rename that moved the fold key would be undone by the next pass, and
a folder still calling the series by its old name would start a shelf
of its own. Renaming onto a name already visible in the caller's scope
is refused, because giving two shelves one name is a merge, which is a
different operation.

Merging and splitting are that operation (ADR-0021), and they are the
one thing a name layer cannot express: they change which shelf a book is
on. Because the fold key belongs to the disk, neither can be a plain
edit; a shelf rearranged only here is rearranged back by the next pass
that observes the old name. Both therefore write a `series_bindings`
row, saying what an observed name means, and an observed name now
resolves through that table first: this folder's binding, else the
global one, else `series.normalized_name` as before. A merge binds
everywhere and deletes the absorbed row, so nothing has to filter dead
entities out of listings; a folder-wise split binds in the folder that
left. Deleting a binding is the undo, and what it restores is what the
disk says rather than what readers claimed. Both are admin-written and
shared: they are statements about the library's shape, and a per-reader
version would put a redirect in every series-bearing read to express
what a personal claim already expresses.

Nothing here writes under a watched folder or into Calibre's
`metadata.db`; write-back is a later milestone with its own ADR.

### 10.3 Testing strategy

- Golden-vector tests for partialMD5 alias resolution.
- Adapter
  conformance suites replaying captured KOReader and statistics-plugin
  traffic.
- Property test on the op log: any interleaving of batched
  pushes from N devices yields a totally ordered, gap-free per-user
  `seq`.
- Named regression tests for every legacy bug this design
  routes around: falsy-zero percentage, xpointer round-tripping,
  redirect header carriage, open-route access.
- Tenant-isolation
  matrices for every route that touches reading state.
- Shared
  reconcile tests for idempotency, incomplete and zero-observation
  passes, plain-folder replacement, and Calibre path changes that keep
  the same book id.
- Hostile EPUB corpus covering zip bombs, zip slip,
  symlinks, malformed or oversized XML, font obfuscation, unsupported
  DRM, SVG covers, and MIME confusion.
- Catalog protocol tests for
  cursor pagination, range and conditional downloads, OPDS escaping,
  Basic authentication, and scope-set compatibility.
- Browser-reader
  CSP, sandbox, asset-header, and linked-token lifecycle tests.

A Go test cannot tell you the Calibre writer worked. It only ever reads
back its own rows, and Calibre's opinion of a library is formed by
opening it. `PRAGMA user_version` is the sharp edge: a library left at 0
is one Calibre upgrades from the beginning on sight, rebuilding the
tables and dropping every book in them, which is why the fixture in
`internal/calibre/testdata/schema.sql` carries the real version. The
check that settles it is Calibre's own:

    calibredb --with-library=<copy of the folder> list \
      --fields=id,title,authors,formats

Run it on a *copy*, because it is Calibre and it will upgrade what it
opens.

### 10.4 Client-side work

1. Add `start/end_progression` to Liseur's session recorder.
2. A `data/liseursync/` peer implementing the existing
   `PeerPositionSync` interface.
3. Reuse `ReadingStateMerge` as-is for three-way reconciliation.
4. Use the native catalog API to browse folders, keep catalog `book_id`
   separate from sync `work_id`, and call `/v1/books/{id}/resolve`
   before reporting reading state for a catalog book.

### 10.5 Milestones

| # | Deliverable | Proves |
|---|---|---|
| M1 | Native API: auth, resolve, ops, changes; SQLite; single binary | The core loop, two Liseur devices syncing |
| M2 | kosync adapter + pairing codes | A stock KOReader joins the same op log |
| M3 | Sessions + insight endpoints; Liseur recorder change | Honest statistics across devices |
| M4 | koplugin adapter; inferred sessions | KOReader statistics are represented natively |
| M5 | Admin CLI + web UI for users, tokens, pairing and insights | Self-host UX complete |
| M6 | Catalog identity, scope sets, bounded EPUB metadata reads, native catalog API and OPDS | Readers browse and download shared books without cross-user sync leakage |
| M7 | Watched plain folders and Calibre folders | Existing EPUB trees appear without mutation |
| M8 | Isolated web reader | Browser reading uses the same position/session protocol safely |
| M9 | Android and desktop catalog integration | One server supplies content, sync, and statistics |

M1–M8 are the current server shape. ADR-0017 replaces the earlier
content plan: the catalog is not a transfer pipeline, it is a reflection
of the folders the operator names. A folder added at runtime is watched
and reconciled without a restart; a file appearing under it shows up
without waiting for the safety timer; a file removed by a complete pass
marks its book missing; a book removed from a Calibre library's
`metadata.db` is deleted, and a complete, non-empty Calibre pass collects
every non-pending work with no book mapping or reading history (ADR-0022);
a vanished or empty mount marks nothing missing; and the server never
mutates the watched tree.

## 11. Future work (explicitly out of v1)

- **E2E mode**: encrypt `locator`/`foreign_pos` client-side; server
  keeps only `progression` or nothing, sacrificing server insights. The
  schema already treats those fields as opaque data, so this is
  additive.
- **Cross-user shared works / social features**: deliberately excluded,
  privacy-sensitive and design-heavy.
- **Annotation sync.** Liseur has annotations; syncing them wants the
  same op-log shape but its own conflict semantics.
- **calibre-web / Komga bridging**: the server pulling positions from a
  remote catalog on the user's behalf. Liseur already handles this
  client-side.
- **OPDS 2.0 and OPDS-PSE.** OPDS 1.2 acquisition feeds land first for
  existing-reader compatibility.
- **Additional content formats.** PDF, CBZ/CBR, audiobook, and
  fixed-layout support require format-specific parsing, metadata,
  serving, and reader decisions.
- **Two-way Calibre synchronization.** Calibre remains authoritative for
  Calibre folders until a later ADR defines a write side.

## 12. Risks

| Risk | Mitigation |
|---|---|
| Fuzzy `ta:` aliases merge distinct books | Low-confidence flag, client confirmation, `split` repair endpoint |
| kosync adapter drift as KOReader evolves | Conformance tests are captured transcripts; KOReader's plugin has been wire-stable for years |
| Op/session growth on chatty clients | Per-user seq compaction, daily session rollups; clients batch |
| SQLite write contention at larger scale | WAL remains the self-host default; PostgreSQL parity is enforced through shared store tests |
| Shared catalog leaks private reading data | Catalog routes have no reading-state vocabulary; per-user works join only through `user_book_works` |
| Malicious or oversized EPUB exhausts or escapes the server | Bounded EPUB parser, rooted read-only opens, symlink refusal, hostile fixture corpus |
| A mounted folder disappears and looks empty | Incomplete and zero-observation passes never mark books missing and never purge |
| A Calibre metadata edit rewrites a book and forks the reader's work | A changed digest is registered additively against the work already mapped to the book; a digest another work claims is left for an explicit merge |
| Publisher content attacks the authenticated web UI | Sandboxed documents, strict CSP, fixed MIME and `nosniff`, optional separate reader origin |
| Content features expand maintenance cost | ADR-0017 keeps the content side to watched folders and a disposable cover cache |
