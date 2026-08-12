# Architecture decision records

This directory is the living implementation plan for expanding
`liseur-sync`. [DESIGN.md](../DESIGN.md) remains the protocol authority;
ADRs explain decisions that add to or supersede parts of that design.

## Index

| ADR | Title | Status |
|---|---|---|
| [0001](0001-content-server.md) | Become a content server | Proposed |
| [0002](0002-library-storage-and-ownership.md) | Library storage and ownership | Proposed |
| [0003](0003-catalog-work-identity.md) | Catalog and sync work identity | Proposed |
| [0004](0004-metadata-and-categorization.md) | Metadata and categorization | Proposed |
| [0005](0005-upload-and-ingestion.md) | Upload and ingestion pipeline | Proposed |
| [0006](0006-catalog-api-and-opds.md) | Catalog API and OPDS | Proposed |
| [0007](0007-web-reader.md) | Web reader | Proposed |
| [0008](0008-liseur-android-client.md) | Liseur Android client plan | Proposed |
| [0009](0009-liseur-desktop-client.md) | Liseur Desktop client plan | Proposed |

## Convention

- Files use a four-digit sequence: `NNNN-short-title.md`.
- New ADRs are appended. Do not renumber an active ADR.
- Status is one of `Proposed`, `Accepted`, `Superseded`, or `Rejected`.
- Each ADR records context, decision, consequences, implementation phases,
  and acceptance criteria.
- Update the status and checked implementation items as work lands. The ADR
  must describe the current decision, not become a diary of code changes.
- These ADRs are active implementation documents. Remove one from this
  directory and index only after its complete behavior is implemented,
  reviewed, covered by CI-equivalent tests, and absorbed into permanent
  design, API, deployment, and integration documentation.
