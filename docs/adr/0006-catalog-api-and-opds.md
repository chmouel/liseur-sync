# ADR-0006: Catalog API and OPDS

- **Status:** Accepted
- **Date:** 2026-08-12
- **Depends on:** [ADR-0003](0003-catalog-work-identity.md),
  [ADR-0004](0004-metadata-and-categorization.md)

## Context

Liseur clients need a full-fidelity API, while KOReader and other existing
readers can browse and download through OPDS. The native API must remain the
primary contract; OPDS is an interoperability adapter.

Current tokens have one scalar scope. A catalog-reading client also needs
sync, and an uploading reader may need management. The authorization model
must support combinations without breaking existing clients.

## Decision

### Token scope sets

Tokens store a set of scopes. Authorization succeeds when a route's required
scope is in the effective set.

- `library-manage` implies `library-read`.
- `admin` implies every scope.
- Existing implications remain centralized with these rules.

Migration preserves compatibility:

- Existing scalar scopes become singleton sets.
- Token creation accepts legacy `scope` or new `scopes`.
- Supplying both is allowed only when they describe the same set.
- Responses always include `scopes` and continue to include `scope` for a
  singleton set during a documented deprecation period.

A user presenting a fresh login credential may explicitly expand or reduce
one of their existing tokens in place through a token-update endpoint. A
bearer token cannot expand itself. The update preserves token ID, device ID,
secret, and retry identity. The endpoint applies the same grant rules as
token creation: it can never add `admin` without existing admin
authorization. Clients must prefer in-place expansion over replacing an
active sync token merely to add `library-read`.

New capabilities are:

- `library-read`: browse, search, covers, metadata, and download;
- `library-manage`: upload, edit metadata, delete managed books, and manage
  libraries where the ACL also grants manage access.

Routes remain exhaustively declared in `internal/api/routes.go`.

### Native API

Collection and member routes live in separate namespaces — `/v1/libraries`,
`/v1/books`, `/v1/ingest/jobs` — rather than under one `/v1/library/{id}/*`
prefix. A single prefix cannot hold both `/v1/library/{library}/books` and
`/v1/library/books/{id}`: `net/http` rejects the pair as ambiguous, because
"books" is indistinguishable from a library id.

The catalog API covers accessible libraries, paginated and filtered books,
book detail and metadata, covers, download and upload, ingestion job status,
metadata edits, and managed-book trash/restore. Series, contributors,
tags, genres, collections, and reading lists appear as the catalog gains them
(ADR-0004); the MVP subset is libraries, books, book detail, covers,
download, upload, and job status.

List endpoints use stable cursor pagination. Responses can include the
current user's optional `work_id`, position, completion, or reading-state
fields only when the token also has `sync`, and never include another user's
mapping. Aggregated statistics additionally require `read-insights`.
`library-read` by itself returns catalog metadata only.

OPDS feeds are always catalog-only. They suppress work mappings, positions,
completion, reading-state filters, and statistics even when the Basic token
also carries `sync` or `read-insights`. A future OPDS progress extension
requires its own decision and tests.

Downloads support `GET`, `HEAD`, byte ranges, `ETag`, `Last-Modified`, and
conditional requests. Filenames in `Content-Disposition` are sanitized and
encoded independently of the storage path. Covers use immutable cache keys
where possible and explicit MIME types with `nosniff`.

### OPDS 1.2

OPDS is served below `/opds/v1.2`. The MVP feeds are root navigation and
paginated acquisition feeds with EPUB acquisition links — enough for a
reader to find a book and download it. The root feed *is* the library list:
a separate libraries feed would be the same document behind another link.

A "recently added" feed and cover thumbnails are deferred with the rest:
neither is on the path from opening a reader to reading a book, and covers
have no storage behind them yet. Search through OpenSearch and
series/contributor feeds follow later; OPDS 2.0 and OPDS-PSE are deferred
entirely.

XML is generated through an encoder, never string interpolation, and has
escaping and pagination tests. OPDS 2.0 and OPDS-PSE are deferred.

OPDS authenticates with HTTP Basic for broad client compatibility:

- username is the token name or the literal `token`;
- password is a token secret containing `library-read`;
- account passwords are rejected;
- credentials are never accepted in query strings;
- 401 includes `WWW-Authenticate`;
- HTTPS enforcement follows the server's existing proxy and
  `insecure_http` rules;
- authentication attempts share the bounded rate-limit infrastructure.

## Consequences

Scope sets require a storage and API migration before catalog routes land.
The compatibility window avoids forcing existing Liseur clients to upgrade
in lockstep.

OPDS exposes less functionality than the native API by design. Upload,
metadata edits, work resolution, and rich sync remain native operations.

## Implementation phases

1. Scope-set migration, in-place token update, API compatibility, implication
   matrix, and UI. **Implemented.**
2. **Native catalog read and download API.** Done: `GET /v1/libraries`,
   `GET /v1/libraries/{library}/books` with opaque cursor pagination,
   `GET /v1/books/{id}`, and `GET`/`HEAD /v1/books/{id}/download` with
   ranges, `ETag` and conditional requests. **Remaining:** covers, and
   filtering beyond a plain listing.
3. **OPDS 1.2 acquisition feeds.** Done: `GET /opds/v1.2` navigation over
   the caller's libraries, `GET /opds/v1.2/libraries/{library}` paginated
   acquisition feeds, and `GET /opds/v1.2/books/{id}/download`, all behind
   HTTP Basic. **Remaining:** recently added, covers, OpenSearch, and
   series/contributor feeds.
4. Mutation API and ingestion job resources. Done: the ADR-0005 upload
   endpoint, the job resource, and `DELETE`/`POST .../restore` for
   reversible deletion. **Remaining:** metadata edits.

## Acceptance criteria

- Legacy singleton token clients continue working unchanged.
- In-place scope changes preserve token and device identity and cannot
  self-grant admin.
- Contradictory `scope` and `scopes` requests fail with a precise 400.
- Every catalog route checks both token capability and library ACL.
- Catalog-only and OPDS credentials cannot filter by or observe sync-derived
  reading state.
- OPDS remains metadata-only even when authenticated with a multi-scope
  token.
- Range, `HEAD`, conditional download, pagination, and safe filename tests
  pass on both stores.
- OPDS rejects account passwords and correctly escapes hostile metadata.
