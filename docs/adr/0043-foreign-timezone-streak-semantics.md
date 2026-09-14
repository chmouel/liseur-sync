# ADR-0043: Which day a sitting happened on

**Status:** Deferred
**Scope:** Later

## Context

A day is not a property of a moment. A sitting that ran from 23:50 to
00:10 happened on one day in Paris and a different one in New York, and
the streak — the number the stats screen is proudest of — is built
entirely out of that judgement.

The server resolves it with the account's timezone. `session_rollups_v2`
records the zone it used alongside the day, and its unique key is
`(user_id, work_id, day, timezone)`, so a bucket carries the assumption
that produced it rather than hiding it. `ApplyRollups` now re-reads the
account's zone inside its own transaction and refuses with `ErrConflict`
if it has moved since the batch was built, so a batch computed under one
zone is never filed under another.

That guard closes the race. It does not answer the two questions behind
it.

**What happens to the days already filed.** A reader who moves from Paris
to New York keeps every bucket that was computed in Paris. New buckets
are in New York. The streak is then counted across a history filed under
two different definitions of "day", and at the seam a single day can
appear twice or not at all. Nothing currently notices.

**Whose zone is it anyway.** The account's, which is a single value. But
reading happens on devices, and a device knows its own zone with more
authority than the account does — the Android client computes local
statistics in the device's zone, which is why `ReadingStats` takes a
`ZoneId` rather than reading a setting. So the same reading can be filed
under one day locally and a different day on the server, and the two
figures the stats screen puts side by side disagree for a reason the
reader cannot see.

There is a third thing, smaller but sharper: **the account's timezone is
now load-bearing for identity-adjacent behaviour**. Changing it
invalidates in-flight rollup batches and shifts what the server believes
about days already counted. It is no longer a display preference.

## Decision

*Not decided. The candidates and their consequences.*

### The seam

**Leave it.** Buckets keep the zone they were computed in; the streak
walks them in order and accepts that a move produces one odd day. Cheap,
and the error is bounded to the day of the move. Its cost is that the
oddity is invisible and unexplained.

**Recompute on a move.** When the account's zone changes, re-derive every
bucket under the new zone. Correct, expensive, and impossible for buckets
whose raw sittings were deleted — which is all of them past the rollup
horizon. The `timezone` column tells you what was assumed but not what to
re-derive from. This probably rules it out unless retention (ADR-0041)
keeps something finer.

**Record the move.** Keep a history of the account's zones with the dates
they applied from, and count the streak against whichever zone was in
force. This is the honest answer and the most work: it makes the zone a
time-varying property rather than a setting, and every day-attribution
question has to consult it.

### The device's zone versus the account's

The pull is toward the device, because that is where the reading
physically happened, and away from it, because a reader with a phone and
an e-reader in different zones would then have a history filed under two
answers at once — the seam problem, permanently, rather than at a move.

An account-level answer at least has the virtue of being one answer. That
argues for keeping the account's zone as the server's definition and
making the *client* aware that its local figures use a different one,
which is the subject of liseur ADR-0030.

### The deployment note

Whatever is decided, `docs/deployment.md` should say plainly that an
account's timezone is not a cosmetic setting: it decides which day every
future sitting is filed under, and changing it creates a seam in the day
history that nothing repairs.

It should not claim the change defers the next rollup batch, because
usually it does not. `rollupSessionsOnce` re-reads the zone from
`UserByID` at the top of every pass, so a change made between passes is
simply the zone the next batch is built with, and `ApplyRollups` accepts
it. Deferral is the narrow case where the change commits after a batch
has been built under the old zone: that batch is refused, and the pass
that follows builds a fresh one and succeeds.

## Consequences

The race is already closed: a batch built under a zone the account no
longer keeps is refused, and the next pass rebuilds it under the current
zone. Deferring the rest leaves the seam where it is. Buckets filed under
an old zone stay filed that way, and the day history of a reader who has
moved has a discontinuity at the move that nothing repairs.

Since the client side of the same question is open (liseur ADR-0030),
deciding here alone would be deciding for both.

## Implementation and acceptance

`ApplyRollups` refusing a foreign-zone batch is implemented and covered.
Nothing else is. Whatever is chosen for the buckets already filed is
accepted only when:

- A reader who changes zone sees a streak that is defensible across the
  change, under a rule the record states rather than one that emerges.
- The client and server agree on which zone decides a day, so the
  combined screen does not add two definitions together.
- `docs/deployment.md` describes what an operator changing an account's
  timezone is actually doing.
- Anything that rewrites existing buckets is idempotent and leaves the
  streak unchanged where the zone did not move.

## Open questions

- Is a one-day oddity at a move worth any machinery at all, or is this a
  case where the right answer is a sentence in the docs?
- If the zone becomes time-varying, does the client need to know the
  history too, or only the current value?
- Should a zone change be refused outright while a rollup batch is
  deferred, rather than merely deferring it again?
- Does a reader who travels regularly — not moves, travels — want their
  streak counted in the zone they were in, or in their home zone? These
  give different answers for a red-eye.
