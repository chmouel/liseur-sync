# ADR-0012: Replace the reader engine with foliate-js

- **Status:** Accepted
- **Date:** 2026-08-15
- **Depends on:** [ADR-0007](0007-web-reader.md)
- **Supersedes:** the renderer choice in ADR-0007. Everything else in
  that record — client-side unzip, the CSP, the token lifecycle, the
  locator envelope — stands unchanged.

## Context

ADR-0007 made the renderer the reversible decision, and it has now been
reversed twice. The hand-written renderer could not turn a page. Its
replacement, epub.js, could — until real publications arrived, and then
it failed three separate ways, each one a consequence of the same
design property: **epub.js measures the publication's own layout to
size its pages.**

- A heading parked at `left: -9999px` — the screen-reader idiom used by
  every Standard Ebooks title — made a chapter measure thirty-two
  thousand pixels wide. Patched with a content hook that measures only
  what is on the page.
- epub.js loads chapters into a `srcdoc` iframe sandboxed as
  `allow-same-origin` only, and modern Chromium refuses to run *any*
  script in such a frame — including the engine's own instrumentation.
  Patched by enabling `allowScriptedContent`, leaning on the page CSP
  to keep publication script from running.
- A commercial title page carrying its entire content inside
  `position: absolute; width: 100%; overflow: hidden` measured zero
  wide, and the book opened onto a blank page that no fallback
  measurement could repair honestly.

Three patches in, the pattern is the diagnosis: any engine that asks a
publication how big it is will be lied to by some publication. The
comparison the user reached for was Komga, whose reader simply works on
the same files. Komga runs a Readium engine (a fork of R2D2BC) — which
paginates inside the frame's own viewport, measuring nothing — but it
feeds that engine over HTTP: the server exposes a manifest and a route
for every resource inside the book, and the browser fetches publisher
HTML from the server's own origin. ADR-0007's central decision is that
no such route exists here — the archive travels as one authenticated
blob and is opened in the browser — and it also requires an npm build
step, which this repository does not have.

## Decision

Vendor [foliate-js](https://github.com/johnfactotum/foliate-js), the
engine of the Foliate desktop reader — a project that made this exact
migration itself, away from epub.js. Pinned at commit `78914aef`, MIT,
about 190 KB of plain ES modules: `view.js` and its static imports,
plus the prebuilt `zip.js` it reads archives with. No bundler exists
upstream, so "no build step, no CDN" is not a compromise this time; the
files are served from `/ui/static/vendor/foliate/` exactly as they are
published.

What foliate-js changes structurally:

- **Pagination is CSS multi-column inside the frame's viewport.** The
  engine never asks the publication its size, so there is nothing for
  `position: absolute` or off-screen headings to break. This is the
  same layout strategy as Readium, obtained without the resource
  routes.
- **`open()` takes the archive as a `File`.** The one-blob download and
  client-side unzip of ADR-0007 carry over without adaptation.
- **Chapters live in iframes inside closed shadow roots**, loaded from
  `blob:` URLs. Nothing on the page can reach a chapter document by DOM
  query; the tests now probe through the engine's public API, which is
  the honest interface anyway.
- **Positions are EPUB CFIs and total-book fractions from day one** —
  the same `epubcfi(...)` strings the epub.js reader wrote into
  `locator.locations.fragments`, so stored positions round-trip across
  the engine change, and there is no "generating locations" pause:
  section sizes are known from the container listing.

**Where the script barrier now lives.** foliate's paginator sets
`sandbox="allow-scripts allow-same-origin"` on its frames — it needs
its own event listeners inside them. The guarantee that publication
script never runs therefore rests on two other fences, both already
built: the page CSP's `script-src 'self'`, which every `blob:` chapter
document inherits and which has no hole in it; and the reader's
transform hook, which strips script elements and empties JavaScript
resources before the engine ever makes a blob of them. The browser test
keeps asserting the observable fact — the fixture's hostile script does
not run — rather than the mechanism.

## Consequences

- epub.js and JSZip are removed; all three measurement patches die with
  them. Net vendored weight drops by roughly a third.
- The reader page loads one `<script type="module">`; the engine
  arrives as module imports under the existing `script-src 'self'`.
- Only the EPUB pipeline is vendored. foliate-js can read MOBI, FB2,
  CBZ and PDF, but the catalog only serves EPUBs to the reader; those
  loaders are dynamic imports that are simply absent.
- Upstream has no releases; the pin is a commit hash recorded in
  `foliate-js.LICENSE`. Updating is a deliberate re-copy, which for a
  security-sensitive component is a feature.
- The user stylesheet the engine injects after the publication's own is
  what reader appearance settings (theme, font, size, spacing, layout)
  are built on. They are a browser preference in `localStorage`, applied
  client-side, and never sent to the server; the default for every one
  of them is the publisher's design, untouched.
- The browser-test fixture now carries the `position:absolute` title
  page that defeated epub.js, so the failure that forced this ADR is
  pinned as a regression test.

## Acceptance

- The real books that failed — Standard Ebooks titles and the
  commercial title-page pattern — open, paginate to the end, and resume
  from stored positions.
- The fixture's hostile script does not run, in Chromium and Firefox.
- Positions written by the previous engine resolve (CFI first,
  fraction as fallback), and positions written by this one are ordinary
  Readium locators to every other client.
