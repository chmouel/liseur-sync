# ADR-0004: Metadata and categorization

- **Status:** Proposed
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

The catalog supports:

- series with an optional fractional sequence number;
- contributors with roles such as author, editor, translator, and
  illustrator;
- tags and genres;
- languages and publishers;
- user-created collections and ordered reading lists.

Entity normalization is case-insensitive for matching but preserves display
spelling. Merges are explicit and reversible where practical.

### External providers

OpenLibrary and Google Books are optional providers. They are disabled
unless enabled in instance configuration and are contacted only after an
authorized user explicitly requests a lookup for a book.

Requests use fixed allowlisted hosts, short timeouts, response-size limits,
and redirect validation. Results are cached and shown as candidates with
provider attribution before application. No background scan phones home.

The official container image includes a maintained CA certificate bundle
copied into the scratch image. External lookup does not disable TLS
verification or add provider-specific trust exceptions. The provider phase
adds a container-level HTTPS smoke test so a release cannot ship lookup code
without usable trust roots.

### Search

SQLite uses FTS5 and PostgreSQL uses `tsvector`. Search covers title,
subtitle, contributors, series, identifiers, tags, genres, and publisher.
Indexes are updated transactionally with metadata changes. Results support
filters for library, availability, series, contributor, language, tag,
and genre. The native catalog API adds reading-state filters only when the
requesting token also has `sync`; aggregated statistics require
`read-insights`. OPDS never exposes sync-derived filters or fields,
regardless of the authenticating token's additional scopes. A
`library-read`-only native API token likewise cannot infer positions,
mappings, completion, or reading history.

## Consequences

Field locks add schema and UI complexity, but they make rescans predictable.
Conservative parsing leaves more fields empty than aggressive heuristics;
manual review is preferable to silently miscategorizing a library.

External metadata cannot be a required ingest dependency.

## Implementation phases

1. **Extraction, precedence, and ingest wiring.** Done, except where noted.

   The OPF extractor returns bounded title, subtitle, description, publisher,
   date, identifiers, languages, subjects, series, contributors, and cover
   references from EPUB2 and EPUB3. The ingest worker persists that as a
   canonical JSON snapshot in the `validated -> extracted` transition.

   The precedence engine, entity schema, provenance, and locks are
   implemented. A blank candidate never clears a value; a locked field takes
   manual edits only; a manual edit locks the field. Set merges use the same
   rules on whole-set assertions, so an empty assertion never empties a set,
   and locked or stronger-owned rows are left alone. Names match case- and
   whitespace-insensitively while keeping their display spelling.

   The filename parser handles the four documented layouts per library. It
   records which fields a layout had to guess at, and those are dropped rather
   than applied — so a layout still contributes an author it read from a
   directory of its own even when it could not explain the rest of the name.
   The one layout that reads every field out of a single name therefore
   contributes nothing until a confirmation step exists.

   Both sources map to one source-neutral proposal. Subjects become tags, not
   genres: a subject list mixes both and picking one would be guessing. A
   proposal declares whether its sets are complete or partial, since an
   extraction reads the whole publication but a path names at most one author
   and one series.

   The promotion worker runs `extracted -> promoted`: it publishes the blob,
   then creates the book and its file in one revision-checked transaction.
   The book's scalar metadata is resolved into that same transaction, because
   a new book has no persisted rows to reconcile against and `promoted` is
   terminal — a book created without a title could never be listed again to
   receive one. Entity sets are applied immediately afterwards, against an
   expected revision so a concurrent editor is never overwritten; a failure
   there leaves a correct but untagged book rather than undoing the
   promotion.

   **Remaining:** per-library parser configuration (the pattern list is still
   the built-in default), and cover extraction.

2. Metadata edit UI, series/contributor/tag pages, and merge tools.
3. Full-text search and facets.
4. Explicit external-provider lookup and candidate review.

## Acceptance criteria

- Managed uploads and watched scans produce identical metadata for identical
  files and parser settings.
- Manual locks survive rescans and external lookups.
- External hosts are never contacted without both configuration and a user
  action.
- The official container verifies HTTPS provider connections with its shipped
  CA bundle.
- SQLite and PostgreSQL search return equivalent results for shared fixtures.
- Catalog-only search responses and filters expose no sync-derived state.
