# ADR-0047: Mirroring reading to a peer that speaks KOReader

- **Status:** Accepted
- **Date:** 2026-09-02
- **Depends on:** [ADR-0003](0003-catalog-work-identity.md),
  [ADR-0046](0046-a-catalog-book-carries-a-koreader-fingerprint.md)

## Context

A reader can have more than one server. The case in hand is two of them
on one machine, indexing one directory tree: this one, and another that
serves the same books over its own web reader and its own OPDS feed.
The same book gets read in this server's web reader, in Liseur on
Android, in that server's reader, and in KOReader on a phone. Each of
the two servers keeps its own idea of where the reader is, and neither
has ever heard of the other, so every crossing loses the place.

The other server speaks kosync — KOReader's sync protocol — as a
*server*. So does this one
(`internal/adapter/kosync`). Two kosync servers cannot talk to each
other by standing still: somebody has to be the client.

That is the whole of the decision here. Everything else follows from
three facts about the situation:

- **There is no push.** The peer has no webhook, no event stream and no
  monotonic counter. Whatever this server learns from it, it learns by
  asking.
- **There is no shared clock beyond a timestamp.** Ordering across the
  two systems is wall time and nothing better.
- **This server has never made an outbound HTTP request.** Every network
  edge it has is inbound. A component that dials out is a new kind of
  thing in this codebase and needs its boundaries written down rather
  than discovered.

## Decision

**An egress adapter: a background mirror that speaks KOReader's protocol
outwards to a peer, for one account, joined on the document
fingerprint.**

It is the mirror image of `internal/adapter/kosync` and it inherits that
adapter's central rule. **Nothing legacy is stored.** A position that
arrives from the peer becomes a native op before it touches the store,
exactly as one arriving from a KOReader device does.

### What it speaks

Three calls: authenticate, put a position, get a position. Stock
kosync, nothing more.

BookOrbit offers a great deal more behind the same credential — bulk
position writes, page statistics, annotation exchange, book states —
and it would be convenient to reach for. Those routes are BookOrbit's
own, though, and they move when it moves. The three above are
KOReader's contract, which BookOrbit implements because everyone does,
and which any other peer worth pointing this at will implement too. So
positions, the part that was actually asked for, are built on the part
that does not move, and the richer surface is a later milestone's
problem rather than a dependency of this one.

### Identity

The join key is the KOReader fingerprint from
[ADR-0046](0046-a-catalog-book-carries-a-koreader-fingerprint.md), which
both servers compute from the same bytes on the same disk.

A fingerprint that matches more than one catalog book is **refused, not
guessed**. Twelve kilobytes of a file is a name, not a proof, and the
cost of being wrong is a reader's place in the wrong book.

### Echo

A mirror that reads back what it wrote is a loop, and a loop that writes
ops is a loop with a permanent record.

- Outbound writes carry a fixed device identity. A reply stamped with it
  is this server's own echo and is dropped.
- Inbound positions are appended as ops with `origin=kosync`, an
  `origin_alias` naming the fingerprint, and a device id marking the
  peer. That is what they are: kosync-shaped records from a kosync
  server. **No new `Origin` value**, so insights grouping, rollups and
  compaction are untouched.
- An op whose device id marks the peer is never sent back to it.

### Conflict

**Newest wins, on timestamps.** A tie leaves the local position alone,
because the local one is the one a reader can see.

There is no better rule available. Neither side has a causal clock, and
inventing one that only this server honours would be a rule the peer
breaks on its next write.

### Fidelity

**The bridge is percentage-grade, and that is the honest limit.** A
Readium locator does not survive kosync's wire shape. A book read in
the peer and then opened in Liseur lands on the progression, not on the
exact spot. Within either system nothing is lost. This is a property of
the protocol, not of the implementation, and no amount of care recovers
it.

### What the mirror refuses to do

- **It does not apply a reset it did not ask for.** The peer answers a
  read with a synthetic start-of-book position while a reading reset is
  outstanding on its side, and keeps answering that way until every
  device agrees. A mirror that applied it would send the reader to the
  beginning and do it again on the next poll.

  There is less to go on than there looks. The reply is stamped with
  the current time, so newest-wins hands it the argument every time,
  and it is attributed to the peer's own web reader — the same device
  and device id a genuine web position carries. Nothing in the envelope
  separates the two. The only signal left is the position itself: the
  first fragment of a reflowable document, or page one of a paged one,
  at zero. That is what the mirror matches on, and it declines to apply
  it.

  This is a judgement about which mistake is cheaper, not a
  disambiguation. Refusing costs a genuine reset that has to be made
  twice, once on each side. Applying costs somebody their place in a
  book, repeatedly, with no way to stop it from this end.
- **It does not fabricate a page number.** The standing rule holds
  across the bridge: pages come from a known edition page count or they
  do not come at all.
- **It does not invent a position format it does not have.** A highlight
  made in this server's reader holds a Readium locator; the peer wants a
  KOReader pointer. Only an annotation that already carries one goes
  out. Everything the peer sends comes in.
- **It does not touch another account.** The mirror belongs to exactly
  one, named in configuration, and reads and writes nothing outside it.

### The credential

The repository rule is that secrets are stored hashed. That rule is
about credentials this server *verifies*. One it must *present* cannot
be hashed, and pretending otherwise would mean storing something
unusable.

**The peer credential lives in configuration, not in a table.** It is
read at startup beside the database URL, held in memory, never logged,
never included in an error message, and never returned by any route. A
half-configured mirror refuses to start rather than running with a
credential it would then fail to use.

This is the narrow boundary a single account buys. A general
multi-account version would need encrypted credential storage and a key
to encrypt it with, which is a larger decision and is deliberately not
taken here.

### Bounds

The mirror polls; polling is a cost paid forever. It asks only about
works read within a configured recent window, at a configured interval,
with timeouts, bounded response bodies and no redirect following. Its
failures back off and never block a pass, a request or a shutdown.

## Consequences

- This server gains an outbound network dependency. It is optional,
  disabled by default, and everything else keeps working when the peer
  is unreachable.
- Cross-server position fidelity is a percentage. Say so in the
  documentation rather than letting a reader discover it.
- The peer's own protocol extensions, beyond stock kosync, are its own
  and can change. The position mirror is built on the kosync routes,
  which are KOReader's contract rather than the peer's, and stays
  independent of anything richer.
- Reading status has nowhere to land coming *in*: this server has no
  status model, and "finished" is derived from progression when it is
  displayed. Status is outbound only until that changes, which is a
  bigger decision than this one.
- Statistics are deferred. The route that fits this server's session
  shape needs a credential the mirror does not have, and the one the
  mirror can reach wants page numbers it must not invent. The decision
  is recorded as open rather than answered badly.

## Acceptance criteria

- A position written in this server appears in the peer, and one written
  in the peer appears here, for the same book, within one poll.
- A position that crossed the bridge is not sent back across it. A test
  holds this, because a loop that is only noticed in production has
  already written its history.
- A reset outstanding on the peer never moves a local reader.
- A fingerprint matching two catalog books mirrors neither.
- The peer credential appears in no log line and no response body.
- With the mirror disabled or the peer down, every existing test still
  passes and nothing degrades.
