# ADR-0002: Library storage and ownership

- **Status:** Proposed
- **Date:** 2026-08-12
- **Depends on:** [ADR-0001](0001-content-server.md)

## Context

Users have two common library shapes: an existing directory managed by
another tool, and books uploaded directly to the server. Treating them as
the same filesystem lifecycle risks modifying user-owned files or making
metadata renames move large files unexpectedly.

A household also needs to share one catalog while keeping positions,
sessions, and tokens private to each account.

## Decision

### Library kinds

An instance can contain multiple libraries:

- **Managed:** uploads are copied into server-owned content-addressed
  storage.
- **Watched:** an administrator registers an existing root. The server reads
  it but never writes, renames, moves, trashes, or deletes anything below it.
  Ingest copies each discovered EPUB into an immutable validated CAS snapshot;
  downloads never serve the mutable source path directly.

Every library has one owner and an ACL of users with `read` or `manage`
access. `manage` implies `read`. Revocation takes effect on the next request;
there is no copied permission state on books.

Catalog rows are library-scoped. Positions and sessions remain user-scoped.
The mapping is defined by
[ADR-0003](0003-catalog-work-identity.md).

### Content-addressed snapshots and quota

Managed files are stored by streaming SHA-256:

```text
<data_dir>/content/sha256/ab/cdef.../file.epub
```

Metadata never determines the on-disk path. A `blobs` table records hash,
size, creation time, and orphan-mark time. `book_files` references blobs.
Physical deduplication is instance-wide, but authorization is always checked
through the user's accessible library and book references.

Every managed or watched library has a `quota_user_id`, defaulting to its
owner. Managed uploads and watched CAS snapshots are both charged to that
principal. A watched scan that would exceed quota records a bounded failed
job and does not replace the current snapshot. Granting read or manage access
does not charge the grantee, and an upload by a manager does not silently
transfer ownership. Changing the quota principal is an explicit admin
operation.

A `blob_reservations(quota_user_id, blob_sha256, bytes)` row charges one
principal once for a blob while any of that principal's libraries reference
it. A second reference by the same principal costs no additional quota;
another principal referencing the deduplicated blob receives a separate
logical charge. Deleting the principal's last live reference
does not release quota while that reference remains in trash. Quota is
released when the last retained reference, active or trashed, is permanently
deleted. Physical storage is reclaimed independently by garbage collection.

Managed deletion first moves the catalog reference to trash. Retention is
configurable, and trashed bytes continue to count against quota, preventing
upload/delete cycles from growing disk without bound. Restoring within
retention relinks the existing blob. Watched books have no trash operation:
removing one from the catalog only unlinks the catalog row and never touches
the source path.

### Garbage collection

GC is mark-and-sweep with a configurable grace period:

1. Reconciliation marks blobs with no retained active or trash references.
2. A later sweep deletes only blobs that are still unreferenced after the
   grace period.
3. Any new reference clears the orphan mark.

Reconciliation never deletes an apparently orphaned file immediately. This
protects interrupted transactions and restores where the database and content
directory become visible at different times.

GC reachability includes active references and unexpired trash references.
A blob cannot be orphan-marked while any retained reference can still be
restored. Expiring trash removes its reference and quota reservation before
the blob becomes eligible for the separate orphan grace period.

Regenerable cover thumbnails are cached outside the backup-critical blob
set.

### Watched folders

The scanner combines filesystem notifications with a periodic full sweep.
Notifications reduce latency; only a complete sweep may declare a file
missing.

- The scanner copies from one descriptor into CAS-side staging; hashing,
  validation, and extraction operate on that immutable snapshot. The
  source's descriptor metadata and hash are recorded only for later
  reconciliation.
- Downloads and covers always use the recorded CAS blob, so an in-place
  source mutation can never serve unvalidated bytes. A later notification or
  sweep creates and validates a new snapshot, then reconciles identity before
  changing the catalog:
  - the same hash at another path is a rename only when a paired filesystem
    rename event proves continuity or a complete sweep finds an unambiguous
    one-missing-path to one-new-path match for that hash; zero-to-many,
    many-to-one, and many-to-many matches are flagged for review and do not
    transfer identity; if both paths exist, they remain distinct catalog
    references that deduplicate to the same blob;
  - a new hash preserves `book_id` only when a stable embedded identifier
    agrees with the existing book or an administrator confirms the match;
  - an ambiguous replacement leaves the old snapshot available, records a
    review item, and does not change user work mappings;
  - a confirmed different publication marks the old path missing and creates
    a new catalog book with no inherited mappings.
- Rename detection uses content hash, preserving book identity.
- A disappeared root marks its books `missing`; it never deletes rows.
- Files reappearing with the same hash restore availability.
- Traversal uses descriptor-relative `os.Root` operations to keep reads
  beneath the configured root.
- Symlinked entries are skipped by a separate, explicit policy. Beneath-root
  containment does not itself mean "no symlinks."

Watched roots are semi-trusted administrator-configured paths. The guarantee
is that the server does not escape the configured root, not that it can
defend against a hostile administrator who controls that root and the
server process.

### Configuration and deployment

Configuration adds explicit paths and limits for:

- managed content and staging roots (initial staging may be on another
  filesystem; atomic promotion always uses a CAS-side incoming directory);
- watched roots;
- upload quota;
- trash, quarantine, and orphan grace periods;
- scan interval and maximum scan concurrency.

Container deployments mount the database and managed content as persistent
volumes and watched roots read-only.

### Backup and restore

The database and content-addressed directory form one backup unit. A
consistent backup either uses coordinated filesystem/database snapshots or:

1. enters maintenance mode, pausing ingest promotion, deletion, and GC;
2. backs up the database;
3. backs up the content directory;
4. verifies that every database-referenced blob exists in the backup;
5. leaves maintenance mode.

Restore runs the same verification before normal service and marks extra
blobs for grace-period reconciliation instead of deleting them.

## Consequences

Content-addressed paths make metadata edits cheap and avoid unsafe path
construction, at the cost of a separate export operation for users who want
a human-readable directory tree.

Instance-wide physical dedup saves disk without conflating ownership.
Logical reservations may charge two quota principals for the same physical
bytes, which is intentional: quota measures library responsibility, not
server disk allocation.

## Implementation phases

1. Schema, ACL queries, CAS, logical quota, trash, GC, and backup verifier.
2. Managed-library upload and management UI.
3. Watched-folder scanner and reconciliation.
4. Export tooling, if later required.

## Acceptance criteria

- No watched-library operation mutates the watched root.
- Watched downloads never read from the mutable source path and cannot serve
  bytes that differ from the validated blob.
- No catalog query crosses a library ACL.
- Concurrent identical uploads create one blob and separate authorized
  references and quota reservations.
- Readers and managers are not charged merely because a library is shared
  with them.
- Managed uploads and watched snapshots obey the same explicit quota
  principal and limit.
- Trashing a book does not free quota; permanent deletion of the last
  retained reference does.
- An orphan is not physically removed before the grace period.
- A restorable trash entry remains a GC root until its retention expires.
- Backup verification detects every missing referenced blob.
- Scanner rename, missing-root, symlink, and ancestor-swap cases have tests.
- Replacing a watched path with an unrelated valid EPUB cannot inherit the
  previous `book_id`, metadata, or user work mappings.
- Identical EPUBs at two live paths remain distinct catalog references; hash
  equality alone never proves a rename.
