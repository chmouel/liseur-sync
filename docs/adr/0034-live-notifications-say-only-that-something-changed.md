# ADR-0034: A live notification says only that something changed

- **Status:** Accepted
- **Date:** 2026-09-05
- **Depends on:** [ADR-0007](0007-web-reader.md),
  [ADR-0028](0028-annotation-sync.md),
  [ADR-0033](0033-a-device-outlives-its-token.md)
- **Amends:** [ADR-0007](0007-web-reader.md), which gave the reader "no
  new sync surface". It gets one, and it carries no reading state.

## Context

Every client here polls. Liseur runs a full sync when a book opens or
closes, when the user asks, and hourly from WorkManager. The web reader
fetches a position and a live annotation set when a book opens and then
never asks again. Both are correct and both are late: a passage
highlighted on the phone is invisible to the tab already open on the
desktop until something else happens to make it look.

The durable half of the problem is already solved. `GET /v1/changes`
and `GET /v1/annotations/changes` are ordered per-user feeds with
cursors, and Liseur advances a cursor in the same transaction that
stores the page it covers. Nothing about being told sooner improves
that. What is missing is only the telling.

The obvious mistake is to send the news itself: the locator, the
highlight, the progression. That makes the notification a second
protocol with its own delivery guarantees, its own conflict rules and
its own way of being wrong, and it puts reading state on a path where a
dropped connection loses data. The feeds already have all of those
answers.

## Decision

### The event carries no reading state

`GET /v1/events` is a `text/event-stream` under the same bearer auth,
transport and CORS policy as the rest of `/v1`. It sends one kind of
frame:

```
event: invalidate
data: {"topics":["positions","annotations"]}
```

The topics are `positions`, `annotations` and `insights`. There are no
identifiers in the payload: no work, no book, no locator, no excerpt,
no device, no count, and no sequence number. A topic names a feed the
client already knows how to read, and the client answers it by reading
that feed the way it always has.

There is no aggregate sequence because there is no aggregate. Positions
and annotations have separate per-user counters, and insights are
derived from sessions, which have no feed at all. A single high-water
mark would be a fiction the client would then have to translate back
into two cursors.

Unknown topics are ignored, so a later topic is an addition rather than
a version.

### Correctness stays in the feeds

A notification may be lost, duplicated, coalesced or delivered out of
order without any client being wrong. Losing every notification costs
latency and nothing else. This is the property that lets the stream
stay in memory, with no delivery table, no replay window and no
acknowledgement.

Nothing observes an event to decide *what* changed. A client that
receives `positions` reads `/v1/changes` from its own cursor; a client
that receives `annotations` runs the annotation pass it already runs.

### Subscribing is race-free without a cursor

The server registers the subscriber first, then queues an invalidation
for every topic that credential is allowed. A change committed before
registration is covered by the read that initial invalidation provokes;
a change committed during that read is either in the answer or still
queued. So `/v1/events` takes no `since` parameter and compares no
cursor, and a client that reconnects after an outage needs no special
case.

Coalescing is by union of topics, never by replacement, and each
subscriber holds one pending set and one wake-up slot. `positions` then
`annotations` collapses to both, never to the later one.

### Authorization is per topic

`positions` and `annotations` require `sync`; `insights` requires
`read-insights`. The endpoint authenticates once and then filters,
rather than demanding every scope: the web reader's token carries
`sync` and `library-read` (ADR-0007) and must keep working without
being handed insight authority it has no use for. A credential with no
eligible topic is refused rather than parked on an empty stream.

### The stream is not a longer-lived credential

A stream ends when its token expires, and revocation is rechecked at
heartbeat boundaries. A connection authenticated at 12:00 must not
still be authorized at 13:00 because nobody asked again. Once a `200`
has been written the answer cannot become a `401`, so the server closes
the stream and lets the reconnect be refused in the ordinary way.

Heartbeat comments go out every 20 seconds and clients treat 60 seconds
of silence as a dead stream. Both numbers exist to sit under proxy idle
timeouts. Admission is bounded per account; a refusal is `429` with
`Retry-After`, and a client that reads too slowly is disconnected
rather than buffered.

### Notifications are published after a commit, by the store

The signal is raised where the write lands, not in `HandlePushOps`.
Positions arrive through the native API, through the web reader's
reading-status writes and through the kosync adapter; sessions arrive
through three paths of their own. A hook on the HTTP handler would
cover one of them and quietly miss the rest.

It fires only after a successful commit and only when the state
actually changed: a rolled-back transaction, a rejected write and a
batch that was entirely duplicates all notify nobody. That, plus
idempotent client writes, is what stops two devices from talking each
other in a circle. There is no source-device suppression, which a
coalesced event could not express anyway.

### One process

The hub is in memory, which is exactly right for the single-process
deployment this server is. Several replicas would need PostgreSQL
`LISTEN`/`NOTIFY`, and that is the day to add it. Not Redis, and not
today.

## Consequences

Reading gets live on every surface that has a feed, and only those.
Statistics refresh because sessions commit, not because they are
delivered.

Structural changes stay outside the guarantee. Merging two works
rewrites `ops.work_id` in place without minting a new `seq`, so a
delta pull cannot recover it however promptly a client is told to look.
The catalog has no feed at all. Both remain what a full sync is for,
and the documentation says so rather than implying that live means
converged.

A closed app is still a closed app. Waking one needs FCM or Web Push
and the infrastructure under them; the case that actually matters to a
reader, opening Liseur after reading in a browser, is already covered
by the sync on foreground.

An older server has no `/v1/events`. A client treats that as "not
available on this connection" and keeps its existing schedule, with no
warning banner: nothing is degraded that was ever promised.

## Acceptance criteria

- A position or annotation committed by one client reaches another
  connected client without a manual refresh, with no reading state on
  the stream.
- Subscribing and committing in either order loses no refresh; a burst
  during a held refresh leaves at most one coalesced follow-up.
- A token without `read-insights` never receives `insights`; a token
  with no eligible topic is refused.
- Expiry and revocation end a stream; shutdown releases subscriptions;
  a slow reader cannot block a write path.
- Duplicate-only and rolled-back writes notify nobody, on both storage
  backends.
- `docs/openapi.yaml`, `docs/integrating.md` and `docs/deployment.md`
  describe the frame, the topic scopes, the heartbeat and idle-timeout
  requirement, and the old-server fallback.
