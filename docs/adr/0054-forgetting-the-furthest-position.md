# 54. Forgetting the furthest position

Status: Accepted

## Context

[ADR-0051](0051-furthest-position-survives-rereading.md) keeps the
furthest position a reader has reached and counts navigation jumps. That
is right most of the time, but a slip of the finger on a progress bar can
record 98% in a book the reader is a third of the way through. From then
on every client offers a "furthest known position" that was never read,
and nothing short of deleting the work makes it go away.

The op log is append-only. Deleting or rewriting the peak operation would
break that rule, change what `/v1/changes` already delivered, and would
not be enough anyway: a second wrong peak, or the peak of another edition,
would simply take its place.

## Decision

A reader can forget a work's furthest position. Each work gets a
`furthest_floor`, a per-user op sequence that starts at 0. Furthest
candidates are ranked only among the work's operations whose `seq` is
greater than the floor. Forgetting raises the floor; it forgets every past
peak of that work for that reader, across all editions and origin aliases,
and reading after the forget counts again.

`POST /v1/works/{id}/furthest/forget` takes `{"upto_seq": N}` and answers
`{"furthest_floor": F}`. The server clamps `N` to the newest operation the
work holds, so a floor can never hide an operation not yet written, and
keeps the higher of the old floor and the new value, so the floor never
falls and a retry is harmless. It runs under the work-graph lock that
appends, compaction, splits and merges take. A floor that rises notifies
the `positions` topic like any other change to positions.

The per-work positions response adds `furthest_floor`, and the heads
snapshot and every `/v1/changes` page add `furthest_floors`, read in the
same transaction as the operations they describe. Forgetting appends no
operation, so a device already caught up on the feed would otherwise
never hear of the floor. The op log, the operations `/v1/changes`
returns, the latest position and the kosync adapter are unchanged.

Compaction stops pinning a forgotten peak: it is retained only while it is
still a head or a daily snapshot, like any other operation. A split work
inherits its source's floor, because the operations it takes keep their
sequence numbers. A merge keeps the higher of the two floors, so a peak
either side forgot stays forgotten.

## Consequences

There is no undo. A forgotten peak is still in the log until compaction
removes it, but no snapshot offers it again, and it cannot be restored
through the API. Merging can hide a peak the surviving work had not
forgotten when the absorbed work's floor is higher; that is the price of a
single floor per work and is the safer side to err on.

Clients that keep their own maxima have to drop the ones at or below the
floor, or they will offer the forgotten position again and may upload it
as a new peak. An older server without this route answers 404 or 405;
clients then keep their local behavior.

## Implementation phases

1. Server: migration adding `works.furthest_floor`, the floor filter in
   the furthest read and compaction, split and merge carry, the route and
   the snapshot fields. Done.
2. Android: a "forget" action next to the furthest known position that
   sends the newest known `seq` and drops local maxima at or below the
   returned floor.

## Acceptance criteria

- After forgetting, neither the per-work response nor the heads snapshot
  offers any earlier peak, and `ops` and `/v1/changes` still return every
  operation.
- A `/v1/changes` page with no new operations still carries the floor.
- An operation appended after the forget becomes the furthest position
  again.
- `upto_seq` above the newest operation is clamped; repeating the request
  or sending a lower value answers the same floor.
- Another account's work, or a missing one, is 404; a missing or negative
  `upto_seq` is 400.
- Compaction no longer retains a forgotten peak older than its cutoff.
- A split keeps the source floor and a merge keeps the higher one.
