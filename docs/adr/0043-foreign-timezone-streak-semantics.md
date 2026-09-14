# ADR-0043: Which day a sitting happened on

**Status:** Draft
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
account's timezone is not a cosmetic setting: changing it defers the next
rollup batch and creates a seam in the day history. An operator changing
it on a reader's behalf should know that.

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
