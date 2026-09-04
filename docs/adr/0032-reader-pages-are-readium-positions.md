# ADR-0032: The reader's page is the app's page

- **Status:** Accepted; implemented
- **Date:** 2026-09-03
- **Amends:** [ADR-0031](0031-web-reader-footer.md), which put a page
  number in the footer and took it from the engine's locations;
  [ADR-0012](0012-reader-engine-foliate.md), whose vendored engine
  gains three small patches

## Context

ADR-0031 gave the web reader a footer and said its page was "the same
idea as the app's positions". Same idea, different arithmetic — and a
reader holding a phone in one hand and a laptop in the other sees two
numbers for the same paragraph:

| Client | Shown | Count |
|---|---|---|
| Web reader (foliate-js) | `853 of 1156` | `ceil(Σ uncompressed spine bytes / 1500)` |
| Android app (Readium 3.3.0) | `427 of 578` | `Σ max(1, ceil(stored entry bytes / 1024))` |

Both from the same file: *Dominium Mundi Livre 2*, 14 linear spine
items, 1,733,097 uncompressed bytes.

It looks like a doubling and is not. The ratio is
`(uncompressed / stored) × (1024 / 1500)`, so it tracks how well each
book compresses: 2.00× for that book, 1.54× for *Red Rising 3* (768
against 498). Two units that happened to land on a round number once.

Readium's number is the one the reader already trusts, because it is
the one on the device they read on most, and it is the number the app
has always shown. It comes from
`ReflowableStrategy.recommended` — `ArchiveEntryLength(pageLength =
1024)` — which counts each reading-order resource as
`max(1, ceil(archiveEntryLength / 1024))` and sums them. The archive
entry length is the resource as it is *stored*: the compressed length
for a deflated entry, the plain length for a stored one, falling back
to the resource's own length when the container cannot say. Non-linear
spine items never enter the reading order, so they count for nothing.

Foliate's locations are a perfectly honest measure of a book. They are
simply a different one.

## Decision

**The web reader counts Readium's positions.** The footer's `n of m`
is the position count above, computed from the book the browser already
holds. For every EPUB, the browser and the app now name the same page
for the same spot.

**It is display only.** The `progression` fraction the reader pushes is
the engine's, untouched, so no stored op changes meaning, nothing is
migrated, and there is no server or API change. Two consequences of
that are deliberate:

- **The percentage stays as it is.** Foliate weights it by uncompressed
  bytes and Readium by positions, so the two disagree slightly — 74%
  against 73% on the book above, under a tenth of a percent apart.
  Making them agree would mean changing the number every web reader
  sends the server, which is a great deal of risk to buy a rounding.
- **"Time left" stays as it is.** Foliate estimates from a fixed 1600
  bytes per minute; the app measures the reader's own pace. That is a
  real disagreement in the same footer and a separate decision.

**The arithmetic lives in our tree, not the engine's.**
`static/reader-positions.js` is pure functions over a book's sections —
`positionTable`, `pageAt` — with no DOM and no engine in it, so the
agreement it exists to guarantee is checked by `node --test` in CI
rather than only by a browser check that skips.

**The engine's own locations remain the fallback.** A book the recipe
cannot measure — nothing linear, no lengths — leaves `location.current`
and `location.total` saying what page it is, exactly as before. A
footer that has nothing honest to say still says nothing rather than
inventing something, per ADR-0031.

**Three patches to the vendored engine**, all additive, all recorded
here because ADR-0012 makes updating foliate-js a deliberate re-copy
and a re-copy must reapply them:

1. `view.js`, `makeZipLoader`: a `getCompressedSize` beside `getSize`,
   reading the zip entry's `compressedSize` and falling back to its
   `uncompressedSize`. `makeDirectoryLoader` aliases it to `getSize`: a
   loose directory has no archive entry, which is the fallback Readium
   takes too.
2. `epub.js`: the loader's `getCompressedSize` is carried onto each
   section as `compressedSize`, beside the existing `size`.
3. `view.js`, `#onRelocate`: `sectionFraction` on `lastLocation`. The
   engine has the within-section fraction in hand there and nowhere
   else, and the position containing a spot is computed from it.

`progress.js` is deliberately untouched. Its `fraction` and its `time`
are what the reader syncs and what the middle slot says; leaving it
alone is what makes this change display only.

## Consequences

- `TestReaderPositionArithmetic` runs
  `testdata/readerpositions.test.mjs` under `node --test`, alongside
  the session tests. It pins the per-resource floor, the exclusion of
  non-linear sections, the fallback to the resource's own length, the
  clamps, monotonicity across a section boundary, and the fourteen real
  stored lengths that must come out at 578.
- The browser check asserts the footer's `m` exactly. The expected
  value is computed in Go from the fixture archive with the same
  recipe and passed as `SMOKE_PAGES`, because the fixture is deflated
  by whatever Go compiles the test and a written-down total would be a
  test of Go's compressor.
- **A position is not a screen.** A short chapter is one position
  however many screens it takes at a large font, so turning a page can
  leave the number where it was. That is what a stable page number
  costs and what the app has always done; the browser check's
  "counts forward" is `>=` with an overall rise, not a step per turn.
- A third page count still exists and is untouched:
  `editions.page_count`, KOReader's own, arriving through the koplugin
  adapter and used by insights and the dashboard. The reader does not
  have it and does not want it — it is per user and per edition, and
  absent for most books.
- No `docs/openapi.yaml` change, no `docs/integrating.md` change, no
  migration, nothing new stored server-side.
