# ADR-0004: Metadata and categorization

- **Status:** Accepted
- **Date:** 2026-08-12
- **Depends on:** [ADR-0003](0003-catalog-work-identity.md)

## Context

Useful library organization cannot rely on one source. EPUB OPF metadata may
be excellent, incomplete, or wrong. Existing folder layouts often encode
series and author information. External services can fill gaps but introduce
privacy and availability concerns. Rescans must not overwrite corrections
made by a user.

## Decision

Every ingest source uses one field-level precedence engine:

1. embedded EPUB OPF metadata;
2. configured filename and folder parsers;
3. explicitly requested external metadata;
4. manual edits.

Later stages do not automatically replace earlier non-empty values unless
the field is unlocked and the new source has higher precedence. Every
editable field records its source and a manual-lock flag. A rescan never
overwrites a locked value.

### Embedded metadata

Extract bounded OPF values for title, subtitle, identifiers, language,
publisher, publication date, subjects, series metadata, contributors and
roles, and cover reference. Preserve raw source values needed for later
repair, but expose normalized values through the catalog.

### Filename and folder parsing

Parsing is configurable per library and starts with conservative patterns
commonly used by Komga/Kavita users:

```text
Author/Title.epub
Author/Series/02 - Title.epub
Series/Author - Title.epub
Author - Series 02 - Title.epub
```

Parsers produce candidates with confidence and never discard the original
path. Ambiguous results remain unset rather than guessed. Patterns and edge
cases are table-driven tests.

### Catalog entities

The catalog supports series with an optional fractional sequence number,
contributors with roles, tags, genres, languages, and publishers. Entity
normalization matches case-insensitively but preserves display spelling, and
merges are explicit.

User-created collections and ordered reading lists have schema but no
operations or UI, and stay that way for now: they are organization on top of
a catalog, not part of describing a book.

### External providers — later

No external service is contacted in the MVP, and nothing in the ingest path
may ever depend on one. If provider lookup is built later, OpenLibrary and
Google Books are the candidates, and these constraints are already settled:
disabled unless configured, contacted only when an authorized user asks for
a specific book, fixed allowlisted hosts with bounded timeouts and response
sizes, **the allowlist re-checked on every redirect hop** so a 302 to a
link-local or internal address cannot defeat it, results shown as attributed
candidates rather than applied, and TLS verified against the CA bundle
shipped in the container image — with a smoke test, so a release cannot ship
lookup code without usable trust roots. No background scan phones home.

### Search — later

SQLite uses FTS5 and PostgreSQL uses `tsvector`, updated transactionally
with metadata changes. One rule matters now because it constrains the API
rather than the index: **a catalog-only credential must not be able to
observe reading state.** Reading-state filters require `sync` on the same
token, aggregate statistics require `read-insights`, and OPDS exposes
neither regardless of what the authenticating token also carries.

## Consequences

Field locks add schema and UI complexity, but they make rescans predictable.
Conservative parsing leaves more fields empty than aggressive heuristics;
manual review is preferable to silently miscategorizing a library.

External metadata cannot be a required ingest dependency.

## Implementation phases

1. **Extraction, precedence, and ingest wiring.** Done, except where noted.

   The OPF extractor and the filename parser both feed one source-neutral
   proposal, which the precedence engine applies: a blank candidate never
   clears a value, a locked field takes manual edits only, and a manual edit
   locks the field. The same rules apply to whole-set assertions, so an empty
   assertion never empties a set.

   Three decisions there are worth keeping visible, because each one is a
   place where guessing would have been easier than being right:

   - A parser drops the fields its layout had to guess at rather than
     applying them, so a layout still contributes an author it read from a
     directory of its own even when it could not explain the rest of the
     name. The layout that reads every field out of a single filename
     therefore contributes nothing until a confirmation step exists.
   - Subjects become tags, never genres. A subject list mixes both, and
     picking one would be inventing information.
   - A proposal declares whether its sets are complete or partial: an
     extraction reads the whole publication, but a path names at most one
     author and one series.

   The promotion worker publishes the blob, then creates the book, its file
   and its resolved scalar metadata in one revision-checked transaction —
   the title cannot be a later step, because `promoted` is terminal and a
   book created without one could never be listed again to receive it.
   Entity sets are applied immediately after against an expected revision, so
   a failure there leaves a correct but untagged book rather than undoing the
   promotion.

   **Remaining:** per-library parser configuration (the pattern list is still
   the built-in default), and cover extraction.

2. Metadata edit UI, series/contributor/tag pages, and merge tools — later.
3. Full-text search and facets — later.
4. Explicit external-provider lookup and candidate review — later, and
   optional forever.

## Acceptance criteria

- Managed uploads and watched scans produce identical metadata for identical
  files and parser settings.
- Manual locks survive rescans and external lookups.
- Ingest never depends on an external service.
- External hosts are never contacted without both configuration and a user
  action, and the official container verifies those connections with its
  shipped CA bundle.
- SQLite and PostgreSQL search return equivalent results for shared fixtures.
- Catalog-only search responses and filters expose no sync-derived state.
