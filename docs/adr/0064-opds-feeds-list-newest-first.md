# ADR-0064: OPDS feeds list newest first, and the root carries the books

- **Status:** Accepted
- **Date:** 2026-11-10
- **Depends on:** [ADR-0006](0006-catalog-api-and-opds.md),
  [ADR-0017](0017-folders-not-pipelines.md)
- **Amends:** ADR-0006, which gave each folder a separate newest-first
  feed behind `http://opds-spec.org/sort/new`

## Context

KOReader's catalog sync, and the OPDS Plus plugin built on it, downloads
new books from a configured catalog URL. It fetches that one feed, skips
leading entries that carry no acquisition link, takes the first book as
the newest, and follows `next` until it reaches the book it downloaded
last time.

The root at `/opds/v1.2` listed only folders, so a sync pointed at it found
nothing to download. A folder feed listed oldest first, so a sync pointed at
one took the oldest books once, remembered the oldest as its marker, and
reported itself up to date from then on. The newest-first feed existed only
at `/folders/{folder}/recent`, which nobody configures.

## Decision

**The root is an acquisition feed: its first page lists the newest books across
all granted folders, then the folders. Folder feeds list newest first.**

- Root page one carries up to 20 books from the granted folders, newest
  first, followed by one navigation entry per folder. Books come first
  because KOReader's OPDS sync reads the first entry as the newest book and
  crashes on a navigation entry there. A `next` link continues with books only. The cursor is the
  catalog's own `(created_at, id)` keyset, so the walk neither skips nor
  repeats a book.
- `/opds/v1.2/folders/{folder}` lists newest first. The separate
  `/folders/{folder}/recent` route and its `sort/new` link are removed: they
  would now be the same feed under two URLs.
- Folder grants decide which books the root shows, exactly as they decide
  which folders it lists. A book in an ungranted folder never appears.
- The native API and the web UI keep their orders and their `order`
  parameter.

## Consequences

The URL a user already typed in, `https://<host>/opds/v1.2`, syncs the whole
library with no other setting. A browsing reader sees the latest arrivals
under the folder list instead of only the folder list. The page stays at
20 books, so the memory budget for embedded clients is unchanged.

The same file held in two granted folders is two catalog books and appears
twice at the root, as it already did across two folder feeds.

The root changes media type from navigation to acquisition. OPDS 1.2 gives
a feed that contains acquisition entries that type, and readers that render
both kinds of entry (KOReader, CrossPoint) need nothing new.
