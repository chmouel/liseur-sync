# liseur-sync — Design

**Status:** draft, pre-implementation
**Audience:** contributors to liseur-sync and client authors (Liseur first,
third parties later)
**Purpose:** define the data model, protocol, and architecture of a
self-hostable server that syncs reading positions and collects reading
statistics across devices and reader apps — including stock KOReader
devices — without repeating the mistakes of the servers that came before it.

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

- **Not a library server.** No book files, covers, or catalogue. Komga,
  calibre-web, and local folders already do that; Liseur keeps them.
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
4. **First-class self-hosting**: one static Go binary, one SQLite file,
   TLS via reverse proxy or built-in ACME. No external services.
5. **Legacy interop as adapters**: stock KOReader syncs via a
   kosync-compatible endpoint; its statistics plugin uploads via a
   KoInsight-compatible endpoint. Neither contaminates the native model.
6. **Multi-user** from day one: a family or small community on one
   instance, data strictly scoped per user.

## 3. Architecture overview

```
                    ┌─────────────────────────────────────────┐
                    │              liseur-sync                │
                    │                (Go)                     │
 Liseur (native) ──▶│  /v1/*           native API             │
                    │                                         │
 KOReader kosync ──▶│  /adapter/kosync/*   translation layer  │
                    │                                         │
 KOReader stats  ──▶│  /adapter/koplugin/* translation layer  │
 plugin             │                                         │
                    │  /admin/*        instance management    │
                    │                                         │
                    │  storage: SQLite (WAL) ── single file   │
                    └─────────────────────────────────────────┘
```

- **One process, one binary, one SQLite database.** SQLite in WAL mode
  comfortably serves this workload (tens of users, bursts of small
  writes); it keeps backup a file copy and deployment a `scp`.
- **Adapters translate at the edge.** The kosync and koplugin endpoints
  parse the legacy wire formats and write *native* records tagged with
  their origin. Nothing downstream knows or cares which protocol a
  record arrived on.
- **The native API is the only full-fidelity surface.** Adapters are
  deliberately lossy where the legacy protocol is (e.g. kosync carries
  no session data); the design never bends the native model to fit them.

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
edition, and aliases as needed. Resolution order: `sha256` → `partial-md5`
→ `source` → `dc` → `ta`. The first hit wins; all supplied identifiers are then
registered as aliases of the resolved work, which is how the graph
converges over time.

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

## 5. Position sync

### 5.1 The record

Positions are an **append-only operation log per work per user**:

```json
{
  "op_id":        "client-generated UUIDv7",
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
- `op_id` is UUIDv7 (client-generated): idempotent retries for free,
  roughly time-ordered for debugging.

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
  "session_id":        "UUIDv7",
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
- Tokens carry scopes: `sync` (ops + sessions), `read-insights`,
  `admin`. A device token is `sync` only; a stolen e-reader cannot
  delete the account.

### 8.2 Instance posture

- **Every route authenticated** except `/healthz` and login. There is no
  anonymous read anywhere; CORS is deny-by-default with an explicit
  origin allowlist for the (future) web UI.
- Registration is invite-only by default (admin-generated codes).
- Per-token and per-IP rate limits on auth endpoints; constant-time
  compares on all credential checks.
- All data strictly scoped by `user_id` at the query layer — the lesson
  of KoInsight's open `DELETE /api/books/:id`.

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

## 9. Implementation shape

### 9.1 Stack

- **Go**, standard library HTTP + `chi` or stdlib mux; no framework.
- **SQLite** via `modernc.org/sqlite` (pure Go, CGO-free → trivial
  cross-compilation for ARM NAS/Pi self-hosters).
- Single static binary; container image `FROM scratch`.
- Config: one TOML file + env overrides. Built-in ACME optional;
  reverse-proxy TLS documented as the default posture.

### 9.2 Schema (core tables)

```
users            id, name, argon2_hash, created_at
tokens           id, user_id, name, scope, sha256, created_at, last_used, revoked_at
works            id, user_scope?, title, author, created_at
editions         sha256 PK, work_id, page_count?, char_count?, meta_json
aliases          kind, value, work_id   (unique on kind+value per user)
ops              seq PK (per-user monotonic), op_id UNIQUE, user_id, work_id,
                 edition_sha, device_id, client_ts, progression,
                 locator_json?, foreign_pos?, origin, received_at
sessions         session_id PK, user_id, work_id, device_id,
                 started_at, ended_at, start_prog, end_prog,
                 idle_ms, origin, received_at
kosync_devices   user_id, device_slot, key_sha256, label, revoked_at
```

Works are per-user in v1 (no cross-user shared works): simpler privacy
story, and a family syncing the same EPUB simply gets parallel works.
Cross-user dedup is an optimisation, not a semantic.

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

### 9.4 Liseur-side work (tracked in the liseur repo)

1. Add `start/end_progression` to `reading_sessions` +
   `ReadingSessionRecorder` (already scoped as the blocked
   `koinsight-stats` todo — this design is what unblocks it).
2. A `data/liseursync/` peer implementing the existing
   `PeerPositionSync` interface — the `CompositePositionSync` seam built
   for kosync means no coordinator changes.
3. Reuse `ReadingStateMerge` as-is for three-way reconciliation.

### 9.5 Milestones

| # | Deliverable | Proves |
|---|---|---|
| M1 | Native API: auth, resolve, ops, changes; SQLite; single binary | The core loop, two Liseur devices syncing |
| M2 | kosync adapter + pairing codes | A stock KOReader joins the same op log |
| M3 | Sessions + insight endpoints; Liseur recorder change | Honest statistics across devices |
| M4 | koplugin adapter; inferred sessions | KOReader statistics ingested |
| M5 | Admin CLI + minimal web UI (tokens, pairing, insights) | Self-host UX complete |

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

## 11. Risks

| Risk | Mitigation |
|---|---|
| Fuzzy `ta:` aliases merge distinct books | Low-confidence flag, client confirmation, `split` repair endpoint |
| kosync adapter drift as KOReader evolves | Conformance tests are captured transcripts; KOReader's plugin has been wire-stable for years |
| Op/session growth on chatty clients | Per-user seq compaction (§5.3), daily session rollups (§6.1); clients batch |
| SQLite write contention at larger scale | WAL + single-writer queue is fine for the target (≤ ~100 users); Postgres is a non-goal until proven otherwise |
| A second project to maintain | Small surface on purpose: no book files, no rendering, adapters gated; the native API is ~10 endpoints |
