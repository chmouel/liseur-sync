# Architecture decision records

This directory is the living implementation plan for expanding
`liseur-sync`. [DESIGN.md](../DESIGN.md) remains the protocol authority;
ADRs explain decisions that add to or supersede parts of that design.

## Index

`Scope` says whether an ADR is needed for the first release, as defined in
[ADR-0001](0001-content-server.md). `Later` does not mean unlikely; it means
the decision is recorded so it cannot invalidate storage or identity choices
made now, and is not a commitment to build yet. `Client` means the server
work is needed for the first release of a *client* (ADR-0008, ADR-0009)
rather than of the server.

`Implemented` in the state column means every phase of that ADR is built
and nothing is outstanding: it is archived in place rather than moved, so
the links that point at it from the other records keep working, and it is
read as history rather than as a plan.

`Next` is the one thing to build next under that ADR, so the table can be
read instead of the whole directory. The ordering *between* ADRs, and the
loose ends that must come before any of it, are in
[ADR-0001](0001-content-server.md#after-the-mvp).

| ADR | Title | Scope | State | Next |
|---|---|---|---|---|
| [0001](0001-content-server.md) | Become a content server | — | MVP implemented | Nothing outstanding |
| [0002](0002-library-storage-and-ownership.md) | Library storage and ownership | MVP | Superseded by [0017](0017-folders-not-pipelines.md) | Nothing outstanding |
| [0003](0003-catalog-work-identity.md) | Catalog and sync work identity | MVP | Implemented | Nothing outstanding |
| [0004](0004-metadata-and-categorization.md) | Metadata and categorization | MVP | Implemented; external lookup and manual editing superseded by [0017](0017-folders-not-pipelines.md) | Nothing outstanding |
| [0005](0005-upload-and-ingestion.md) | Upload and ingestion pipeline | MVP | Superseded by [0017](0017-folders-not-pipelines.md) | Nothing outstanding |
| [0006](0006-catalog-api-and-opds.md) | Catalog API and OPDS | MVP | Implemented; book payload amended by [0015](0015-catalog-payloads-for-clients.md) | Nothing outstanding |
| [0007](0007-web-reader.md) | Web reader | Later | Implemented | Nothing outstanding |
| [0008](0008-liseur-android-client.md) | Liseur Android client plan | Later | Other repository | Add stable account identity in [0016](0016-token-self-introspection.md) so replacement tokens do not force a full replay |
| [0009](0009-liseur-desktop-client.md) | Liseur Desktop client plan | Later | Other repository | Add stable account identity in [0016](0016-token-self-introspection.md) so replacement tokens do not force a full replay |
| [0010](0010-duplicate-detection.md) | Duplicate detection | MVP | Superseded by [0017](0017-folders-not-pipelines.md) | Nothing outstanding |
| [0011](0011-web-ui-revamp.md) | Web UI revamp | Later | Implemented | Nothing outstanding |
| [0012](0012-reader-engine-foliate.md) | Replace the reader engine with foliate-js | Later | Implemented | Nothing outstanding |
| [0013](0013-admin-panel.md) | The admin panel | Later | Implemented | Nothing outstanding |
| [0014](0014-library-sources-and-storage.md) | Library sources, storage modes and Calibre | Later | Implemented; storage modes, refresh leases and the review queue superseded by [0017](0017-folders-not-pipelines.md) | Nothing outstanding |
| [0015](0015-catalog-payloads-for-clients.md) | Catalog payloads clients can walk | Client | Implemented | Nothing outstanding |
| [0016](0016-token-self-introspection.md) | Token self-introspection | Client | Accepted; phases 1–2 implemented | Return a stable opaque `account_id` for safe reconnect detection |
| [0017](0017-folders-not-pipelines.md) | Folders, watched — not an ingest pipeline | MVP | Implemented | Nothing outstanding |
| [0018](0018-series-overrides.md) | Series a reader can shape | Later | Accepted | Phases 1–4; Calibre write-back deferred to its own ADR |
| [0019](0019-library-wide-entities.md) | Catalog entities belong to the library | Later | Accepted | Renaming decided in [0020](0020-series-renaming.md), merge and split in [0021](0021-series-merge-split.md) |
| [0020](0020-series-renaming.md) | Renaming a series | Later | Accepted; implemented | Merge and split decided in [0021](0021-series-merge-split.md) |
| [0021](0021-series-merge-split.md) | Merging and splitting a series | Later | Accepted; implemented | All four phases; Calibre write-back constrained here, decided in its own ADR |
| [0022](0022-calibre-metadata-db-is-authoritative.md) | A Calibre library's `metadata.db` is authoritative | Later | Accepted; implemented | Purge, unservable observations and digest re-registration; amends rules on [0017](0017-folders-not-pipelines.md) |
| [0023](0023-uploads-land-in-a-folder.md) | An upload is a file written into a folder | Later | Accepted; implemented | Folder opt-in, `library-upload` scope, plain and Calibre folders; amends rule 3 of [0017](0017-folders-not-pipelines.md) and reinstates phase C of [0008](0008-android-client.md) |
| [0024](0024-deleting-a-work.md) | Deleting is a reader forgetting, or an administrator retiring | Later | Accepted; implemented | Per-user work delete for a work no book backs, admin delete of a missing catalog row; amends the append-only rule for that one case |
| [0025](0025-deleting-a-book.md) | A book may be deleted where a book may be written | Later | Accepted | Deleting a book's file and row from an `accepts_uploads` folder; `library-delete` scope over the API, admin in the browser; amends [0024](0024-deleting-a-work.md) and rule 3 of [0017](0017-folders-not-pipelines.md) again |

## Convention

- Files use a four-digit sequence: `NNNN-short-title.md`.
- New ADRs are appended. Do not renumber an active ADR.
- Status is one of `Proposed`, `Accepted`, `Deferred`, `Superseded`, or
  `Rejected`.
- Each ADR records context, decision, consequences, implementation phases,
  and acceptance criteria.
- **An ADR states the current decision. It is not a changelog.** Record what
  is true and what remains, in a sentence or two per phase — not a narration
  of what each commit added. If a phase's status needs a paragraph, it is
  being written as a diary.
- Prefer deferring a decision to over-specifying one. A section describing
  mechanics for code that does not exist is a liability: it will be wrong by
  the time it is built, and it hides the decisions that are actually load
  bearing.
- These ADRs are active implementation documents. Remove one from this
  directory and index only after its complete behavior is implemented,
  reviewed, covered by CI-equivalent tests, and absorbed into permanent
  design, API, deployment, and integration documentation.
