# ADR-0012: Replace the reader engine with foliate-js

- **Status:** Accepted
- **Date:** 2026-08-15
- **Depends on:** [ADR-0007](0007-web-reader.md)
- **Supersedes:** the renderer choice in ADR-0007. Everything else in
  that record — client-side unzip, the CSP, the token lifecycle, the
  locator envelope — stands unchanged.
- **Amended by:** [ADR-0031](0031-web-reader-footer.md), which moved the
  figures out of the top bar;
  [ADR-0032](0032-reader-pages-are-readium-positions.md), which patches
  the vendored engine in three places

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
script never runs therefore rests on two other fences, both under this
project's control: the reader page CSP, whose `script-src` is a
per-response nonce plus `'strict-dynamic'` — every `blob:` chapter
document inherits it, and the only script it admits is the module tag
the server minted the nonce for and that module's own imports, so even
a script tag aimed at a same-origin file gets nothing — and the
reader's transform hook, which parses each (X)HTML and SVG resource
with `DOMParser` and removes every script element in any namespace
(regexes miss SVG islands and parser-repaired markup; parsing the
document exactly as the engine will leaves no second interpretation to
aim between), besides emptying JavaScript-typed resources outright.
The browser tests keep asserting the observable facts — the fixture's
inline, SVG-embedded and same-origin-external scripts do not run, and
its attempt to reach the parent page changes nothing — rather than the
mechanism.

## Consequences

- epub.js and JSZip are removed; all three measurement patches die with
  them. Net vendored weight drops by roughly a third.
- The reader page loads one `<script type="module">`, carrying the
  response's CSP nonce; the engine arrives as that module's imports,
  which `'strict-dynamic'` trusts transitively.
- Only the EPUB pipeline is vendored. foliate-js can read MOBI, FB2,
  CBZ and PDF, but the catalog only serves EPUBs to the reader; those
  loaders are dynamic imports that are simply absent.
- Upstream has no releases; the pin is a commit hash recorded in
  `foliate-js.LICENSE`. Updating is a deliberate re-copy, which for a
  security-sensitive component is a feature. A re-copy must reapply the
  three additive patches listed in
  [ADR-0032](0032-reader-pages-are-readium-positions.md); each is
  marked `liseur-sync patch` in the vendored source.
- The user stylesheet the engine injects after the publication's own is
  what reader appearance settings (theme, font, size, spacing, layout,
  and whether the bar and page-turn arrows hide themselves while you
  read) are built on. They are a browser preference in `localStorage`,
  applied client-side, and never sent to the server; the default for
  every one of them is the publisher's design, untouched.
- The chrome that hides has to leave a way to turn a page behind it, so
  the sides of the window turn pages wherever they are clicked —
  margin or text — and the middle brings the chrome back. Clicks on the
  text are handled per chapter document, next to the reading keys,
  because a chapter is a document of its own and nothing that happens
  over it reaches the page. A drag, a selection or a control is not a
  tap and is left alone.
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
