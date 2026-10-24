# ADR-0055: Following an audiobook on Audiobookshelf

- **Status:** Accepted
- **Date:** 2026-10-23
- **Depends on:** [ADR-0047](0047-mirroring-reading-to-a-koreader-peer.md),
  [ADR-0048](0048-a-second-protocol-for-the-mirror.md),
  [ADR-0022](0022-calibre-metadata-db-is-authoritative.md),
  [ADR-0024](0024-deleting-a-work.md)

## Context

Some books are both read and listened to. The audiobook lives on an
[Audiobookshelf](https://www.audiobookshelf.org/) server, the EPUB here,
and the reader switches between them: a chapter in the car, the next one
in bed. Each side knows exactly where the reader is and neither knows
about the other.

The mirror (ADR-0047, ADR-0048) already exchanges positions with another
server, and it is tempting to add Audiobookshelf as a third protocol. It
does not fit. The mirror joins books on a KOReader fingerprint of the
same bytes; an audiobook and an EPUB share no bytes, no fingerprint and
usually no ASIN, and their titles differ ("Disquiet Gods: The Sun Eater:
Book Six" against "Disquiet Gods"). A position does not translate as a
percentage either: an ebook carries front and back matter the recording
skips, and an audiobook can cover only part of a book (a French edition
split in two volumes covers chapters 37 to 68). And the mirror's inbound
path is kosync's, which would feed listening into session inference as
if it were reading.

What does line up is chapters. Both sides number them, and the numbers
agree even when the titles do not ("CHAPTER 1" against "Chapter 01",
"CHAPITRE 37: …").

## Decision

A separate integration, `internal/audiobookshelf`, configured in its own
`[audiobookshelf]` section and run by its own runner beside the mirror.
It keeps every operational rule of the mirror: one peer and one account
from configuration, a credential that is never logged, stored or
returned and is scrubbed from errors, https unless `insecure_http`, no
userinfo, query or fragment in the URL, timeouts, bounded bodies, no
redirects followed, backoff, and never blocking a pass, a request or
shutdown. It is off by default.

**Peer identity.** As ADR-0048: a digest of protocol, base URL and the
Audiobookshelf user id from `/api/me`. Everything the integration writes
is namespaced by it: links, rejections, device ids
(`abs-<peer8>:<client>`) and session source keys
(`abs:<peer>:<session id>`). When the identity changes, the old links go
inert and matching starts again; imported history stays.

**Links.** A link joins one Audiobookshelf item to one of the reader's
works, per peer, unique on both sides in every state, so a paused link
still reserves both. It names the catalog book and edition its mapping
was computed from; the book is not a foreign key, so a replaced or
deleted book suspends the link, and the next pass re-resolves it to the
work's current book. States: `active`, `needs_mapping` (nothing crosses
until a reader confirms), `suspended` (book not visible or servable).
A merge keeps the survivor's link when both works hold one; a split
moves a link with its edition; deleting the work deletes the link.
Unlinking records a rejection, so automatic matching never proposes that
pair again; linking the pair by hand clears it.

A link does not keep a work alive. Work collection (ADR-0022) is
unchanged: a work with ops or sessions survives anyway, so the only work
collected from under a link is one nothing has synced through yet, whose
book has gone. Its link goes with it.

**Concurrency.** Each link has a random generation that changes on
creation, remapping, confirmation, suspension and work reassignment.
Cursor writes compare that generation and leave identity and mapping
fields alone. The service serializes each link's exchange with UI
mutations. Immediately before a PATCH, op append or session import it
rechecks the generation, active state, account, ebook grant and local
head. Audiobookshelf has no conditional progress update, so a remote
change between GET and PATCH can still be overwritten. Agreement checks
recover a successful PATCH whose cursor write failed.

**Matching.** Conservative. A link is proposed only when the normalized
title (subtitle and "Book N" dropped) and normalized first author match
exactly one catalog book the account can see. Anything else waits for
the reader on `/ui/audiobookshelf`. Matching never touches a manual link
or a rejected pair. A truncated catalog search cannot establish
uniqueness and never produces an automatic match.

**Mapping.** Chapter anchors on both sides. Ebook anchors come from the
flattened table of contents: an entry naming a whole resource starts at
that resource's exact start; an entry with a fragment is placed by the
share of the resource's text before it, and is marked approximate.
Audio anchors are Audiobookshelf's chapters. Numbers are parsed from
titles (digits or roman numerals, after an optional chapter prefix) and
paired by the longest run increasing on both sides. Full confidence
means every numbered audio chapter matched, contiguously, with no gap.
Audio duration and chapter times must be finite, positive-length,
ordered, non-overlapping and within the duration plus one second for rounding, including unnumbered
introductions and credits.
Only then does an automatic link go `active`. Coverage is first matched
chapter to last; within a chapter the position is interpolated
linearly; outside coverage nothing is exchanged. A whole-book linear
mapping is used only when the reader confirms the recording covers the
whole book; equal chapter counts are not proof. A change to the book's
bytes or the chapter list recomputes the mapping and, if it moved,
returns the link to `needs_mapping`. The reader can accept a previewed
alignment. A whole-book linear claim is offered only when numbered
chapters provide no usable alignment; it cannot repair a failed partial
alignment. Editing individual chapter pairs is deferred.

**Positions.** One `GET /api/me` per pass. Per active link, the remote
and local positions are mapped to the other side and compared. They
agree when within one audio second with the same completion, and
nothing is written. Otherwise newest wins on timestamps, a tie keeps
local, and that also decides a link's first exchange. A remote position
of zero never displaces a non-zero local one. The value last written is
kept as a mark, with the remote timestamp read back after the write, so
its echo is recognised. Pulled positions are native ops with origin
`audiobookshelf`, the link's edition and a locator naming the chapter.
A local op from another edition is never pushed, nor is one from this
peer's own devices.

Coverage records `CoversFirst` and `CoversLast` independently against
the ebook's numbered narrative chapters. `FullNarrative` requires every
numbered ebook chapter to match once, in order, without an internal gap;
a confirmed whole-book linear mapping also has it. A finished audiobook
maps to the ebook's end only with `FullNarrative`. Every incoming
position from a partial recording is capped at 0.998, below the ebook
completion threshold, even when it covers the final chapter. A finished
ebook marks a partial recording finished only with `CoversLast`.

**Listening sessions.** Imported for active links only, from
Audiobookshelf's per-item session list, newest `updatedAt` first, down
to a fixed watermark less an overlap. Each remote revision is a new
session row in the existing supersession chain, keyed by source key;
nothing is overwritten. Revisions are ordered by the remote `updatedAt`
(a new nullable `remote_updated_at` on `session_supersessions`), so a
late older revision never becomes current. Attribution is fixed by the
first revision: later ones inherit its work, edition and device, so an
updated session never moves between works. `active_ms` is the listening
time; progression comes from the mapping; pages are never reported.
Like koplugin sessions they keep a source key and so are never rolled
up. Reusing a local session id is a duplicate only when its contents
agree; different contents conflict. Malformed remote sessions are
skipped. Future-dated sessions are retried later, without advancing the
watermark past them.

The first session revision also writes a durable import record in the
same transaction. `audiobook_mappings` holds immutable canonical JSON,
keyed by its SHA-256 digest; `audiobook_session_imports` ties a source key
to that mapping and its first import time. These rows belong to the user
and survive unlinking, remapping and work deletion. A later revision uses
the original mapping and the current session row's attribution, which
follows merges and splits. An import record without a session means the
reader deleted that history; later remote revisions cannot recreate it.
The importer recognizes a remote revision before mapping it, so replay
through a changed link does not cause a payload conflict.

Session validation bounds IDs, finite non-negative audio times and
listening duration. Listening cannot exceed 24 hours or the session's
wall span plus 60 seconds. Out-of-duration positions are clipped for
session statistics. A traversal holds its starting watermark fixed and
commits a new one only after reaching the lower boundary; network or
store failures leave it unchanged. The new watermark cannot exceed the
local clock, so a future-dated session stays eligible for retry.

**Statistics.** Listening counts toward time, days, streaks, sessions
and progress. It is excluded from measured reading pace, because
listening at 1.5x says nothing about how fast the reader reads. The rule
lives in `store.MeasuresReadingPace` and is applied by the aggregator,
the materializer and both rollup writers.

**Origin.** `audiobookshelf` is a new op and session origin, visible in
the API.

## Consequences

- A listener and a reader of one book see each other's place, chapter
  accurate, without sharing a file.
- Partial recordings work: the ebook moves only inside what was
  recorded.
- Raw listening sessions are retained indefinitely, as koplugin's are.
- A stale offline phone can win newest-wins, as with any client.
- Audiobookshelf's API is unversioned; the client decodes leniently and
  logs the server version.

## Implementation phases

1. Store: links, rejections, merge/split handling, ordered session
   revisions.
2. Configuration, client, mapping, matching.
3. Position and session sync, runner.
4. Web UI for linking and confirming; documentation.

## Acceptance criteria

- Store suite covers link uniqueness across states, rejections, merge
  with both works linked, split, delete, collection of an unsynced
  linked work, and revision ordering, on both backends.
- Mapping fixtures cover a full book, a partial recording, several
  chapters in one resource, nested parts and intro/credits.
- Sync tests cover agreement, echo, newest-wins, the zero guard, finish
  and unfinish, a lost cursor write, and an edition mismatch.
- Session tests cover a late update to an old session, newer then older
  revisions, equal timestamps, an interrupted traversal, relink and
  remap.
- Pace excludes listening; totals include it.
