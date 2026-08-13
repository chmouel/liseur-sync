# ADR-0007: Web reader

- **Status:** Proposed
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

### Publication content renders in a sandbox with no origin and no network

The reader document is ordinary UI, served from the authenticated origin
and subject to the session cookie. Publication content is rendered inside
an iframe with `sandbox` **not** containing `allow-same-origin`, so the
document has an opaque origin: it cannot read `document.cookie`, cannot
reach `localStorage`, cannot make credentialed requests, and cannot touch
the parent DOM. A `Content-Security-Policy` on that document forbids
network egress, so a book cannot phone home or leak which page you are
on.

This satisfies the binding constraint recorded in the original decision —
publication content never executes on the authenticated UI origin —
because an opaque origin *is not* the UI origin, whatever host it was
loaded from.

A separately configured content origin remains the hardened deployment
mode and is phase 3, not a precondition. It defends against browser
sandbox escapes rather than against the book, which is a different and
much rarer threat, and it costs an operator a second hostname and
certificate. The `cors_allowed_origins` config field, parsed today but
read by nothing, is what that phase will finally use; until then it stays
unused rather than being given a half meaning.

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

Given that, the first renderer is chosen on the constraint the repository
has already committed to — vendored assets, no CDN, no build pipeline
beyond `templ generate`. A renderer that ships a usable prebuilt bundle
can be vendored next to `htmx.min.js`; one that assumes an npm bundler
cannot, without introducing a Node build to a Go project that has
deliberately avoided one. That constraint, not rendering quality, decides
phase 2, and the locator envelope means a later change of mind costs a
file swap rather than a migration.

### The reader is an ordinary API client with a derived, short-lived token

The reader uses `/v1/ops`, `/v1/changes` and `/v1/sessions` exactly as the
Android and desktop clients do. It is not given privileged access and
gets no new sync surface.

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
Expired reader tokens are cleaned up when the next one is minted, so the
credential list does not grow without bound.

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

2. **The reader itself.** Next, and the one part of this ADR that needs
   explicit sign-off, because it means vendoring a third-party renderer
   into a repository that vendors almost nothing.

   A `/ui/books/{id}/read` page, the renderer vendored beside `htmx`, the
   sandboxed iframe and its CSP, and position round-tripping through the
   locator envelope. It must restore a position written by another
   client from `progression` alone, since that is the only field every
   client shares.

3. **Hardened content origin.** Optional, operator-configurable.

   Serves the reader document from a second origin so that a sandbox
   escape still lands somewhere with no credentials. This is where
   `cors_allowed_origins` becomes real.

## Acceptance criteria

- No extracted publication asset is served in a way that lets publisher
  markup execute against the authenticated UI origin. Since no route
  serves publication resources at all, the test for this is that none is
  added.
- A reader token carries exactly `library-read` and `sync`, expires, and
  is refused on a `library-manage` route.
- The web device identity is stable across re-mints, so two tabs and two
  sessions produce one device in the op log rather than several.
- A position written by the reader is resumable by an Android or desktop
  client, and a position written by them is resumable by the reader —
  exactly when the fragment is understood, approximately by progression
  when it is not.
