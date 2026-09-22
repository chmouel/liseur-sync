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
| [0026](0026-credential-enrolment-does-not-weaken-account-authentication.md) | Credential enrolment does not weaken account authentication | MVP | Accepted; implemented | Kosync pairing only; administrator password re-verification before creating an account or invite |
| [0027](0027-explicit-per-user-folder-access.md) | Explicit per-user folder access | MVP | Accepted; implemented | Folder grants across storage, catalog surfaces, administration and CLI; amends [0017](0017-folders-not-pipelines.md) |
| [0028](0028-annotation-sync.md) | Annotations are mutable reading state, not history | Later | Accepted | Phase 1: the `annotations` table, second per-user counter and store methods in both backends, with the storetest suite |
| [0029](0029-a-folder-somebody-can-read.md) | A folder somebody can read | MVP | Accepted; implemented | Creator grant in the folder's own transaction, guarded backfill migration 6, a Library page that names which situation it is in; amends [0027](0027-explicit-per-user-folder-access.md) |
| [0030](0030-web-reader-reading-sessions.md) | The web reader counts its sittings | Later | Accepted; implemented | Visibility-bounded sittings, three-minute idle cap, ten-second minimum, no local persistence; amends [0007](0007-web-reader.md) |
| [0031](0031-web-reader-footer.md) | The web reader's footer mirrors the app | Later | Accepted; implemented | Percent, middle slot including positions left in the current chapter, page in the engine's bottom margin; pages are engine locations, never fabricated; click cycles the middle; a browser preference; amends [0012](0012-reader-engine-foliate.md) |
| [0032](0032-reader-pages-are-readium-positions.md) | The reader's page is the app's page | Later | Accepted; implemented | Pages counted as Readium positions so the browser and the app name the same page; display only, no server or API change; three recorded patches to the vendored engine; amends [0031](0031-web-reader-footer.md) and [0012](0012-reader-engine-foliate.md) |
| [0033](0033-a-device-outlives-its-token.md) | A device outlives its token | MVP | Accepted; implemented | `POST /v1/tokens` inherits a `device_id` the account already carried, `400 unknown_device` otherwise; `account_id` on `GET /v1/token`; the store keeps comparing devices; amends [0016](0016-token-self-introspection.md) |
| [0034](0034-live-notifications-say-only-that-something-changed.md) | A live notification says only that something changed | Client | Accepted | `GET /v1/events` as topic-only SSE, published post-commit by the store, authorized per topic; correctness stays in the existing feeds; amends [0007](0007-web-reader.md) |
| [0035](0035-naming-a-shelf-is-one-request.md) | Naming a shelf is one request | Client | Accepted; implemented | `POST /v1/books/resolve` for up to 500 books, one transaction and one answer each, `200` with per-item `not_found`/`ambiguous`; same `library-read` + `sync` pair; amends [0003](0003-catalog-work-identity.md) |
| [0036](0036-one-bounded-offline-web-reader.md) | One bounded offline web reader | Later | Accepted; implemented pending iOS acceptance | Same-origin multi-book PWA under `/ui/offline/`, private IndexedDB publication/state storage, foreground-only reconnect, and explicit annotation conflict handling; separate reader origins leave it disabled |
| [0037](0037-durable-online-reading.md) | Durable online reading, and a disagreement the reader answers | Now | Accepted; implemented | The online reader queues positions and sittings to the same IndexedDB outbox, drains through one shared lock and sender, keeps an agreed baseline per book and device, and presents a two-sided move as a conflict; same-origin deployments only; amends [0007](0007-web-reader.md), [0030](0030-web-reader-reading-sessions.md), [0036](0036-one-bounded-offline-web-reader.md) |
| [0038](0038-a-web-session-that-renews-itself.md) | A web session that renews itself | Now | Accepted; implemented | Browser sessions last `web_session_ttl_days` (default 180) of disuse and slide forward on use, throttled to one write a day, never reviving a revoked or lapsed session; no "keep me signed in" checkbox |
| [0039](0039-a-refused-reading-change-leaves-the-queue.md) | A refused reading change leaves the queue | Now | Accepted; implemented | A position or sitting refused with a terminal verdict (`id_reused` or a malformed field) is discarded; anything else stuck is named for the open book with *Try again* and *Discard*; `unknown_work`, an oversized locator and an op conflict are named for the reader instead; annotation conflicts stay with their own dialog, now resolved online too, and discarding any other refused annotation settles the note as well as the queue; a sitting is finalized into the queue before an unload sends it; amends [0037](0037-durable-online-reading.md) |
| [0040](0040-asking-rather-than-waiting-to-be-asked.md) | Asking, rather than waiting to be asked | Now | Accepted; implemented | The library page and the offline shelf answer a pull-down and a refresh button with one single-flight command — drain the offline queue, then redraw — suppressing the browser's own pull-to-reload only where the gesture is armed; one presenter describes a reading position as an exact or interpolated page, an age and the passage that was on screen, used by the catch-up panel and by a new top-bar *Sync this book* dialog whose verdicts port `BookSyncChoice` over the same three-way merge an automatic sync uses, where cancelling changes nothing; amends [0037](0037-durable-online-reading.md) |
| [0041](0041-statistics-retention.md) | What the server keeps of a reading life | Later | Deferred | Rollups and tombstones grow without bound past the one compaction the rollup job performs, and koplugin sittings never reach it at all (`source_key IS NULL`), so they stay raw for the life of the account; records what a retention rule must never lose (the day set behind the streak, a proof for as long as a device may re-offer a sitting) and weighs koplugin compaction at ingestion, a tombstone horizon and coarsening old per-work buckets; also notes that `StatisticsSnapshot` should learn the caller's window with the streak read separately |
| [0042](0042-graceful-missing-edition-pages.md) | A page count the server does not have | Later | Deferred | `insights.ErrMissingEdition` fails a whole statistics read rather than the one book it cannot measure; proposes splitting the answer — the rollup keeps failing closed, because it writes a number nothing revisits, while the read paths count what they can and name the incompleteness, borrowing `SyncOutcome.Incomplete`'s vocabulary; a silent 0 stays ruled out |
| [0043](0043-foreign-timezone-streak-semantics.md) | Which day a sitting happened on | Later | Deferred | `ApplyRollups` now refuses a batch built under a zone the account no longer keeps, which closes the race but not the two questions behind it: what becomes of buckets already filed under an old zone, and whether the device's zone or the account's decides a day; weighs leaving the seam, recomputing, and recording the zone's own history, and asks that `docs/deployment.md` say an account timezone is not cosmetic |
| [0044](0044-not-asking-about-the-page-in-your-hand.md) | Not asking about the page in your hand | Now | Accepted; implemented | The catch-up panel offered a trip to the page already on screen, because the merge compares anchors and two clients on one Readium position spell a CFI differently; the reconciler stays a faithful port and the *panel* now asks the rendered page table instead — a shared `samePage()` requiring an exact page on both sides, interpolation never matching — and a suppressed offer is answered through the same `keepHere()` the *Stay here* button uses, silent on the wire when nothing was owed; amends [0040](0040-asking-rather-than-waiting-to-be-asked.md), whose "same page, not the same spot" answer remains available in *Sync this book* |
| [0045](0045-a-prompt-about-the-passage-in-front-of-you.md) | A prompt about the passage in front of you | Now | Accepted; implemented | The reader copies a filled-in prompt about the passage it has selected, from a `{placeholder}` template the account keeps in `users.reader_prompt_template` and the server never interprets; empty by default and an empty one means no button, as does no selection; substitution happens in the browser (`{title} {author} {series} {chapter} {page} {pages} {percent} {text}`, a known-but-missing value becoming an empty string and an unknown `{word}` left alone); delivered as a data attribute same-origin, through a new `GET /v1/me` under `library-read` to the detached reader origin, and from a `localStorage` copy offline; a refused clipboard falls back to a dialog with the text selected |
| [0046](0046-a-catalog-book-carries-a-koreader-fingerprint.md) | A catalog book carries a KOReader fingerprint | Now | Accepted; implemented | A folder pass now computes KOReader's own `partialMD5` beside the content digest and stores it on the book, filled where it is missing and never replaced, so a deployment heals itself on its next pass without an unchanged file being reopened for anything else; it is offered as resolution evidence below `sha256`, which stops a KOReader device creating a pending work for a book the catalog already serves, and it is the only vocabulary a kosync peer has for naming a document |
| [0047](0047-mirroring-reading-to-a-koreader-peer.md) | Mirroring reading to a peer that speaks KOReader | Now | Accepted | An optional background mirror speaks kosync *outwards* for one account, joined on [0046](0046-a-catalog-book-carries-a-koreader-fingerprint.md)'s fingerprint, appending what it learns as native ops with no new `Origin`; newest timestamp wins, the mirror's own echo and the peer's outstanding-reset replies are dropped, a fingerprint matching two books mirrors neither, and the peer credential lives in configuration because a secret this server must present cannot be hashed. Positions first; status is outbound only until this server has a status model, highlights go out only when they already carry a KOReader pointer, and statistics are recorded as an open question rather than answered by inventing page numbers |
| [0048](0048-a-second-protocol-for-the-mirror.md) | A second protocol for the mirror, BookOrbit's own API | Now | Accepted | [0047](0047-mirroring-reading-to-a-koreader-peer.md)'s mirror gains a protocol seam and a second protocol, BookOrbit's native REST API, which carries an EPUB CFI in both directions instead of a percentage; when to push and pull is unchanged, `kosync` stays the default because BookOrbit's API costs a full-account password where its KOReader credential is scoped, the peer's echo is recognised by content because its progress row has no device, and a book is resolved by title then decided on size, filename and an optional path mapping, refusing ambiguity and caching the answer |

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
