# Integrating with liseur-sync

This guide is for client authors: Liseur first, any reading app after.
The machine-readable contract is [openapi.yaml](openapi.yaml); this
document explains the flows and the rules that make sync correct.

The design rationale is in [DESIGN.md](DESIGN.md). You do not need it to
integrate.

## Concepts

- **Work**: the abstract book. Positions and sessions attach to works.
  You get a `work_id` from `/v1/works/resolve`; everything else
  references it.
- **Op**: one position event in an append-only, per-user log. Every op
  gets a server-side monotonic `seq` per user. Sync is a cursor over
  that sequence.
- **Session**: one reading session, a measured fact with progression
  fractions. Sessions feed the statistics endpoints.
- **Folder**: a watched server-side directory of books. An administrator
  explicitly grants it to accounts; its filesystem path is never returned to
  a non-admin client. Administrator status does not itself add the folder to
  that account's library.
- **Device token**: a scoped bearer token per physical device. The
  server stamps ops and sessions with the token's `device_id`; you do
  not send one. A token may carry several scopes.

## Setup

1. Create an account (invite-only): `POST /v1/register` with the invite
   code, or an admin runs `liseur-sync admin create-user <name>`.
2. Log in once: `POST /v1/login` returns an `auth_token` valid for one
   hour and usable only for token management.
3. Create a device token: `POST /v1/tokens` with
   `{"name": "Boox Palma", "scopes": ["sync", "read-insights"]}`.
   Store the returned `secret`; it is shown once. Legacy clients may
   continue sending `"scope": "sync"`. Responses always include
   `scopes` and include the deprecated scalar `scope` only for
   singleton sets.

Use `PATCH /v1/tokens/{id}` with the same `scope`/`scopes` shape to
change capabilities without changing the token secret, device id, or
retry identity.

### Keeping one device identity across reconnects

Every mint draws a fresh `device_id`, and the server compares
`device_id` when it decides whether a replayed `op_id` or `session_id`
is a duplicate. So a client that reconnects with a new token after a
lost acknowledgement would replay its stored records as `conflict`
(ops) or `409` (sessions), not `duplicate`.

To stay one device, store the `device_id` the first mint returned and
send it back on every later mint for the same server:

```json
POST /v1/tokens
{"name": "Boox Palma", "scopes": ["sync"], "device_id": "dev_9c21"}
```

The server honours it when any token of the same account — live,
expired or revoked — already carries that id. If none does (the
predecessor was deleted, or the id belongs to another account) it
answers `400` with `code: unknown_device`; retry once without the field
and store the fresh id. An older server ignores the field and mints a
new id: compare the `device_id` in the response with the one you sent,
and treat a mismatch as "not inherited", nothing more. Never offer a
device id to a different server.

A device id is a label in the op log, not a credential: two live tokens
that share one are one device in `GET /v1/heads` and in the statistics.
Only a mint through `/v1/login` can ask for one; a bearer secret pasted
from elsewhere carries whatever device id it was minted with.

The scopes are `sync`, `read-insights`, `library-read`,
`library-manage`, `library-upload`, `library-delete`, and `admin`. `admin` implies every scope and is not
self-grantable: requesting it returns `403` unless the account is
already an administrator. Clients normally need only `sync`; add
`library-read` to browse and download books, `read-insights` to show
statistics, and `library-manage` only when the client lets readers state
series claims. `library-manage` does not grant catalog reads, so a
series editor usually asks for both `library-read` and `library-manage`.
Add `library-upload` to send a book up to a folder that accepts them
(ADR-0023); a client that holds it should still read `accepts_uploads`
from `GET /v1/folders`, because the right and the folder are two
separate answers and an account can hold the first with no folder
giving the second.

Add `library-delete` to take a book back out of such a folder
(ADR-0025). It is deliberately not implied by `library-upload`: that
one adds your own book, this one destroys everyone's, and the file is
gone with no trash behind it. The same `accepts_uploads` caveat
applies, and for the same reason; it is the flag that decides where
this server may write at all.

All API calls then use `Authorization: Bearer <secret>`.

Folder grants and token scopes are independent. A catalog list contains only
the caller's granted folders. Direct access to another folder or one of its
books returns `404`; lacking the scope for the attempted operation returns
`403`. A newly created account has no folder grants.

### Finding out what your token can do

A client is usually handed a secret it did not mint (pasted into a
settings field, copied from another device, restored from a backup) and
has no idea what it is allowed to do. Ask:

```
GET /v1/token
```

```json
{
  "id": "tok_7f3a",
  "account_id": "usr_31b0",
  "device_id": "dev_9c21",
  "name": "Boox Palma",
  "scopes": ["sync", "library-read"]
}
```

Call this once at startup and draw the interface from the answer: show
the catalog browser when `library-read` is present, the series editor
when `library-manage` is present, and the reading statistics when
`read-insights` is. Probing routes and reading `403`s works, but it
means the first thing the user sees is an error the client could have
avoided.

`account_id` is the stable name of the account. Every token minted on
the account carries the same value, so when the user pastes a new
secret or reconnects, compare it with the one you stored: equal means
the same account, keep your catalog mirror, aliases and sync cursor;
different means an account switch, clear them. Neither `id` nor
`device_id` is suitable for that comparison: both change with every
minted token.

The route needs no particular scope (the narrowest token can ask about
itself) but it is still authenticated: an absent, revoked or expired
credential gets `401`. Treating that `401` as "this secret is no longer
good, ask the user for a new one" is the correct reaction, and it is the
cheapest way to check that a stored credential is still live.

`HEAD /v1/token` is the same check without the body. It returns `200` if
the secret is still good and `401` if it is not.

### A credential for code running in the browser

A reader or dashboard running as a page inside the web UI does not go
through the two steps above; it already has the user's session cookie.
`POST /ui/reader/token`, sent with that cookie and the page's `csrf`
field, returns a token to use as an ordinary bearer credential:

```json
{
  "token": "...",
  "device_id": "...",
  "expires_at": "2026-08-13T18:21:00Z",
  "scopes": ["sync", "library-read"]
}
```

The scopes are fixed. It can read the catalog and sync positions,
whatever else its owner is allowed to do. It expires in an hour and
there is no refresh token; ask for another the same way. The device id
is stable, so all browser reading is one device in the op log rather
than one per tab. Signing out revokes it.

This is the credential the built-in web reader uses, at
`/ui/books/{id}/read`. That reader fetches the whole EPUB from the
ordinary download route and unpacks it in the page. No route serves
publication resources, so do not look for one. Rendering publisher
markup anywhere it can reach a session cookie is the mistake the design
is arranged to prevent.

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

Send every identifier you can compute. The server tries them in priority
order (`sha256`, `partial-md5`, `source`, `dc`, `ta`), and registers all
of them on whichever work matches, so next time any variant of the file
resolves directly. Compute `partial-md5` exactly like KOReader
(`util.partialMD5`: 1024-byte samples at offsets 1024<<2i for i =
-1..10) if you want to share identity with KOReader devices.

In LuaJIT the first of those offsets is not 256 but 0: `lshift(1024,
-2)` is masked to a shift of 30, which overflows 32 bits to zero. So the
first sample is the head of the file. Reproduce the overflow rather than
the formula, or your hashes will not match KOReader's.

`ta` values must be normalised identically by every client. The server
compares them as exact strings and computes nothing itself, so a client
that normalises differently will silently never match another client's
books. The normalisation is:

    fold(title) + "|" + fold(author)

where `fold` is NFKD decomposition, Unicode mark characters (`\p{Mn}`)
stripped, lowercased, every run of non-alphanumeric characters replaced
with a single space, and the result trimmed. Send `ta` only when both a
title and an author are known; a title alone matches far too much.

Two responses need care:

- `confidence: "low"` means only a title+author match was found. Show a
  "same book?" confirmation before merging reading state. Nothing was
  registered on a low match; once the user says yes, resolve again with
  `confirmed: true` and the same identifiers.
- `409` with a `works` array means your identifiers map to two different
  works. Nothing was changed. Ask the user, then either resolve with a
  narrower identifier set or call `POST /v1/works/merge`.

### Pushing

Batch position updates; replay is safe:

```json
POST /v1/ops
{
  "ops": [{
    "op_id": "018e6f1a-…",
    "work_id": "…",
    "edition_sha": "…",
    "client_ts": "2026-08-10T12:58:04Z",
    "progression": 0.4137,
    "locator": { "…" }
  }]
}
```

`op_id` is the idempotency key: generate it once per position event and
keep it until the server acknowledges it. A network retry then reports
the original `seq` instead of writing twice. Never reuse an `op_id` for
a different position; the server rejects it as a conflict. The device
is part of what "the same op" means: a replay from a token with a
different `device_id` is a `conflict`, which is why a reconnecting
client should keep its device identity (see *Keeping one device
identity across reconnects*).

The per-item `results` are `applied`, `duplicate` or `conflict`. A
`conflict` is a result, not a refusal: the other items in the batch are
stored. Treat it as a permanent answer for that `op_id` — report it and
send that position again under a fresh id; sending the same op again
will only conflict again.

#### When a batch is refused

The batch is appended atomically, so a `400` means nothing in it was
stored. The body names the first item that caused it:

```json
{ "error": "op <op-id>: locator too large", "code": "locator_too_large",
  "item_index": 3, "op_id": "…", "limit": 16384 }
```

`item_index` is always present for an item-level refusal; `op_id` when
the item had one (a `missing_field` about the id itself cannot name
it). Key the recovery off `code`, never off the message text:

- `unknown_work` (with `work_id`): orphan cleanup deleted a work that
  had no catalog mapping and no reading history. Re-resolve the book
  (`POST /v1/works/resolve`, or `/v1/books/{id}/resolve` for a catalog
  book), reseed from the fresh work's positions, rebuild the batch under
  the new `work_id` and retry.
- `locator_too_large` (with `limit`): the server's `ops.max_locator_bytes`
  is configurable and may be smaller than you assumed. Retry that op
  under the same `op_id` without its locator; the progression still
  syncs.
- `batch_too_large` (with `limit`, no `item_index`): split the batch.
- Anything else (`missing_field`, `bad_time`, `time_in_future`,
  `progression_out_of_range`) is malformed input and will never be
  accepted as is. Set that item aside and send the rest.

A body over `ops.max_body_bytes` is `413`; halve the batch and retry.

`POST /v1/sessions` answers the same shape keyed by `session_id`, plus
`409 {"code": "id_reused", "session_id": …, "item_index": …}` when a
session id was already accepted with a different payload. Never mark a
whole refused batch as sent: only the named item is the problem, and
an hour of reading dropped because a neighbour was malformed is an hour
that never happened.

`progression` is the lingua franca every device understands. `locator`
is your reader's exact position: a Readium locator, a CFI, whatever
your engine uses. The server stores and replays it verbatim and never
reads it.

### Pulling

Keep a cursor: the highest `seq` you have reconciled, starting at 0.

```
GET /v1/changes?since=<cursor>&limit=500
-> { "ops": [...], "high_water": 1234, "has_more": false }
```

Apply the ops, then set your cursor to the last returned `seq`, or to
`high_water` when the page is empty. While `has_more` is true, request
the next page immediately. The page, `high_water` and the compaction
check are read from one snapshot, so a page you were not told to resync
from has no holes in it. A `since` that is not a non-negative integer
is `400`, not a replay from zero.

If the server answers `410 {"error": "resync_required"}`, your cursor
fell behind the compaction horizon. Fetch `GET /v1/heads`, rebuild your
local state from those heads, set your cursor to its `snapshot_seq`, and
resume normal delta sync.

A work can also disappear from under a cursor: a reader deleting a work
(ADR-0024) removes its ops without a tombstone in this feed. A client
that holds ops for a work the server no longer knows learns so on its
next push (`unknown_work`), not on pull.

### Conflicts

The server orders ops; it never picks a winner. Reconcile locally with a
three-way merge: your current position, the newest op from other
devices, and the last op you acknowledged as baseline. Liseur's
implementation (`domain/ReadingStateMerge.kt`) is the reference; the
protocol is shaped so that logic transfers unchanged.

The merge is only half of what makes two clients land on the same page.
Liseur also pushes every persisted page turn as it happens, queues a
backstop push when the reader leaves the book, and on open or resume
*offers* a place another device has read further to rather than
jumping there. A client that does the same converges with it; one that
pushes only on close, or that takes the furthest position outright,
will be seen by Liseur as a device that keeps moving backwards. The
behaviour is written out, against Kindle Whispersync as the yardstick,
in [Liseur's ADR-0023](https://github.com/chmouel/liseur/blob/main/docs/adr/0023-position-sync-versus-whispersync.md).

## Annotations

Highlights, notes and bookmarks sync too (ADR-0028), and they are the
one *mutable* kind of reading state: an annotation is a record with a
stable client-chosen `id` and a server-held `rev`, and every write is a
compare-and-set against that rev. A create sends `base_rev: 0`; an edit
sends the rev you read. Like ops, they need only the `sync` scope, and
`device_id` comes from your token.

```json
POST /v1/annotations
{
  "annotations": [{
    "id": "018e6f20-…",
    "base_rev": 0,
    "work_id": "…",
    "edition_sha": "…",
    "kind": "highlight",
    "locator": { "href": "…", "locations": { "…": "…" } },
    "progression": 0.413,
    "excerpt": "the sea was calm",
    "color": "yellow",
    "client_ts": "2026-07-13T10:00:00Z"
  }]
}
-> { "results": [{ "id": "018e6f20-…", "status": "applied", "rev": 1, "seq": 7 }] }
```

The kinds carve up the fields: a **highlight** anchors to the text
(`locator` required) and may carry a `body`; a **bookmark** is an
anchor with no body; a **note** is a body with no anchor. `color` is a
token from a fixed palette (`yellow, green, blue, pink, purple,
orange`), highlights only, never CSS. Excerpts are capped at 1 KiB and
bodies at 16 KiB by default.

The rev dance:

- A byte-identical retry of your last write answers `duplicate` with
  the stored rev and seq — interrupted pushes are free to repeat.
- Any other stale `base_rev` answers a per-item `conflict` carrying the
  server's current copy. The server orders; it never merges. Show the
  user both, or take the server copy and reapply — your call, then push
  again from the new rev.
- The batch is never atomic: one bad item fails alone, whatever its
  problem — a shape error (bad kind, oversized field, a color off the
  palette) and a bad reference (unknown work, per-work cap) both come
  back as a per-item `invalid` with the reason. Only a request nothing
  in it can excuse is refused whole: malformed JSON, an empty or
  oversized batch (`400`), or a body larger than any legal batch could
  need (`413`).

Delete with the rev you read; a mismatch is a 409 with the server copy:

```
DELETE /v1/annotations/{id}?rev=3
-> { "id": "…", "status": "applied", "rev": 4, "seq": 21 }
```

Pull with a cursor, exactly like `/v1/changes` but on its own counter:

```
GET /v1/annotations/changes?since=<cursor>&limit=500
-> { "annotations": [...], "high_water": 42, "has_more": false }
```

This feed is *state*, not history: an edit moves the record to the head
with its current content, so key by `id` and let the highest rev win.
Deletes appear as tombstones — `id`, `rev`, `seq`, `deleted: true`,
`deleted_at` and nothing else. Tombstones are swept after 180 days by
default; a device offline longer than that must reconcile its local set
against `GET /v1/works/{id}/annotations` (the live set, sorted by
progression) rather than trust the delta feed alone — an annotation the
feed no longer mentions and the live set does not contain is gone.

Positions and annotations never mix: `/v1/changes` stays ops-only, and
annotation writes do not move the op high-water.

## Reading sessions

Report what you measured at the time of reading:

```json
POST /v1/sessions
{
  "sessions": [{
    "session_id": "018e6f20-…",
    "work_id": "…",
    "edition_sha": "…",
    "started_at": "…", "ended_at": "…",
    "start_progression": 0.401,
    "end_progression": 0.413,
    "idle_ms": 30000
  }]
}
```

The same rules as ops: `session_id` is the idempotency key, batch
freely, same id with another payload is a 409. Sessions are never
updated after acceptance. Raw immutable sessions are retained for 180
days by default, then reduced to daily totals. Insights remain complete;
raw session history beyond the retention horizon is intentionally not
available.

Do not send page numbers. The server derives pages from progression ×
the edition's page count, and speed from progression delta over active
time (duration minus `idle_ms`). If you only know pages, convert through
`page / total_pages` yourself and say so in the fractions.

## Insights

With a `read-insights` token:

- `GET /v1/insights/summary?from=2026-07-13&to=2026-08-11`: totals,
  streak, speed trend
- `GET /v1/insights/works?from=…&to=…`: aggregates for every work with
  reading history
- `GET /v1/insights/works/{id}?from=…&to=…`: per-work time, pace, ETA
- `GET /v1/insights/calendar?year=2026`: daily minutes for heatmaps
- `GET /v1/insights/calendar?from=2025-04-01&to=2026-03-31`: the same,
  over an arbitrary span

Every endpoint takes a span the same way: a `from`/`to` pair of
`YYYY-MM-DD` days, **both inclusive**, resolved in the user's configured
timezone. The older `range=Nd` (up to `3660d`) and `range=all` spellings
still work, and `Nd` now means the last N *calendar* days ending today
rather than N × 24 hours ending at the moment of the request — the
difference is a partial extra day at the far end, which no client could
reconcile against days it had counted for itself.

Prefer `from`/`to`. A count of days is resolved against the server's
clock; a pair of dates says exactly what was meant and comes back in the
answer.

The summary and the works endpoints name the span they covered:
`range_days` always, plus `from` and `to` when the span is bounded. The
calendar echoes `from`, `to` and `range_days` only when it honoured an
explicit `from`/`to` pair; a `year=` request comes back with `year` and
`days` alone. **Check it.** A server too old to
understand the parameters ignores them and answers about some other
span, and the aggregates give no sign of it: thirty days of reading and
ten years of it are both just a number of minutes. An answer that does
not name back the days you asked about is not an answer to your
question.

Check an unbounded request too, against `range_days == 0`. An older
server answers `range=all` with the ten-year horizon it used to apply,
and an older one still does not know the word at all — and the summary,
given nothing it understands, falls back on thirty days.

A span is optional on the works endpoints, where absent means everything
on record — which is what they answered before spans existed. The
summary defaults to `30d` instead, for the same reason. Say `range=all`
rather than leaving it out if you mean a lifetime. A `range` that cannot
be parsed is treated as absent, so a malformed span gets the endpoint's
default rather than its whole history.

Give the works endpoints the same span as the summary if the two are
shown on one screen: a headline covering thirty days above rows covering
a lifetime is a dashboard whose numbers cannot be added up.

Each work carries `title` and `author` when the server has them. They
are there so a client can list a work it holds no file for: reading done
on another device is in the totals whether it can be named or not, and a
list that silently omits it is smaller than the headline above it for no
reason the reader can see. Both are absent rather than empty when the
server has nothing to say, and a work you cannot name is better left out
of a list than shown blank.

`streak_days` is deliberately **not** narrowed by the span. It counts
consecutive days ending today or yesterday, looking back up to ten
years, so asking about the last week does not report a hundred-day run
as seven.

`range_days` reports how many days the answer covers, and is `0` for an
unbounded one.

A sitting that straddles the first morning of the span counts, whole, on
the day it *ended* — both in the summary and in the per-work rows, so
the two cannot disagree. The calendar splits such a sitting across the
midnight it crosses, because a day-by-day answer that did not would
attribute an hour to a day it was not read on.

The calendar additionally accepts `year`, and falls back to the current
year when no usable span is given — which is what makes the echo the
only way to tell an honoured request from an ignored one. A single
calendar may not span more than 4000 days; a longer one is refused with
`400` rather than served.

All day boundaries are computed in the user's configured timezone.
Rereading counts time but never negative pages. ETA is `null` until the
user has enough speed history on the work.

`current_progression` and `eta_seconds` are never narrowed by the span
either. Where the reader is in a book is true now, whatever span the
totals beside it cover.

`total_pages` will be `0` unless the work has a page count on its
edition, and nothing in the native API sets one. A reflowable EPUB has
no inherent number of pages, and one derived from a particular device's
font size would make the total depend on which device synced. Report
minutes and progression, which mean the same thing everywhere, and treat
pages as something you may not have.

## Browsing and downloading books

A server reflects folders the operator pointed it at. Start at `GET
/v1/folders`, then page through `GET /v1/folders/{folder}/books`. The
first response is:

```json
{
  "folders": [{
    "folder_id": "…",
    "name": "Shelf",
    "kind": "plain",
    "created_at": "2026-08-16T06:42:00Z"
  }],
  "next_after": "…"
}
```

`kind` is `plain` or `calibre`. In a plain folder, the directory tree is
the organisation: a subdirectory of EPUBs is a series. In a Calibre
library, the server reads `metadata.db` and keys books by Calibre's id,
not by path. `root_path` is never returned by the catalog API.

The book listing is cursor-paginated. It is oldest first by default;
`?order=recent` reads the same collection newest first. A cursor means
"where the last page ended", which is a different place in each order,
so do not carry one across a change of `order`.

Every route that returns books returns the same shape: the folder
listing, `GET /v1/folders/{folder}/search`, the entity listings and `GET
/v1/books/{id}` all hand back one `CatalogBook`. Detail is not a richer
one. Parse it once.

```json
{
  "book_id": "…",
  "folder_id": "…",
  "title": "Neuromancer",
  "status": "active",
  "sha256": "9797d5f3…",
  "size_bytes": 431109,
  "media_type": "application/epub+zip",
  "filename": "neuromancer.epub",
  "cover_url": "/v1/books/…/cover",
  "created_at": "…",
  "updated_at": "…",
  "contributors": [{"id": "…", "name": "William Gibson", "role": "author"}],
  "series": [{"id": "…", "name": "Sprawl", "position": 1, "source": "folder"}]
}
```

A book is one file, so `sha256`, `size_bytes`, `media_type` and
`filename` are top-level fields. There is no `files` array. Optional
metadata fields such as `subtitle`, `description`, `publisher` and
`published_date` are omitted when unknown. `contributors` and `series`
are always present, and empty when the book has none. Status is either
`active` or `missing`; a missing book stays in the catalog because a
disconnected disk is not a deleted book, but it is left out of the
listing and search responses, because fetching it would answer `410`.
It is still readable by id, so a client holding one can tell the
difference between a book that went away and a book that never existed.

`contributors` carries every credit in every role rather than only the
authors. Pick authors with `role == "author"`. `series` carries every
series the book is in; a `position` that nobody recorded is left out
rather than sent as `0`, which would read as "first". `source` is
`folder`, `shared` or `personal`: it tells you whether the membership
came from the last folder pass, an administrator's claim, or the
calling reader's own claim.

`sha256` is the digest of the file's content. It is what a client
holding local files matches against the catalog, and it is never the
address of anything inside the server. `size_bytes` is the length the
server last saw; treat a mismatch as "re-check", not as "different
book". The download is the truth.

`GET /v1/books/{id}/download` serves the file. It supports `HEAD`, byte
ranges and conditional requests. A `410` means the book is catalogued
but the last pass could not find its content; a `404` means there is no
such book. A `409` means the file behind the row changed on disk before
a pass reconciled it.

`GET /v1/books/{id}/cover` serves a JPEG cover. `?size=full` is sized
for a book page; the default, `thumbnail`, is sized for a grid;
`?size=icon` is a small square crop meant for a browser tab. Every
book record carries a `cover_url`, and it is offered whether or not the
book has one. A `404` is the answer for a book whose EPUB declares no
cover, whose Calibre cover cannot be read, or whose cover cannot be
decoded — except at `?size=icon`, which serves a drawn placeholder card
instead, since an icon asker needs an image rather than an answer.

All of these need a token with `library-read`.

### Finding a book

`GET /v1/folders/{folder}/search?q=left+hand` matches against everything
a book says about itself (title, subtitle, description, publisher, and
the names of the series, contributors and tags it claims) and returns
the best matches first. A book called Dune ranks above one that only
mentions it.

`q` is words, never index syntax. Punctuation and boolean operators are
split away rather than interpreted, so nothing you can type changes how
the query is read, and a query made only of punctuation matches nothing
instead of erroring. Case and diacritics are folded, so `emile` finds
`Émile`.

The answer is unpaged:

```json
{
  "books": [{"book_id": "…", "title": "The Left Hand of Darkness"}],
  "facets": [{"kind": "tag", "id": "…", "name": "Fantasy", "book_count": 1}],
  "truncated": false
}
```

`truncated` says the answer was cut at `limit` (100 at most). There is
no cursor on purpose: a relevance order has no stable one, and search
answers "where is that book", a question with a short answer. When you
see `truncated`, ask for a narrower query rather than another page.

`facets` describe the books actually returned, counted over that set
rather than over the folder. Pass any facet's `id` back as `?entity=` to
narrow, without saying what kind it is. Repeat `entity` to stack
filters; they are ANDed.

There is no reading-state filter. A catalog-only credential must not be
able to observe reading state, and the surest way to keep that true is
for this route to have no vocabulary for it.

### Browsing by series, contributor or tag

Entities are library-wide, not folder-scoped: one series held across two
folders is one entity with one id, and its shelf lists every folder's
volumes (ADR-0019).

`GET /v1/entities/{kind}` lists entities, where `kind`
is `series`, `contributors` or `tags`. `book_count` counts active books
only, so a name whose books are missing reads as empty rather than
leading to a blank page. Paging resumes after `next_after` rather than
at an offset, because an offset would skip or repeat entries as books
are added underneath it. A series may carry a reader's own name for it
(ADR-0020), so an entity also reports `scanned_name` and `name_source`;
listings page on the name the caller sees.

`GET /v1/entities/{kind}/{entity}/books` lists one
entity's books, from every folder. A series comes back in reading order, with books that
have no position last: an unplaced book is an unanswered question, not
book zero.

### Correcting series

Series metadata comes from the folder first, but a reader can state a
claim over it. There are two writable layers:

- `personal`: the default, visible only to the caller.
- `shared`: visible to everyone without a personal claim, and allowed
  only to tokens that also have `admin`.

The write routes need `library-manage`. Reading the catalog, including
the layers below, still needs `library-read`; the two scopes are
separate.

To show an editor what it is changing, read all layers:

```json
GET /v1/books/{id}/series
```

```json
{
  "book_id": "…",
  "source": "personal",
  "series": [{"id": "…", "name": "Foundation", "position": 2, "source": "personal"}],
  "folder": [{"id": "…", "name": "Sci-Fi", "source": "folder"}],
  "shared": null,
  "personal": [{"id": "…", "name": "Foundation", "position": 2, "source": "personal"}]
}
```

`series` is the effective answer, the same list every book payload
returns. `folder` is what the last reconcile pass saw. `shared` and
`personal` are `null` when that layer has no claim, and arrays when it
does. An empty array is meaningful: it claims "this book is in no
series". That is different from `null`, and it is how a client knows
whether a reset button has anything to clear.

Every catalog book also has `series_source`, the layer that supplied its
effective series list. It is present even when `series` is `[]`, so an
explicit empty personal or shared claim cannot look like the folder
layer. `series_claim_updated_at` is present only when that winning layer
is a claim; clients use it to order claim adoption without comparing
their clocks to the server's. The layer endpoint similarly returns
`shared_updated_at` and `personal_updated_at` for the most recent layer
mutation, including a deletion represented by `null`.

Set a claim by replacing the whole layer:

```json
PUT /v1/books/{id}/series
{
  "client_ts": "2026-08-17T12:00:00Z",
  "series": [
    {"name": "Foundation", "position": 2}
  ]
}
```

Each item names exactly one of `series_id` or `name`. A `series_id`
must already exist. A new `name` creates or reuses a series by the same
normalized-name folding the scanner uses. `position` is optional, but
if sent it must be a finite number.

`client_ts` is optional, but sync clients should generate it
deterministically for a local edit and reuse it on every retry. It is an
idempotency key, not a revision: a reused key with a different claim is
`409`, while the identical retry returns `"outcome": "duplicate"`.
`updated_at` is assigned by the server when it accepts a mutation and is
the revision returned to clients. Send that value as `if_updated_at` on a
later mutation; a different current revision returns `"outcome":
"stale"` without changing the claim. Omit it only when no claim revision
was observed. A successful write returns `"outcome": "applied"` plus
the current layers.

Revisions are millisecond-precise, and a precondition is compared at that
precision. A client may therefore keep one as milliseconds since the
epoch (which is all an integer column holds) and quote it back without
losing the match.

Leaving `series` out, or sending `"series": []`, is a claim that the
book is in no series. It is not a reset. To drop the claim and fall
back to the layer beneath, delete it:

```json
DELETE /v1/books/{id}/series
```

Pass the idempotency key as `?client_ts=2026-08-17T12:00:01Z` and the
last observed server revision as `if_updated_at`. Reuse the key exactly
when retrying. The server retains delete tombstones, so an old PUT cannot
revive a deleted claim without matching the tombstone revision.

Add `?scope=shared`, or send `"scope": "shared"` on `PUT`, only for the
shared layer. A non-admin token gets `403` even if it has
`library-manage`.

Drag-reordering a series is a bulk renumbering:

```json
PUT /v1/entities/series/{series}/order
{
  "order": [
    {"book_id": "…", "position": 1},
    {"book_id": "…", "position": 2}
  ]
}
```

It returns `204 No Content`. The order must be non-empty, may contain
at most 1000 books, and may not name the same book twice. The operation
is idempotent and preserves each book's other series memberships, so
renumbering a trilogy does not drop a volume from an omnibus. Only
`kind=series` is accepted; trying to reorder contributors or tags is
`404`.

### Renaming a series

A claim says which series a book is in; it cannot say what that series
is called. Renaming is a layer of its own, in the same two scopes
(ADR-0020):

```json
PUT /v1/entities/series/{series}/name
{"name": "Метро", "scope": "personal"}
```

It answers with the entity as it now reads: `name` is what this reader
sees, `scanned_name` is what the last scan called it, and `name_source`
is the layer `name` came from: `folder`, `shared` or `personal`. Those
last two are what a client needs to show a rename as a rename and to
offer a revert:

```
DELETE /v1/entities/series/{series}/name?scope=personal
```

The rename never touches the name a scan observed, and that name stays
the one a folder pass matches against. A renamed series therefore keeps
absorbing a folder that goes on calling it by its old name, and a
rescan does not undo the rename. Only `kind=series` can be renamed; a
tag or a contributor is what the scan said it was.

A name that already belongs to another series in the caller's view is a
`409`. Giving two shelves one name would be a merge, which is a
different request (see below).

### Merging and splitting a series

A rename changes what a shelf is called. Merging and splitting change
which shelf a book is on, so they are admin work and everybody sees the
result (ADR-0021). Both need `library-manage` **and** `admin`; there is
no personal form, because a reader who wants their own arrangement
already has the personal claim layer.

Fold the shelf in the path into the one in the body:

```json
POST /v1/entities/series/{absorbed}/merge
{"into": "{survivor}"}
```

It answers with the survivor. Memberships and claims naming the
absorbed series follow it, a book that was on both shelves keeps the
survivor's position, and **nothing is renumbered**; two shelves that
each held a volume 1 still do. Reading state is untouched: a series
never appears in the op log, in a session or in a position.

Send one folder's books to a shelf of their own:

```json
POST /v1/entities/series/{series}/split
{"folder_id": "…", "name": "Essays (Montaigne)"}
```

This undoes the automatic fold that put two folders' identically named
series together (ADR-0019). Splitting a shelf whose books all came from
one folder is a rename, and is refused as one; splitting a single
folder's shelf in two (an omnibus directory holding two series) is a
per-book decision, so use a shared claim.

Neither is a plain database edit, because the name on disk is what a
folder pass resolves against: a shelf rearranged only in the database is
rearranged back by the next scan. Both therefore leave a **binding**
behind, recording that an observed name now means a given series, and
the resolver reads it first. A merge binds everywhere; a split binds
only in the folder that left.

```
GET    /v1/entities/series/{series}/bindings
DELETE /v1/entities/series/{series}/bindings/{binding}
```

The listing is what a client shows to offer an undo: each entry carries
the absorbed `name`, a `folder_id` that is `null` when the binding
applies to every folder, and when it was made. Deleting a binding moves
no book. The next pass over a folder that observes the freed name
resolves it to nothing, mints the series again and refills it from what
the folder says, so an unmerge restores what the disk holds, not what
readers claimed. A claim that named the absorbed series was repointed
by the merge and stays repointed.

### Syncing a book you downloaded from the catalog

Positions and sessions attach to a work, not to a catalog book, so a
reader that fetched a book from the catalog needs a `work_id` before it
can sync. Ask for one:

```json
POST /v1/books/{id}/resolve
{}
```

```json
{
  "book_id": "…",
  "work_id": "…",
  "confidence": "high",
  "created": true,
  "identifiers": [{"kind": "sha256", "value": "…"},
                  {"kind": "source", "value": "liseur-sync:…"}]
}
```

Send no identifiers of your own. The server reads them off the catalog,
which knows the file's digests and embedded ids even when you have only
browsed and not downloaded. It answers with the evidence it used, so you
can show a reader why two books were treated as one.

Call it again whenever you need the mapping. It is idempotent, returning
`200` and the same work instead of creating another one.

The mapping is yours alone. Two people reading one shared book get two
different `work_id`s, which is what stops one reader's position from
becoming the other's.

A `confidence: "low"` answer means the only thing that matched was the
title and author. Nothing was stored. Ask the reader whether it really
is the same book and repeat with `{"confirmed": true}` to accept it.

A `409` means the book's identifiers already point at more than one of
your works, and it lists them; nothing was changed. Repair it with `POST
/v1/works/merge` or `POST /v1/works/{id}/split`.

This route needs both `library-read` and `sync`, the only one that does:
it reads the catalog and writes your work graph. A catalog-only token
gets `403`, and so does a sync-only one.

## KOReader through OPDS

KOReader can browse the catalog with no plugin at all. Add an OPDS
catalog pointing at `https://<host>/opds/v1.2`, with the username
`token` and, as the password, a device token secret carrying `library-read`.

The account password will not work, and that is deliberate: a reader
keeps its catalog credential in plain text on the device, so it gets a
credential that can only read books and that can be revoked on its own.
Create one per device.

The root feed lists the folders that credential can read; each links to
an acquisition feed of books, and each book to its EPUB. Feeds are
catalog-only; they never expose positions, sessions or statistics, even
if the same token also carries `sync`. Pair OPDS with the kosync adapter
to get downloads and position sync on a stock device.

A folder feed also carries a `http://opds-spec.org/sort/new` link to its
newest books, a `search` link to an OpenSearch description, and
`http://opds-spec.org/facet` links to its series, contributors and tags.
A reader discovers them from the feed it already has; neither URL needs
configuring. The search template offers `searchTerms` and nothing else,
which is how reading state stays off this surface even for a token that
could ask about it elsewhere.

Browsing a series gives its books in reading order, with unplaced ones
last. Search results are unpaged and carry no `next` link: the best
matches are already there, and more of them is a better query rather
than another page.

## KOReader without a liseur-sync plugin

Stock KOReader devices can join through the kosync adapter instead of
this API: the user generates a pairing code in the web UI or with
`liseur-sync admin pairing-code`, then sets kosync's server to
`https://<host>/adapter/kosync`, username to a device name, password to
the pairing code. The client hashes that password itself — KOReader
sends `md5(password)` to `users/create` and reuses it as `x-auth-key` —
so the code is typed in once and never transmitted. Their positions
land in the same per-user op log with
`origin: "kosync"`, so a native client sees them through `/v1/changes`
like any other device; the xpointer rides in `foreign_pos` and
round-trips verbatim to kosync pulls.

## Errors and limits

- Every error is `{"error": "reason"}` with a 4xx; a 5xx is a server
  bug, report it. A refusal a client can recover from also carries a
  machine-readable `code`, currently only `unknown_work` (see
  [Pushing](#pushing)).
- Batch limits: 500 ops, 1000 sessions per request, 1
  MiB body, 16 KiB per `locator`.
- Auth endpoints are rate-limited per
  IP (429 + `Retry-After`).
- Credential traffic requires HTTPS; on
  plain HTTP the server answers 403 unless the instance explicitly sets
  `insecure_http`.
