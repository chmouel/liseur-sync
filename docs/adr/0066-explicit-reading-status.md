# ADR-0066: An explicit reading status, carried beside the position

- **Status:** Accepted
- **Date:** 2026-11-15
- **Depends on:** [ADR-0051](0051-furthest-position-survives-rereading.md),
  [ADR-0054](0054-forgetting-the-furthest-position.md),
  [ADR-0055](0055-following-an-audiobook-on-audiobookshelf.md),
  [ADR-0059](0059-deriving-a-position-across-protocols.md),
  [ADR-0034](0034-live-notifications-say-only-that-something-changed.md)

## Context

Whether a book is finished has always been *derived* here, from the
latest op's progression, against `0.999` in the web library, the OPDS
shelves, the insights snapshot and Audiobookshelf's push. Liseur derives
it from the same position against `0.97`, because a book rarely ends on
its last page. Two devices therefore disagree about the same book
without either of them being wrong about the position: a book at
`0.98` is finished on the phone and reading in the browser, and the
reading statistics of the same account count it twice, or not at all.

Deriving cannot fix that. A reader marking a book read at `0.4` — an
abandoned book, a story read in another edition, an anthology whose
last story they skipped — has said something the position cannot carry,
and the same is true of a reader putting a book back on the pile. Liseur
already stores that choice locally (`FinishedOverride`) and carries it to
KOReader, calibre-web and BookOrbit, but not to this server, so it does
not survive the next device.

The position also cannot say *how much of the narrative a recording
covers*. A French edition split in two volumes covers chapters 37 to 68,
and ADR-0055 caps every position mapped from such a recording at `0.998`
to keep it below this server's completion threshold. Liseur uses `0.97`,
so that cap protects nothing once the position reaches the phone: a
partial audiobook "finishes" the ebook.

## Decision

**Status is a fact, not a threshold.** The op log carries an explicit
choice beside the position, and every surface resolves a book's status
in one place per side: the explicit choice first, then automatic
completion from the newest position and the recording coverage that came
with it.

### The record

An op is one of two kinds:

```json
{ "op_id": "…", "work_id": "…", "client_ts": "…", "progression": 0.4137, "locator": {…} }
{ "op_id": "…", "work_id": "…", "client_ts": "…", "progression": 0.4137,
  "kind": "status", "reading_status": "finished" }
```

- `kind` is absent for a position op and `"status"` for a status act.
  No other value is accepted.
- `reading_status` is `"finished"`, `"unread"` or `"automatic"`, and is
  present exactly when `kind` is `"status"`.
  - `finished`: the reader says the book is read, wherever the position is.
  - `unread`: the reader put it back on the pile. Liseur also resets the
    position to `0` when it says this; the server does not require that.
  - `automatic`: the reader withdrew an earlier choice — resuming reading
    does this — so the position decides again.
- A status op carries the sender's own current progression. It is
  required (the position column is not nullable and a status act is
  refused without one), it is stored, and **no position query reads
  it**: furthest ranking, `/positions`, heads' "newest position",
  statistics' `current_progression` and the session inference all look at
  `kind = 'position'` only. A status act is not a move.
- `coverage` is optional on a position op: `"full"` or `"partial"`. It
  reports whether the recording the position came from covers the whole
  narrative. Only an audiobook-origin writer sets it, and only from
  evidence it already has (`FullNarrative` in `audiomap.Mapping`).
  Absent means "unknown", which is every position recorded before this
  ADR and every position from an ebook.
- Idempotency is unchanged and now covers the new fields: the same
  `op_id` with a different `kind`, `reading_status` or `coverage` is a
  conflict, not a duplicate.

### Resolution

One rule, two implementations of the same rule (Go here, Kotlin in
Liseur), and the same threshold on both sides: `0.97`.

```
resolve(choice, position, partialCoverage):
    finished      -> finished
    unread        -> unread
    automatic     -> derive
    no choice     -> derive

derive(position, partialCoverage):
    no position       -> ready_to_read
    position <= 0     -> ready_to_read
    partialCoverage   -> reading        # a partial recording never finishes the ebook
    position >= 0.97  -> finished
    otherwise         -> reading
```

`choice` is the `reading_status` of the newest status op for the work by
`seq` (the server assigns it, so the order is total and needs no clock);
`position` and `partialCoverage` come from the newest position op for the
work, also by `seq`. A status act before any position op still resolves:
`unread` is unread, `finished` is finished.

Threshold alignment. This server's `0.999` becomes `0.97`, the number
Liseur already uses for both "finished" and "not worth resuming", so the
two sides cannot drift. The `0.998` cap in `audiomap` stays as a second
line of defence for clients that ignore `coverage`.

### Conflicts

The server orders, clients decide — the rule ADR-0051 already set for
positions. For status:

- **An act the server has not acknowledged is sent afterwards and wins**,
  because it gets a higher `seq`. Two devices marking the same book
  differently while one is offline end with the offline act winning,
  which is what the reader last did.
- **Two acts both already appended**: the higher `seq` is the choice, and
  the losing device adopts it on its next pull rather than re-sending its
  own. A status act is not retried after it was acknowledged.
- A position op never clears or overrides a status act. Only `automatic`
  does, and that is itself an act: the client sends it when it withdraws
  a local choice (resuming reading), not when it merely has none.
- No device clock is consulted. `client_ts` stays what it is: a claim,
  part of the idempotency payload, never an ordering key.

### Wire and storage

- `POST /v1/ops`, `GET /v1/changes`, `GET /v1/heads` and
  `GET /v1/works/{id}/positions` gain `kind`, `reading_status` and
  `coverage` on their op objects. A client that does not know them sees a
  position op exactly as before: unknown JSON fields are ignored, and a
  status act's `progression` is the author's real position.
- `ops` gains `kind TEXT NOT NULL DEFAULT 'position'`,
  `reading_status TEXT` and `coverage TEXT`. Position queries filter on
  `kind`. Compaction keeps the newest position op *and* the newest status
  act per work and device — one head per kind, and one retained daily
  last-op snapshot per kind — so neither fact can hide the other from a
  resync; a work's furthest candidates remain position ops only. `heads`
  returns the same two heads per (work, device).
- The insights snapshot grows `status` on each work, resolved as above,
  and `covers_narrative` as the coverage the resolution used. The web
  library, the OPDS shelves and Liseur's server-only statistics rows all
  read that resolution instead of a threshold of their own.
- Nothing is rewritten: existing ops stay position ops with no choice and
  no coverage, so they keep the classification they had (ambiguity is
  preserved rather than guessed). Only new writes carry the new fields.

### What does not change

- Marking unread in Liseur still resets the local position to zero and
  still records the unread reset revision; the status act rides beside
  that reset, it does not replace it.
- Resuming reading still clears the local override, now by sending
  `automatic`.
- KOReader, the KOReader statistics plugin and CrossPoint exchange
  progress only; their status stays derived from the percentage they
  actually send. Nothing here claims their manual status is synced.
- The server's two-minute statistics filter is a product policy of its
  own and is untouched.

## Consequences

- A book marked read on a phone is finished in the browser, in the
  OPDS shelf, in the statistics and on the next phone, at whatever
  position it was marked.
- A book at `0.98` is finished on both sides, and the disagreement
  between `0.97` and `0.999` is gone.
- A partial recording cannot finish the ebook, on this server or on a
  device that reads `coverage`.
- A status act is one more op per act. A client must derive its `op_id`
  from its own status revision, not from its position revision, or two
  acts would collide and be refused as a conflict.
- Positions recorded before this ADR carry no coverage. A work whose
  recording is partial keeps its historical classification until a new
  position arrives; the classification is not rewritten backwards.
- `0.97` finishes more books than `0.999` did. That is the point, and it
  is the number the client has shipped for longer.

## Acceptance criteria

- Store suite, both backends: a status act is appended, idempotent,
  conflicting on a changed field, included in `changes`, recovered by
  `heads` and survives compaction; position queries (furthest, latest
  position, `/positions`, statistics) ignore it.
- Resolution tests: explicit finished at `0.4`; explicit unread above the
  threshold; `automatic` returning to the derived result; no op; a
  partial-coverage position at `0.998`; a full-coverage position at
  `0.998`; a position at `0.96` and at `0.97`; a historical position with
  no coverage.
- Conflict tests: offline act wins by `seq`; both-appended act loses and
  is adopted; a stale position op never changes the resolved status.
- Client tests: an explicit finished at low progress reaches a second
  device; unread then reading clears the override; retries and duplicate
  delivery; stale position after a newer status; 100% counted as
  finished in local and merged statistics; partial audiobook at `0.998`
  not finished; moved-book alias picks the library row's status.
- `openapi.yaml`, `integrating.md` and Liseur's `DEVELOPER.md` describe
  the fields, the resolution rule and the `0.97` alignment.
