# ADR-0008: Liseur Android client plan

- **Status:** Proposed
- **Date:** 2026-08-12
- **Target repository:** `../liseur`
- **Depends on:** [ADR-0006](0006-catalog-api-and-opds.md),
  [ADR-0015](0015-catalog-payloads-for-clients.md),
  [ADR-0016](0016-token-self-introspection.md)

## Context

Liseur Android already treats calibre-web and Komga as mutually exclusive
catalog servers. `liseur-sync` is currently a second sync peer implemented in
`data/liseursync/`; it is intentionally not a `ServerKind` because the server
originally held no catalog.

Once liseur-sync serves books, a user should be able to select it as the
catalog while retaining the existing append-only sync protocol, merge logic,
cursor guarantees, and offline library behavior.

## Decision

Add liseur-sync as a catalog provider behind the existing provider-neutral
contracts, not as conditionals in screens or workers.

### Phase A: contracts and account setup

- Add `LISEUR_SYNC` to `ServerKind` with the canonical
  `liseur-sync:<book_id>`
  remote URL prefix and Room migration.
- Implement `LiseurSyncCatalogClient`, `LiseurSyncFileSource`, and setup
  capability detection under `data/liseursync/`. Capability detection reads
  the token's own scopes through
  [ADR-0016](0016-token-self-introspection.md) instead of probing routes.
- Add a catalog-account `PositionSync` entry to `RemoteRouter.positions`.
  Its cursor, credentials, and work mappings are tied to the catalog account,
  not the singleton optional `SyncAccount`.
- Extend `ServerCapabilities` with upload/manage capability without changing
  calibre or Komga behavior.
- Expand the existing device token in place to the required scope set where
  the server supports it, preserving token/device identity and deterministic
  retry semantics. Mint a new token only for a genuinely new account/device.
- Keep credentials encrypted by the existing credential store.

The current `LiseurSyncPositionSync` protocol implementation,
`CompositePositionSync`, `ReadingStateMerge`, cursor transaction rule,
deterministic operation IDs, and session IDs remain unchanged. The
implementation is parameterized over catalog-bound versus dedicated-peer
account state rather than copying the protocol.

The app may use a liseur-sync catalog and a different optional liseur-sync
peer. If setup points both roles at the same server account, composition
deduplicates the peer instead of pushing and pulling twice.

### Phase B: browse and download

- Route catalog pages and search through `CatalogSource`.
- Route resumable EPUB requests through `FileSource`.
- Preserve progressive page delivery, lazy covers, local-first shelf, and
  existing download worker behavior.
- Store both `book_id` and resolved `work_id`; use `book_id` for catalog
  identity and `work_id` only for sync.
- Show library/series/contributor/tag filters as provider capabilities permit.
- Render authors, series and file sizes from the catalog page itself
  ([ADR-0015](0015-catalog-payloads-for-clients.md)); a shelf must never cost
  one request per book.

OPDS is a compatibility fallback and diagnostic path, not the primary Android
integration; the native API exposes richer metadata and identity.

### Phase C: upload

- Add an explicit upload action for local EPUBs when the token and library
  ACL permit `library-manage`.
- Use WorkManager for streaming upload and job-status follow-up.
- Surface server validation and quota errors without removing the local copy.
- Deduplicate the returned catalog book into the existing local row through
  SHA-256 and work identifiers.

### Phase D: migration and UX

- Offer a guided path from a Komga/calibre catalog to liseur-sync; never
  silently change the connected server.
- Match local books to the new catalog by the content SHA-256 carried in the
  catalog listing ([ADR-0015](0015-catalog-payloads-for-clients.md)), source
  aliases where available, then low-confidence title/author confirmation.
- Keep downloaded files and all reading data during account changes.
- Explain the difference between catalog access and optional independent
  sync peers when both are configured.

## Repository updates

When implemented in `../liseur`:

- update its `AGENTS.md` architecture text that currently says liseur-sync is
  not a catalog;
- update user-facing server documentation and strings;
- add Room migrations and JVM tests for stored enum/prefix compatibility;
- test provider contracts, token scope compatibility, pagination, resumable
  downloads, upload jobs, and migration matching;
- run `./gradlew testDebugUnitTest`, `lintDebug`, and `assembleDebug`.

## Acceptance criteria

- A user can connect to liseur-sync as the only catalog, browse progressively,
  download, read offline, and sync exact positions.
- Existing calibre-web, Komga, and standalone liseur-sync configurations
  continue working.
- A liseur-sync catalog and a distinct optional sync peer maintain separate
  cursors and credentials; the same account is deduplicated.
- Cursor advancement remains in the same transaction as covered local state.
- Upload retries are safe and never lose the local EPUB.
- No proprietary dependency or non-user-initiated external network call is
  introduced.
