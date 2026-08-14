# ADR-0014: Library sources, storage modes and Calibre

- **Status:** Proposed
- **Date:** 2026-08-15
- **Depends on:** [ADR-0002](0002-library-storage-and-ownership.md),
  [ADR-0003](0003-catalog-work-identity.md),
  [ADR-0005](0005-upload-and-ingestion.md)
- **Amends:** [ADR-0002](0002-library-storage-and-ownership.md), whose
  "Library kinds" section this replaces.

## Context

A library is one of two things today, and the two things are welded
together in a single `kind` column. A **managed** library holds books
users uploaded, copied into content-addressed storage. A **watched**
library is an existing directory an administrator registered, which a
sweep walks periodically, handing every EPUB it finds to the same ingest
pipeline an upload uses — which copies each one into content-addressed
storage as well.

That last clause is the problem. Pointing this server at a 200 GB
Calibre tree costs 200 GB a second time, for bytes that are already on
the disk. ADR-0002 chose that deliberately and gave a good reason:
downloads never serve a mutable source path, so a book cannot change
underneath a reader, and a snapshot is immutable in a way somebody
else's directory can never be. The reason is still good. It is just not
worth paying on every library, and for a large existing collection it is
the difference between using this server and not.

The second problem is vocabulary. `watched` names two unrelated things
at once: *where the books come from* (a directory, not an upload) and
*how often we look again* (periodically). Because they are one word,
every new shape of library arrives as another kind with another scanner.
"A directory I point at once and never rescan" and "a Calibre library"
are not two more kinds; they are two points in a space the current model
cannot express.

The third is that a Calibre library already knows far more about its
books than this server can learn by parsing their EPUBs. Series and
series index, tags, publisher, publication date, description,
identifiers, and a cover that somebody already chose — all sitting in a
SQLite database at the root of the tree. Reading the EPUBs instead, and
discarding what Calibre knows, is not a neutral choice: it produces a
visibly worse catalog than the one the user already has.

Two properties of the existing code make this much cheaper than it
looks, and both were found by investigation rather than assumed.

**The presence of a source file is already a first-class fact.**
Migration 14 added `book_files.source_seen_at`, `source_absent_at` and
`source_modified_at`, for a reason its own comment states: "source
presence is a separate axis from blob presence". The sweep already
maintains those columns. A file served in place needs exactly that
signal and no other.

**Serving publication bytes is a single chokepoint.** Downloads and
cover rendering — and through them the EPUB validator and the zip reader
— both arrive at `BlobStore.OpenBlob(ctx, sha)`, an interface with one
method and two production call sites. The web reader is client-side and
unzips in the browser, so there is no server-side interior-resource
route to convert. Ingest is *not* part of that chokepoint: validation
and extraction read the durable staged artifact instead, which is what
makes the ingest state machine restartable, and which is why an in-place
scan needs a pass of its own rather than a new resolver.

## Decision

### Three axes replace one kind

`libraries.kind` becomes three columns.

**`source`** — where books come from.

- `managed`: uploads. Server-owned bytes, no root path.
- `directory`: an administrator's directory of EPUBs. What `watched` is
  today, minus the assumption about rescanning.
- `calibre`: a Calibre library directory, recognised by `metadata.db` at
  its root.

**`storage`** — where the bytes we serve live.

- `cas`: copied into content-addressed storage, as today. Forced for
  `managed`, and the default for `directory`.
- `in_place`: read where they lie, never copied. Legal only with a root
  path, and the default for `calibre`.

**`refresh`** — how often we look again: `manual` or `interval`, with
`refresh_interval_seconds`, the timestamps a refresh records and the
lease it is claimed with.

A watch folder is therefore no longer a kind of library but a policy any
directory-backed library can have, and today's watched library is
exactly `directory` + `cas` + `interval` — which is what the migration
writes for every existing row. The static library the operator asked for
is `directory` + `in_place` + `manual`.

The axes are columns rather than keys in `config_json` because the
scheduler asks a question across libraries — which are due? — and
because a `CHECK` constraint is the cheapest place to state that a
managed library has no root and an in-place library must have one.

### An in-place file has no blob

This is the load-bearing decision, and it is the one that keeps the rest
of the system out of the change.

The obvious design gives every file a `blobs` row and marks the ones
that live outside the store. It is wrong here, for a reason particular
to this schema: `blobs` is keyed by digest **globally**, with no library
scoping, because physical deduplication is instance-wide (ADR-0002). Two
users who upload the same book share one row, and that is the point. But
an uploaded copy and a user's own file with the same bytes would then
also share one row, and every consumer that reasons about that row —
reference counting, orphan collection, `blob_reservations`, backup
verification — would have no way to tell whose bytes it is describing.
Garbage collection could unlink a CAS file a managed library still needs
because the last *in-place* reference went away, or keep one alive
forever for the same reason.

`ReconcileBlobInventory` makes it worse rather than better. Its model is
"walk `<data_dir>/content/sha256/`, and mark every database row whose
digest is not there as missing". An in-place digest is never there. On
the first maintenance tick after a scan, every in-place book would be
marked missing and would stop being offered for download.

So an in-place file gets no `blobs` row at all.

- `book_files.blob_sha256` becomes nullable and keeps its foreign key.
  It now means precisely "the CAS copy", and is NULL for in-place files.
- A new `book_files.content_sha256`, NOT NULL, carries identity: what
  two copies of a book have in common, what the cover cache is keyed by,
  what duplicate detection groups on, what an edition is matched by.
- A `CHECK` states the pairing: a `cas` row has a blob, an `in_place`
  row has a source relative path and no blob.

Everything downstream then holds without modification, not by luck but
because the premise it was written against is still true:

- **Quota** charges nothing, because there is no `blob_reservations`
  row. That is also the honest answer: quota exists to account for
  storage this server provides, and here it provides none.
- **Garbage collection, backup verification and blob reconciliation**
  keep "presence means found under `sha256/`", because they enumerate
  `blobs` and every row there is still a CAS file.
- **Deleting a book cannot delete a user's file**, because there is
  nothing to unlink — and this stops being an accident of a missing path
  and becomes a structural fact.
- **Duplicate detection** is unaffected; it groups on a digest, and the
  digest is still there under a different column name.

The cost is a schema migration that touches every query mentioning
`blob_sha256`, split by intent: identity queries move to
`content_sha256`, CAS queries stay and gain `blob_sha256 IS NOT NULL`
where they enumerate. That sweep is mechanical, and the compiler finds
most of it.

### Availability of an in-place file

`BookFileAvailability` already has the vocabulary — `available`,
`missing`, `superseded` — and needs no new state. What changes is where
the signal comes from: for a CAS file, blob presence as today; for an
in-place file, `source_absent_at`, which the sweep already writes when a
completed traversal proves a path is gone. A file that comes back is
seen again and becomes available again.

The queries do not follow for free, and this is the one place the
"nothing downstream changes" story is false. `markFilesMissing` and
`markFilesAvailable` both **inner join `blobs`**, because until now every
file had one; an in-place row would match neither and would never
transition in either direction. Both statements become a `LEFT JOIN` with
the presence rule branching on `storage`: a `cas` row is missing when its
blob is, an `in_place` row when its source is. Every listing and
availability path is re-read for the same assumption rather than trusted,
and the store test asserts an in-place book going missing and coming
back.

### Reading bytes that are not ours

`BlobStore.OpenBlob(ctx, sha)` becomes `OpenBookFile(ctx, file, root)`,
taking the `store.BookFile` the caller already holds. A CAS file
resolves as today. An in-place file is opened through `os.OpenRoot` on
the library root and then by relative path with `O_NOFOLLOW|O_NONBLOCK`,
so that a symlink planted in the tree cannot reach outside it and a FIFO
left in its place cannot block the server on open. The check that follows
is made **on the descriptor, not on the path**: `fstat` it, refuse
anything that is not a regular file, and refuse unless size and
modification time still match what the last scan recorded. The same
descriptor is then served, so nothing is re-resolved between the check
and the read.

It also requires the caller to know the root, which today it cannot:
`ListBookFiles` returns files, not libraries, and the API handlers hold
only a `BookFile`. Rather than bolt an unscoped root lookup onto the
side — the one shape that could read across libraries — the store gains
an ACL-scoped read that returns the file together with its library's
storage mode and root in one authorised answer. The handler never
constructs a path and never receives one from a client.

That requires two things the schema does not have. `book_files` gains
`content_size_bytes`, because size comes from `blobs` today and an
in-place row has no blob; and `source_modified_at`, which exists but is
currently only written by the sweep, becomes the recorded mtime the check
compares against.

The check is deliberately not a re-hash. Hashing a 50 MB EPUB on every
download and every cover render would make the feature unusable, and the
scan already re-hashes when size or mtime move. What it buys is that the
server does not silently serve bytes it never catalogued: a mismatch
refuses the read and flags the book for review, which is the same queue
an ambiguous scan result already lands in.

It does not buy immutability, and the response headers must stop
claiming it does. A download today sends `ETag: "<digest>"` with
`Cache-Control: private, max-age=31536000, immutable`, which is correct
for content-addressed bytes and wrong for a file somebody can replace: a
client would cache altered bytes for a year under the old digest. An
in-place response sends a weak validator derived from digest, size and
mtime, and `private, no-cache`, so the client revalidates and the
integrity check runs again. Managed downloads keep the strong, immutable
form they have earned.

The residual hole is stated rather than hidden: a replacement that
preserves both size and mtime is undetectable without hashing, and a
file can be modified after the check while it is being read. In-place is
a trade of immutability for disk. It is opt-in everywhere except
Calibre, and the documentation says exactly this.

### Ingesting without staging

The other read path is ingest, and the ADR's first draft called it part
of the same chokepoint. It is not: validation and extraction read the
**durable staged artifact** (`ValidateEPUBArtifact`,
`ExtractIngestMetadata`), and that artifact is what makes the ingest
state machine restartable — a worker that dies between stages finds the
bytes exactly where its job says they are.

An in-place scan has no such artifact by construction, so it does not
get to reuse that state machine unchanged. Its lifecycle is stated here
rather than left to the implementation.

A durable job is still created at discovery, with the same derived id
today's sweep uses — path, size and mtime — so rescanning an unchanged
file re-enters its job instead of queueing the book twice. The job
carries `storage = in_place`, which is what tells a worker there is no
artifact to find; the states are `received` and then, in one step,
either `promoted` or `failed`, with no `staged` or `extracted` in
between, because those states exist to name where the bytes are and
here they never moved.

The pass opens the source once and does everything from that one
descriptor: hash, then seek to the start and validate, then seek again
and extract. It `fstat`s the descriptor again at the end and abandons
the pass if size or mtime moved underneath it. The commit is a single
atomic store operation that inserts the book, the file row and the
metadata together, with ids derived exactly as promotion derives them,
so a commit whose response was lost replays onto the same rows and
changes nothing the second time.

A crash before the commit leaves a `received` job and no artifact. That
is not recoverable from durable state and is not meant to be: there is
nothing to recover, and the next sweep redoes the work for a few cents
of I/O. Such a job is distinguishable from an abandoned upload by its
`storage` and `scanned` source — an upload with no artifact is a
genuine loss and stays a failure, an in-place job with no artifact is
simply not started yet — so the recovery pass that hunts orphaned
uploads must learn to leave these alone rather than declaring their
artifact missing.

### Calibre, read live

`internal/calibre` opens `metadata.db` **read-only** — `mode=ro` on the
`modernc.org/sqlite` driver already in the build, which cannot create
the file and cannot write it — and never writes anything under the root.
There is no write-back of ratings or read state; Calibre's database is
its own, and a sync server that edits it is a bug waiting for a version
bump.

Reaching the database safely is the one place where the rooted-open
pattern used everywhere else does not fit, and it is worth being exact
rather than reassuring. The driver takes a path, not a descriptor, so
SQLite performs its own `open(2)` — and a check performed on a
descriptor we opened first says nothing about the file SQLite opens a
moment later. Escaping the URI stops `?` and `#` in a directory name
from injecting parameters, but it does not close that race.

Rather than claim a guarantee this cannot deliver, the ADR narrows the
threat model and says so. **A library root is trusted input.** An
administrator names it on the server's command line, the recommended
deployment mounts it read-only, and anybody who can create files inside
it can already have the scanner ingest whatever they like. Hostile
*concurrent* mutation of a root — a symlink swapped in between one
syscall and the next — is explicitly out of scope, here and for the
in-place read path, and the documentation says which posture that
assumption rests on.

Two checks are still made, as defence against accident rather than
against an attacker: `metadata.db` is opened `O_NOFOLLOW` through
`os.Root` first, so a root containing a symlink named `metadata.db` is
refused outright rather than followed; and once the connection is up,
the path SQLite reports through `PRAGMA database_list` is `stat`ed and
compared by device and inode with that descriptor, so an ordinary
misconfiguration — a bind mount that moved, a root that is itself a
symlink into another library — is caught before a single row is read.
Neither closes the race against a hostile local user, and the ADR does
not pretend otherwise.

**Discovery comes from the database, not from walking the tree.**
Calibre's `books` table names every book, its directory, and through
`data` its formats; `path` and `name` give the file's relative location.
Walking instead would mean guessing which stray files are books, and
would find things Calibre itself does not consider part of the library.
The consequence, accepted deliberately: a file dropped into the tree
behind Calibre's back is invisible until Calibre is told about it, which
for a Calibre library is the correct answer rather than a limitation.

**Identity is Calibre's book id, and only that.** A new
`library_calibre_books(library_id, calibre_id, book_id)` mapping, unique
on both sides, is what resolves a Calibre row to a catalog row. Matching
by content digest was in the first draft and is wrong: ADR-0002 keeps two
identical files at two live paths as two distinct catalog references, and
a digest match would silently merge them. A digest still says "these are
the same bytes" — for duplicate reporting, and for deciding that a book
whose file moved needs no re-ingest — but it never selects the row to
update.

**Metadata flows through the precedence engine that already exists**,
with one addition. `internal/metadata` ranks sources embedded(1) <
filename(2) < external(3) < manual(4), ignores blank candidates, accepts
a candidate of strictly higher precedence *or one from the same source* —
which is how a re-read refreshes its own earlier value — and locks a
field the moment a human edits it. Calibre cannot simply be `external`,
because network lookups are already `external`: the same-source rule
would then let a Google Books result overwrite Calibre and Calibre
overwrite it, forever, depending on which ran last. So `calibre` becomes
its own source, ranked above `external` and below `manual`. Three
properties follow: Calibre beats both the EPUB and any lookup service,
for a library whose whole premise is that Calibre knows best; a title
corrected in Calibre lands on the next refresh; and a title corrected in
this server's own UI is never clobbered by one.

The engine ignores blank candidates by design, so a value cleared in
Calibre would otherwise persist here forever. Representing a clear as
"empty value from Calibre" is not enough either, because an empty field
today accepts a candidate from *any* source: the description Calibre
just cleared would be refilled from the EPUB on the next extraction, and
the two would fight forever.

So a cleared field is a tombstone rather than an absence. Clearing
writes an empty value **and keeps the provenance**, and `Apply` gains
one rule to match: an empty field with recorded provenance accepts only
candidates of rank greater than or equal to that provenance. An empty
field with no provenance behaves as it does today and accepts anything,
so nothing about existing rows changes. A manual clear therefore stays
cleared against everything, a Calibre clear stays cleared against the
EPUB and against lookup services, and a human can still refill either.

Covers come from `cover.jpg` in each book's directory, which is a cover
somebody chose, in preference to extracting one from the EPUB, and is
opened through the same rooted, no-follow, regular-file, size-bounded
path as everything else under a root. The cover cache cannot be keyed by
the publication digest alone here: two Calibre entries can share one
EPUB and have different covers, and a digest-keyed cache would serve one
book's cover for the other, or cache "this book has no cover" for both.
The key gains a cover revision — the digest of the cover file — and the
absent marker follows it.

`metadata.opf`, which Calibre writes beside each book, fills fields the
database row does not have — nothing more. It is not a second discovery
mechanism: a book the database does not know does not exist for this
library, and reading an OPF for it would be the tree walk this design
explicitly rejected, arriving through a side door. A database that
cannot be opened or understood fails the whole refresh with a recorded
code, rather than degrading into a directory scan nobody asked for.

**A book that changes keeps its catalog row.** The mapping is what makes
that possible, and the cases are worth naming because "insert a new
book" is only the first of them. A Calibre book whose *metadata* changed
re-runs the precedence engine against the same `book_id`. One whose
*file moved* — a rename in Calibre changes the directory — keeps its row
and its file row, and only the recorded source relative path and stat
snapshot change. One whose *bytes changed*, because Calibre converted or
replaced the format, gets a new file row superseding the old one, which
is the vocabulary `book_files.availability` already has for exactly this
and which keeps the earlier edition's identity intact rather than
transferring it. One that *gains a second format* gains a second file
row. One that *loses its EPUB* but stays in Calibre keeps its catalog
row and becomes unavailable, because Calibre still says the book exists
and only this server's ability to serve it went away. Each of those is
one atomic per-book operation, so an interrupted refresh leaves a book
either as it was or as it will be, never half-updated.

**A book that disappears from `metadata.db` is deleted from the
catalog**, which is what the operator asked for, and the cost is worth
stating precisely rather than softening. Reading positions, sessions and
statistics hang off user-scoped `works` (ADR-0003) and survive
untouched. What the cascade does take is catalog state keyed to the
book: the `user_book_works` mapping, reading-list membership, and any
manual metadata edits. The mapping can be
re-established rather than self-healing: works are identified by edition
digest, so when a returning book is opened again the client resolves the
same edition and lands on the same work, with its history intact. That
is a re-resolution by whoever reads the book next, not something the
refresh does, and it fails if Calibre re-encoded the file in the
meantime — a different digest is a different edition, and the old
history stays attached to the old one. The manual edit is simply gone,
which is a defensible loss in a library whose declared source of truth
is Calibre. Trash does not apply: it exists to hold bytes through a grace
period before unlinking them, and here there are none of ours to hold
and none of theirs we may touch.

**A refresh that changes nothing does no catalog writes**, and the gate
that decides so is not a shortcut around reading the database. Two
earlier candidates were both unsound. Comparing the size and mtime of
`metadata.db` fails because Calibre runs SQLite in WAL mode: a commit
can leave the main file untouched while the change sits in
`metadata.db-wal`, so a stat-gated refresh would miss edits and
deletions indefinitely. Comparing a count and a maximum `last_modified`
fails more subtly: a book edited at the current maximum leaves both
unchanged, and so does one row replaced by another.

The gate is therefore a **full inventory read**, in one read
transaction: every book's id and `last_modified`, its format list, and
the size and modification time of each publication file and cover,
hashed into one digest stored on the library. That is a few thousand
rows of an index scan and a `stat` per book for a large library — cheap
next to opening a single EPUB, and cheap enough to run every interval.
When the digest matches, the refresh stops there and touches no catalog
row. When it moves, the inventory that was just read is also what drives
the work: per-book `last_modified` and file stat select the rows to
re-read, and the id set reconciles deletions, which no timestamp can.

It detects every change that moves a row or a file's size or mtime,
which is not the same as every change. A publication or a cover replaced
with different bytes of the same size and the same modification time is
invisible to it, exactly as it is to the in-place read check, and for
the same reason: the alternative is hashing every file in the library on
every interval. The claim is "a refresh that sees nothing changed did
not miss a normal change", not "the catalog cannot be fooled".

### Refreshing

`serve` gains a scheduler that asks which libraries are due and
refreshes them one at a time. Due is measured from the last *attempt*,
not the last success — `COALESCE(last_refresh_attempt_at,
last_refresh_at, created_at) + interval < now` — because a library whose
refresh fails would otherwise be permanently overdue and would retry in
a tight loop.
The guard against two refreshes of one library is a **lease in the
database**, not a mutex in the process: `refresh-library` in another
process and a second `serve` against a shared Postgres are both real,
and an in-memory guard sees neither. A refresh claims the library by
writing a lease that expires, so a worker that dies does not lock a
library out forever.

An expiring lease alone is not enough, because expiry does not stop the
worker that held it: a slow refresh can still be running when a second
one takes over. The lease therefore carries a random owner token, the
holder renews it while it works, and **the unit of work is a transaction
that begins by taking the lease row**.

One book's update is not one statement — it touches metadata, entity
memberships, file rows, availability and the Calibre mapping — so a
predicate carried on each statement would leave lease validation and the
mutation unsynchronised. Instead each per-book transaction locks the
library's lease row first (`SELECT … FOR UPDATE` on PostgreSQL; the
write transaction itself serialises on SQLite), verifies that the owner
matches and the lease has not expired, and only then performs every
write for that book. A worker that lost the lease finds the mismatch
under the lock, rolls back, and stops.

A refresh writes per book rather than as one transaction over a whole
library, because a library with twenty thousand books cannot be one
transaction. Losing the lease mid-refresh therefore leaves a partially
refreshed catalog, and that is acceptable for a specific reason: every
per-book operation is idempotent and derived from the inventory, so the
refresh is convergent rather than atomic — the next run over the same
inventory finishes what the last one started. What must not happen is a
*successful* completion being recorded by a worker that no longer owns
the library. The final commit takes the lease row the same way each
per-book transaction does, verifies owner and expiry under that lock,
and only then records the outcome and the inventory digest — so a
dispossessed worker leaves the digest untouched and the next refresh
does the remaining work.

The timestamps are three, because collapsing them breaks one case or the
other: `last_refresh_attempt_at` advances on every attempt, so a failing
library retries on its interval instead of continuously;
`last_refresh_at` advances only on success, so "when was this catalog
last correct?" stays answerable; and the lease expiry is separate from
both.

A library's admin page shows those, offers "Refresh now" — session CSRF
token, no admin password re-verification, because reading a directory is
not a credential operation — and `refresh-library` does the same from a
shell.

The maintenance page gains overdue counts and failure counts, and gains
no buttons and no raw error strings. ADR-0013 decided that page reports
and does not act, and that no page renders a filesystem path; a refresh
failure is therefore recorded as a bounded code (`unreadable_database`,
`root_missing`, `unsupported_schema`, …) which the panel renders, with
the underlying error going to the log where paths are allowed.

### What stays out of the browser

ADR-0013 kept `watch-library` a subcommand on purpose: naming a server
filesystem path in a browser hands a remote administrator a
filesystem-existence oracle and a way to point the scanner at any
readable tree on the host. Nothing here weakens that. Creating a
`directory` or `calibre` library remains CLI-only, and so does choosing
its storage mode, which is a property of how it was ingested rather than
a setting. The panel shows all three axes, sets the refresh policy and
interval, triggers a refresh, and keeps the grants and layout controls it
already has.

## Migration

One migration per phase per backend, appended, never editing a shipped
one. Four rather than one, because the `CHECK` constraints widen as the
code that honours them lands, and a single migration would have to admit
values nothing implements yet.

`libraries` gains `source`, `storage`, `refresh`,
`refresh_interval_seconds`, `last_refresh_attempt_at`, `last_refresh_at`,
`last_refresh_code`, `refresh_lease_owner`, `refresh_lease_until` and
`calibre_inventory_digest`, and loses `kind`. SQLite cannot alter a `CHECK` constraint, so the table
is rebuilt — create, copy, drop, rename — inside the migration's
transaction; Postgres drops and adds constraints in place. Existing rows
map `managed` to (`managed`, `cas`, `manual`) and `watched` to
(`directory`, `cas`, `interval`).

Phase 2 adds to `book_files` `content_sha256` (backfilled from
`blob_sha256`, which is why it can be NOT NULL), `content_size_bytes`
(backfilled from `blobs.size_bytes`) and `storage`, makes `blob_sha256`
nullable — again by table rebuild on SQLite — and adds `storage` to
`ingest_jobs` so a worker can tell an in-place pass from a staged one.
Phase 3 adds the lease and timestamp columns. Phase 4 adds
`library_calibre_books`, the library's `calibre_inventory_digest`, and
the `calibre` value in the metadata-source constraints. Each phase's
migration also widens the `source` and `storage` `CHECK`s to admit
exactly what that phase implements.

A SQLite table rebuild is a footgun with a well-known shape: it silently
drops indexes, constraints and any column a later migration added if the
new definition is written from memory. There are several rebuilds here,
not one: `libraries` is rebuilt in Phase 1 for the axes, again in Phase 2
when `storage` admits `in_place`, and again in Phase 4 when `source`
admits `calibre`, because every widened `CHECK` is a new table on SQLite;
`book_files` is rebuilt in Phase 2 to make `blob_sha256` nullable. Each
recreates every index and constraint the table had, is written against
the schema as it stands at that phase rather than against migration 6,
and ends with `PRAGMA foreign_key_check` inside the migration
transaction, so a rebuild that lost a reference fails the upgrade instead
of shipping a broken catalog.

Nothing has shipped, so there is no compatibility shim, no dual read
path and no deprecation window — the ADR-0013 rule applies here too. The
one thing the migration must prove is that a library registered under
the old vocabulary still works afterwards, which is a store test rather
than a promise.

The new column values are gated on the phase that implements them: until
Phase 2 lands, `storage` accepts only `cas`, and until Phase 4 lands,
`source` accepts only `managed` and `directory`. A schema that can
express a state no code implements is a schema that will eventually hold
one.

The type names follow: `store.LibraryKind` becomes `LibrarySource`,
joined by `LibraryStorage` and `LibraryRefresh`, and
`ListWatchedLibraries` becomes `ListScannableLibraries`, returning every
library with a root.

`store.IngestSource`'s `watched` becomes `scanned` — the file was
discovered on disk, and the frequency of discovery is now somebody
else's business — but in Phase 2 rather than Phase 1. The value is
stored in `book_files.source` and `ingest_jobs.source`, both under a
`CHECK`, and both tables are already rebuilt in Phase 2 for
`content_sha256` and `storage`. Renaming in Phase 1 would rebuild them
twice for a word.

## Alternatives considered

**A `blobs.location` column, or a `blobs.external` flag.** Rejected
above: the table is keyed by digest globally, so a flag cannot describe
two files with the same bytes and different owners. Everything else in
this ADR follows from that one observation.

**Hard links or reflinks from the CAS to the user's file.** Tempting on
a filesystem that supports them, and it would preserve the immutability
guarantee at no disk cost. Rejected because it requires the library root
and the data directory to be on the same filesystem — which for the
mount-it-read-only deployment ADR-0002 recommends is exactly what they
are not — and because a hard link into somebody else's directory is a
write to it in every sense that matters.

**Re-hashing on every read.** Rejected on cost. The size and mtime check
plus a re-hash during the scan is the same bargain every backup tool
makes.

**Matching Calibre books to catalog rows by content digest.** Rejected
in favour of an explicit id mapping: ADR-0002 deliberately keeps two
identical files as two catalog references, and a digest match would merge
books the user has as separate entries and hand one's manual edits to the
other.

**Copying Calibre's database rather than reading it live.** Rejected as
the worst of both: still a copy, still stale, and it converts "Calibre
is the source of truth" into "Calibre was the source of truth at import
time", which is the thing the operator specifically did not want.

**Calibre as a `metadata.Provider`.** The provider registry is built
around a network `Fetcher` with a host allowlist and per-host limits;
a local SQLite file matches none of it. Calibre produces candidates for
the same precedence engine, but it is a library-scoped source, not a
lookup service.

**Custom columns and write-back.** Out of scope by decision. Both are
additive later; neither is needed to make a Calibre library work here,
and write-back in particular is a promise about somebody else's schema.

## Consequences

- A large existing collection can be served without doubling its size,
  which is the difference between adopting this server and not.
- The immutability guarantee ADR-0002 made is now a property of a
  storage mode rather than of the whole system. Managed libraries keep
  it unconditionally; a directory library keeps it by default; an
  in-place library trades it, knowingly.
- Quota stops being a proxy for "how many books" and becomes what it
  always claimed to be: how many bytes this server stores for you.
- Backup gains a real caveat that must be documented: a backup of the
  data directory does not contain an in-place library's books, because
  the server never had them.
- The scheduler is a fifth periodic worker, and a slow refresh of a
  large library must not block ingest or the rest of maintenance.
- Downloads stop being uniformly cacheable-forever: an in-place file
  revalidates on every request, which costs a round trip and is the
  price of not lying about immutability.
- An in-place ingest is not restartable from durable state, because
  there is no staged artifact to restart from. A crashed pass is simply
  redone by the next sweep.
- A fourth metadata source (`calibre`) enters the precedence ladder,
  which every place that renders or reasons about provenance must know
  about.
- `internal/content` is `//go:build linux`. `internal/calibre`'s
  parsing and mapping stay portable and testable anywhere; its rooted
  `O_NOFOLLOW` opener does not, and lives behind a small
  platform-guarded helper rather than pretending the package as a whole
  is portable.

## Phases

**Phase 1 — The axes.** The migration, the type split, the rename of the
watched vocabulary, `add-library` with `-source`/`-storage`/`-refresh`
flags, and the three axes shown on the admin panel's library list. The panel's
create form still makes managed libraries only — a root-backed library
names a path, and ADR-0013 keeps that out of the browser. Pure
refactor: the suite stays green throughout and no behaviour
changes.

**Phase 2 — In place.** `content_sha256`, `content_size_bytes` and
`storage` on `book_files`; the read path taking a `BookFile` and
checking the descriptor it opened; revalidating cache headers for
in-place responses; a single-descriptor scan path that hashes, validates
and extracts without staging and commits without a blob, reservation or
quota check; the availability statements rewritten to a left join with a
per-storage presence rule; and the never-touch-their-files invariant
asserted after ingest, deletion, trash purge and collection. `storage`
accepts `in_place` only from this phase on.

**Phase 3 — Refresh.** The scheduler with a database lease, the three
timestamps, bounded failure codes, "Refresh now", `refresh-library`, and
aggregate refresh health on the maintenance page.

**Phase 4 — Calibre.** The `calibre` metadata source and its rank; the
`library_calibre_books` mapping; `internal/calibre` reading `metadata.db`
read-only with a `metadata.opf` fallback; the refresh that gates on an
hashed inventory read, resolves changed books by
id, feeds the precedence engine including explicit clears, takes covers
from the tree under a cover-revision cache key, and deletes what Calibre
no longer has. `source` accepts `calibre` only from this phase on.

**Phase 5 — Documentation.** `DESIGN.md`, `docs/deployment.md`,
`README.md`, `docs/openapi.yaml` if the library shape on the wire grows
the axes, the ADR-0002 amendment, and this record flipped to Accepted.

## Acceptance criteria

- Scanning an in-place library writes nothing under
  `<data_dir>/content/sha256` and charges no quota, asserted directly.
- Nothing under a library root is ever written, renamed or unlinked:
  asserted after ingest, after deleting a book, after a trash purge and
  after orphan collection.
- An in-place file whose size or mtime no longer matches the recorded
  snapshot is refused rather than served, and lands in the review queue;
  so does a path that has become a symlink, a directory or a FIFO.
- An in-place download never carries an `immutable` cache directive or a
  strong validator, and a managed download still carries both.
- An uploaded book and an in-place book that share a digest survive each
  other's deletion, on both backends.
- Backup verification, blob reconciliation and orphan collection ignore
  in-place files entirely; no in-place book is ever marked missing by
  the CAS walk.
- An in-place book whose file is deleted becomes unavailable, and
  becomes available again when it returns.
- A field edited in the web UI survives every subsequent Calibre
  refresh; a field not edited here follows Calibre, including on the
  second and third refresh.
- A refresh whose `metadata.db` has not changed performs no catalog
  writes, and a change committed to the WAL alone is still seen.
- A value cleared in Calibre is cleared here; a value cleared in Calibre
  on a field edited here is not.
- Two Calibre books sharing one EPUB with different `cover.jpg` files
  each render their own cover.
- Two `refresh-library` runs against one library, or a run racing the
  scheduler, do not both refresh it; a lease left behind by a killed
  process expires; and a worker whose lease was taken over performs no
  further writes and can neither record completion nor advance the
  inventory digest, whatever it had already committed.
- A `metadata.db` that *is* a symlink is refused before SQLite sees it,
  and a database that resolves to a different file than the one checked
  yields no rows. Neither test races the check, because a root mutated
  concurrently by a hostile local user is out of scope by decision.
- A field cleared in Calibre stays cleared across a subsequent EPUB
  re-extraction and a subsequent provider lookup.
- An in-place ingest interrupted before its commit leaves no catalog
  row, is not reported as a lost artifact, and is completed by the next
  sweep.
- A failing refresh retries on its interval rather than continuously,
  and does not advance the last-success timestamp.
- A book deleted in Calibre leaves the catalog, and the reading history
  for its work is still readable afterwards.
- A Calibre book that is renamed, re-converted, given a second format or
  stripped of its EPUB keeps its catalog row, with the file rows and
  availability each case implies, and never becomes a second book.
- A refresh interrupted halfway leaves a consistent catalog and is
  completed by the next run; a worker whose lease was taken over does
  not advance the inventory digest.
- A `metadata.db` that is missing, truncated, or from an unexpected
  Calibre version fails one library's refresh with a recorded error and
  does not stop the scheduler.
- A library migrated from the old `watched` kind behaves identically
  afterwards, asserted by a store test.
- No page under `/ui` accepts or renders a filesystem path, and no
  refresh error string reaches the browser; the ADR-0013 test that
  plants a path and a DSN is extended to cover the new pages.
- Every route and CLI subcommand added here is covered by the existing
  scope, CSRF and transport matrices.
- The SQLite rebuilds preserve every index and constraint, and
  `PRAGMA foreign_key_check` passes inside the migration.
- Both backends pass the shared store suite, PostgreSQL included.
