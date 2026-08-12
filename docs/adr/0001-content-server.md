# ADR-0001: Become a content server

- **Status:** Proposed
- **Date:** 2026-08-12
- **Owners:** liseur-sync maintainers

## Context

The original design deliberately excluded book files, covers, catalogs, and
rendering. That kept the first implementation focused on position sync,
reading sessions, legacy adapters, and self-hosting.

The product direction has changed. A self-hosted Liseur installation should
be able to keep an EPUB library, accept uploads, scan existing folders,
organize metadata, serve clients directly, and eventually read in a browser.
Users should not need a separate Komga or calibre-web installation solely to
store books.

This reverses the "not a library server" non-goal in
[DESIGN.md](../DESIGN.md). It does not weaken the protocol invariants that
made the sync server safe.

## Decision

`liseur-sync` will become an EPUB content server in addition to its existing
sync and statistics roles.

The following existing invariants remain non-negotiable:

- Position operations and reading sessions remain user-scoped.
- The operation log remains append-only and `seq` is never renumbered.
- Legacy adapters continue to translate into native records only.
- No page number is fabricated.
- Secrets remain hashed and credential-bearing traffic requires HTTPS unless
  the existing explicit insecure mode is enabled.
- Every new route is added to the fail-closed authentication and scope table.
- SQLite and PostgreSQL receive the same behavior through shared store tests.

The content server is EPUB-only initially. Other formats require separate
ADRs because comics, PDFs, and audiobooks have different metadata, serving,
and reading models.

The complete direction is split into focused ADRs:

- [ADR-0002](0002-library-storage-and-ownership.md): storage, access, GC,
  watched folders, and backups.
- [ADR-0003](0003-catalog-work-identity.md): the boundary between a shared
  catalog and per-user sync works.
- [ADR-0004](0004-metadata-and-categorization.md): extraction, parsing,
  search, and manual overrides.
- [ADR-0005](0005-upload-and-ingestion.md): durable and secure ingestion.
- [ADR-0006](0006-catalog-api-and-opds.md): native catalog API and OPDS.
- [ADR-0007](0007-web-reader.md): isolated browser reading with native sync.
- [ADR-0008](0008-liseur-android-client.md): Android integration.
- [ADR-0009](0009-liseur-desktop-client.md): desktop integration.

## Implementation roadmap

Each phase is independently releasable. Route or payload changes update
`docs/openapi.yaml` in the same commit.

### Phase 0: decisions

- Add ADR-0001 through ADR-0009 and this index.
- Update every affected section of `DESIGN.md`.

### Phase 1: catalog identity, authorization, and ingestion core

- Add catalog, ACL, blob, job, metadata, and catalog-to-work mapping tables.
- Migrate token scopes from a scalar to a compatible scope set.
- Implement the content-addressed store, durable ingestion state machine,
  metadata precedence engine, and grace-period garbage collector.
- Add admin commands for library creation, access grants, scans, maintenance,
  and backup verification.

### Phase 2: managed uploads and management UI

- Add bounded upload endpoints and htmx drag-and-drop upload.
- Show ingest progress, errors, quarantine state, and retry actions.
- Add catalog, book-detail, metadata-edit, trash, and restore pages.

### Phase 3: watched folders

- Add recursive, read-only watched libraries.
- Feed filesystem discoveries through the same ingestion state machine.
- Detect renames by hash and mark unavailable roots as missing without
  deleting catalog history.

### Phase 4: categorization and search

- Add series, contributors, tags, genres, collections, and reading lists.
- Add SQLite FTS5 and PostgreSQL full-text search.
- Add explicitly invoked external metadata lookup.

### Phase 5: catalog clients

- Add the native `/v1/library/*` API.
- Add OPDS 1.2 acquisition feeds and OpenSearch.
- Verify download, pagination, cache, and authentication behavior against
  real client expectations, including KOReader.

### Phase 6: web reader

- Vendor an EPUB renderer.
- Isolate all active book content from the authenticated UI origin.
- Use the native sync and session APIs as an ordinary client.

### Phase 7: Liseur clients

- Execute the Android plan in ADR-0008.
- Execute the desktop plan in ADR-0009.

## Consequences

The binary now owns durable content as well as a database. Deployment,
backup, quota, and security documentation must reflect that larger
responsibility. File parsers and browser rendering substantially enlarge the
attack surface, so phases must not bypass the bounds in ADR-0005 or the
isolation in ADR-0007.

The advantage is one coherent identity and sync system from upload to every
reader. A catalog book and its reading history converge through the same
native work-resolution protocol instead of being joined by client-specific
guessing.

## Acceptance criteria

- The project documents no contradictory "not a library server" assumption.
- The roadmap preserves all existing sync, tenant-isolation, and adapter
  guarantees.
- Every implementation phase points to a focused decision and a testable
  completion condition.

