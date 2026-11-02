# ADR-0059: Deriving a position across protocols

- **Status:** Accepted
- **Date:** 2026-11-02
- **Depends on:** [ADR-0017](0017-folders-not-pipelines.md),
  [ADR-0026](0026-credential-enrolment-does-not-weaken-account-authentication.md)
- **Amends:** the rule that the server never invents a position
  (`internal/mirror/sync.go`), for reads only

## Context

A KOReader-engine device (KOReader, CrossPoint Reader) records its place
as a CRe XPath with a character offset,
`/body/DocFragment[3]/body/p[42]/text().17`. Liseur's web reader and
Android app record a Readium locator, which they make exact with an
app-owned text anchor: `liseurAnchor: 1`, a `cssSelector`, and a
`text.before` / `highlight` / `after` quote.

Each side's exact place is opaque to the other. The kosync adapter keeps
the XPath verbatim in `foreign_pos`, so KOReader to KOReader is exact,
but every crossing between the two families falls back to a book-wide
percentage. On an e-ink device a percentage lands pages away from the
passage.

The server has the EPUB both sides read, so it can translate.

## Decision

**A position is translated when it is read, never when it is written.**
Stored ops are unchanged: no new op shape, no migration, and an op never
gains a field it was not pushed with. A translation is computed per
response from the op and the catalog, and is marked as derived wherever
it appears. A translation that fails for any reason falls back silently
to what the server returns today.

**Same file only.** A position is interpreted only in the EPUB it was
written against. A native op names that file by `edition_sha`. A kosync
op names a partial MD5, which resolves only among catalog books the op's
owner currently holds a folder grant for, and only when they share
exactly one content `sha256` (byte-identical copies are fine). Partial
MD5s can collide, so two different EPUBs behind one fingerprint mean no
translation. A translation is emitted only when the source edition and
the target edition are the same file. Works join several editions on
purpose, and a selector resolved in the wrong one can land on an
unrelated passage.

**Exact positions only.** An XPath is derived from an exact anchor: a
Liseur text anchor (`liseurAnchor: 1`), resolved in that edition. A
locator with only a progression never becomes an XPath, and neither does
a CFI: no Liseur reader writes one, so the only CFIs on the server came
from mirror peers, and translating them is left for when a client needs
it. Readium progression and CRe
offsets weigh images, markup and density differently, so turning one into
the other would claim a precision nobody measured. An XPath names an
element and an offset, so translating it into a Liseur anchor is exact,
and the quote is taken from the file at that offset.

**Text is counted the way CrossPoint counts it.** `DocFragment[n]` is
the nth spine itemref, counting `linear="no"` items that Readium's
reading order leaves out. A text node is the
character data between two pieces of markup (an element boundary, a
comment, a processing instruction, a CDATA boundary). Text inside
`head`, `style`, `script`, `title` and `rp` is not counted, though those
elements keep their place among their siblings. Offsets are raw Unicode
code points, and a position in whitespace-only text moves to the next
text. Generated steps always carry an index, which KOReader reads too.
KOReader's engine differs in two places: it drops some whitespace-only
nodes, which shifts a `text()` index, and it collapses whitespace before
counting, which shifts an offset inside a node whose source wraps lines.
An XPath is read CrossPoint's way first and, when that lands in
whitespace or past the end of the node, again skipping whitespace-only
nodes. A KOReader offset can therefore land a few characters away inside
the right node, never in another paragraph. CrossPoint's own resolver
tests are the fixtures. A translated anchor is emitted only when its
quote occurs exactly once in its block, and an anchor is translated
only when its selector names an element whose text holds the quote
exactly once; otherwise there is no translation.

**Where translation happens.**

- A kosync `GET /syncs/progress/{document}` whose newest op has no
  `foreign_pos` returns a derived XPath as `progress` when the op carries
  an exact anchor and its `edition_sha` is the file behind the requested
  document. Otherwise `progress` is unchanged.
- `GET /v1/changes`, `GET /v1/heads` and `GET /v1/works/{id}/positions`
  fill a kosync op that has an XPath with a derived `edition_sha` and an
  exact Liseur locator: `href`, in-resource `progression`, `cssSelector`,
  the quote, and `liseurAnchor: 1`. A `derived` object says so:
  `{"from": "kosync-xpath"}`. Every other field, including `device_id`,
  `origin` and `foreign_pos`, is reported as stored. A client never
  pushes a derived locator back as its own position.

**Authorization.** Translating reads publication text, so it sits behind
the catalog boundary. Every lookup runs as the op's owner, never as an
empty viewer, and needs a current grant on a folder holding that
edition. A derived locator carries words from the book, so it is
returned only to a credential that also holds `library-read`. A derived
XPath carries no text and needs only the grant. Without these,
translation is skipped silently: a plain sync response is never refused
because a translation could not be made.

## Consequences

- KOReader and CrossPoint devices and the Liseur readers land on the same
  passage of the same file, in both directions.
- A device reading a different file of the same work (another download,
  a re-saved EPUB) still syncs by percentage. So do two different EPUBs
  that share a partial MD5.
- The same op can read differently over time: revoking a grant, losing
  `library-read`, or deleting the book takes the translation away, and
  restoring the book brings it back. Clients must not cache a derived
  locator as if it were the op.
- Responses that carry kosync ops now open EPUBs. That cost is bounded by
  the number of kosync ops in a response, and a translation is cheap next
  to downloading the book.
- `mirror` still pushes only what an op holds. Translating for peer
  servers is a separate decision.

## Implementation phases

1. `internal/xpointer`: parse and generate CRe XPaths, resolve them to a
   spine resource and text offset, and convert to and from Liseur anchors,
   with CrossPoint's resolver fixtures.
2. The kosync read derives an XPath, with conformance tests for another
   edition, a progression-only op, a revoked grant and a partial-MD5
   collision.
3. The three native reads derive a locator, with tenant-isolation,
   scope and grant tests, documented in `openapi.yaml` and
   `docs/integrating.md`.
4. Liseur Android and the web reader restore exactly from a derived
   locator.

## Acceptance criteria

- A position written on CrossPoint opens on the same paragraph in the web
  reader and on Android, and the reverse, with the same EPUB everywhere.
- A progression-only position, another edition, an ungranted folder or a
  colliding partial MD5 gives today's percentage, never an error.
- A sync-only credential never receives text from a book it cannot read.
- No stored op, session or rollup changes.
