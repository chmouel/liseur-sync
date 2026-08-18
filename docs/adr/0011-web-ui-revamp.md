# ADR-0011: Web UI revamp

- **Status:** Implemented
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

**One library page, not a catalog page and a reading page.** The revamp
shipped `/ui/books` (what this server holds) and `/ui/works` (what has
been read) as two grids that knew nothing about each other. The same
book was on both, under two words a reader had to learn, and neither
list was complete on its own: a book nobody has opened exists only in
the catalog, and progress synced from a device holding a file this
server never saw exists only in the reading history. `/ui/library`
replaces both with their union — keyed by book where there is a book,
by work where there is not — and the two old paths are deleted rather
than redirected, this project never having shipped.

Over the grid are filter chips (`All`, `Reading`, `Unread`, `Finished`,
`On this server`) and a sort, both kept in the URL so a filtered shelf
is a link somebody can send. `On this server` is the one the page lands
on, added later: the union above is what the library *is*, but a work
with no file is a card you cannot open, and a shelf should open on the
books. `All` is one chip away and is still the whole union. Above them
is a continue-reading banner:
the most recently started unfinished book whose file is here, absent
entirely when there is nothing to go back to. It repeats a card from
the grid on purpose — coming back to a half-read book is the commonest
reason to open the page.

The two halves are paged differently, because they are counted
differently. A reading-state filter is answered from the reading
history, which is complete in memory and bounded by what one person has
read; a catalog filter is answered from the cursor-paged catalog. The
alternative — filtering a page of twenty-five catalog rows — would mean
clicking "next page" past ninety unread books to find the second book
you are in the middle of. Sorting alphabetically is missing for the
same reason and is honest about it: it needs a second cursor ordering
in the store, not a re-sort of whichever page arrived.

**A cover's actions are a link, not a menu.** Hover does not exist on a
phone and a web page cannot claim long-press, so the old reveal-on-hover
overlay left touch users with no way to a book's actions at all. The
first attempt was a ⋮ disclosure menu on each cover — no script needed,
Escape closes it for free — and it was wrong on contact: a cover is an
overflow-hidden box, so the panel was clipped by the picture it hung
off, and unclipping it meant lifting the menu out of the element it
belongs to and hand-rolling a popup. So the corner button is a link to
the book's page, which already lists every action, and the two or three
worth reaching without a page load (stats, sessions, download) are
small text sublinks under the cover. No popup, nothing to close, and
the same behaviour under a finger as under a pointer.

**Dark first.** The default theme is dark, because that is what a
reading application is for and what the comparison does. Light remains,
as a full palette rather than a grudging inversion. Tokyo Night and Rosé
Pine are also available as named dark palettes.

**The theme is a cookie, not local storage.** The server renders
`<html data-theme="dark">`, so a reload cannot flash the wrong palette,
and the toggle cycles dark, light, system, Tokyo Night and Rosé Pine as a
form POST carrying the session's CSRF token like every other mutation in
this UI. It therefore works with JavaScript off. Local storage would
mean a client-side flash on every navigation and a preference the server
cannot honour on first paint.

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
stylesheet. When this was written `/ui` set no Content-Security-Policy
— only the reader did — and the point of the rule was that adding one
later would not first require unpicking a hundred inline styles. It
since has been added, and it cost nothing:

```
default-src 'self'; script-src 'self'; style-src 'self';
img-src 'self' data:; font-src 'self'; connect-src 'self';
object-src 'none'; base-uri 'none'; form-action 'self';
frame-ancestors 'none'
```

Three things had to move for it: the layout's inline `<script>` and the
library picker's `onchange` both went into `static/ui.js`, the UI's only
script, and htmx was told not to inject its indicator stylesheet
(`includeIndicatorStyles: false` via a `<meta name="htmx-config">`,
with those rules moved into `style.css`). The reader keeps its own,
stricter, per-response nonce policy, which it sets after this one and
therefore replaces.

The policy matters because everything this UI renders about a book —
title, author, series, description — arrived inside somebody's EPUB,
and the description is HTML. The sanitizer is what removes the
dangerous parts of it; the policy is what makes a mistake in the
sanitizer survivable. `TestTemplatesStayPolicyClean` fails the build if
a template reintroduces a style attribute, an `on*=` handler, or an
un-nonced inline script, because each of those works perfectly in
development and breaks silently in production.

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
   radius, elevation, type scale — dark first with light and named dark
   palette overrides.
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

## As built

Phases 1 to 8 are in. What differs from the plan above, and why:

- **A card names its author through a batched read.** Reading
  contributors per card would be a query per book on a page of
  twenty-five, so `CatalogAuthorsForBooks` answers a whole page at once,
  scoped to the libraries the reader may read and restricted to the
  author role — a translator printed where a card says "author" is a
  false credit rather than a partial one. Three names fit; a crowd is
  counted. A book nobody is credited with still falls back to the date
  it was added.
- **The toolbar sorts by the two orders that exist.** Filter chips and a
  sort arrived with the merged library page, but only over orderings
  something can actually produce: recently added, which is the catalog
  cursor, and last read, which the reading history holds. Alphabetical
  is not offered, because it is not a control — it is a column. Titles
  would have to carry a normalized sort key written by every path that
  sets a title, since `LOWER()` sorts accents one way in SQLite and
  another in PostgreSQL, and a cursor that orders differently per
  backend is a cursor that skips rows. That is a schema decision, so it
  waits for one rather than for a layout.
- **Narrow tables scroll sideways rather than becoming cards.** A
  card-per-row stack needs every cell labelled, and the tables that are
  wide — tokens with an inline scope form, admin invites — are exactly
  the ones where that reads worst. A scroll container is honest and is
  three lines.
- **A work's tile has no cover.** A work is user-scoped sync state that
  may have no catalog entry at all. Its tile is its title and its
  progress instead of a lookup invented to decorate it.
- **The placeholder cover follows the theme.** This was not planned. It
  became necessary the moment the grid existed: a shelf of sideloaded
  books is mostly placeholders, and the old pale card turned the dark
  theme into a wall of glare. Two cached images, chosen by the same
  cookie the page was rendered from, with `Vary: Cookie`.
- **A screenshot walk was added** (`TestUIScreenshots`, skipped unless
  `LISEUR_UI_SHOTS` is set) because a layout is judged by looking at it.
  It asserts nothing; the assertions that can be written about a layout
  are in the other tests.
