# Integrating with liseur-sync

This guide is for client authors: Liseur first, any reading app after.
The machine-readable contract is [openapi.yaml](openapi.yaml); this
document explains the flows and the rules that make sync correct.

The design rationale (why the API looks like this) is in
[DESIGN.md](DESIGN.md). You do not need it to integrate.

## Concepts

- **Work**: the abstract book. Positions and sessions attach to works.
  You get a `work_id` from `/v1/works/resolve`; everything else
  references it.
- **Op**: one position event in an append-only, per-user log. Every op
  gets a server-side monotonic `seq` per user. Sync is a cursor over
  that sequence.
- **Session**: one reading session, a measured fact with progression
  fractions. Sessions feed the statistics endpoints.
- **Device token**: a scoped bearer token per physical device. The
  server stamps ops and sessions with the token's `device_id`; you do
  not send one. A token may carry several scopes.

## Setup

1. Create an account (invite-only): `POST /v1/register` with the invite
   code, or an admin runs `liseur-sync admin create-user <name>`.
2. Log in once: `POST /v1/login` → `auth_token` (valid 1 hour, only
   usable for the next step).
3. Create a device token: `POST /v1/tokens` with
   `{"name": "Boox Palma", "scopes": ["sync", "read-insights"]}`.
   Store the returned `secret`; it is shown once. Legacy clients may
   continue sending `"scope": "sync"`. Responses always include
   `scopes` and include the deprecated scalar `scope` only for
   singleton sets.

   Use `PATCH /v1/tokens/{id}` with the same `scope`/`scopes` shape to
   change capabilities without changing the token secret, device id,
   or retry identity. `library-manage` implies `library-read`; `admin`
   implies every scope. No other implication exists.

   `admin` is not self-grantable: requesting it returns `403` unless
   the caller already holds an admin token. The first one comes from
   `liseur-sync admin mint-token -scope admin`. Clients never need it.

All API calls then use `Authorization: Bearer <token secret>`.

## Position sync

### Resolving books

Before pushing or pulling for a file, resolve it:

```json
POST /v1/works/resolve
{
  "identifiers": [
    {"kind": "sha256",      "value": "…"},
    {"kind": "partial-md5", "value": "…"},
    {"kind": "dc",          "value": "urn:isbn:…"},
    {"kind": "ta",          "value": "…"}
  ],
  "title": "A Memory Called Empire",
  "author": "Arkady Martine"
}
```

Send every identifier you can compute. The server tries them in
priority order (sha256, partial-md5, source, dc, ta), and registers all
of them
on whichever work matches, so next time any variant of the file
resolves directly. Compute `partial-md5` exactly like KOReader
(`util.partialMD5`: 1024-byte samples at offsets 1024<<2i for
i = -1..10) if you want to share identity with KOReader devices.
`source` is the catalog server's own id for the book (e.g.
`komga:<id>`), useful when several devices browse the same catalog:
they share it before any of them has downloaded the file.

Note that in LuaJIT the first of those offsets is not 256 but 0:
`lshift(1024, -2)` is masked to a shift of 30, which overflows 32 bits
to zero. So the first sample is the head of the file. Reproduce the
overflow rather than the formula, or your hashes will not match
KOReader's.

**`ta` values must be normalised identically by every client.** The
server compares them as exact strings and computes nothing itself, so a
client that normalises differently will silently never match another
client's books. The normalisation is:

    fold(title) + "|" + fold(author)

where `fold` is NFKD decomposition, Unicode mark characters
(`\p{Mn}`) stripped, lowercased, every run of non-alphanumeric
characters replaced with a single space, and the result trimmed. Send
`ta` only when both a title and an author are known; a title alone
matches far too much.

A book with no file — one your client only knows from a catalog — still
resolves, on `dc` and `ta` alone. That is weaker, and the server says
so with `confidence: "low"`, but refusing to resolve until a file
exists would strand the reader's place on whichever device holds it.

Two responses need care:

- `confidence: "low"` means only a title+author match was found. Show a
  "same book?" confirmation before merging reading state, and remember
  a refusal: a client that forgets it will resolve the same book again
  on its next run and ask the same question forever. Nothing was
  registered on a low match; once the user says yes, resolve again with
  `confirmed: true` and the same identifiers — the match comes back
  high and the identifiers are registered.
- `409` with a `works` array means your identifiers map to two
  different works (usually a fuzzy false positive). Nothing was
  changed. Ask the user, then either resolve with a narrower identifier
  set or call `POST /v1/works/merge`.

### Pushing

Batch position updates; replay is safe:

```json
POST /v1/ops
{
  "ops": [{
    "op_id": "018e6f1a-…",          // UUIDv7 you generate
    "work_id": "…",
    "edition_sha": "…",              // sha256 of the file, if known
    "client_ts": "2026-08-10T12:58:04Z",
    "progression": 0.4137,           // 0..1, required
    "locator": { "…" }               // your exact position, opaque
  }]
}
```

`op_id` is the idempotency key: generate it once per position event and
keep it until the server acknowledges it. A network retry then reports
`"status": "duplicate"` with the original `seq` instead of writing
twice. Never reuse an `op_id` for a different position; the server
rejects it as a conflict.

`progression` is the lingua franca every device understands. `locator`
is your reader's exact position (a Readium locator, a CFI, whatever
your engine uses); the server stores and replays it verbatim and never
reads it.

### Pulling

Keep a cursor: the highest `seq` you have reconciled, starting at 0.

```
GET /v1/changes?since=<cursor>&limit=500
→ { "ops": [...], "high_water": 1234, "has_more": false }
```

Apply the ops, then set your cursor to the last returned `seq` (or
`high_water` when the page is empty). While `has_more` is true, request
the next page immediately.

If the server answers `410 {"error": "resync_required"}`, your cursor
fell behind the compaction horizon (default: ops older than 180 days
are reduced to daily snapshots). Fetch `GET /v1/heads`, which returns
the newest op per work per device plus an atomic `snapshot_seq`.
Rebuild your local state from those heads, set your cursor to
`snapshot_seq`, and resume normal delta sync.

### Conflicts

The server orders ops; it never picks a winner. Reconcile locally with
a three-way merge: your current position, the newest op from other
devices, and the last op you acknowledged as baseline. Liseur's
implementation (`domain/ReadingStateMerge.kt`) is the reference; the
protocol is shaped so that logic transfers unchanged.

## Reading sessions

Report what you measured at the time of reading:

```json
POST /v1/sessions
{
  "sessions": [{
    "session_id": "018e6f20-…",       // UUIDv7 you generate
    "work_id": "…",
    "edition_sha": "…",
    "started_at": "…", "ended_at": "…",
    "start_progression": 0.401,
    "end_progression": 0.413,
    "idle_ms": 30000                  // screen-off / paused time
  }]
}
```

The same rules as ops: `session_id` is the idempotency key, batch
freely, same id + different payload is a 409. Sessions are never
updated after acceptance. Raw immutable sessions are retained for 180
days by default, then reduced to daily totals. Insights remain complete;
raw session history beyond the retention horizon is intentionally not
available. The server retains a compact fingerprint so replaying an old
session remains idempotent and reusing its ID with another payload still
returns 409.

Do not send page numbers. The server derives pages from progression ×
the edition's page count, and speed from progression delta over active
time (duration minus `idle_ms`). If you only know pages, convert
through `page / total_pages` yourself and say so in the fractions.

## Insights

With a `read-insights` token:

- `GET /v1/insights/summary?range=30d` — totals, streak, speed trend
- `GET /v1/insights/works` — aggregates for every work with reading history
- `GET /v1/insights/works/{id}` — per-work time, pace, ETA
- `GET /v1/insights/calendar?year=2026` — daily minutes for heatmaps

All day boundaries are computed in the user's configured timezone.
Rereading (a session whose progression goes backwards) counts time but
never negative pages. ETA is `null` until the user has enough speed
history on the work.

`total_pages` will be `0` for you unless the work has a page count on
its edition, and nothing in the native API sets one — only the KOReader
statistics adapter reports it, because only a paginated reader has a
page count to report. A reflowable EPUB has no inherent number of
pages, and one derived from a particular device's font size would make
the total depend on which device synced. Report minutes and
progression, which mean the same thing everywhere, and treat pages as
something you may not have.

## Errors and limits

- Every error is `{"error": "reason"}` with a 4xx; a 5xx is a server
  bug, report it.
- Batch limits: 500 ops, 1000 sessions per request, 1 MiB body,
  16 KiB per `locator`.
- Auth endpoints are rate-limited per IP (429 + `Retry-After`).
- Credential traffic requires HTTPS; on plain HTTP the server answers
  403 unless the instance explicitly sets `insecure_http` (LAN-only
  setups).

## KOReader without a liseur-sync plugin

Stock KOReader devices can join through the kosync adapter instead of
this API: the user generates a pairing code (web UI or
`liseur-sync admin pairing-code`), then sets kosync's server to
`https://<host>/adapter/kosync`, username to a device name, password to
the pairing code. Their positions land in the same per-user op log with
`origin: "kosync"`, so a native client sees them through
`/v1/changes` like any other device; the xpointer rides in
`foreign_pos` and round-trips verbatim to kosync pulls.
