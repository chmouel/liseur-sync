# ADR-0028: Annotations are mutable reading state, not history

- **Status:** Accepted
- **Date:** 2026-08-25
- **Depends on:** [ADR-0003](0003-catalog-work-identity.md),
  [ADR-0007](0007-web-reader.md),
  [ADR-0024](0024-deleting-a-work.md)
- **Amends:** section 11 of [DESIGN.md](../DESIGN.md), which listed
  annotation sync as future work. It does **not** amend the append-only
  rule for ops and sessions; annotations sit beside that rule, not
  inside it.

## Context

DESIGN.md left one sentence where this decision now stands: "Liseur has
annotations; syncing them wants the same op-log shape but its own
conflict semantics." The web reader's ADR made the matching promise —
no annotations, but a locator envelope chosen so they would be an
addition rather than a migration.

A reader who highlights a passage on a phone expects the highlight on
the desktop, and a note taken on the couch expects to be readable at
the desk. Today the server has no word for any of it: highlights live
in each Liseur client's local store and die with it.

The temptation is to reuse what already works. Positions sync as an
append-only op log with per-user monotonic `seq`, one round-trip delta,
and a hard rule that the same id with a different payload is a
conflict, never an overwrite. That shape is right for positions because
a position is an *observation*: a device reports where the reader was,
the report is a fact, and no later write may pretend it said something
else.

An annotation is not an observation. It is a small *document* the
reader edits — a highlight recolored, a note reworded, a bookmark
removed — and a log that forbids rewriting history forbids exactly the
operations an annotation exists for. Forcing annotations into `ops`
would either break the op invariants (`seq` never renumbered, heads
never compacted, same id + different payload is a conflict) or break
the annotations. So they get their own shape, which is what the design
doc predicted they would want.

## Decision

### One record, three kinds

An annotation is one per-user row against a work:

```
annotations   user_id, id, seq, rev, work_id, edition_sha?, kind,
              locator_json?, progression?, excerpt?, color?, body?,
              device_id, client_ts, updated_at, deleted_at?
```

- `id` is client-generated, opaque and deterministic — the `op_id`
  convention: the same local annotation must produce the same id on
  retry.
- `kind` is `highlight`, `note` or `bookmark`. A highlight anchors to a
  range and may carry a `body` — that body *is* the attached note, not
  a second record. A standalone `note` is a body with no anchor. A
  `bookmark` is an anchor with no body. One table, one API shape; kind
  is a column, not a schema.
- `locator_json` is the same opaque envelope as `ops.locator_json`:
  Readium Locator JSON (or whatever the client's engine emits inside
  it), never parsed by the server, replayed verbatim. A `highlight` or
  `bookmark` requires it; a standalone `note` has none.
- `progression` (0..1) is the one anchored field the server itself
  reads, and only to sort a list. No page number is ever fabricated
  from it.
- `excerpt` is the selected text a highlight shows in a list, bounded
  and optional. `color` is one token from a small fixed palette the
  server validates — never an arbitrary string a page would hand to
  CSS.
- `edition_sha` is optional and carries the same deferrable composite
  FK as an op's. Anchors are made against an edition but belong to the
  work: a Calibre book whose bytes changed keeps its readers
  (ADR-0022), and it keeps their highlights the same way — the
  annotation stays on the work, and a locator that no longer resolves
  in the new edition degrades to its `progression`. A work split or
  merge (ADR-0003) reassigns annotations in the same transaction as
  everything else it moves: in a split one with an `edition_sha`
  follows its edition and one without stays with the surviving work;
  in a merge all of them follow to the survivor. A moved annotation is
  a write like any other — new `seq`, `rev` incremented — so every
  synced device learns its new `work_id` on the next pull.
- A highlight's locator must describe a *range*, not a point; that is
  the client's obligation, since the server never parses the envelope.
  The web reader renders the envelopes it understands and degrades the
  rest to a list entry at `progression` — best-effort display, never
  an error.

The handler edge bounds every field — `excerpt` at 1 KiB, `body` at
16 KiB, batches at 100 items, and the whole request body capped before
any of it is parsed — but the cap of 2,000 live annotations
per user per work is an invariant only the store can hold, so it is
enforced inside the write transaction, where two concurrent creates
cannot both squeeze under it. Each bound is configurable, each refusal
a precise 4xx, nothing truncated silently.

### State, not history

Annotations are the third kind of reading state, and the first mutable
one. The append-only rule is untouched because it never applied here:
that rule is about *reports* not being edited, and an annotation is not
a report, it is the reader's own artifact, theirs to change.

Concurrency is compare-and-set, the shape series claims already use
(ADR-0018): every record carries a server-assigned `rev`, incremented
on every accepted write, and a push carries the `rev` the client last
saw. A mismatch is `409` carrying the server's current copy, and the
client resolves — the server orders, it never merges, and `client_ts`
never decides a winner; it is display metadata only. A retry of an
accepted write (same id, same base `rev`, byte-identical payload — the
comparison ops already do) is acknowledged, not conflicted, so a lost
response is harmless.

### Deletion is a tombstone, bounded

ADR-0024 deliberately gave work deletion no tombstone, and accepted
that a still-syncing device sends the book back. That trade is wrong
here: deleting one highlight is an everyday edit, not a rare act of
forgetting, and a deletion other devices silently undo would make the
feature untrustworthy.

So a deleted annotation keeps its row as a tombstone — `deleted_at`
set, everything but identity, `rev` and `seq` cleared (no body, no
excerpt, no locator, no progression, no color) — long enough for every
device to learn of it, and is swept after a configurable window
(default 180 days, the op-compaction default). Deletion is a write like
any other: it carries the expected `rev`, and a stale delete is `409`
with the server copy, so an offline device cannot delete an edit it
never saw. After the sweep the id is simply unknown: a device offline
longer than the window that pushes it again creates a new record, `rev`
starting over at 1. That is accepted and stated, the same honesty
ADR-0024 applies to a deleted work.

`DeleteWork` is unchanged in shape: the unit is still the whole work
graph, which now includes its annotations and their tombstones, gone in
the same transaction by the same cascade, with no tombstone for the
work itself.

### A second sequence, not a wider op log

Delta sync reuses the mechanism, not the log. Each accepted write —
create, edit, tombstone — stamps the record with the next value of a
per-user annotation counter, a second counter added beside
`seq_counters`' op counter and never shared with it. The counter is
advanced *inside* the write's transaction, holding the counter row, so
`seq` order is commit order per user and a cursor can never advance
past a write that has not landed yet — the same serialization the op
counter already relies on. A pull is one round trip:

```
GET /v1/annotations/changes?since=<seq>&limit=500
```

returning records (tombstones included) whose `seq` is newer, ordered
by `seq`, with `has_more` and the high-water mark — the same page
contract as `/v1/changes`: a client advances `since` to the last `seq`
it received, and to the high-water mark only on an empty page. Because
this is a *state* feed, an edited record moves to the head of the
stream with its current content — there is no history to replay, and a
record edited while a client pages may appear again on the next pull,
which is harmless: the client keys by id and the newest `rev` wins.

`/v1/changes` remains positions-only. The op log's invariants —
gap-free per-user `seq` under concurrency, never renumbered, heads
never compacted — are not widened, not shared, and not at risk from a
record type that moves.

### The API surface

- `POST /v1/annotations` — batched push, at most 100 items, never
  atomic: the response is `200` with one result per item — the
  assigned `rev` and `seq`, `already accepted`, `conflict` with the
  server copy, or `invalid` with the reason. One bad item fails alone.
- `GET /v1/annotations/changes` — the delta pull above.
- `GET /v1/works/{id}/annotations` — the live set for one work,
  ordered by `progression` then `client_ts`.
- `DELETE /v1/annotations/{id}?rev=N` — writes the tombstone iff `rev`
  matches, `409` with the server copy otherwise; deleting a tombstone
  is `already accepted`.

Every route is authenticated and added to the scope table in
`internal/api/routes.go` under the `sync` scope: annotations are
reading state, they travel with positions, and the reader token
(ADR-0007) already carries exactly what they need. All state is scoped
by `user_id` — no route, ever, returns another reader's annotations,
and the catalog surface gains no annotation vocabulary: annotations
hang off works through the same private bridge as every other piece of
reading state. Errors are JSON `{"error": ...}` with a precise 4xx; no
redirects.

### The web UI reads, it does not yet write

The reader fetches the work's annotations with the derived token it
already holds and renders them with the engine it already ships:
foliate-js's overlayer and `addAnnotation` are in the vendored tree
today, unused. Highlights whose envelopes the reader understands draw
over the text; the rest, and every bookmark, list in the sidebar at
their `progression`; a work's page in the library gains an annotations
panel — excerpts, notes, each linking into the reader through a book
the viewer's folder grants can actually reach (ADR-0027), and listing
without a link when none can.

Excerpts and bodies enter the page as text nodes, never as markup — a
body is plain text to this server and to this UI, whatever a client
renders it as locally — and the reader's CSP and sandbox posture do
not change: an annotation is reader-authored content, but it is
*displayed* under the same rules as publisher content, which is to say
inert. A highlight's color reaches the overlayer as a palette token
the server already validated, never as raw CSS.

Creating and editing from the web UI is explicitly not in this ADR's
committed scope. It is a natural later phase — the API will already
accept it — but the web reader earns writing after viewing works, not
before.

### Explicitly out

- **Adapters.** kosync does not carry annotations and the koplugin
  protocol as spoken here does not either; no import is designed, and
  adapters continue to write native records only — none of which are
  annotations.
- **Sharing.** No cross-user reads, no public highlights. The design
  doc's exclusion of social features stands.
- **Export** (Markdown, CSV) and **full-text search over notes**: both
  plausible, neither designed here.
- **E2E mode** interaction is noted, not built: `locator_json`,
  `excerpt` and `body` are already opaque to the server, so client-side
  encryption remains additive, at the cost of the web UI's view.

## Consequences

- A third store surface with its own invariants: mutable rows, `rev`
  conflicts, tombstones with a sweep — none of which may leak into ops
  or sessions, and the shared storetest suite must prove both backends
  agree on all of it.
- `seq_counters` grows a second per-user counter (a new appended
  migration). The property test for gap-free op `seq` keeps passing
  untouched; the annotation counter gets its own concurrent-push test
  with the weaker guarantee it needs (monotonic, unique — gaps are
  harmless in a state feed).
- The reader gains its first per-work fetch beyond the book itself; the
  work page gains one panel; neither gains a write.
- Liseur clients get the sync their annotations wanted, in the envelope
  shape they already emit.

## Implementation phases

1. **Store.** The `annotations` table in both backends, the second
   per-user counter, push/pull/list/delete store methods, tombstone
   sweep, split/merge reassignment, `DeleteWork` cascade — with the
   storetest suite covering idempotent retry, rev conflict, tombstone
   delta, sweep, a moved annotation resurfacing in the feed, and
   cross-user isolation.
2. **API.** The four routes, the scope-table entries, handler-edge
   bounds, `docs/openapi.yaml` and `docs/integrating.md` in the same
   commit, and scope-matrix and tenant-isolation tests extended.
3. **Web UI, view-only.** Reader rendering through the vendored
   overlayer, the bookmark sidebar, the work-page panel, and the
   browser check extended to prove a highlight actually draws.
4. **Later, each its own decision:** web UI editing, KOReader import,
   export formats.

## Acceptance criteria

- The same annotation pushed twice is stored once; a push or a delete
  with a stale `rev` returns `409` and the server copy, and changes
  nothing.
- A deleted annotation stays deleted on every device that syncs within
  the window, and its tombstone carries nothing but identity, `rev`,
  `seq` and when.
- A pull since a cursor misses no committed write, returns records in
  `seq` order with tombstones included and a new high-water mark, and a
  record re-sent because it changed mid-pull converges by `rev`.
- Two concurrent creates cannot push a work past its annotation cap.
- One reader's annotations are invisible to every other reader on every
  route, including the changes feed and the work listing.
- Op `seq` behaviour is byte-for-byte unaffected: the gap-free property
  test passes unmodified, and `/v1/changes` never mentions annotations.
- `DeleteWork` removes the work's annotations and tombstones in the
  same transaction as everything else.
- A highlight whose envelope the web reader understands draws over the
  right text; one it does not — or whose locator no longer resolves —
  degrades to a list entry at its progression rather than an error.
- Oversized bodies, excerpts, batches and per-work counts are refused
  with a 4xx; malformed input never produces a 5xx.
