# ADR-0037: Durable online reading, and a disagreement the reader answers

**Status:** Accepted
**Scope:** Now
**Amends:** [ADR-0007](0007-web-reader.md), [ADR-0030](0030-web-reader-reading-sessions.md), and [ADR-0036](0036-one-bounded-offline-web-reader.md)

## Context

ADR-0036 gave the installed offline reader a durable queue: a position, a
session checkpoint and a finished sitting are written to IndexedDB before
anything is sent, and the same bytes are replayed until the server
acknowledges them. The ordinary online reader never got any of that. It
held a position in a variable, posted it, and kept a single in-memory
retry. A closed tab, a crash or a reload before the acknowledgement lost
the page turn outright, and a sitting that failed to upload went with it.

The catch-up offer had a second problem. It kept a "baseline", but the
baseline was an identifier held in memory and used only to suppress the
reader's own echo. It was not a position, it did not survive a reload,
and it was never compared against anything. When the reader turned a page
while another device's position was on offer, the offer was folded away
and forgotten. One side always won, and the reader was never told there
had been a disagreement.

The Android client already solved this. `ReadingProgress` keeps agreed and
pending positions, `SyncPeerState` keeps the agreed baseline per book and
peer, and `ReadingStateMerge` compares baseline, local and remote and
answers with pull, push, conflict or in-sync. A conflict is kept and
presented; nothing decides it in the background.

The two clients also disagree about identity in a way that matters here.
Every browser tab of one account shares a single web device id, because
op-log heads are keyed by work *and* device. Two tabs are therefore one
device competing with itself for a single head, and the browser cannot
fix that on its own.

## Decision

The online reader uses the same durable machinery as the offline one.
Positions and finished sittings are written to the account-partitioned
IndexedDB outbox before delivery is attempted, and delivered by
`drainOfflineOutbox`: the same sender, the same immutable-replay rule,
the same per-work head blocking that stops a stale page overtaking a
newer one. Session checkpoints are kept online too, so a crash
mid-sitting resumes from its last checkpoint rather than losing the
sitting. A checkpoint names one sitting, so only the page that can claim
the book keeps one; a second tab on the same book is refused the claim
and reads on with its own fresh sitting.

The retry ladder, the Web Lock and the wake-up events move into one
coordinator shared by both modes. The lock is named after the account's
queue rather than after the page holding it, so an ordinary reader tab, a
second reader tab and the installed app take turns on one outbox instead
of racing each other into a reordered op log.

Each book and device gets a durable agreed baseline: the position this
browser and the server both know. It advances on exactly three events and
on nothing else: the position read when the book opens, an acknowledged
local op, and a remote position the reader accepted. A local page turn is
movement away from the baseline, and it leaves the baseline where it was.

Reconciliation is a port of `ReadingStateMerge`: given the baseline, this
device's position and the newest remote one, it answers pull, push,
conflict or in-sync. It never consults a clock and never prefers the
furthest position, since a wall clock says nothing about which position a
reader wants and moving backwards through a book is ordinary. Where
Android compares a reading `status`, the web reader compares the exact
pointer instead, since a web op carries a progression and a locator and no
status. Whether this side moved comes from the durable queue, so it
survives a reload and a crash.

A remote move on its own stays an offer with a button, as before: text
does not move under a reader's eyes without a click, even though Android
pulls. A move on both sides extends the same panel to name both
positions and label each button with where it goes. Answering it settles
it: the position the reader did not take becomes agreed too, so a reload
does not ask again. Taking the other device's position withdraws this
browser's own undelivered pages, because the queue drains oldest first and
one of them would otherwise land after the answer and reinstate the
position the reader just refused.

This applies to same-origin deployments only. It is gated on exactly the
condition that already gates the IndexedDB account marker, its epoch guard
and the logout wipe, so no new storage appears anywhere those protections
are absent. A `reader_origin` deployment keeps today's best-effort
behaviour and ADR-0036's origin boundary.

## Consequences

There are no API, route or database changes. The server's op log and
session semantics are unchanged and remain authoritative; every retry
replays identical bytes, so a duplicate is a duplicate and never a
conflict.

Ordinary online reading now leaves rows in the same private IndexedDB
store as offline reading, under the same account epoch and the same
logout wipe. The logout confirmation therefore speaks about reading
changes waiting on this device rather than about offline changes.

Two tabs are still one device. The lock serializes delivery; it cannot
stop two tabs authoring divergent heads for the same work. The conflict
panel is what surfaces that to the reader.

A reader who closes the last tab with a page still queued keeps it: it
goes out when a reader is next opened on that account, which drains the
whole queue and not merely that book. Nothing promises delivery while no
page of the deployment is open.

## Implementation and acceptance

- [x] Write online positions and finished sittings to the outbox before
      sending them, and keep a session checkpoint in the page that holds
      the book's reader claim.
- [x] Drain both modes through one coordinator, one lock and one sender.
- [x] Persist an agreed baseline per book and device, advanced only by an
      opening read, an acknowledgement or an accepted offer.
- [x] Reconcile baseline, local and remote without consulting a clock.
- [x] Present a disagreement with both positions and per-position buttons,
      and settle it when it is answered.
- [x] Keep separate-reader-origin deployments as they were.
- [x] Cover reconnection, conflict, credential renewal, two windows and a
      reload with an undelivered queue in the browser suites.
