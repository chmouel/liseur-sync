# ADR-0067: Additional explicit reading choices

- **Status:** Accepted
- **Date:** 2026-11-16
- **Depends on:** [ADR-0066](0066-explicit-reading-status.md)

Readers can explicitly choose `want_to_read`, `reading`, `paused`, or
`did_not_finish`, in addition to the choices in ADR-0066. The newest act
by server sequence wins over the position. These choices remain in effect
when a position arrives, including when a reader opens or continues a
paused or unfinished book. `automatic` withdraws the choice and resumes
position-based resolution. An act changes neither the locator, sessions,
nor furthest position; the existing unread action still resets position.

## The accepted values

`reading_status` on a status act is one of, and only one of:

```
want_to_read    reading    paused    did_not_finish
finished        unread     automatic
```

`finished`, `unread` and `automatic` are ADR-0066's; the first four are
this ADR's. Any other value is `400` with `code: bad_value`, and the
refusal fails the whole batch rather than storing a neighbouring status.
The values reach no column that needs a new one: `ops.reading_status` is
already free text validated at the edge, so the enum widens without a
migration.

## Resolution order

Unchanged from ADR-0066, with the new choices in the same place:

```
newest status act by seq, then the newest position op:
  want_to_read -> want_to_read
  reading      -> reading
  paused       -> paused
  did_not_finish -> did_not_finish
  finished     -> finished
  unread       -> unread
  automatic    -> derive          # the choice is withdrawn
  no choice    -> derive

derive(no position or <= 0) -> ready_to_read
derive(partial coverage)    -> reading
derive(>= 0.97)             -> finished
derive(otherwise)           -> reading
```

An explicit choice outranks the position in both directions: `finished`
at `0.4` is finished, and `did_not_finish` at `0.99` is not. Coverage
caps what a *position* may claim, never an explicit `finished`, which is
a statement about the book rather than about the recording.

## A choice is not a move

A status act carries the sender's own current `progression` and is
stored, but no position query reads it: furthest ranking,
`/v1/works/{id}/positions`, the newest position in `/v1/heads`, the
statistics' `current_progression` and session inference look at
`kind = 'position'` only. Telling this server that a book is paused does
not move the reader's place in it, cannot resurrect a stale position, and
is not evidence that reading happened.

## The shelf each value belongs to

One decision, in `internal/reading`, so the web library, the OPDS feeds
and series "next up" cannot disagree:

| status | unread shelf | next up |
|---|---|---|
| `ready_to_read` (derived, no position) | yes | yes |
| `unread` | yes | yes |
| `want_to_read` | yes | yes |
| `reading` | no | yes |
| `paused` | no | no |
| `did_not_finish` | no | no |
| `finished` | no | no |

A book waiting to be read is *unstarted*, so it belongs on the unread
shelf beside `unread` and `ready_to_read` — otherwise it is neither
started nor unread and disappears from every list. A paused or
unfinished book is the opposite: the reader said "not now" or "never",
so offering it as the next thing to open would be arguing with them. It
keeps its own shelf, and its own chip, and nothing else claims it.

## Rollout

The wire shape is additive, but the enum is not: today's released client
cannot *parse* these four values. It is not a client that stores an
unknown choice and leaves it alone — an unrecognised value on a status
op is dropped, and a client that substitutes another status overwrites
what the reader said on the next pull. So:

- **Ship client support first.** The server may emit these values for an
  account only once every client that account syncs has been upgraded.
  Until then the reader keeps the three ADR-0066 choices, which every
  released client understands.
- **A client must never substitute another status for one it cannot
  read.** Ignoring the op is recoverable; writing back a guess is not,
  because the guess becomes the newest act and wins by `seq`.
- **An older server refuses these writes**, with a `400` on the whole
  batch and `code: bad_value`. That is recoverable: nothing was
  appended, so the reader's existing choice is untouched and the client
  can retry the older three values, or wait for the server upgrade.
  Deploy the server before the clients that write the new values.
- **No migration is needed**, and none is written. `ops.reading_status`
  is already free text validated at the edge, so the column holds these
  values unchanged; a server that predates them still reads the op and
  derives its status from the position instead of failing on it.

These statuses are private to the reader like all status acts. Other
providers remain limited to what their own protocols can represent.
