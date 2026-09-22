# ADR-0048: A second protocol for the mirror, BookOrbit's own API

- **Status:** Accepted
- **Date:** 2026-09-04
- **Depends on:** [ADR-0046](0046-a-catalog-book-carries-a-koreader-fingerprint.md),
  [ADR-0047](0047-mirroring-reading-to-a-koreader-peer.md)

## Context

ADR-0047 built an egress mirror and had it speak stock kosync. The peer
in practice is BookOrbit, reached at `/api/v1/koreader`, and the choice
of protocol was deliberate: KOReader's three routes are KOReader's
contract rather than BookOrbit's, they do not move, and a mirror that
speaks them works against a second liseur-sync or a stock kosync server
as readily as against the peer in front of us. That ADR recorded the
richer surface as a later milestone's problem. This is that milestone.

What the KOReader bridge costs is fidelity. A position crosses as a
percentage, so a book read on the peer and reopened here lands on the
progression rather than on the sentence. That is not a limitation of
either server: both of them know exactly where the reader was. It is a
limitation of the pipe between them.

BookOrbit's native API carries an EPUB CFI. So does every position this
server already holds, from its own reader and from the Android app, in
the locator's `locations.fragments`. The two systems have been speaking
the same language on either side of a border that discards it.

## Decision

Add a second protocol behind the existing mirror, not a second mirror.

Everything ADR-0047 decided about *when* a position crosses is
unchanged and is now written once for both protocols: one peer, one
account, polling on a timer, a bounded active window, newest wins, a
backoff that cannot take the server down, and books joined on the
KOReader fingerprint. What became pluggable is only the conversation
with the peer, behind a `Protocol` interface with four methods and a
protocol-neutral `Place` describing a position in whatever fidelity the
peer managed.

`mirror.protocol` selects it. It defaults to `kosync`, which is both
the more compatible choice and, for the reason below, the better
secured one.

### What the native path buys

A CFI in both directions. On this side that is immediately worth
having: the web reader's restore path offers a stored CFI before
anything else and resolves the spine step in the browser, so an inbound
position carrying nothing but `locations.fragments: ["epubcfi(...)"]`
reopens on the exact spot with no resource named and no CFI parser on
the server.

A KOReader xpointer also survives instead of being flattened, because
BookOrbit stores one beside the CFI. A position this server took from a
real KOReader device therefore reaches the peer in the shape the peer
would have received it in directly.

And there is no reset ambush. The synthetic start-of-book reply
ADR-0047 has to refuse is fabricated only on BookOrbit's KOReader read
path; the native read returns the stored row.

### Three facts about the peer, and what each one forces

**Its progress row has no device.** It is keyed by file and reader, and
the write has no device field. ADR-0047's echo rule, which recognises
this server's own writing by the device id on it, has nothing to match
on.

So the echo is recognised by its content instead. The last push is
remembered on the cursor as a mark — the CFI when there is one, the
rounded fraction when there is not — and a read identical to it is
taken as our own writing. This is sound rather than a compromise. A
read identical to the last write is either the echo or somebody having
independently arrived at the same place, and applying it would be a
no-op either way. If the peer ever normalises a CFI on write, the worst
case is one duplicate op per push, already bounded by the existing
timestamp guard, and no loop is possible because an applied op is filed
under the peer's device prefix and is never pushed back.

The mark is spent the moment the peer shows something else. It
describes what the peer's row holds because of this server, and once
the row holds a reader's own position it no longer does. Keeping it
would mean that a reader who wandered off and came back to that exact
sentence, months later, had their return read as our echo and ignored.

**Nothing maps a file to a file id.** BookOrbit computes the same
KOReader partial MD5 this server does and stores it on every file. It
exposes it nowhere. There is no lookup by hash, by path or by filename,
the free-text search covers title, author, series and narrator only,
and the only file identity on the wire is a size on the book card plus
a filename and an absolute path on the book detail — and that absolute
path is the path inside the peer's own container, which is not this
server's path for the same bytes.

So a resolution ladder that narrows on metadata and decides on bytes:
search by title to get a shortlist, keep only the books holding a file
of exactly our size and format, refuse more than one survivor rather
than guess, confirm the filename on the detail route, and confirm the
path too when an operator has said how the two machines spell the same
directory. A search that fills its page is not a shortlist and is
refused outright: the match it offers may have a twin on the page
nobody asked for, and paging through to find out would be a great many
requests to reach the only useful answer, which is "too many". The answer is cached on the cursor, and a book that does not
resolve is remembered as unresolved and retried a day later rather than
searched for every poll.

The cache is stamped with which peer gave the answer, and so is the
rest of the cursor. A mirror is keyed by the name an operator chose,
and nothing stops that name outliving a change of address or of
account, so `Protocol` grows an `Identity` — a short digest of the
protocol, the address and the account, held in
`mirror_cursors.peer_identity` — and a cursor whose stamp does not
match the peer in front of it is discarded whole before the exchange
begins.

Whole, rather than just the lookup cache, because every field is a
statement about one library. A remembered file id would name a
different book on a different installation. A remembered "already
sent" would silence a position for a peer that never received it. A
remembered remote clock would make that peer's real position look like
something already taken. The one that is easiest to miss: a position
this server learned from the old peer is filed under the peer's device
prefix and is therefore never pushed back, which is right for the peer
it came from and wrong for a library that has never seen it, so the
first exchange after an identity change ignores that prefix too.

"First exchange" is read off the cursor — nothing pushed and no remote
clock — rather than remembered from the moment the cursor was reset. A
new peer that does not hold the book yet, that is down for a week, or
that refused the push halfway has not had its turn, and a flag spent on
those passes would lose the position for good: the next time the book
did resolve, the only local position would look like one of that
peer's own and never be offered to it. Those two watermarks are used
and no others, because both are written when something crosses or is
deliberately refused and neither is written by a failure. The time of
the last poll is not one of them: it is recorded the moment the peer
answers at all, which is before the push that answer may call for.

This is the syncer's job rather than any protocol's, so it holds for
the KOReader path as much as the native one. The credential is
deliberately not part of the identity: a rotated password is the same
library and the cursor should survive it. The cost is one slower pass
after a move; the cost of not doing it is a reader's position read
from and written to a stranger's book.

A candidate's catalog facts are themselves scoped, in the store, to the
folders the mirrored account was granted. The fingerprint on a work is
the reader's own and says only that they hold those bytes; it is not a
claim on a shelf nobody gave them. That matters more here than in an
ordinary read, because the title is what gets typed into another
server's search box.

Refusing ambiguity rather than guessing is the same rule ADR-0047
applied to an ambiguous fingerprint, for the same reason: the cost of
being wrong is a reader's place in the wrong book.

**Its credential is a real account password.** Native login takes the
account username and password and hands back a short access token and a
rotating refresh token. There is no personal access token, no app
password, nothing scoped. BookOrbit's KOReader credential, by contrast,
is a separate non-expiring pair that unlocks progress by hash and
nothing else.

So the native path is the less safe of the two, and that is not a
detail to be discovered later: where the KOReader path keeps a scoped
key in the config file, this one keeps a password that opens the whole
peer account. It is the reason `kosync` stays the default, and it is
said in the deployment documentation and on the admin page rather than
left to be worked out.

Tokens are held in memory only. Sign in at startup, refresh before the
access token expires, sign in again if a refresh is refused, and sign
out on shutdown so sessions do not accumulate on the peer. Nothing
token-shaped reaches the database or a log line.

Holding that line takes two more rules, because a mirror failure is
logged *and* written to `mirror_cursors.last_error`, where an operator
will read it. A sign-in failure is never quoted at all, since its
request carried the password. Every other failure is quoted, because
the peer's own words are usually the answer, but with the access token,
the refresh token and the password struck out of the text first: a peer
or a proxy that echoes the request headers back in an error page must
not be able to write a live token into this server's database. And the
peer's address may not carry a credential either — userinfo, a query
string and a fragment are all refused at startup — because that address
is logged whole when the mirror starts.

### Churn

`/api/v1` is a fixed prefix, not a stability promise: the peer's
catalog controller changed in most of its weekly releases this year and
its 3.0 shipped a breaking change to authentication. So every reply is
decoded leniently and every unknown field ignored, the search reply's
envelope is found rather than pinned, the peer's version is read once
after sign-in and logged so a broken pass is diagnosable, and every
failure stays inside the existing backoff. The KOReader path is
untouched and stays the default precisely because it does not move.

## Consequences

An inbound position keeps `origin=kosync` and the `partial-md5:`
alias whichever protocol carried it. ADR-0047 chose the existing origin
so that insights grouping, rollups and compaction never learn the
mirror exists, and nothing about that reasoning changes with the wire
shape.

The inbound locator names no resource, because the peer did not say
which one and guessing would file the position against the wrong
chapter. This server's reader resolves the CFI itself, so it costs
nothing here. Readium on Android wants the resource and falls back to
the fraction, which is where the KOReader bridge left it anyway. So the
fidelity gain is real on this server's reader and not yet on the phone,
and the documentation says so rather than implying parity.

A residual unknown: whether a reading reset on the peer also zeroes its
stored row, or only fabricates the KOReader reply, is not established
from its source. The guard is therefore kept in spirit rather than in
letter — a remote position at exactly zero never displaces a non-zero
local one — which costs nothing a reader would notice and cannot send
anybody to page one.

An upstream ask would delete the resolution ladder outright. If
BookOrbit exposed the file hash it already stores, or offered a lookup
by it, the join would be the same fingerprint this server computes for
ADR-0046 and the title search, the size match and the path mapping
could all go. Worth filing; not worth waiting for.

The seam is behaviour-preserving for the existing path, and the
existing mirror tests are the proof. If they had needed editing beyond
a field rename, the seam would have been in the wrong place.
