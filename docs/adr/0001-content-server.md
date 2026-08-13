# ADR-0001: Become a content server

- **Status:** Accepted
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

The complete direction is split into focused ADRs. Those marked *later* are
recorded so their constraints are known before storage decisions harden;
they are not commitments to build in the first release.

- [ADR-0002](0002-library-storage-and-ownership.md): storage, access, GC,
  watched folders, and backups.
- [ADR-0003](0003-catalog-work-identity.md): the boundary between a shared
  catalog and per-user sync works.
- [ADR-0004](0004-metadata-and-categorization.md): extraction, parsing,
  search, and manual overrides.
- [ADR-0005](0005-upload-and-ingestion.md): durable and secure ingestion.
- [ADR-0006](0006-catalog-api-and-opds.md): native catalog API and OPDS.
- [ADR-0007](0007-web-reader.md): isolated browser reading — *later*.
- [ADR-0008](0008-liseur-android-client.md): Android integration — *later*,
  and in another repository.
- [ADR-0009](0009-liseur-desktop-client.md): desktop integration — *later*,
  and in another repository.

## The minimum viable server

One sentence: **a user can put an EPUB on the server and read it, at the
right page, in the reader they already use.**

That is the whole first release. It is done when someone can:

1. create a managed library and grant a second account read access;
2. upload an EPUB through the web UI or the API and watch it become a book;
3. list and download that book from Liseur or KOReader;
4. read it and have the position sync, as it already does today.

Nothing else is in the first release. A library that cannot be searched,
tagged, or read in a browser is still a working library; one that cannot be
downloaded is not.

Two rules keep the scope honest:

- **A feature is in the MVP only if one of those four steps fails without
  it.** Tags, series pages, full-text search, external metadata lookup,
  collections, and the web reader all fail this test. They are listed under
  "after the MVP", and their ADRs are not commitments to build until then.
- **Storage decisions are settled; presentation decisions are not.**
  Anything that writes durable bytes or defines identity is expensive to
  change later and is decided now. Anything that only reads those bytes back
  can be redesigned cheaply and is deliberately left open.

## Roadmap

Route or payload changes update `docs/openapi.yaml` in the same commit.

### Foundations — done

Catalog, ACL, blob, ingest-job, metadata, and catalog-to-work mapping
schema; scope sets; the content-addressed store; the durable ingest state
machine with crash recovery and grace-period collection; and the metadata
precedence engine. Each ADR records its own remaining edges.

### To the MVP

1. **Ingest input.** Done: `POST /v1/libraries/{library}/upload` and the
   upload form on `/ui/books` ([ADR-0005](0005-upload-and-ingestion.md)).
2. **Admin.** Done: `create-library`, `grant-library`, `revoke-library` and
   `list-libraries` in the `admin` subcommand.
2. **Catalog output.** Done: libraries, paginated books, book detail and
   download, in the API and as web pages
   ([ADR-0006](0006-catalog-api-and-opds.md)).
3. **An existing reader.** Done: OPDS 1.2 navigation and acquisition feeds
   under `/opds/v1.2`, authenticated with HTTP Basic
   ([ADR-0006](0006-catalog-api-and-opds.md)).

All four are in place: the MVP definition above is satisfied end to end.
The operational work behind it is done too — catalog availability
reconciliation, deletion and trash, backup verification (M6), duplicate
reporting, and `POST /v1/books/{id}/resolve`, which is what lets a book
taken from the catalog sync its reading position
([ADR-0003](0003-catalog-work-identity.md)).

### After the MVP

What remains is new surface, ordered by how much a real library misses it
rather than by how interesting it is to build.

**Loose ends in shipped work.** None. The last one, mapping backfill
([ADR-0003](0003-catalog-work-identity.md) phase 3), shipped as
`liseur-sync admin backfill-works`.

Cover extraction shipped since: `GET /v1/books/{id}/cover` renders the
publication's own cover out of the CAS blob and caches the result, and
the catalog JSON, the web UI and the OPDS feeds all point at it.

Per-library parser configuration shipped since: a library states the
filename layouts its files are read with, and `liseur-sync admin
library-layout` is how an operator sets it.

Watched folders shipped since
([ADR-0002](0002-library-storage-and-ownership.md) phase 3): a library
can be a read-only directory the server scans, and a file whose bytes
changed underneath it is put to an administrator rather than silently
reinterpreted.

Similarity-based duplicate detection shipped since
([ADR-0010](0010-duplicate-detection.md) phase 2). The normalization rule
it was waiting for turned out to be the one search already used, which is
also why it is worth defending: a librarian sees the same two books
called duplicates that one query found.

Metadata editing, categorization, search and facets shipped since
([ADR-0004](0004-metadata-and-categorization.md) phases 2 and 3), and
with them the OPDS discovery surface they made possible: an OpenSearch
description, browse feeds for series, contributors, tags and genres, and
a recently-added feed ([ADR-0006](0006-catalog-api-and-opds.md) phase 3,
which is now complete).

**Features**, in order:

1. **The web reader** ([ADR-0007](0007-web-reader.md)). It was waiting
   for a catalog worth reading from, and now it is not: that ADR's
   deferral has been replaced by a decision, and its phase 1 — the
   short-lived credential a browser client authenticates with — is
   built. What remains is the reader page itself, which is the one
   thing on this list that adds a vendored third-party dependency.
2. **External metadata providers**
   ([ADR-0004](0004-metadata-and-categorization.md) phase 4) — optional
   forever.

The client work in [ADR-0008](0008-liseur-android-client.md) and
[ADR-0009](0009-liseur-desktop-client.md) is independent of all of the
above: it depends only on the MVP API surface, which is now stable and
includes `POST /v1/books/{id}/resolve`, the route a downloaded book needs
before it can sync.

## Consequences

The binary now owns durable content as well as a database. Deployment,
backup, quota, and security documentation must reflect that larger
responsibility. File parsers substantially enlarge the attack surface, so no
feature may bypass the bounds in ADR-0005.

The advantage is one coherent identity and sync system from upload to every
reader. A catalog book and its reading history converge through the same
native work-resolution protocol instead of being joined by client-specific
guessing.

The cost of drawing the MVP line here is that the first release will look
sparse next to Komga or calibre-web: no search, no tag browsing, no
metadata editing. That is accepted. Those are additive and can land
against a stable catalog; getting identity, durability, or the ownership
model wrong cannot be fixed additively once real libraries exist.

## Acceptance criteria

- The project documents no contradictory "not a library server" assumption.
- The roadmap preserves all existing sync, tenant-isolation, and adapter
  guarantees.
- Every item on the path to the MVP is required by the four-step definition
  above, and everything else is listed after it.
