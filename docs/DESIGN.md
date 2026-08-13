# liseur-sync — Design

**Status:** living design; sync core implemented, content-server expansion
proposed in [ADR-0001](adr/0001-content-server.md)

**Audience:** contributors to liseur-sync and client authors (Liseur first,
third parties later)
**Purpose:** define the data model, protocol, and architecture of a
self-hostable server that syncs reading positions, collects reading
statistics, and is expanding into an EPUB content server for reader apps —
including stock KOReader devices — without repeating the mistakes of the
servers that came before it.

---

## 1. Why another sync server

Liseur (an Android EPUB reader) already syncs positions against
calibre-web, Komga, and kosync-compatible servers. Building the kosync
client made the protocol's limits concrete. Together with KoInsight (the
only FOSS server with a reading-statistics write API), the existing
options define the problem by their flaws:

| Existing behaviour | Consequence |
|---|---|
| kosync's credential is the hex MD5 of the password, stored verbatim server-side | Password-equivalent secret on every request; no revocation, no per-device tokens |
| One `GET /syncs/progress/:document` per book, no bulk or delta endpoint | A library sweep costs one round trip per book; clients ration syncing |
| Positions are CRe xpointers, resolvable only by KOReader's rendering engine | Other clients can carry the string but not produce one; cross-app push is lossy |
| Book identity is `partialMD5` over ~12 KiB of sampled bytes | Collides by design; breaks whenever calibre re-encodes on export |
| Last-write-wins on a single stored position | No history; a mis-tap on one device silently destroys the position everywhere |
| KoInsight authenticates only its kosync routes; stats, uploads, and deletion are open, with `cors({origin:'*'})` | Unusable on the open internet without a reverse proxy bolted on |
| KoInsight stats rows require `page`/`total_pages` and silently drop rows where `total_pages <= 0` | Clients that track progression fractions, not pages, cannot report honestly |

liseur-sync exists to be the server those clients deserved: **positions as
an ordered, per-work operation log; sessions as append-only facts;
identity that survives re-encoding; auth that can be revoked; and a
kosync adapter so stock KOReader devices participate without modification.**

### 1.2 Non-goals

- **Formats other than EPUB.** The former "not a library server" non-goal is
  superseded by [ADR-0001](adr/0001-content-server.md). The first content
  server supports EPUB only; PDF, comics, and audiobooks require separate
  decisions.
- **Not a merge authority.** The server stores and orders operations; it
  never decides which position "wins". Conflict resolution is a client
  concern (§5.4).
- **No proprietary dependencies.** Same constraint as Liseur: the server
  and any client code must be FOSS-compatible (F-Droid rules for the
  client side).
- **No E2E encryption in v1.** Positions and sessions are visible to the
  server; that is what enables server-side insights. An E2E mode with
  on-device-only insights is acknowledged as future work (§10), and the
  schema keeps payloads opaque enough not to preclude it.

## 2. Goals

1. **Correct multi-device position sync** for the same user across
   Liseur instances, KOReader devices, and future clients — with
   history, not last-write-wins.
2. **Reading statistics done honestly**: sessions carry progression
   fractions measured at the time of reading; the server derives pages
   and aggregates. Never fabricate page numbers.
3. **Work identity that survives format shuffling**: the same book
   recognised across a Komga original, a calibre re-encode, and the
   copy on a Kindle-jailbreak KOReader.
4. **First-class self-hosting**: one static Go binary, SQLite by default or
   PostgreSQL when desired, and explicit persistent content storage. TLS is
   provided by a reverse proxy or built-in ACME; no mandatory external
   services.
5. **Legacy interop as adapters**: stock KOReader syncs via a
   kosync-compatible endpoint; its statistics plugin uploads via a
   KoInsight-compatible endpoint. Neither contaminates the native model.
6. **Multi-user** from day one: a family or small community on one
   instance, data strictly scoped per user.
7. **First-class EPUB catalog**: managed uploads and read-only watched
   folders, shared through explicit library ACLs without sharing users'
   positions or sessions.
8. **Broad reader integration**: a native catalog API for Liseur clients,
   OPDS 1.2 for existing readers, and eventually an isolated web reader using
   the same sync protocol.

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
                    │  ingest workers       validate/index/cover   │
                    │                                              │
                    │  DB: SQLite or PostgreSQL                    │
                    │  files: managed CAS + read-only watched roots│
                    └──────────────────────────────────────────────┘
```

- **One process and one binary.** SQLite in WAL mode is the default for
  small self-hosted installations; PostgreSQL is supported for deployments
  that already operate it. Managed EPUB blobs live beside the database in a
  content-addressed directory, as defined by
  [ADR-0002](adr/0002-library-storage-and-ownership.md).
- **Adapters translate at the edge.** The kosync and koplugin endpoints
  parse the legacy wire formats and write *native* records tagged with
  their origin. Nothing downstream knows or cares which protocol a
  record arrived on.
- **The native API is the only full-fidelity surface.** Adapters are
  deliberately lossy where the legacy protocol is (e.g. kosync carries
  no session data and OPDS cannot edit metadata); the design never bends
  the native model to fit them.
- **Catalog storage and sync state have separate ownership.** Libraries and
  books may be shared through ACLs. Works, operations, and sessions remain
  per-user and are joined lazily through the mapping in
  [ADR-0003](adr/0003-catalog-work-identity.md).
- **Ingestion is durable and bounded.** Uploads and watched files pass
  through the same persistent state machine and security limits in
  [ADR-0005](adr/0005-upload-and-ingestion.md). Watched sources remain
  untouched, but downloads use immutable validated CAS snapshots rather than
  mutable source paths. A hash-changing source replacement preserves catalog
  identity only after strong embedded matching or explicit confirmation.

## 4. Identity: works, editions, aliases

The hardest problem, so it is solved structurally rather than by hashing
harder.

### 4.1 Three layers

- **Work** — the abstract book ("A Memory Called Empire"). What positions
  and statistics attach to. Server-assigned UUID.
- **Edition** — one concrete file: a specific EPUB with a specific
  SHA-256. Editions belong to a work. Carries `total_progression_chars`
  or page-map metadata when the client knows it.
- **Alias** — an identifier observed in the wild that maps to a work:
  - `sha256:<hex>` — exact bytes (also the edition key)
  - `partial-md5:<hex>` — KOReader's fingerprint, registered so kosync
    adapter traffic resolves
  - `source:<catalog-id>` — the catalog server's own id for the book
    (e.g. `komga:<id>`); shared by devices browsing the same catalog
    before any of them has downloaded the file
  - `dc:<urn>` — the EPUB's `dc:identifier` (ISBN, UUID, calibre ID)
  - `ta:<fingerprint>` — normalised title+author hash, the fuzzy fallback

### 4.2 Resolution

A client asks `POST /v1/works/resolve` with every identifier it has for
a file. The server answers with the matching `work_id`, creating work,
edition, and aliases as needed. Resolution order is `sha256` → `partial-md5`
→ `source` → `dc` → `ta`, but every supplied alias that already matches must
agree on one work. Aliases spanning multiple works return 409 without
mutation. After one high-confidence match, supplied identifiers are
registered as aliases of that work, which is how the graph converges over
time. A `ta:`-only match stays low-confidence and does not promote stronger
aliases until the reader confirms it.

Lookup, work/edition creation, alias registration, and any catalog mapping
are one store transaction. A uniqueness race is re-evaluated and returns
success on the one agreed work or 409; it never silently ignores a conflicting
alias or turns an identity conflict into a 500
([ADR-0003](adr/0003-catalog-work-identity.md)).

Fuzzy (`ta:`) matches are marked `confidence: low` in the response, and
clients may present a "same book?" confirmation before merging. Wrong
merges are repairable: `POST /v1/works/{id}/split` detaches an edition
and its records into a new work (the operation log makes this tractable —
every record knows its edition).

**Why not hash-only:** the Liseur kosync work established that calibre
re-encodes on export, so byte identity fails exactly when a user mixes a
calibre library with anything else. **Why not metadata-only:** two
editions of the same ISBN can have different pagination, and anthologies
share titles. The layered scheme lets exact identity carry when it can
and metadata rescue it when it cannot.

### 4.3 Catalog identity

The planned content catalog does not make works cross-user. Catalog
`book_id`s are stable library identities; a
`user_book_works(user_id, book_id, work_id)` row maps a book into each
reader's existing work graph. Catalog browse and download do not mutate sync
identity. Before syncing or reporting sessions, a client with both
`library-read` and `sync` explicitly runs the normal alias resolver in that
user's namespace. Resolution, alias promotion, and mapping insertion commit
in one per-user graph transaction, which also records the stable
`liseur-sync:<book_id>` source alias. Splits and merges repair mappings and
that source alias in the same graph transaction. The same shared book can
therefore have different work IDs for different users without leaking either
graph. See
[ADR-0003](adr/0003-catalog-work-identity.md).

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

- `progression` (0..1) is the **lingua franca** — every client can
  produce and consume it. It is the only field the server itself reads.
- `locator` is Liseur's exact position, opaque to the server, replayed
  verbatim to other Liseur devices.
- `foreign_pos` carries a position string another engine owns (a CRe
  xpointer from kosync). Stored verbatim, replayed verbatim to kosync
  pulls, never parsed. This is the lesson of KOReader's
  `GotoXPointer`: `percentage` rides along but is not navigated by, so
  the server must preserve the engine-native string or KOReader users
  land on page 1.
- `op_id` is a client-generated opaque deterministic string (currently at
  most 64 bytes). The same local fact must produce the same ID and payload on
  retry; UUIDv3 and deterministic hash-derived IDs are both valid.

The server assigns each accepted op a **per-user monotonic `seq`**.

### 5.2 Delta sync in one round trip

```
GET /v1/changes?since=<seq>&limit=500
```

returns every op (all works) accepted for this user after `seq`, plus the
new high-water mark. A full-library sync is one request — the direct fix
for kosync's per-book polling, which forced Liseur to restrict sweeps to
books with existing progress. Pushing is symmetric:
`POST /v1/ops` with a batch; the response reports each op's assigned
`seq` (or that it was a duplicate).

### 5.3 History, not just heads

`GET /v1/works/{id}/positions?limit=50` returns recent ops. This enables:

- **Undo**: "position jumped to 100% because a device mis-synced" is
  recoverable by re-issuing an older position as a new op.
- **Session inference for legacy devices** (§6.3).
- **Trust**: users can see what each device claimed and when.

Retention: ops older than a configurable window (default 180 days)
are compacted to daily last-op-per-device snapshots.

### 5.4 Conflicts are resolved by clients

The server orders; it never merges. A client tracks the last `seq` it
reconciled and, on sync, performs the same three-way comparison Liseur
already implements in `domain/ReadingStateMerge.kt`: local position vs
newest remote op vs the recorded agreed baseline. This code exists, is
pure Kotlin, and is battle-tested against Komga and kosync — the protocol
is shaped so it transfers unchanged: *baseline = last op this device
acknowledged; remote = newest op from any other device; local = where the
reader is now.*

For non-interactive clients (the kosync adapter), the server applies the
only safe default: the adapter's `GET` returns the newest op's
`foreign_pos`/`progression`, which is exactly kosync's semantics today.

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

`POST /v1/sessions` accepts batches, idempotent on `session_id`. Rows are
never updated in place — a session is a historical fact. Immutable
native and inferred sessions older than the configurable retention
window (default 180 days) are reduced to per-work, timezone-local daily
totals (active time, pages, progression delta, and session count), then
their raw rows are removed. A compact id/fingerprint tombstone preserves
the original idempotency and conflict behavior. Mutable koplugin
supersession chains stay raw because a later revision may replace them.

The design principle, learned from the Liseur/KoInsight investigation:
**never fabricate resolution you didn't measure.** Liseur's
`reading_sessions` table records duration but not position, so uploading
KoInsight-style page rows would have attributed every historical session
to the book's current page. liseur-sync instead requires
`start/end_progression` — which Liseur will add to its recorder — and
*derives* everything else server-side:

- pages read = progression delta × edition page count (when known)
- reading speed = progression delta / active duration, per work and rolling
- streaks, per-book time, calendar heatmaps: raw sessions plus daily
  rollups

### 6.2 Insight API

Read-only aggregation endpoints, all per-user:

```
GET /v1/insights/summary?range=30d      totals, streak, speed trend
GET /v1/insights/works                  all per-book aggregates
GET /v1/insights/works/{id}             per-book: time, sessions, pace, ETA
GET /v1/insights/calendar?year=2026    daily minutes for heatmaps
```

ETA ("time left in book") = remaining progression / rolling speed — the
same estimator Liseur ships on-device, now computable across all devices'
sessions combined.

### 6.3 Sessions inferred for position-only devices

A stock KOReader device syncing via the kosync adapter sends positions
but no sessions. The server can *infer* coarse sessions from its op log:
consecutive ops from one device with gaps < 15 min form an inferred
session with `origin: "inferred"`, always distinguishable from measured
ones and excluded from speed statistics by default. Better than nothing,
honest about being so. (KOReader's own statistics plugin, where installed,
uploads real sessions via the koplugin adapter instead.)

## 7. Legacy adapters

### 7.1 kosync (`/adapter/kosync/*`)

Implements the four calls KOReader's kosync plugin makes, verified from
KOReader source during the Liseur client work:

| kosync call | Translation |
|---|---|
| `POST /users/create` | Creates a **device credential** bound to an existing liseur-sync user via a one-time pairing code used as the kosync "password" (§8.3). Open registration is off by default. |
| `GET /users/auth` | Validates the device credential. |
| `PUT /syncs/progress` | Resolves `document` (a partialMD5) via the alias table → appends a native op with `origin: kosync`, `foreign_pos` = the xpointer, `progression` = percentage (0 accepted — the KoInsight falsy-zero bug is a named regression test). |
| `GET /syncs/progress/:document` | Returns the newest op: `progress` = stored `foreign_pos` if the newest op has one, else the percentage string (KOReader accepts a bare percentage for non-CRe engines); `percentage`, `device`, `timestamp` filled natively. |

Unresolvable `document` digests create a **pending work** keyed on the
partialMD5 alias; when any client later resolves a file to that alias,
the history merges in. A KOReader-first book is not lost.

### 7.2 KOReader statistics plugin (`/adapter/koplugin/*`)

Accepts the KoInsight-shape upload (verified against KoInsight source:
route `/api/plugin/*`, `start_time` in **seconds**, `version` pinned
`"0.3.0"`, upsert key `(device_id, book_md5, page, start_time)`).
Page-based rows are converted: `progression = page / total_pages`,
`origin: koplugin`. Rows the legacy protocol would silently drop
(`duration <= 0`) are rejected loudly with a 422 listing the reason.

### 7.3 Adapter rules

- Adapters **write native records**; no legacy shape is ever stored.
- Adapters are **feature-gated per instance** and per user.
- Anything the legacy protocol cannot express is dropped *at the edge,
  documented*, never approximated silently.

## 8. Security and auth

Designed as the direct negation of the KoInsight findings (no auth
outside kosync, `cors({origin:'*'})`, MD5 password-equivalents).

### 8.1 Accounts and tokens

- Users authenticate with username + password, hashed with **argon2id**.
- Login yields nothing directly usable; it authorises creating
  **per-device API tokens**: random 256-bit, stored hashed (SHA-256),
  shown once, revocable individually, named ("Boox Palma",
  "Liseur pixel8"). Native API auth is `Authorization: Bearer <token>`.
- Tokens carry scope sets containing `sync`, `read-insights`, `library-read`,
  `library-manage`, or `admin`, as defined by
  [ADR-0006](adr/0006-catalog-api-and-opds.md).
  `library-manage` implies `library-read`; `admin` implies
  all scopes. Existing singleton tokens remain wire-compatible, and explicit
  in-place scope updates preserve a token's device identity and secret.
  `admin` is never self-grantable: minting or adding it requires an existing
  admin token or the admin CLI, so a login credential alone can never
  escalate.

### 8.2 Instance posture

- **Every route authenticated** except the documented login, invite
  registration, health, and adapter-pairing routes. There is no anonymous
  catalog read; CORS is deny-by-default with an explicit origin allowlist.
- Registration is invite-only by default (admin-generated codes).
- Per-token and per-IP rate limits on auth endpoints; constant-time
  compares on all credential checks. Every path that verifies a
  password shares one rate-limit budget and burns the same argon2id
  work for an unknown username, so neither surface is a way around the
  other or a user-enumeration oracle.
- All data strictly scoped by `user_id` at the query layer — the lesson
  of KoInsight's open `DELETE /api/books/:id`.
- Catalog queries additionally require access through the library ACL.
  Shared catalog access never grants access to another user's work,
  operation, or session rows.

### 8.3 The kosync credential problem, contained

The kosync protocol forces a password-equivalent MD5 header; a stock
KOReader cannot do better. Containment:

- The kosync "user/password" pair is a **dedicated pairing credential**,
  never the account password: the user generates a pairing code in the
  web UI/admin CLI, types it into KOReader once, and KOReader's derived
  MD5 key is then stored hashed and bound to that device slot.
- Compromise of that key exposes exactly one revocable device slot with
  `sync` scope — not the account.
- The kosync adapter refuses plain HTTP unless the instance is
  explicitly configured `insecure_http: true` (for LAN-only setups).

### 8.4 Redirects and secrets

The server never redirects API routes (the Liseur client work showed
OkHttp will happily carry custom auth headers like `x-auth-key` across
redirects; well-behaved servers should not create that trap). `301`s
exist only on the web UI.

Credentials never reach a log. The koplugin adapter is the one place a
secret rides in the URL path (stock KOReader can only be pointed at a
URL), so any path the server logs goes through a redactor first, and
the deployment docs show the equivalent rule for the proxy's access
log.

### 8.5 Untrusted book content

EPUB files are untrusted ZIP and XML input. Ingestion applies size, entry,
decompression-ratio, path, symlink, XML, and encryption-algorithm bounds
before promotion. Covers are rasterized and served with fixed MIME types and
`nosniff`. Details and the required hostile-input corpus are in
[ADR-0005](adr/0005-upload-and-ingestion.md).

The planned browser reader never executes publication content as an ordinary
document on the authenticated UI origin. It uses sandboxed documents without
`allow-same-origin`, a restrictive CSP, and optionally a separate content
origin. Each reader tab gets a short-lived `sync + library-read` device token
linked to the UI session; tabs do not revoke each other, and all linked
tokens are revoked with the session. See
[ADR-0007](adr/0007-web-reader.md).

## 9. Implementation shape

### 9.1 Stack

- **Go**, standard library HTTP mux; no framework.
- **SQLite** via `modernc.org/sqlite` (pure Go, CGO-free → trivial
  cross-compilation for ARM NAS/Pi self-hosters).
- Single static binary; container image `FROM scratch`.
- The scratch image copies a maintained CA certificate bundle from its build
  stage so opt-in HTTPS metadata providers retain normal certificate
  verification.
- Config: one TOML file + env overrides. Built-in ACME optional;
  reverse-proxy TLS documented as the default posture.
- Managed content, staging, watched roots, quotas, retention, and maintenance
  mode are explicit configuration. Container deployments mount managed
  content persistently and watched roots read-only
  ([ADR-0002](adr/0002-library-storage-and-ownership.md)).

### 9.2 Schema (core tables)

```
users            id, name, argon2_hash, created_at
tokens           id, user_id, name, scope (legacy singleton), sha256, created_at, last_used, revoked_at
token_scopes     token_id, user_id, scope  PK (token_id, scope)
works            user_id, id            PK (user_id, id)
editions         user_id, sha256, work_id  PK (user_id, sha256)
aliases          user_id, kind, value, work_id  PK (user_id, kind, value)
ops              user_id, seq, op_id, work_id  PK (user_id, seq),
                 UNIQUE (user_id, op_id),
                 edition_sha, device_id, client_ts, progression,
                 locator_json?, foreign_pos?, origin, received_at
sessions         user_id, session_id, work_id, device_id,
                 started_at, ended_at, start_prog, end_prog,
                 idle_ms, origin, received_at  PK (user_id, session_id)
kosync_devices   user_id, device_slot, key_sha256, label, revoked_at

# Planned catalog tables (ADRs 0002–0006)
libraries        id, owner_user_id, quota_user_id, kind, name, root?, config
library_access   library_id, user_id, role(read|manage)
books            id, library_id, status, metadata fields + source/lock data
blobs            sha256 PK, size, created_at, orphaned_at?, missing_at?
blob_reservations quota_user_id, blob_sha256, bytes
book_files       id, book_id, blob_sha256, source_relative_path?, availability
user_book_works  user_id, book_id, work_id
ingest_jobs      id, user_id, library_id, source, state, bytes, error, timestamps
series           id, library_id, name
contributors     id, library_id, name
book_contributors/book_tags/collections/reading_lists ...
```

Works are per-user in v1 (no cross-user shared works): simpler privacy
story, and a family syncing the same EPUB simply gets parallel works.
Cross-user work dedup is an optimisation, not a semantic.

Catalog rows may be shared through library ACLs, but the
`user_book_works` bridge keeps the work graph per-user. Physical blob
deduplication likewise conveys no access or semantic sharing.

Catalog metadata requires `library-read`. Work mappings, positions,
completion, and reading-state filters additionally require `sync`; aggregate
statistics require `read-insights`. OPDS feeds remain metadata-only
regardless of extra token scopes and therefore cannot inspect private reading
state.

### 9.3 Testing strategy

- Golden-vector tests for partialMD5 alias resolution (vectors already
  produced and validated during the Liseur client work — reuse them).
- Adapter conformance suites replaying **captured KOReader and
  statistics-plugin traffic** — the protocols are defined by their only
  real implementations, so tests are transcripts, not specs.
- Property test on the op log: any interleaving of batched pushes from N
  devices yields a totally ordered, gap-free per-user `seq`.
- Named regression tests for every legacy bug this design routes around:
  falsy-zero percentage, xpointer round-tripping, redirect header
  carriage, open-route access.
- Shared-library ACL and `user_book_works` tenant-isolation matrices.
- Concurrent resolver tests proving alias uniqueness races are atomic and
  return one work or 409 without partial mutation.
- Crash and concurrency tests for every ingestion state, duplicate promotion,
  orphan reconciliation, grace-period GC, and backup verification.
- A hostile EPUB corpus covering zip bombs, zip slip, symlinks, malformed or
  oversized XML, font obfuscation, unsupported DRM, SVG covers, and MIME
  confusion.
- Catalog protocol tests for cursor pagination, range and conditional
  downloads, OPDS escaping, Basic authentication, and scope-set
  compatibility.
- Browser-reader CSP, sandbox, asset-header, and linked-token lifecycle
  tests before web reading is enabled.

### 9.4 Client-side work

1. Add `start/end_progression` to `reading_sessions` +
   `ReadingSessionRecorder` (already scoped as the blocked
   `koinsight-stats` todo — this design is what unblocks it).
2. A `data/liseursync/` peer implementing the existing
   `PeerPositionSync` interface — the `CompositePositionSync` seam built
   for kosync means no coordinator changes.
3. Reuse `ReadingStateMerge` as-is for three-way reconciliation.

The content-server changes are planned in
[ADR-0008](adr/0008-liseur-android-client.md) for Android and
[ADR-0009](adr/0009-liseur-desktop-client.md) for desktop. Both clients use
the native catalog API, keep catalog `book_id` separate from sync `work_id`,
remain local-first, and preserve their existing conflict/cursor guarantees.

### 9.5 Milestones

| # | Deliverable | Proves |
|---|---|---|
| M1 | Native API: auth, resolve, ops, changes; SQLite; single binary | The core loop, two Liseur devices syncing |
| M2 | kosync adapter + pairing codes | A stock KOReader joins the same op log |
| M3 | Sessions + insight endpoints; Liseur recorder change | Honest statistics across devices |
| M4 | koplugin adapter; inferred sessions | KOReader statistics ingested |
| M5 | Admin CLI + minimal web UI (tokens, pairing, insights) | Self-host UX complete |
| M6 | Catalog identity, ACLs, scope sets, bounded metadata extraction, and durable ingestion core | Shared catalog without cross-user sync leakage |
| M7 | Managed upload and library-management UI | Books can be safely added without filesystem access |
| M8 | Read-only watched libraries | Existing EPUB folders can be indexed without mutation |
| M9 | Metadata editing, categorization UI, external lookup, and search | A large library is organized and discoverable |
| M10 | Native catalog API and OPDS 1.2 | Liseur and existing readers browse and download |
| M11 | Isolated web reader | Browser reading uses the same position/session protocol safely |
| M12 | Android and desktop catalog integration | One server supplies content, sync, and statistics |

M6 is in progress: compatible token scope sets and the shared catalog,
metadata, blob, ingestion-job, ACL, and per-user work-mapping schema are
implemented on SQLite and PostgreSQL, together with ACL-scoped store
operations, atomic catalog resolution, and revision-checked idempotent ingest
job transitions. Bounded restart-safe filesystem staging and no-replace CAS
publication are also implemented. Database promotion now atomically installs
blob identity, logical quota reservations, catalog book/file rows, and job
state; transient holds cover staged artifacts, full request fingerprints make
promotion replay-safe, and expired failed artifacts use terminal tombstones
with retryable two-phase filesystem cleanup. A recovery coordinator verifies
stale stages, accepts an already-durable final blob after a lost promotion
response, and terminalizes missing or corrupt artifacts. The server opens the
configured private CAS and recovers every pre-existing nonterminal job before
listening. The CAS also supports strict verified final-blob inventory.
Database reconciliation records missing content and grace-period orphan marks
without deletion, and the complete comparison runs before the server accepts
traffic. Bounded extraction, the rest of the CAS lifecycle, ingest workers,
catalog availability reconciliation, and administration remain.

## 10. Future work (explicitly out of v1)

- **E2E mode**: encrypt `locator`/`foreign_pos` client-side; server keeps
  only `progression` (or nothing, sacrificing server insights). The
  schema already treats those fields as opaque blobs, so this is
  additive.
- **Cross-user shared works / social features** (reading together,
  shared annotations). Deliberately excluded: privacy-sensitive and
  design-heavy.
- **Annotation sync.** Liseur has annotations; syncing them wants the
  same op-log shape but its own conflict semantics (merge, not
  supersede). Same machinery, separate design.
- **calibre-web / Komga bridging** (the server pulling positions from a
  library server on the user's behalf). Liseur already handles this
  client-side; server-side bridging doubles the credential surface for
  little gain.
- **OPDS 2.0 and OPDS-PSE.** OPDS 1.2 acquisition feeds land first for
  existing-reader compatibility.
- **Additional content formats.** PDF, CBZ/CBR, audiobook, and fixed-layout
  support require format-specific ingestion, metadata, serving, and reader
  decisions.

## 11. Risks

| Risk | Mitigation |
|---|---|
| Fuzzy `ta:` aliases merge distinct books | Low-confidence flag, client confirmation, `split` repair endpoint |
| kosync adapter drift as KOReader evolves | Conformance tests are captured transcripts; KOReader's plugin has been wire-stable for years |
| Op/session growth on chatty clients | Per-user seq compaction (§5.3), daily session rollups (§6.1); clients batch |
| SQLite write contention at larger scale | WAL remains the self-host default; the existing PostgreSQL backend must maintain parity through shared store tests |
| Shared catalog leaks private reading data | Library ACLs govern catalog rows; per-user works are joined only through `user_book_works` composite constraints |
| Malicious or oversized EPUB exhausts or escapes the server | Durable bounded ingestion, descriptor-relative watched-root traversal, hostile fixture corpus |
| Blob/database backup becomes inconsistent | Quiesced or snapshot backup, DB-before-CAS ordering, reference verification, grace-period GC |
| Publisher content attacks the authenticated web UI | Sandboxed non-same-origin documents, strict CSP, fixed MIME and `nosniff`, optional separate content origin |
| Content features expand maintenance cost | Phase gates, native API as full-fidelity contract, adapters kept narrow, EPUB-only initial scope |
