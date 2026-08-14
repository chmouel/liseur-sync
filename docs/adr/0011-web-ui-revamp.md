# ADR-0011: Web UI revamp

- **Status:** Accepted
- **Date:** 2026-08-14
- **Depends on:** [ADR-0006](0006-catalog-api-and-opds.md),
  [ADR-0007](0007-web-reader.md)

## Context

The web UI is correct and unpleasant. It grew a page at a time alongside
the catalog, and every page was answered the same way: put the rows in a
table. There are twenty-one tables across nine templates, a forty-pixel
cover in a table cell, a top nav, and about a hundred lines of CSS in a
warm light palette.

That shape is right for tokens and devices, where a row is a record with
fields. It is wrong for a library. Browsing books is a visual task —
people recognise a cover before they read a title — and a table of
filenames makes a shelf of two hundred books harder to use than a
directory listing. The comparison the user reached for is
[Komga](https://komga.org): dark by default, a persistent left rail, and
a wall of cover art with progress painted across the bottom of each card.

Two things about the current code make this cheaper than it looks. The
cover pipeline already renders and caches two sizes on disk keyed by
blob digest, and the UI already asks for the thumbnail; a grid needs no
new image machinery. Cursor pagination already exists on the book list,
so an endless grid is wiring rather than invention. One thing makes it
more expensive: htmx has been vendored since the first UI commit and is
used nowhere — fifty-one kilobytes shipped to every page for nothing.

## Decision

Rewrite the presentation layer into a library rather than an admin
panel, and change nothing else. No store method, no API route, and no
adapter behaviour is in scope; this ADR may not add a way to read data
that did not exist before it.

**The constraint that shapes everything: no build step.** Hand-written
CSS in `internal/webui/static/`, embedded and served like every other
asset. No npm, no CDN, no framework. Komga is a Vuetify application and
this will not be one. The target is recognisably the same shape and
density — sidebar, cover grid, progress on the card, detail page with a
hero cover — not a pixel copy of its Material components. Ripples,
elevation transitions and virtualized scrolling are not worth
hand-writing and are not being written.

**Dark first.** The default theme is dark, because that is what a
reading application is for and what the comparison does. Light remains,
as a full palette rather than a grudging inversion.

**The theme is a cookie, not local storage.** The server renders
`<html data-theme="dark">`, so a reload cannot flash the wrong palette,
and the toggle is a form POST carrying the session's CSRF token like
every other mutation in this UI. It therefore works with JavaScript
off. Local storage would mean a client-side flash on every navigation
and a preference the server cannot honour on first paint.

**Tables are kept, not deleted.** The grid becomes the default view and
the table becomes the list view, behind a toggle stored in the same
cookie. Dense browsing is a real use, a grid is a poor way to scan five
thousand titles, and keeping both costs a class rather than a rewrite.

**htmx starts being used or it goes.** Endless scroll on the existing
cursor, and inline actions that do not reload the page. The rule that
makes this safe is the one already followed everywhere: every mutation
works without JavaScript, and htmx only removes a full page load.

**A progress bar's width is a class, not a style attribute.** A
rounded ladder of percentage classes keeps every declaration in the
stylesheet. `/ui` sets no Content-Security-Policy today — only the
reader does — and this decision means adding one later will not first
require unpicking a hundred inline styles.

**The reader is out of scope.** Its chrome is deliberately excluded.
It has an open rendering problem, and mixing a layout rewrite into a
rendering bug hunt would make both harder to judge.

## Consequences

- Every template is touched. This is a wide, shallow change that will
  conflict with anything else editing the UI, so it is done in one run
  rather than alongside other work.
- The web UI tests churn. They assert mostly on content and hrefs
  rather than on markup, which bounds the damage, but the scope matrix
  gains a route and several page tests gain new selectors.
- A first visit to a large library renders many thumbnails. They are
  cached on disk afterwards, keyed by digest, so this is a cold-start
  cost rather than a per-visit one — but it is a real one, and worth
  measuring before deciding it needs a queue.
- Accessibility is inherited work: focus rings, reduced motion, a skip
  link and keyboard reachable actions are part of the rewrite, because a
  rewrite is the only cheap moment to get them right.

## Implementation phases

1. **Design system and shell.** Tokens — colour scales, spacing,
   radius, elevation, type scale — dark first with a light override.
   `layout.templ` becomes a left rail, a top bar holding search and the
   account menu, and a content column, collapsing to a drawer on narrow
   screens.
2. **Preferences.** Theme and view mode as a cookie, rendered onto the
   root element, written by `POST /ui/preferences` with CSRF, added to
   the scope table.
3. **The card and the books grid.** A cover card component, a
   responsive grid, and a browse toolbar carrying sort, filters and the
   grid/list toggle.
4. **Endless scroll.** htmx against the existing cursor, with handlers
   answering a fragment when `HX-Request` is set and a whole page
   otherwise.
5. **Works, entities, search** on the same grid, with an entity card
   variant for an author or a series.
6. **Book detail** as a hero: cover left, metadata and chips right, a
   prominent Read button, activity below.
7. **Dashboard** as stat cards and a continue-reading shelf.
8. **Devices, settings, admin** restyled, with tables that become
   card-per-row when the screen is narrow.

## Acceptance criteria

- No route gains or loses authentication, and no query gains a way to
  read across users. The scope matrix test is the check, extended with
  the preferences route.
- Every page renders and every mutation completes with JavaScript
  disabled. htmx may remove a page load; it may not be the only way to
  do something.
- The stylesheet contains every declaration: no `style` attribute
  carries presentation, so a CSP for `/ui` remains a decision rather
  than a migration.
- A cold reload never paints the wrong theme, because the theme is known
  before the first byte of body is written.
- A cover card states its intrinsic dimensions, so a grid does not
  reflow as images arrive.
- `go build` is still the whole toolchain, and `/ui/static/` still holds
  every asset the browser fetches.
