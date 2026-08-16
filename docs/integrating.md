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
- **Folder**: a watched server-side directory of books. It is shared by
  every logged-in user, and its filesystem path is never returned to a
  non-admin client.
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

The scopes are `sync`, `read-insights`, `library-read`,
`library-manage`, and `admin`. `admin` implies every scope and is not
self-grantable: requesting it returns `403` unless the account is
already an administrator. Clients normally need only `sync`; add
`library-read` to browse and download books, `read-insights` to show
statistics, and `library-manage` only when the client lets readers state
series claims. `library-manage` does not grant catalog reads, so a
series editor usually asks for both `library-read` and `library-manage`.

All API calls then use `Authorization: Bearer <secret>`.

### Finding out what your token can do

A client is usually handed a secret it did not mint — pasted into a
settings field, copied from another device, restored from a backup — and
has no idea what it is allowed to do. Ask:

```
GET /v1/token
```

```json
{
  "id": "tok_7f3a",
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

The route needs no particular scope — the narrowest token can ask about
itself — but it is still authenticated: an absent, revoked or expired
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
a different position; the server rejects it as a conflict.

`progression` is the lingua franca every device understands. `locator`
is your reader's exact position — a Readium locator, a CFI, whatever
your engine uses. The server stores and replays it verbatim and never
reads it.

### Pulling

Keep a cursor: the highest `seq` you have reconciled, starting at 0.

```
GET /v1/changes?since=<cursor>&limit=500
→ { "ops": [...], "high_water": 1234, "has_more": false }
```

Apply the ops, then set your cursor to the last returned `seq`, or to
`high_water` when the page is empty. While `has_more` is true, request
the next page immediately.

If the server answers `410 {"error": "resync_required"}`, your cursor
fell behind the compaction horizon. Fetch `GET /v1/heads`, rebuild your
local state from those heads, set your cursor to `snapshot_seq`, and
resume normal delta sync.

### Conflicts

The server orders ops; it never picks a winner. Reconcile locally with a
three-way merge: your current position, the newest op from other
devices, and the last op you acknowledged as baseline. Liseur's
implementation (`domain/ReadingStateMerge.kt`) is the reference; the
protocol is shaped so that logic transfers unchanged.

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

- `GET /v1/insights/summary?range=30d` — totals, streak, speed trend
- `GET /v1/insights/works` — aggregates for every work with reading
  history
- `GET /v1/insights/works/{id}` — per-work time, pace, ETA
- `GET /v1/insights/calendar?year=2026` — daily minutes for heatmaps

All day boundaries are computed in the user's configured timezone.
Rereading counts time but never negative pages. ETA is `null` until the
user has enough speed history on the work.

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
disconnected disk is not a deleted book.

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
for a book page; the default, `thumbnail`, is sized for a grid. Every
book record carries a `cover_url`, and it is offered whether or not the
book has one. A `404` is the answer for a book whose EPUB declares no
cover, whose Calibre cover cannot be read, or whose cover cannot be
decoded.

All of these need a token with `library-read`.

### Finding a book

`GET /v1/folders/{folder}/search?q=left+hand` matches against everything
a book says about itself — title, subtitle, description, publisher, and
the names of the series, contributors and tags it claims — and returns
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
no cursor on purpose — a relevance order has no stable one, and search
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
are added underneath it.

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

Set a claim by replacing the whole layer:

```json
PUT /v1/books/{id}/series
{
  "series": [
    {"name": "Foundation", "position": 2}
  ]
}
```

Each item names exactly one of `series_id` or `name`. A `series_id`
must already exist. A new `name` creates or reuses a series by the same
normalized-name folding the scanner uses. `position` is optional, but
if sent it must be a finite number.

Leaving `series` out, or sending `"series": []`, is a claim that the
book is in no series. It is not a reset. To drop the claim and fall
back to the layer beneath, delete it:

```json
DELETE /v1/books/{id}/series
```

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
catalog-only — they never expose positions, sessions or statistics, even
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
the pairing code. Their positions land in the same per-user op log with
`origin: "kosync"`, so a native client sees them through `/v1/changes`
like any other device; the xpointer rides in `foreign_pos` and
round-trips verbatim to kosync pulls.

## Errors and limits

- Every error is `{"error": "reason"}` with a 4xx; a 5xx is a server
  bug, report it.
- Batch limits: 500 ops, 1000 sessions per request, 1
  MiB body, 16 KiB per `locator`.
- Auth endpoints are rate-limited per
  IP (429 + `Retry-After`).
- Credential traffic requires HTTPS; on
  plain HTTP the server answers 403 unless the instance explicitly sets
  `insecure_http`.
