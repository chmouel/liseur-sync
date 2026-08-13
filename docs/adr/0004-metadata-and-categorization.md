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

1. Define the pure bounded-extraction result, metadata entities, provenance,
   locks, precedence engine, and filename parser. ADR-0005's ingestion worker
   calls this interface after structural EPUB validation. The relational
   entities, field provenance, and lock schema are implemented. The pure OPF
   extractor now returns bounded embedded title, subtitle, description,
   publisher, publication date, identifiers, languages, subjects, series,
   contributors, and cover references from EPUB2 and EPUB3 metadata. The
   ingestion worker now durably persists that bounded embedded result as a
   canonical JSON snapshot in the atomic `validated -> extracted` transition.
   The pure scalar precedence and lock engine is implemented: a blank
   candidate never clears a value, a locked field only accepts manual edits,
   an unlocked field accepts a strictly higher-precedence candidate or a
   refresh from its own source, and a manual edit locks the field. Entity-set
   merging uses the same rules with whole-set assertions: an empty assertion
   is never treated as a request to empty a set, a source drops the unlocked
   rows it owns or outranks and no longer asserts, and locked rows and rows
   owned by a stronger source are left alone. Merging also accepts a
   set-level lock that rejects an assertion outright, which is what will keep
   a deliberately emptied set empty once that lock is persisted and raised by
   the edit path. Entity names match on a case- and whitespace-insensitive
   key while keeping their display spelling.
   The filename and folder parser is implemented as a pure per-library
   pattern list: the four documented layouts, conservative " - " splitting,
   plain decimal series positions only, a confidence grade that marks values
   recovered from a single name rather than a directory boundary, the
   original path retained, and unusable or ambiguous names left unset.
   Materializing the snapshot into catalog fields and normalized metadata
   entities, persisting the set-level lock, wiring the parser to per-library
   configuration, cover extraction, and automatic promotion remain.
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
