# ADR-0041: What the server keeps of a reading life

**Status:** Deferred
**Scope:** Later

## Context

A sitting arrives as a row in `sessions` and stays one for a long time.
The rollup job runs hourly, but it only looks at sittings that ended
before the retention horizon — `ops.retention_days`, 180 by default — so
a row lives untouched for half a year. When its turn comes the job folds
it into a `session_rollups_v2` bucket — one row per work, per day, per
timezone — writes a `session_tombstones` row as proof it was counted, and
deletes the raw row. That is already a compaction, and it is the only one
sittings get. Everything on either side of it grows without bound:

- **`sessions`** holds six months of raw rows at any time, which is the
  intended cost of letting a device re-offer a sitting. It also holds
  koplugin sittings permanently: see below.
- **`session_rollups_v2`** gains a row for every work the reader touched
  on every day they touched it. A reader with a book on the go every day
  adds 365 rows a year; a reader who dips into several adds a multiple of
  that. Nothing ever removes one.
- **`session_tombstones`** gains a row per *sitting*, forever, and a
  sitting can be a few minutes. This is the larger of the two by an order
  of magnitude, and it is the one nobody looks at: it exists so that a
  device re-uploading a sitting the server already counted is recognised
  rather than counted twice.
- **koplugin** sittings arrive at whatever granularity KOReader recorded
  them, which can be one per page turn on a device that suspends often.
  They never reach the rollup at all: `SessionsEndedBefore` selects
  `source_key IS NULL`, and the koplugin adapter sets one. So they are
  not what inflates the tombstone table — they and their
  `session_supersessions` rows stay raw in `sessions` for the life of the
  account, and the only compaction the server has does not apply to them.
  Whether that exclusion is deliberate (a superseded sitting is mutable,
  and the rollup writes a number nothing revisits) or merely untouched is
  itself part of what this record has to settle.

None of this is a problem at the scale liseur-sync runs at today, and
that is precisely why it should be decided now rather than when it is.
The question is not "is this too big" but "what has this server promised
to be able to tell the reader, and for how long", because the answer
constrains what may ever be thrown away.

There is a second pressure. `StatisticsSnapshot` is asked for a list of
candidate session ids and answers with the rollups, the archived proofs
and the live sittings that bear on them. The Android client asks for a
window — this month, this year — but the snapshot is not window-scoped
in storage terms: a long-lived account makes every stats read walk more
rows than the answer needs, and the streak, which is deliberately counted
over the whole history rather than the window, is the reason a naive
window-scoped read would be wrong.

## Decision

*Nothing here is decided yet. This records the shape of the decision and
the constraints any answer has to respect.*

### What must never be lost

**The streak.** `ReadingStats.streakDays` is counted over the whole of a
reader's history on purpose, and the client documents that the window
narrows every figure except this one. Any retention rule must therefore
keep enough to answer "did this reader read on day D" for every D since
the account began. That is a *set of days*, not a set of sittings, and it
is very small: a bit per day, or a row per day, for the life of the
account.

**The proof that a sitting was counted.** Not the same as keeping the
sitting, and it answers more than one question. The first is *have I
already counted this id?*, which only has to hold for as long as some
device might re-offer that id — a window bounded by how long a device
keeps an unacknowledged sitting, not by the life of the account. The
second is the one that constrains a horizon much harder: the snapshot
endpoint reads a tombstone's fingerprint, timezone, work and day, and
its exact archived contribution, to prove how much of a candidate the
server already holds and subtract it (`insights_snapshot.go:272-300`).
That is what makes a combined figure *server + local - overlap* rather
than a guess. Sweep a proof and the reader does not get a smaller number
with a warning; they get an overlap the server cannot demonstrate, which
is either an answer marked incomplete or the same reading counted twice.

So a tombstone horizon must cover both: the re-offer window *and* the
span over which any client may still ask for a combined answer, which is
the whole of the history the stats screen offers. A horizon shorter than
that is a decision to degrade combined figures for old periods, and it
has to be taken deliberately rather than fall out of a storage rule.

### The three candidates, and what each costs

**Compacting koplugin sittings at ingestion.** Merge adjacent sittings
from the same device and work that are separated by less than some gap,
as they arrive, so `sessions` grows at the rate of *reading* rather than
the rate of *page turns*. Since these rows never reach the rollup, this
is the only lever there is on them short of admitting them to it.
Cheapest by far, and the only one of the three that reduces what is
written rather than what is kept. Its cost is that `uploaded_at` is not
overlap proof and merging changes session ids, so the merged sitting
needs an id derived from its parts or the device will re-offer the parts
it still holds. This interacts directly with ADR-0039's refusal handling.

**Aging tombstones out past a horizon.** Drop tombstones older than some
period. The risk is exactly the resurrection problem the annotation
reconcile phase exists to prevent: a device that was offline longer than
the horizon re-offers a sitting whose proof has been swept, and it is
counted twice. The overlap obligation above binds tighter still, so a
horizon has to clear both the longest plausible offline stretch and the
oldest period a client may ask for a combined answer about. It must be
announced to clients rather than assumed, and the client must be able to
tell a swept proof from an unknown one. Of the three candidates this is
the one whose cost is least contained by storage reasoning alone.

**Coarsening old rollups.** Fold per-work daily buckets older than some
period into per-day totals, losing which book was read but keeping how
much and when. This preserves the streak exactly and collapses the row
count for a heavy reader. Its cost is that the per-book history the stats
screen shows becomes truncated at that boundary, which is a visible
product decision and not a storage one.

### The snapshot

Whatever is decided about retention, `StatisticsSnapshot` should learn
the window the caller actually wants, with the streak answered from a
separate and deliberately cheap read over the day set rather than over
the rollups. That is a change worth making on its own merits and does
not depend on any retention rule.

## Consequences

Deferring costs nothing today and is the reason to write this down now:
the server keeps everything, which is the only position that forecloses
no option. Every candidate below throws something away, and a thing
thrown away cannot be reconsidered.

What deferring does cost is that koplugin accounts keep accumulating raw
sittings with no compaction reaching them, so the longer the decision
waits the more of that history has to be handled by whatever is chosen.

## Implementation and acceptance

Nothing is implemented. Whichever candidate is taken, it is accepted only
when all of these hold:

- A reader's streak is identical before and after the rule runs, over the
  whole history of an account, proven by a test that builds a long day
  set and compacts it.
- A combined snapshot over any period the client offers still returns
  `complete: true` where it did before, or the narrowing is deliberate,
  documented, and visible to the client rather than silent.
- A device re-offering a sitting from beyond the horizon is recognised as
  a swept proof rather than as an unknown id, and the client can tell the
  two apart.
- The horizon is in the capabilities document, not a server constant a
  client has to guess.
- The rule is covered by the shared backend suite, so SQLite and
  PostgreSQL cannot drift on what they discard.

## Open questions

- Is the per-book history past some age something a reader would miss, or
  is "how much, and when" enough? This decides whether coarsening is on
  the table at all.
- What is the longest offline stretch the server should be obliged to
  recognise a re-offered sitting across, and should that number be
  advertised in the capabilities document?
- Should koplugin compaction happen at ingestion or at rollup? At
  ingestion it reduces storage immediately but makes refusals harder to
  attribute to a sitting the device recognises.
- Does an account with no reading in a period need anything kept for it
  at all, or can a gap simply be absence?
