# ADR-0007: Web reader

- **Status:** Accepted
- **Date:** 2026-08-13
- **Supersedes:** the deferral recorded in this file on 2026-08-12
- **Depends on:** [ADR-0005](0005-upload-and-ingestion.md),
  [ADR-0006](0006-catalog-api-and-opds.md)

## Context

A browser reader completes the self-hosted product: a user with a URL and
a password can read their library without installing anything. The
original decision was to defer it until the catalog was stable and in real
use, and to record two constraints so that earlier features could not
paint the reader into a corner. That condition is met — upload, catalog,
metadata editing, search, duplicate reporting and OPDS are all built — so
the deferral is replaced here by the decision it asked for.

An EPUB is an archive of publisher-controlled HTML, CSS, images, fonts and
sometimes scripts. It is the only untrusted, executable content this
server holds. The danger is not that the content is rendered; it is
*where* it is rendered, and what that place has access to.

Three questions were left open and are answered below: renderer choice,
content isolation, and token lifecycle.

## Decision

Build the reader, in the order given under implementation phases. Three
decisions carry it.

### The browser opens the archive; the server never unpacks it

The reader fetches the whole publication from the existing
`GET /v1/books/{id}/download`, which already serves the stored blob with
`Accept-Ranges` and a strong `ETag`, and it unpacks the archive in the
browser. **No server route serves publisher HTML, CSS or fonts, and none
is added.**

The alternative — extracting spine resources server-side and serving them
under a per-book path — is the obvious design and the wrong one. It would
require the server to emit `text/html` it did not write, at a URL a
browser will navigate to. Every defence after that point (sandbox
attributes, CSP, a separate origin) is a mitigation of a hazard we chose
to create. Unpacking in the browser means there is no such URL to
navigate to: publication resources exist only as `blob:` URLs minted
inside an already-sandboxed document, and they inherit that document's
opaque origin. The class of bug is removed rather than defended against.

This also keeps `internal/epub` as narrow as it is today. It parses the
OPF for metadata and extracts one cover under strict bounds; it needs no
spine reader, no resource extractor, and no general "read entry N out of
this stored blob" API. That code would be a permanent attack surface in
the server, and it is not written.

The cost is honest: the reader downloads the whole book before the first
page, and very large publications are slower to open than they would be
with server-side range extraction. For a personal library this is the
right trade, and the `immutable` cache headers the download route already
sets mean it is paid once per book per browser.

### Publication content renders in frames that cannot run its scripts

The reader document is ordinary UI, served from the authenticated origin
and subject to the session cookie. Publication content is rendered
inside frames the engine builds. What keeps a book's script from ever
executing is the reader page's `Content-Security-Policy`, revised by
ADR-0012 to a per-response nonce with `'strict-dynamic'`: every chapter
document the engine creates inherits that policy, and the only script it
admits is the module tag this server wrote into this response and the
imports that module makes. A publication cannot present the nonce and
cannot even point a script tag at a same-origin file. The reader
additionally strips script elements out of every resource before the
engine touches it, so in the ordinary case there is nothing left for the
policy to refuse. A book cannot read `document.cookie`, cannot reach the
parent DOM, and cannot phone home, because nothing in it runs.

The frame is same-origin rather than opaque, which is a change from what
this ADR first decided. A paginating engine has to reach into the
laid-out document — where a CFI lands, what to observe for resizing —
and that means reading `contentDocument`, which an opaque origin
forbids. The original design paid for opacity with a renderer that could
only scroll.

The `Content-Security-Policy` on the reader page is what confines the
publication now, and it does so directly: a `srcdoc` or `blob:` document
inherits its creator's policy container, so the one header covers both.
`script-src` is a per-response nonce with `'strict-dynamic'` — the only
script it admits is the module tag the server minted the nonce for and
that module's own imports, so a book cannot even point a script tag at a
same-origin file, and there is no `unsafe-inline` and no `unsafe-eval`.
`style-src` must admit `'unsafe-inline'`, because books carry style
attributes, and `default-src 'none'` keeps the network shut.

The binding constraint recorded in the original decision — publication
content never executes on the authenticated UI origin — therefore
survives in the only form that ever mattered: it does not execute at all.
A browser check asserts this rather than trusting it. The test EPUB
carries a hostile `<script>` that stamps `documentElement.dataset`, and
the test fails if the stamp appears.

### An operator who does not want to bet a cookie on that can name a second hostname

`reader_origin` is optional and empty by default. Set to a hostname
pointed at the same server, the reader moves there and the bet changes
shape: publication content is laid out on an origin that holds no
session cookie and answers no authenticated route, so a browser bug in
sandbox enforcement reaches a page with nothing on it.

What the second hostname serves is a closed list — the reader shell and
the static assets — and everything else is a 404, enforced in one place
around the whole mux rather than route by route. A route added next year
does not appear there by being registered, and a 404 rather than a
redirect, because a redirect would teach a browser that this hostname is
a way to reach authenticated pages.

The handoff is the interesting part. The reader page there cannot be
authenticated by a cookie, since the point is that no cookie exists on
it, so the main origin authorises the book, mints the short-lived reader
token, and redirects with the credential in the URL **fragment**. A
fragment is never sent to a server: it is absent from access logs,
`Referer` headers and the operator's proxy, and readable only by script
on the origin that was navigated to. The page erases it from the address
bar on arrival, so it does not survive in history either. The two
addresses that travel beside it — where the API is, and where "Back"
goes — are not secret and ride in the query instead, where the server
receiving them can act on them: it names the API origin in `connect-src`
rather than widening the policy to "any host", and it renders the back
link only if it points at that same origin. Both are validated as bare
origins first, because a value that reaches a policy header is a value
that can add a directive.

The reader is then an ordinary cross-origin API client. `CORS` answers
the listed origins on `/v1` only, and **never** with
`Access-Control-Allow-Credentials`: the reader carries a bearer token it
was handed, and allowing credentials would let any listed origin ride a
logged-in visitor's cookie instead — the CSRF protection of the whole UI
handed to whoever an operator typed into a config file. `/ui` stays
same-origin, so a mistake in that list cannot reach it.

The cost is what an operator sees: a second hostname and certificate,
and a reading session that ends when its token expires rather than
renewing itself, because the origin that could prove who you are is not
this one.

### Position is a Readium Locator envelope, which is why the renderer is replaceable

The server stores `locator` as opaque JSON and never parses it, so
nothing in the protocol forces a shape. That freedom is a trap: two
clients that each store "whatever my engine uses" cannot resume each
other's position, and the sync protocol's entire purpose is that they
can.

The reader therefore emits a **Readium Locator** — `href`, `type`,
`locations.progression`, `locations.totalProgression`, and any
engine-specific string in `locations.fragments`, which is exactly what
that field is for. A renderer that natively speaks EPUB CFI puts its CFI
there; a renderer that speaks something else puts that there instead.
Every client can then resume approximately from the fields it
understands, and exactly when it recognises the fragment.

This makes the renderer a replaceable part rather than a protocol
commitment, which is the point: **the renderer choice is the decision
here most likely to be wrong, so it is the one made reversible.**

That reversibility was then used, which is the honest thing to record
here. The first version of this decision wrote the renderer by hand —
unpack the archive, lay out one spine item at a time — on the grounds
that every mature EPUB engine assumes an npm bundler, and that this
repository ships no build step. That reasoning was sound and the result
was not: the hand-written renderer could not turn past the second page of
a real book, and each fix revealed the next thing a reading engine is
expected to already know.

So the engine is vendored. **Which engine has since changed:**
[ADR-0012](0012-reader-engine-foliate.md) supersedes the rest of this
section. The first vendored engine, epub.js with JSZip, kept the
constraint that mattered — no build step, no CDN, prebuilt files served
from `/ui/static/vendor/` with `go build` as the whole toolchain — but
its design sizes a page by measuring the publication's own layout, and
real publications defeated that three ways in a row: off-screen
screen-reader headings, `srcdoc` sandbox script refusals, and title
pages built entirely of `position: absolute`. The escape hatch named
below (a Readium-style engine that paginates inside the frame's own
viewport and measures nothing) is what ADR-0012 takes — via foliate-js,
which does it client-side and so keeps this ADR's one-blob download,
client-side unzip, CSP, token lifecycle and locator envelope exactly as
written. The paragraphs that follow record why epub.js was chosen and
what it cost, as history.

The engine was `epub.min.js` (BSD-3) and `jszip.min.js` (MIT),
beside the other static assets, about 320 KB, pinned, with their
licences next to them. What was given up is "the
repository writes all its own JavaScript", which was a preference, not a
promise — and it bought pagination, a spine-wide progress model, CFI
positions, and typography nobody here was going to write.

Vendoring an engine buys its bugs too, and one surfaced immediately.
epub.js sizes a chapter from the bounding box of everything in the
document, and most publications — every Standard Ebooks title, among
others — park a heading at `left: -9999px` so a screen reader announces
what an eye does not see. That one element made a title page measure
thirty-two thousand pixels wide: the reader turned the page correctly,
onto forty-five blank columns, and looked exactly like the hand-written
renderer it replaced. The fix was a `rendition.hooks.content` hook that
measured only what is laid out on the page. It is worth being plain
about the trade this exposed: Komga, which does the same job, does not
use epub.js — it runs a fork of R2D2BC, a Readium engine, which
paginates inside the frame's own viewport and so has nothing to measure
wrongly. That escape hatch was taken when epub.js's sizing was defeated
a third time; ADR-0012 records how it was taken without the npm build
step that made it not the choice at the time of writing.

Neither library needed `unsafe-eval`: their `new Function` uses were in
guarded fallback paths that the browser never reached. This was checked
before vendoring, because a CSP hole for a convenience path would have
been a real cost — and the same check was repeated on foliate-js, with
the same answer.

What the reader still does not do: no full-text search inside a book, no
annotations, no read-aloud. The locator envelope means those remain
additions rather than migrations.

### The reader is an ordinary API client with a derived, short-lived token

The reader uses `/v1/ops`, `/v1/changes` and `/v1/sessions` exactly as the
Android and desktop clients do. It is not given privileged access and
gets no new sync surface. (What a sitting is in a browser — when it
opens, when it closes, what counts as idle — is decided in
[ADR-0030](0030-web-reader-reading-sessions.md).)

Its credential is minted from the web session it is already running
under: a cookie-authenticated, CSRF-protected endpoint returns a token
scoped to `library-read` and `sync` and nothing else, expiring in an
hour. Four properties make this safe and workable:

- **Derived, never elevated.** The scopes are fixed at the two the reader
  needs. A reader token can never carry `library-manage` or `admin`, so a
  stolen one cannot delete a book or mint another credential.
- **Short-lived, and refreshed by asking again.** There is no refresh
  token. The page re-mints with the cookie it already has. This is the
  same shape as the one-hour login credential, and it means expiry is the
  only revocation mechanism the reader needs to implement.
- **One web device per user, not one per tab.** The op log's heads are
  per work *and device*, so a device identity per tab would multiply heads
  and make "where did I stop reading" ambiguous between two windows of one
  browser. Re-minting reuses the stable device identity.
- **It cannot outlive its session.** Logging out ends reading everywhere,
  because a token that survived the session that created it would be a
  quiet way to keep access after signing out.

Tabs do not coordinate. A tab whose token has expired mints another; it
does not invalidate anyone else's, because a mint that revoked its
predecessors would let two open tabs invalidate each other in a loop.

Dead reader tokens are deleted when the next one is minted, not revoked:
nobody asked for them, so there is no cut-off device for a revoked row to
record, and one row an hour for as long as somebody reads is a leak. The
newest per device survives whatever its state, because it carries the
device id the next mint inherits.

None of this appears in the credential list. A reader token is not
something a person made or can manage — it is replaced before they could
finish reading its name — so the Devices page counts browsers instead,
one row per device id, with a single button that ends browser reading
everywhere.

## Consequences

The server gains a browser reader without gaining the ability to serve
publisher markup — the reader is almost entirely client-side, and the new
server surface is one credential endpoint plus one page.

Large books open more slowly than they would with server-side extraction.
Books that require scripting to render at all may not work, and that is
accepted rather than solved.

`internal/epub` stays a metadata-and-cover parser. If a future feature
does need server-side spine access, it must revisit this ADR, because
adding it silently would reintroduce exactly the hazard this decision
removes.

## Implementation phases

1. **The reader credential.** Done.

   `POST /ui/reader/token` mints the derived short-lived token described
   above, with a stable per-user web device identity. This phase is
   independent of any renderer and is what any browser-side client of
   this server needs.

2. **The reader itself.** Done, then redone.

   `GET /ui/books/{id}/read`, the unscripted iframe and its CSP, and
   position round-tripping through the locator envelope. A position
   written by another client is restored from `progression` when its
   locator means nothing here, since that fraction is the only field
   every client shares.

   The hand-written renderer this phase first shipped was replaced by a
   vendored engine after it proved unable to paginate a real book; see
   the renderer section above. `internal/webui/static/reader-app.js` is
   now the glue — credential, sync, locator translation — and the engine
   underneath it is a vendored file.

3. **Hardened content origin.** Done, and off by default.

   `reader_origin` serves the reader document from a second hostname
   that holds no cookie and answers nothing else, with the credential
   handed over in a URL fragment and the API answering it cross-origin.
   Empty means the reader stays where it was, which is what most
   deployments will want.

## Acceptance criteria

- No extracted publication asset is served in a way that lets publisher
  markup execute against the authenticated UI origin. Since no route
  serves publication resources at all, the test for this is that none is
  added.
- A script inside a publication never runs. A browser check opens a book
  whose first chapter tries to mark the document, and fails if the mark
  appears.
- The reader turns pages through a whole book. The same browser check
  turns the page repeatedly and fails if the reader stalls, which is the
  regression that cost the hand-written renderer its place. The fixture
  is shaped to provoke it: twelve spine documents and a heading parked
  off-screen, the combination that made a real book unreadable while a
  two-chapter fixture reported everything well. The check counts
  chapters left behind, not clicks survived, because a book laid out too
  wide turns the page and goes nowhere.
- With `reader_origin` set, no authenticated route and no cookie answer
  on that hostname, and the reader still opens a book and syncs a
  position from it. Both halves are checked in a real browser, because
  the redirect, the fragment and the cross-origin fetch are things only
  a browser enforces.
- A reader token carries exactly `library-read` and `sync`, expires, and
  is refused on a `library-manage` route.
- The web device identity is stable across re-mints, so two tabs and two
  sessions produce one device in the op log rather than several.
- A position written by the reader is resumable by an Android or desktop
  client, and a position written by them is resumable by the reader —
  exactly when the fragment is understood, approximately by progression
  when it is not.
- A chapter renders in a real browser. This one cannot be checked
  anywhere else: a `srcdoc` document inherits the framing page's CSP in
  addition to declaring its own, so a page policy that is too strict
  produces a blank frame and no error. `TestReaderOpensInARealBrowser`
  drives Chromium over CDP and skips where there is none, including CI —
  a developer's check rather than a gate, since the alternative is a
  browser in CI for one test or a renderer nobody ever ran.
  `TestReaderOpensInFirefox` runs the same judgement over WebDriver BiDi,
  because the two engines disagree about the things this reader leans on:
  the page-turn bug was reported from Firefox, and Firefox reports a
  refused script as a policy violation that never reaches BiDi's log
  channel at all, so the refusal is asserted structurally there — the
  chapter frame is unreachable from the page and the publication's
  script did not run.
