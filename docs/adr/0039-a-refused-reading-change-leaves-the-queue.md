# ADR-0039: A refused reading change leaves the queue

**Status:** Accepted
**Scope:** Now
**Amends:** [ADR-0037](0037-durable-online-reading.md)

## Context

ADR-0037 made the online reader queue every position and finished sitting
in IndexedDB before sending it, so nothing is lost to a closed tab. The
queue answers a delivery question well. It had no answer at all to a
refusal.

`drainOfflineOutbox` sends records in state `pending` and marks anything
the server refuses `failed` or `conflict`. Nothing lists those states
again, nothing retries them, and nothing removes them. A refused change
is a permanent resident. The reader page counted them and wrote a
sentence over the text — *"1 reading change(s) could not be saved; retry
sync."* — naming an action the online reader does not offer and which
would not have helped if it did, since a retry only ever looks at
`pending`. The count was account-wide, so one dead change followed the
reader into every book.

What put a change there was a second bug, in the same area and with the
same shape. On `pagehide` the reader sent the finished sitting directly
with `keepalive` and *then* wrote it to IndexedDB, on the reasoning that
an unload cannot wait for a database. It cannot — which is the problem:
the request went, the durable write did not finish, and the checkpoint
naming that sitting survived. The next page resumed the checkpoint, read
on, and closed the *same* `session_id` with a later ending. The server
holds sessions immutably under their id, so it refused the second
version as `id_reused`, correctly and forever.

`id_reused` is easy to misread as loss. It is the opposite: it says the
server already has that id. The reading was recorded. What was left in
the queue was a variant of something already accepted, kept forever so
it could be complained about.

## Decision

**A change the server will refuse for as long as the same bytes are
offered stops being retried.** Those verdicts already had a name in the
session sender — `permanentCodes` — and that one list is now shared with
the queue, so the two cannot drift apart about what is permanent. It
gains `unknown_work`, which was previously retried forever because it is
recoverable *in the protocol* but not by anything this client can do.

**Of those, only the ones that cost nothing to forget are discarded
silently.** That is `id_reused` — including the op log's item-level
`conflict` verdict's sibling, see below — and the malformed-payload
refusals (`missing_field`, `bad_time`, `time_in_future`,
`progression_out_of_range`, `idle_out_of_range`, `active_out_of_range`).
`id_reused` says the server already holds that id, so the reading is
recorded; a malformed payload can never be accepted and there is nothing
to ask a reader about it. These are dropped with a console warning and
not reported.

**A refusal with a documented recovery this client does not implement is
shown, not dropped.** `docs/integrating.md` says `unknown_work` is
repaired by re-resolving the book and rebuilding the change under the
fresh work, and `locator_too_large` by resending the same `op_id`
without its locator. The queue does neither. Discarding them silently
would be claiming a loss is a non-event, so instead they are named to
the reader.

**An op-level `conflict` is not `id_reused`.** It looked like the same
sentence in a different shape and it is not: it says the id is already
spent on a *different* payload, so this change was **not** stored.
Sending the same bytes again cannot store it, and the protocol's answer
is a fresh id, which this queue does not mint. So it is a refusal the
reader is told about, never one that is dropped along with the local
position it describes.

**An annotation is never discarded on the server's say-so, and an
annotation conflict never appears in that panel.** A position and a
sitting are records of something that happened; the reader's own words
are not. A conflict has a better answer than two buttons —
`resolveStoredAnnotationConflicts` settles it with both versions in hand
— and that answer now runs after every drain in the online reader too,
not only in the installed app, serialized because `window.confirm` is
modal.

**Every other annotation refusal is named, and discarding one puts the
note somewhere honest.** The server can call an annotation invalid — an
unknown work, a missing body, a field limit, the per-work cap — and
those refusals used to sit in the queue unseen. They now reach the
panel, and discarding one no longer deletes the outbox row on its own.
The queued mutation names the local copy it authored, so: a copy a newer
mutation has already replaced is left alone; a copy the server never
acknowledged existed only as the change being discarded and goes with
it; and a copy the server does know stops counting as a local mutation,
which is what lets the server's version replace it — a refused deletion
brings the note back.

That last case is the one the store cannot finish by itself. A rejected
*edit* is still the reader's rejected text, and clearing its pending
flag alone would present it as saved. So discarding an annotation is
followed by re-reading the book's annotations from the server and
setting both the store and the page to what comes back: restored where
the server has a version, removed where it has none. A reader who
cannot reach the server keeps the refused text marked unsaved rather
than clean: the reader's own words are still there, but nothing yet
says they were accepted, and drawing them as saved would be the same
lie the panel exists to stop telling. The next drain asks again, since a
drain only runs when there is something to ask.

The discard transaction reports which annotation it actually spoke for,
and only those are restored — and only by a discard, never by a retry:
*Try again* leaves the reader's payload queued, and the local copy is
what that payload will deliver, so replacing it with the server's
version would send one thing and show another. Reading the server takes
a request, and a reader in another tab can author while it is in the
air, so the restoring write is itself conditional and in one
transaction: anything queued for that annotation, or a local copy that
has been claimed again, means the newer mutation owns it and nothing is
written. Only what was written reaches the page.

**Anything else that is stuck is named, and offered the two answers
there are.** A refusal the queue cannot classify raises a panel in the
reader — the same shape as the catch-up panel — that says what could not
be saved and why, with *Try again* and *Discard*. Both edit the queue
under its own lock, so a drain in flight finishes first. *Try again*
returns the record to `pending`; only a record already carrying a
terminal verdict may be revived, because a `pending` record may still be
in the air. The panel is scoped to the book the reader has open.

**A discarded position stops being this device's position.** It is
cleared from the durable reading state and from any downloaded copy that
named it, so the reader is never offered a catch-up to a page that
exists nowhere. A discarded sitting leaves no tombstone: the tombstone
exists to stop a delivered sitting going out twice, and this one was
never delivered.

**A page the reader turns retires the refused page before it.** A
position that carries a terminal verdict leaves the queue as soon as a
newer position for the same work and device is queued. The drain only
holds newer pages behind a position still in flight, so without this a
newer page would go out first and a revived older one would land behind
it with a later `seq`, making a page the reader has already left the
server's newest position. Nothing is lost by it: the newest position is
the one the queue exists to deliver.

**The durable write spends the session id before the page speaks.** On
an unload the reader now finalizes the sitting into the queue first and
sends afterwards. If the page dies before the write commits, nothing was
sent either, so the surviving checkpoint is still true and the next page
finalizes that id for the first time. Promptness is preserved in every
case where the page lives long enough to send at all, and where it does
not, the queue delivers on the next open.

## Consequences

No API, route, schema or migration change. The server is untouched: its
op log and session semantics were right, and being refused forever is
the correct answer to a reused id.

Discarding is the one place reading data is thrown away. It is bounded
to a change the server has explicitly refused, and for a position the
next page turn writes a fresh one, so the server ends up with the
reader's real position rather than none.

A sitting refused as `id_reused` because of the unload race is not
recovered. The server keeps the version it accepted, which is the
shorter one; the minutes read after the resume are not added to it. The
reordering above stops new ones from being created, and there is
deliberately no backfill: an invented session is worse than a short one.

Because every permanent session refusal is now a named verdict, a
sitting refused for good rarely reaches the panel. What does reach it is
a refusal with a recovery nobody here implements — `unknown_work`, an
oversized locator — an op whose id is already spent on a different
position, and a position refused `403` or `404`. Implementing the
documented recoveries (re-resolve the work, resend without the locator,
resend under a fresh id) would empty the panel further and is deliberately
left as separate work: this decision is about a change being able to
leave the queue, not about how many of them can be rescued first.

## Implementation and acceptance

- [x] Share one list of permanent verdicts between the session sender
      and the queue, and stop retrying `unknown_work` forever.
- [x] Discard only the verdicts that cost nothing to forget, logging
      them rather than reporting them.
- [x] Name a refusal with an unimplemented recovery, and an op conflict,
      to the reader instead of dropping it.
- [x] Keep annotation conflicts out of the panel and resolve them after
      every drain in the online reader as well as the installed app, one
      dialog at a time; name every other annotation refusal, and make
      discarding one settle the note as well as the queue, restoring the
      server's version rather than presenting a rejected edit as saved.
- [x] Report the remaining stuck changes for the open book only, with
      *Try again* and *Discard*, both taking the outbox lock.
- [x] Clear the durable local position and any snapshot copy when a
      queued position is discarded.
- [x] Finalize a sitting into the queue before sending it on unload.
- [x] Cover a spent id, an unclassifiable refusal, retry, discard and
      book scoping in the browser suite.
