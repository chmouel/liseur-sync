# ADR-0049: The web reader paginates and shows its bar

- **Status:** Accepted
- **Date:** 2026-09-24
- **Scope:** Now
- **Amends:** [ADR-0012](0012-reader-engine-foliate.md) and
  [ADR-0031](0031-web-reader-footer.md)

## Context

The reader offered two layouts: pages and continuous scrolling. Compact
screens defaulted to scrolling with narrow margins, and the reader bar
hid itself after a short pause to leave more room for the book. In use,
continuous scrolling was not reliable enough to remain a first-class
choice. The hidden bar compounded the problem by removing the controls
before a reader had settled into the page.

Pagination is the reader's established navigation model everywhere
else: the arrows, keyboard shortcuts, tap zones, footer and Readium
positions all describe pages. Keeping a second layout meant teaching
those same controls a second set of edge cases without making the
common path better.

## Decision

**The web reader has one layout: pages.** The appearance panel no
longer offers a layout choice. Readium is always submitted with
`scroll: false`, the renderer always retains the selected bottom
margin, and a `flow: "scrolled"` value saved by an older build is
ignored. There is no dormant mode to reactivate.

**The reader bar is visible by default everywhere.** Compact screens
still choose narrow margins by default, but they no longer choose
auto-hide. A reader can opt into auto-hide under **Aa**, and a saved
opt-in continues to work. The `z` key and the existing tap model can
still hide or restore the chrome.

Because scrolling no longer clears the engine's bottom margin, the
reading footer from ADR-0031 remains available in the reader's single
layout.

## Consequences

- Existing browser preferences do not need a server migration. The
  reader ignores the retired flow value when loading local storage,
  and all other appearance settings keep working.
- The footer no longer needs a scroll-mode exception in either the
  stylesheet or the browser test.
- No route, API payload, reading position, or session format changes.

## Acceptance

- The reader settings panel has no flow control, and the engine submits
  `scroll: false` regardless of the viewport or stored browser settings.
- A fresh compact reader opens with the bar visible and narrow margins;
  the **Auto-hide bars** control can still opt into hiding it.
- A stored `flow: "scrolled"` value is ignored, and the reading footer
  remains visible with the page layout.
