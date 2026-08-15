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
| [0002](0002-library-storage-and-ownership.md) | Library storage and ownership | MVP | Implemented; "Library kinds" amended by [0014](0014-library-sources-and-storage.md) | Nothing outstanding |
| [0003](0003-catalog-work-identity.md) | Catalog and sync work identity | MVP | Implemented | Nothing outstanding |
| [0004](0004-metadata-and-categorization.md) | Metadata and categorization | MVP | Implemented | Nothing outstanding |
| [0005](0005-upload-and-ingestion.md) | Upload and ingestion pipeline | MVP | Implemented | Nothing outstanding |
| [0006](0006-catalog-api-and-opds.md) | Catalog API and OPDS | MVP | Implemented; book payload amended by [0015](0015-catalog-payloads-for-clients.md) | Nothing outstanding |
| [0007](0007-web-reader.md) | Web reader | Later | Implemented | Nothing outstanding |
| [0008](0008-liseur-android-client.md) | Liseur Android client plan | Later | Other repository | Blocked on [0015](0015-catalog-payloads-for-clients.md) and [0016](0016-token-self-introspection.md) |
| [0009](0009-liseur-desktop-client.md) | Liseur Desktop client plan | Later | Other repository | Blocked on [0015](0015-catalog-payloads-for-clients.md) and [0016](0016-token-self-introspection.md) |
| [0010](0010-duplicate-detection.md) | Duplicate detection | MVP | Implemented | Nothing outstanding |
| [0011](0011-web-ui-revamp.md) | Web UI revamp | Later | Implemented | Nothing outstanding |
| [0012](0012-reader-engine-foliate.md) | Replace the reader engine with foliate-js | Later | Implemented | Nothing outstanding |
| [0013](0013-admin-panel.md) | The admin panel | Later | Implemented | Nothing outstanding |
| [0014](0014-library-sources-and-storage.md) | Library sources, storage modes and Calibre | Later | Implemented | Covers from `cover.jpg` are not wired yet |
| [0015](0015-catalog-payloads-for-clients.md) | Catalog payloads clients can walk | Client | Proposed | Batch store reads for a page's contributors, series and files |
| [0016](0016-token-self-introspection.md) | Token self-introspection | Client | Proposed | `GET /v1/token` and its scope-table entry |

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
