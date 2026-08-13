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

### Search

SQLite uses FTS5 and PostgreSQL uses `tsvector`, updated transactionally
with metadata changes. One rule constrains the API rather than the index:
**a catalog-only credential must not be able to observe reading state.**
Reading-state filters require `sync` on the same token, aggregate
statistics require `read-insights`, and OPDS exposes neither regardless
of what the authenticating token also carries. The search route therefore
has no vocabulary for reading state at all — an absent feature cannot leak.

## Consequences

Field locks add schema and UI complexity, but they make rescans predictable.
Conservative parsing leaves more fields empty than aggressive heuristics;
manual review is preferable to silently miscategorizing a library.

External metadata cannot be a required ingest dependency.

## Implementation phases

1. **Extraction, precedence, and ingest wiring.** Done.

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

   Covers are served rather than stored: `GET /v1/books/{id}/cover` reads
   the cover the publication declares out of the immutable CAS blob,
   transcodes it, and caches the result. Deriving it on demand is what
   avoids a schema change, a promotion-path change and a backfill for
   every book already in the catalog, and it works for those books from
   the day it ships.

   Layouts are configured per library, in the library's existing
   `config_json` column, and resolved per job by the ingest pass under the
   job user's own read access. The list is stored rather than compiled in
   because two of the layouts are the same shape on disk — `Author/Title`
   and `Series/Author - Title` are both one directory and one file — so
   only the operator can say which one a library uses. An absent list is
   the conservative default; an empty list disables filename parsing,
   which is a different answer and is stored as one.

   A library whose configuration cannot be parsed stalls its own backlog
   and is counted as `misconfigured` by the pass. Promoting it with the
   compiled-in list would file its books under the wrong author, and
   nothing afterwards lists the books that were misfiled; failing the pass
   would let one library's typo stop every other library's uploads.

2. **Metadata edit UI, entity pages, and merge tools — done.** A
   librarian corrects a book from its page, browses a library by series,
   contributor, tag or genre, and folds two spellings of one name
   together. This is also what makes a book promoted under the wrong
   layout correctable without deleting and re-adding it.

   The rules the implementation had to settle, all of them places where
   the easy answer is the wrong one:

   - **An edit is only an edit if something moved.** Every input carries
     a hidden copy of what was rendered, and a field whose text is
     unchanged is left alone. Without that, opening a book and pressing
     Save would lock every field on it — turning a glance into an
     assertion and stopping the file from ever being re-read.
   - **A blank value from a person is an assertion**, unlike a blank
     candidate from an extractor, which is only ignorance. Deleting a
     wrong title clears *and locks* it. Unlocking is the separate,
     exclusive gesture that hands a field back to the extractors.
   - **Sets are replaced whole**, because that is what a form does: an
     entry the user removed is one they left out. An empty set is
     therefore how a set is emptied, and the set lock is what keeps it
     empty across rescans, since no row survives to carry a row lock.
   - **Renaming and merging are different operations.** A rename onto a
     name another entity already holds is refused with an offer to merge
     rather than quietly becoming one: folding two things into one is a
     decision about identity and deserves to be made on purpose.
   - **A merge keeps what people said.** A book that claimed both
     entities keeps its existing row, but a manual lock carries over, and
     so does a series position the surviving row does not have. Dropping
     either would discard the only answer anyone gave. Every affected
     book's revision is bumped, so a concurrent edit holding a pre-merge
     snapshot loses on a stale revision rather than resurrecting the
     entity it just lost.
   - **Identifiers are not editable here.** They feed work identity
     (ADR-0003), so changing one moves a reader's reading history between
     books; that is a different operation with different consequences.
   - **Provenance is librarian's information.** The edit form and the
     "where this came from" labels are built only for somebody who could
     submit them, so a reader learns nothing from the page they could not
     learn from the book.

   The native API carries the same operations
   (`GET`/`PUT /v1/books/{id}/metadata` and the
   `/v1/libraries/{library}/entities/{kind}` family), with the write
   gated on the revision the client read, because two people editing one
   book is ordinary in a shared library and last-write-wins would discard
   the first person's work silently.

3. **Full-text search and facets.** Done.

   `GET /v1/libraries/{library}/search` matches words against everything a
   book says about itself — title, subtitle, description, publisher, and
   the names of the series, contributors, tags and genres it claims — and
   returns the best matches first. `/ui/libraries/{library}/search` is the
   same answer as a page, with facets rendered as narrowing links.

   The decisions worth keeping visible:

   - **The two backends must answer the same question**, which is this
     ADR's acceptance criterion, and three choices exist only to keep that
     true. PostgreSQL uses the `simple` text-search configuration rather
     than `english`, because stemming would make "reading" find "Read" on
     one backend and not the other. Diacritics are folded in Go rather
     than by the `unaccent` extension, which a managed database may not
     offer. And both backends split the query with the same shared
     function, so neither can disagree about what a word is.
   - **The query is words, never index syntax.** Splitting on anything
     that is not a letter or digit is also the only sanitizing either
     backend needs: no quote, wildcard or boolean operator can survive to
     reach FTS5 or `to_tsquery`. A query made only of punctuation matches
     nothing rather than erroring.
   - **Reindexing is a call, not a trigger.** A book's searchable text is
     spread over seven tables, so a trigger on `books` would silently miss
     a contributor renamed two tables away. Every write that changes
     indexed text calls the reindex inside its own transaction.
   - **Search is unpaged, and says when it was cut.** A relevance order
     has no stable cursor, and search answers "where is that book", a
     question with a short answer; browsing is what the paged listings are
     for. The result carries `truncated` so a client can ask the person to
     narrow rather than implying it found everything.
   - **Facets are computed from the books returned**, not by re-running
     the search, so a count can never describe a different set than the
     answer. A filter takes an entity id without its kind, because an id
     already knows what it is.
   - **Search is visible exactly as far as the listings are**: it shows
     what `GET /books` shows, which includes a restored book with no
     stored file, and it never reveals a library the caller cannot read.

   **Next for this ADR:** phase 4, external provider lookup, which remains
   optional forever.

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
