# ADR-0027: Explicit per-user folder access

- **Status:** Accepted
- **Date:** 2026-08-22
- **Depends on:** [ADR-0017](0017-folders-not-pipelines.md)
- **Amends:** ADR-0017's catalog visibility rule and sections 2, 3, 8,
  9 and 10 of [DESIGN.md](../DESIGN.md)

## Context

A watched folder is shared catalog data, but treating that as permission for
every account to browse it makes one server unsuitable for people who keep
separate libraries. Administrator status describes who may operate the
instance, not what belongs on that person's reading shelf. Token scopes answer
which operation a credential may perform, not which folders it may reach.

## Decision

Add an explicit many-to-many `user_folders` grant table. New users, new
folders and migrated accounts have no grants. A folder has no owner and may be
granted to any number of users. Administrators manage every folder and every
grant, but browse a folder only when they grant it to themselves.

Every catalog-facing read takes a viewer user id. A real id requires a grant;
the empty id is reserved for reconciliation, watchers, administration and
other trusted internal work. Lists omit inaccessible rows and direct access
returns `404`, including for an empty folder. Series, contributors and tags
remain library-wide identities, while their counts, facets and book results
use only books visible to the viewer.

Downloads, covers, OPDS, search, uploads, deletion, work resolution and
backfill use the same boundary. Capability checks remain separate: a missing
scope is `403`, while a missing grant is `404`. Upload digest deduplication is
viewer-scoped so bytes in a hidden folder do not disclose that folder or
prevent a copy being added to an accessible one.

Revoking a grant removes no reading state and no `user_book_works` row. It
only makes the catalog side of that mapping inaccessible. Restoring the grant
therefore reconnects the existing work, positions, sessions and statistics.

Grant management is available in each user's administration page and through
the administrator CLI. There is no public grant-management API, folder
ownership, grant copying or user-count field on folders.

## Consequences

- An account's library is empty until an administrator assigns folders.
- Administrator role and reading access are independent.
- Catalog queries carry a viewer id even though catalog rows remain shared.
- Deployments upgrading to migration 4 must assign existing accounts' folders.
- Revocation is reversible and does not erase reading history.

## Acceptance criteria

- SQLite and PostgreSQL apply migration 4 without creating grants.
- Assignment, removal and atomic replacement validate both sides and cascade
  when either side is deleted.
- Every catalog and file-serving surface returns only granted content, with
  inaccessible direct URLs answering `404`.
- Upload and deletion require both the relevant token scope and a folder
  grant, and digest deduplication cannot reveal hidden books.
- The web panel and CLI manage grants, including administrator self-assignment.
- Revoking and restoring a grant preserves and reconnects reading history.
