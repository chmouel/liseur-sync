# ADR-0017: Folders, watched — not an ingest pipeline

- **Status:** Accepted
- **Date:** 2026-09-02
- **Supersedes:** [ADR-0002](0002-library-storage-and-ownership.md),
  [ADR-0005](0005-upload-and-ingestion.md),
  [ADR-0010](0010-duplicate-detection.md), the storage-mode, refresh-lease
  and review-queue parts of
  [ADR-0014](0014-library-sources-and-storage.md), and the
  external-lookup and manual-editing half of
  [ADR-0004](0004-metadata-and-categorization.md).

## Context

To put a book on a shelf, the content server ran it through an upload
endpoint, a staging area with a global byte cap, a per-user quota, a
content-addressed store that copied bytes the disk already held, a
persisted five-state ingest job, three worker goroutines, an
abandoned-upload sweep, an orphan-blob collector, a thirty-day trash with
a purge worker, a refresh lease with an owner token, and a review queue
an administrator resolved by hand.

That is roughly 5,700 lines of non-test code, five background workers, 22
migrations and some forty store methods, in service of a job that should
read: *point at a folder; the books in it show up*.

None of it was wrong, exactly. It was answering a question — "how does the
server take custody of bytes it did not have?" — that the people who
actually run this do not ask. They already have a directory of EPUBs, or a
Calibre library, and they want it visible.

## Decision

**A folder is a row: `id`, `name`, `root_path`, `kind`.** No owner, no
quota principal, no ACL, no lease, no scan bookkeeping. Every logged-in
user sees every folder's books; only an administrator sees or manages the
folders themselves, because a folder is the only place a filesystem path
appears.

**A reconcile pass** enumerates a folder's books, compares them against
the `books` rows for that folder, reads metadata for anything new or
changed, and upserts. Books it did not observe are marked `missing` — in a
Calibre folder they are deleted instead, because there the pass reads a
curated catalog rather than a disk ([ADR-0022](0022-calibre-metadata-db-is-authoritative.md)).
Nothing is copied, nothing is written under the folder, and the pass holds
no state: running it twice is running it
once. There is no job table, no queue and nothing to recover after a
crash.

**A watcher** runs a pass at startup, on a debounced filesystem
notification, and on a slow safety timer for mounts where inotify lies.
Adding a folder registers and reconciles it immediately; removing one
unregisters it. Debounce and safety interval are constants, not
configuration.

**How a book is recognised depends on the folder's kind, and this is load
bearing.**

- A **plain** folder is keyed by relative path. Mtime and size decide
  whether metadata needs re-reading. A subdirectory holding EPUBs is a
  series named after the directory, and the files in it are its volumes —
  the directory tree *is* the organisation, and nothing about that is
  configurable or asks a person to confirm it.
- A **Calibre** folder — one with a `metadata.db` at its root — is keyed
  by `calibre_id`, never by path. Calibre rewrites a book's directory
  whenever its title or author changes, so path matching would read a
  metadata edit as one book vanishing and another appearing, losing the
  reading-position mapping every time. Discovery comes from the database,
  and a moved book updates its stored path in place.
- A Calibre pass re-reads metadata **every time**, regardless of the
  EPUB's mtime and size. Series, tags, description and the chosen
  `cover.jpg` change in `metadata.db` without touching the publication
  file, so an mtime gate would make the server permanently blind to them.

## The four rules

1. **A pass that did not fully succeed never concludes anything is
   absent.** Not just an unopenable root: any per-file read or parse
   failure, and any hit against the file or depth bound, makes the pass
   incomplete. An incomplete pass may upsert what it saw; it may not mark
   anything missing, and it may not purge.
2. **A zero-observation pass never marks anything missing.** An unmounted
   mount point is frequently still readable and empty, which otherwise
   reads as a complete scan that found nothing and hides the whole
   catalog. Failing to notice a genuine delete-everything is the better
   error. This guards the Calibre purge too, and more strongly: it is the
   only thing standing between an unreadable `metadata.db` and an empty
   library.
3. **The server never writes under a watched folder.** No temporary files,
   no cover extracted beside the book, no `metadata.db` writes, no
   renames. Every open is rooted, read-only and refuses symlinks.
4. **Content change is not identity transfer.** A file whose bytes changed
   at a path becomes a new catalog book: in one transaction, the old row
   is deleted — cascading its identifiers, relations and `user_book_works`
   — and the new one inserted. `UNIQUE (folder_id, relative_path)` is what
   makes that a delete-then-insert rather than two rows fighting over a
   path. A Calibre book whose file changed keeps its `calibre_id` and is
   therefore an update: there the curator's database, not the bytes, is
   the identity.

Rules 1 and 2 live in `ReconcileFolder`'s signature rather than in a
comment, so a caller cannot forget them.

## Consequences

Gone, with no replacement and no deprecation path: uploads, the
content-addressed store, quotas and the staging cap, ingest jobs and their
workers, the trash and its purge, orphan collection, abandoned-upload
sweeps, refresh leases, the review queue, duplicate detection, manual
metadata editing, external metadata providers, per-library access grants,
and genres — an entity kind nothing was ever going to propose.

The migrations are squashed to a single baseline. This project has never
shipped, so a database from before this change is not upgraded, it is
deleted. Likewise there are no route aliases (`/v1/libraries*` simply
stops existing), no config compatibility shims, and no transitional
flags. The compatibility that still matters is with other people's
software — kosync, koplugin, OPDS 1.2, EPUB, Calibre's `metadata.db` —
and none of it changes.

The cover cache is now the only thing under the server's own directory,
and it is genuinely a cache: safe to delete while the server runs, costing
a re-render and nothing else. `cover_sha256` is recorded so that a
`cover.jpg` the curator replaced invalidates its entry rather than being
served stale under a key naming the old one.

Two-way Calibre synchronisation, uploads expressed as "write a file into a
watched folder", and any form of metadata editing are deliberately out of
scope. None of them gets a hook, a seam or a config key here; the ADR that
introduces one can introduce its own.

## Acceptance criteria

- A folder added at runtime is watched and reconciled without a restart.
- A file appearing under a watched root shows up without waiting for the
  safety timer; a file removed marks its book missing, and a book removed
  from a Calibre library's `metadata.db` is deleted.
- The same folder reconciled twice yields one book per file and no second
  write.
- A root that vanishes mid-pass, and an emptied-but-readable root, mark
  nothing missing.
- A Calibre book whose directory was renamed after a title edit keeps its
  book id, its work mapping and its reading position.
- A file replaced in place produces a new book with no inherited
  `user_book_works`.
- A snapshot of a watched tree — names, sizes, mtimes, and the mtime of
  the root itself — is byte-for-byte unchanged after reconcile, watch,
  cover rendering, download and the reader path have all run against it.
- `root_path` never appears in a response to a non-administrator.
