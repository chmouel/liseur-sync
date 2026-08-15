# ADR-0009: Liseur Desktop client plan

- **Status:** Proposed
- **Date:** 2026-08-12
- **Target repository:** `../liseur-desktop`
- **Depends on:** [ADR-0006](0006-catalog-api-and-opds.md),
  [ADR-0015](0015-catalog-payloads-for-clients.md),
  [ADR-0016](0016-token-self-introspection.md)

## Context

Liseur Desktop already models Komga, calibre-web, and liseur-sync through
`RemoteCatalog`, with all network and heavy work in the worker utility
process. It already streams catalog pages, caches covers, downloads EPUBs,
resolves work identifiers, uploads sessions, and keeps credentials in the
main-process keychain store.

The liseur-sync implementation can evolve from its current sync-oriented
catalog adapter into the full native content API without weakening the
desktop app's snappiness rules.

## Decision

Extend the existing `LiseurSyncCatalog` implementation and capability model.
Do not create renderer networking or a parallel catalog subsystem.

### Phase A: protocol and credentials

- Expand the existing liseur-sync token in place to
  `library-read + sync`, with optional `library-manage`, preserving token ID,
  device ID, secret, and server-side retry identity. Do not replace an active
  sync token merely to add catalog capability.
- Retain compatibility with servers that return legacy singleton `scope`.
- Read the token's own scopes through
  [ADR-0016](0016-token-self-introspection.md) rather than inferring them
  from failed requests, so a pasted token shows the right features.
- Keep secrets only in Electron `safeStorage`; the worker receives headers in
  memory as it does today.
- Generate and persist a random per-installation device key in the protected
  main-process store. Deterministic operation and session IDs include this
  key; never use the shared literal `liseur-desktop` as the device namespace.
- Before switching namespaces, add a durable per-target outbox that stores
  the complete operation/session payload and derived ID. Migrate every
  pending queue item by deriving and storing its legacy ID first. Retries
  always replay the stored payload byte-for-byte; only new facts created
  after migration use the installation key.
- Keep catalog `book_id` and sync `work_id` as separate persisted identities.
  Existing liseur-sync `server_book_links.remote_id` values remain work IDs;
  a migration adds the catalog-book relation without reinterpreting those
  rows. Progress endpoints continue to receive `work_id`, never `book_id`.

### Phase B: catalog and downloads

- Implement cursor-paginated list/search, rich metadata, covers, and
  conditional downloads in `src/worker/sync/liseur-sync.ts`.
- Stream each page through the existing incremental book events; never
  materialize or resend the complete library.
- Preserve asynchronous startup catch-up, limited cover concurrency, local
  cache, and offline reading.
- Use range/resume support for large downloads rather than buffering the
  entire EPUB when the shared contract is extended.

If the current `RemoteCatalog.download(): Promise<Buffer>` contract prevents
resumable streaming, first evolve it into a worker-owned destination/stream
operation for every provider. Do not special-case liseur-sync in the
renderer.

### Phase C: upload

- Add typed renderer/preload/worker messages for file selection, upload
  start, progress, cancellation, and ingest-job state.
- Native dialogs remain in main; file I/O, hashing, and network upload remain
  in the worker.
- Optimistically show the queued action, then merge the completed catalog
  row incrementally.
- Never block startup, the renderer, or reading-position persistence.

### Phase D: migration and multi-server behavior

- Preserve the existing server row. Keep legacy liseur-sync work-link
  `remote_id` values untouched and populate the new catalog `book_id`
  relation only after feature detection and matching.
- For users currently using liseur-sync only for positions, upgrade the same
  connection to catalog capability after feature detection and explicit user
  confirmation.
- Match already-downloaded local EPUBs by SHA-256 and work identifiers. The
  digest comes from the catalog listing under
  [ADR-0015](0015-catalog-payloads-for-clients.md); matching must not walk
  the catalog one detail request per book.
- Keep sync queue, conflicts, annotations, and reading sessions intact.

## Repository updates

When implemented in `../liseur-desktop`:

- update `docs/DESIGN.md`, `docs/TODO.md`, and protocol documentation;
- extend `src/shared/ipc/protocol.ts` rather than adding generic IPC;
- migrate a per-installation device key and durable outbox; test legacy
  pending-item replay, lost-response retry across upgrade, and that two
  installations produce different IDs for new facts with the same work and
  timestamp;
- keep all parsing, scanning, catalog, and network work in `src/worker/`;
- add worker unit tests and Playwright coverage for user-facing flows;
- run `pnpm typecheck`, `pnpm lint`, `pnpm test`, then
  `pnpm build && pnpm test:e2e:headless` on Linux.

## Acceptance criteria

- Startup and renderer interaction remain network-independent and
  non-blocking.
- A 10,000-book catalog remains incremental, virtualized, and cover-lazy.
- Catalog download, exact-position sync, session upload, offline cache, and
  conflict handling continue through existing worker boundaries.
- Upload progress and failures are visible without blocking or replacing the
  whole library dataset.
- Existing Komga and calibre-web behavior remains unchanged.
- Two desktop installations on one account cannot collide on deterministic
  operation or session IDs.
- Upgrading scopes or the device namespace cannot duplicate or conflict with
  a pending operation/session after a lost response.
