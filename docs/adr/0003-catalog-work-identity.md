# ADR-0003: Catalog and sync work identity

- **Status:** Proposed
- **Date:** 2026-08-12
- **Depends on:** [ADR-0002](0002-library-storage-and-ownership.md)

## Context

The existing `works`, `editions`, and aliases are user-scoped because
positions and sessions are private. The new catalog can be shared across
users. Resolving a shared file directly to one work would either leak one
user's work graph or force works to become shared, contradicting the current
tenant-isolation model.

A second, unrelated identity system would be equally harmful: catalog
downloads and sync records would drift apart.

## Decision

Catalog identity and sync identity remain separate layers joined explicitly:

```text
libraries -> books -> book_files -> blobs
                    |
                    +-> user_book_works(user_id, book_id, work_id)
                                      |
                                      +-> works -> editions -> aliases
```

`user_book_works` has composite foreign keys that prove the work belongs to
the same user as the mapping. Store methods always require `user_id`.

Resolution, work/edition creation, alias promotion, and mapping insertion are
one store transaction. Phase 1 refactors the existing resolve endpoint onto
this transaction as well, so catalog and client resolution cannot diverge.
Alias uniqueness races are re-read inside the transaction: aliases resolving
to another work return 409, never conflict-ignore success or a 500.

Catalog browse and download are non-mutating and require only
`library-read`. They never create a work or fail because of a sync-identity
conflict.

Before a client syncs or reports sessions for a catalog book, it performs an
explicit resolution authorized by both `library-read` and `sync`:

1. The server collects the book file's identifiers: SHA-256, KOReader
   partial MD5, EPUB `dc:identifier`, normalized title/author, and the stable
   catalog alias `liseur-sync:<book_id>`.
2. It invokes the existing resolution algorithm in that user's namespace.
   All matching aliases must resolve to one work; matches spanning multiple
   works return 409 without mutation.
3. It inserts the `user_book_works` mapping transactionally after a
   high-confidence or confirmed resolution.
4. Later requests reuse the mapping.

A title/author-only (`ta:`) match remains low-confidence. The resolver does
not promote stronger supplied aliases or create the catalog mapping until
the user confirms it. A rejected match remains recorded as rejected, matching
the existing client/server repair semantics.

The same shared catalog book may therefore map to distinct work IDs for two
users. That is correct and preserves existing privacy. Physical blob
deduplication is not semantic work sharing.

If the catalog metadata or file changes, the mapping is not silently
rewritten. A changed file becomes another edition or invokes the existing
merge/split repair flow. Manual split and merge operations update or
invalidate affected mappings explicitly.

Existing works require no eager migration. The first catalog resolution
finds matching aliases and backfills the join. A maintenance command may
pre-resolve mappings for one user, but it must use the same public resolver.

## Consequences

Catalog browsing and download need no work ID. Work creation is lazy, so
importing a large shared library does not create per-user copies of every
work and OPDS downloads cannot mutate sync state.

Some API responses will expose both `book_id` and the current user's optional
`work_id`. Clients must not persist another user's work ID as catalog
identity.

## Implementation phases

1. Add the mapping table, composite constraints, atomic resolver transaction,
   and tenant-isolation/concurrency tests.
2. Add an explicit catalog-book resolve operation for sync-capable clients.
3. Add maintenance backfill and mapping repair for split/merge.

## Acceptance criteria

- Two users resolving one shared book receive user-owned work IDs.
- Browse, `GET`, and `HEAD` downloads do not mutate works or mappings.
- No user can read, create, or replace another user's mapping.
- Existing aliases are reused rather than duplicated into a parallel graph.
- A fuzzy match cannot acquire stronger aliases or a catalog mapping before
  confirmation.
- Conflicting aliases return 409 and leave aliases and mappings unchanged.
- Concurrent resolutions cannot split aliases across works; uniqueness races
  become deterministic success-on-one-work or 409.
- Split and merge operations leave mappings valid and auditable.
