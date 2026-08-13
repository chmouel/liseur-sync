# ADR-0002: Library storage and ownership

- **Status:** Accepted
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

Watched libraries are post-MVP. The decisions recorded here are the ones
that constrain storage and identity, so they are settled before the scanner
is built; the scanner's own mechanics are deliberately left open until then.

- The server never writes, renames, moves, trashes, or deletes anything
  below a watched root.
- Ingest copies from one descriptor into CAS-side staging. Downloads and
  covers always serve the recorded CAS blob, so mutating a source in place
  can never serve unvalidated bytes.
- Traversal uses descriptor-relative `os.Root` operations, and symlinked
  entries are skipped by explicit policy — staying beneath a root is not by
  itself a decision about symlinks.
- A disappeared root marks its books `missing`, and only a **completed full
  sweep** may do so — a dropped or coalesced filesystem notification, or a
  traversal that ended early, must never flip a book to `missing`.
  Notifications only reduce latency. Nothing is ever deleted, and a file
  returning with the same hash restores availability.
- **Identity is never transferred on hash equality alone.** Two live paths
  holding identical bytes are two catalog references that happen to
  deduplicate. Preserving a `book_id` across a content change requires
  either proof of continuity from the filesystem or an administrator's
  confirmation; anything ambiguous keeps the existing snapshot and records a
  review item rather than guessing. A confirmed different publication gets a
  new `book_id` and inherits no user work mappings.

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

The database and the content directory are one backup unit and must be
captured consistently — either by coordinated snapshots, or by pausing
ingest promotion, deletion, and GC while both are copied. A backup is only
valid if every database-referenced blob is present in it, so the verifier
that checks this is part of the feature, not an optional extra. Restore runs
the same check first, and treats extra blobs as candidates for the ordinary
grace-period reconciliation rather than deleting them.

## Consequences

Content-addressed paths make metadata edits cheap and avoid unsafe path
construction, at the cost of a separate export operation for users who want
a human-readable directory tree. No such export is planned; the content
directory plus the database is the supported representation.

Instance-wide physical dedup saves disk without conflating ownership.
Logical reservations may charge two quota principals for the same physical
bytes, which is intentional: quota measures library responsibility, not
server disk allocation.

## Implementation phases

1. **Schema, ACL queries, CAS, quota, and GC.** Done, except where noted.
   Library and book queries are ACL-scoped, the durable CAS and atomic
   database promotion commit blob identity and per-principal quota together,
   and startup reconciles the database against a strict descriptor-relative
   inventory before a configurable grace-period sweep runs. Active catalog
   references and ingest holds are GC roots, and deletion verifies the
   immutable blob first. Catalog availability follows that inventory:
   files whose blob is recorded missing stop being served, books with no
   servable file become missing, and both reverse when the blob returns.
   Superseded files are exempt, because supersession is not a statement
   about bytes. Trash and restore are in place: trashing retains the files
   so the blob stays a GC root and stays charged, restore relinks them and
   returns the book to `missing` when nothing servable remains, and a
   bounded hourly pass permanently deletes what has outlived its retention
   window, releasing quota and orphan-marking blobs that lose their last
   reference. `admin verify-backup` checks that every referenced blob is
   present in a backup at the size the database recorded, and reports
   unreferenced content without touching it. **Implemented.**
2. Managed-library upload and management UI — the MVP path.
3. Watched-folder scanner and reconciliation — later.

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
