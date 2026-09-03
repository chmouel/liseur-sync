# ADR-0031: The web reader's footer mirrors the app

- **Status:** Accepted; implemented
- **Date:** 2026-09-03
- **Amends:** [ADR-0012](0012-reader-engine-foliate.md), which put the
  chapter name and the percentage in the top bar

## Context

The web reader showed its two figures — chapter title, percentage —
crammed into the right end of the top bar, with no page number, and
they left the screen whenever the bar stepped aside. That is exactly
when a reader glances for them.

The Android app settled this long ago with a quiet line under the
text, Kindle-style: the percentage on the left, the page on the
right, a middle slot that says the chapter's name or the time left,
cycled by a tap. Its pages are Readium's synthetic positions, a fixed
slice of the book's bytes, so the number is the same on every device
and at every font size. That footer stays while the bars are hidden
and goes away in scroll mode, where the text runs under the bottom
edge.

Foliate hands the web reader the same ingredients on every relocate:
`location.current/total` (its "locations", ~1500 characters each),
`tocItem.label`, and `time.section` / `time.total`. The engine also
already keeps a bottom margin under the text — the Margins setting —
and draws nothing in it.

## Decision

**One line, three slots, in the engine's bottom margin.** The footer
is a positioned element inside the stage whose height is the same
value the engine uses for its margin, so it never covers a line and
the stage is never resized; a stage that changed height would
repaginate the book out from under the reader. Its left and right
edges are the text's. It is drawn over the stage's own theme, so its
ink follows the reading theme, not the application's.

**Pages are the engine's locations.** The right slot says `n of m`
with `n = location.current + 1`, clamped to `m`. This is the same idea
as the app's positions: stable across font sizes, derived from the
book alone. It is not the publication's print page-list (most books
have none, and a number that appears for some books and not others is
worse than one that always means the same thing), and it is not the
server's edition page count, which the web reader does not have. The
"never fabricate page numbers" rule in the design is about
*statistics*; nothing here is sent anywhere, and a locations counter
is the engine's honest measure of the book, not a guess.

**The middle slot cycles.** Chapter title by default, then time left
in chapter, then time left in book, then empty, then round again. A
click or a key on the footer advances it; the same choice has a home
in the Aa panel as a "Footer" fieldset. It is a browser preference,
kept alongside the other appearance settings in `localStorage` and
never sent to the server, exactly as ADR-0012 decided for fonts and
themes. A slot with nothing honest to say — a book whose navigation
covers no entry, an engine with no time estimate — stays empty rather
than inventing something.

**It stays while the bars are hidden, and leaves in scroll mode.**
The footer is not part of the chrome that auto-hides; the figures are
what a reader looks for mid-page. In the scrolled layout the engine
clears its margins and the text runs to the bottom, so the footer is
hidden there. It comes back the moment the pages do.

**A click on the footer turns no page.** The footer is not a stage
surface: the stage's tap model checks its target, and the footer is
never one of them.

With the figures out of the bar, the bar is only the way back, the
title, and three controls of one shape.

## Consequences

- `#reader-chapter` and `#reader-progress-text` keep their ids and
  move into the footer; `#reader-page` is new. The browser check
  (`TestReaderOpensInARealBrowser`) asserts the page reads `n of m`
  with `n ≤ m`, counts forward with the page turns, survives the
  chrome hiding, leaves in scroll mode, and that a click on the
  footer cycles the slot without moving the position.
- The footer's font size follows the margin: at the narrow setting
  (16px) it is small. That is the trade the app makes too, and the
  margin values are unchanged so nobody's pagination moved.
- No server change, no API change, nothing new stored server-side.
