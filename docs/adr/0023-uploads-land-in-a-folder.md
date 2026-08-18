# ADR-0023: An upload is a file written into a folder

- **Status:** Accepted
- **Date:** 2026-08-18
- **Depends on:** [ADR-0017](0017-folders-not-pipelines.md),
  [ADR-0022](0022-calibre-metadata-db-is-authoritative.md),
  [ADR-0018](0018-series-overrides.md)
- **Amends:** rule 3 of [ADR-0017](0017-folders-not-pipelines.md);
  reinstates phase C of [ADR-0008](0008-liseur-android-client.md)

## Context

[ADR-0017](0017-folders-not-pipelines.md) removed uploading, and it was
right to. What it removed was an *ingest pipeline*: a staging area with a
global byte cap, a per-user quota, a content-addressed store, a persisted
five-state job, three worker goroutines, an abandoned-upload sweep, an
orphan collector, a thirty-day trash and a review queue. All of that
existed to answer "how does the server take custody of bytes it did not
have?", and the answer that mattered turned out to be "it does not; point
it at a folder".

But a reader with a phone has bytes the server does not have, and no
folder to put them in. They buy a DRM-free EPUB on the train; the file is
on the phone. The catalog does not list it, the tablet cannot download
it, and its reading position has nowhere to sync to, because a position
syncs against a work and a work is resolved from a book. Their recourse
today is to remember, later, from a desktop, to copy the file into a
watched folder by hand — and until they do, finishing the book anywhere
else means copying the file there too.

That is one file and one directory. It does not need a pipeline. It needs
the last two lines of one: *put the file in the folder, then run a pass.*

The obstacle is rule 3 — **"The server never writes under a watched
folder."** That rule is load bearing and this ADR does not repeal it. It
exists because a folder root is somebody else's library, mounted for the
server's convenience, and a bug in a scanner must be incapable of
damaging it. ADR-0017 itself named the way out:

> Two-way Calibre synchronisation, uploads expressed as "write a file
> into a watched folder", and any form of metadata editing are
> deliberately out of scope. None of them gets a hook, a seam or a config
> key here; the ADR that introduces one can introduce its own.

This is that ADR, and this is that seam.

## Decision

### A folder says whether it accepts uploads

`folders` gains one column:

```sql
ALTER TABLE folders ADD COLUMN accepts_uploads BOOLEAN NOT NULL DEFAULT 0;
```

It is false unless an administrator sets it, and only an administrator
can, because a folder is the only place a filesystem path appears and
this is a decision about a filesystem path. `GET /v1/folders` reports it,
so a client offers the action exactly where it applies rather than
offering it everywhere and failing most of the time.

This is the amendment to rule 3, stated in full so that nothing has to be
inferred from it:

> **The server writes under a folder only where an administrator marked
> that folder as accepting uploads, and then only by creating a file that
> was not there.** It does not modify a file, it does not delete one, it
> does not rename one that exists, and it writes nothing at all under a
> folder that did not opt in. Every other guarantee of rule 3 — rooted
> opens, refused symlinks, no temporary files under the root, no cover
> extracted beside the book — is unchanged.

The single-flag alternative was a `[content] upload_root` config key.
It is fewer moving parts and it was rejected: it cannot say "this Calibre
library takes uploads and that mounted archive does not", and it invents
a second, weaker notion of folder standing beside the `folders` table
that ADR-0017 spent 5,700 lines arriving at. Making every plain folder
writable by an upload-scoped token was rejected for the opposite reason —
a read-only archive mounted for convenience would silently become
writable, which is the accident rule 3 was written to prevent.

### An upload has its own scope

`library-upload`. [ADR-0018](0018-series-overrides.md) brought
`library-manage` back with a deliberately narrow meaning — "it grants
series claims and nothing else" — and putting a write-to-disk power
inside it would contradict that sentence rather than extend it. A reader
who may tidy their own series is not thereby a reader who may put files
on the server's disk, and one token should not be the answer to both
questions.

The scope is not admin-only. Uploading is the reader's own book reaching
their own library; requiring an administrator would leave the phone in
exactly the position this ADR exists to fix.

### The catalog is still written by a pass, and only by a pass

`POST /v1/folders/{folder}/books` takes a `multipart/form-data` body and
**never touches a catalog table.** It writes a file and asks for a
reconcile. `ReconcileFolder` remains the single write path into the
catalog, which is what keeps an upload from being a second, subtly
different way for a book to come into existence — the failure mode the
old pipeline had, where an uploaded book and a scanned book were
different kinds of thing forever after.

The sequence, and each step is there for a reason:

1. **Stream to a temp file under `content.cache_dir`**, never under a
   folder root. A half-received upload is therefore never visible to a
   pass, and an abandoned one is never a catalog row — which is the
   entire job the old abandoned-upload sweep existed to do, achieved by
   writing somewhere else instead of by a background worker.
2. **Validate and hash in the same pass over the bytes.** `epub.Validate`
   under the existing `epub_max_*` bounds; the digest falls out of the
   same read. A body larger than `content.max_upload_bytes` is refused by
   `http.MaxBytesReader` before it is stored.
3. **A digest the catalog already holds is not a second book.** The
   response is `200` with the existing `book_id` and `duplicate: true`,
   and the temp file is discarded. This is what makes a retry — from a
   flaky train connection, from a WorkManager backoff — cost nothing and
   produce nothing. The client needs no idempotency key because the bytes
   are the idempotency key.
4. **Rename into place, create-only.** The relative path is derived from
   the publication's own author and title, sanitised, with a numeric
   suffix if it is taken. `link(2)` then `unlink(2)` rather than
   `rename(2)`, because `link` fails when the target exists and `rename`
   silently replaces it, and "never modify a file that was there" is a
   guarantee better held by the kernel than by a stat.
5. **Reconcile that one folder, synchronously**, and read the book back.

The response carries `book_id`, and the client needs it: the book on the
phone becomes the download of the server's copy, which it cannot do
without knowing which copy.

**A pass that concluded nothing must not fail an upload that succeeded.**
Rules 1 and 2 of ADR-0017 mean the reconcile may legitimately decide
nothing, and the file is on the disk either way. So the handler answers
`201` with the book found by path, or failing that by digest; and if
neither finds it, `202` with no `book_id` — the bytes are safe, the
watcher will catch them, and the client resolves the book later rather
than being told its upload failed when it did not.

### A Calibre folder is written the way Calibre writes

A Calibre library is not a directory of EPUBs that happens to have a
database in it. [ADR-0022](0022-calibre-metadata-db-is-authoritative.md)
made `metadata.db` authoritative here: discovery comes from the database,
and a book a complete pass does not find in it is *deleted*. A file
dropped into a Calibre library's tree is therefore not an upload at all.
It is litter. The pass will not see it, the curator will not see it, and
the next Calibre operation may move a directory out from under it.

So an upload into a folder of kind `calibre` writes a Calibre book:
Calibre's directory layout, Calibre's `metadata.db` rows, `cover.jpg` and
`metadata.opf` beside the publication.

This is a real cost and it is worth naming rather than burying. Until
now, `calibre.Library` opened `metadata.db` with `mode=ro` and
`query_only(1)`, carrying a comment saying a bug there should fail loudly
rather than modify a library we promised not to touch. That promise is
narrowed here, to folders that opted in, and the writing is deliberately
kept out of that type: **`calibre.Writer` is a separate type with its own
connection.** The reading path keeps its read-only connection and its
guarantee unchanged, so a bug in a pass still cannot write, and only code
that named itself a writer can.

Three things make this harder than an `INSERT`, and all three are
requirements rather than details:

- **Calibre's triggers call functions Calibre supplies.** `books` has an
  insert trigger that sets `sort` from `title_sort(NEW.title)` and `uuid`
  from `uuid4()`. Those are Python functions Calibre registers on its own
  connection at runtime, not SQLite builtins, so a third-party insert
  fails with *no such function* against a real library while passing
  happily against any fixture that omits the triggers. The writer
  registers both, and the test fixture grows the triggers, or the problem
  is invisible until it is in production.
- **The book's directory contains its own id**, which does not exist
  until the row is inserted. The order is: insert the row, read the id
  back, compute `Author/Title (id)`, create the directory, write the
  file, insert the `data` row, then `UPDATE books SET path`.
- **A half-written Calibre book is not a recoverable state.** Under
  ADR-0022 a file with no row is invisible forever, and a row with no
  file is an unservable book the pass will keep re-observing. So the
  write is all-or-nothing: one transaction, and on any failure the
  transaction rolls back *and* the directories are removed — but only
  the ones this code created. An author's directory is shared, so
  finding it already there is normal and it is removed only if it is
  empty again afterwards.

"Only creates" means the same thing here as it does for a plain folder,
and is enforced the same way. Every path goes through `os.Root`, so a
symlink cannot redirect a write — or a rollback's delete — outside the
library. The book's directory is created with `Mkdir` rather than
`MkdirAll`, and the publication, cover and OPF with `O_EXCL` and
`O_NOFOLLOW`: a book directory that already exists is something this
code did not make, so the upload is refused rather than written into.
Overwriting it would destroy a book that was already there, which is
the one outcome ADR-0017 rule 3 exists to prevent.

**A library that is also somebody's open Calibre session is not
supported, and cannot be.** Calibre holds `metadata.db` in memory and
writes its cache back, so a row inserted underneath it can be lost. A
write lock that does not clear within the busy timeout is answered with
`409` — but this is worth stating plainly rather than overselling: an
*idle* Calibre holds no lock, so that check catches Calibre mid-write
and nothing else.

There is no fix available on this side. Calibre offers no lock to take
and no protocol to ask, and the only reliable answer — refusing to serve
a library any desktop Calibre might open — is a deployment rule, not
code. It is the rule a server-side library already implies. The `409` is
therefore a courtesy for the case it can detect, and this paragraph is
the honest statement of the case it cannot.

The rejected alternative is worth recording because it is the fallback if
the above proves unsafe in the field: uploads to a Calibre folder could
write into a plain sibling folder instead, leaving the curator to add the
book to Calibre themselves. It is safe and it is honest, and it was
rejected because it produces exactly the outcome this ADR exists to
remove — a book on the server that the reader must later go and file by
hand.

### What this does not reintroduce

No content-addressed store, no quota, no staging cap, no ingest job
table, no worker goroutine, no trash, no orphan collection, no review
queue, no duplicate detection beyond the digest check that costs one
indexed lookup. If any of them starts to look necessary, that is evidence
this ADR was wrong, not that ADR-0017 was.

Metadata editing stays out of scope. An upload states what the
publication says about itself; correcting that is a different ADR.

## Consequences

`compose.yaml` has advised mounting book trees `:ro`, on the grounds that
the server opens files `O_RDONLY` and `metadata.db` with `mode=ro`, so
the mount costs nothing and makes a bug incapable of data loss. That is
no longer free: a read-only mount is now the choice to refuse uploads.
The mounts become read-write, and the comment explains the trade rather
than deleting the old advice, because for a folder nobody uploads to the
old advice is still the better one.

`docs/deployment.md`, `docs/DESIGN.md` and `README.md` each state
somewhere that a folder is read-only from the server's point of view.
Each becomes conditional.

On the client side, phase C of [ADR-0008](0008-liseur-android-client.md)
is reinstated: the `can_upload` capability, the upload action and its
WorkManager job, deleted rather than implemented when ADR-0017 landed,
are built. A liseur-sync that predates this ADR simply reports no
`library-upload` scope and the action stays hidden — the capability model
working as designed, with no version check anywhere.

An older client against a newer server is unaffected: nothing existing
changes shape.

## Implementation phases

1. **The seam.** The `library-upload` scope, the `accepts_uploads`
   column by migration in both backends, the field on `store.Folder` and
   its writes, the flag in `GET /v1/folders`, and the toggle in the admin
   folder UI.
2. **Plain folders.** The route, the temp-then-validate-then-link
   discipline, the digest short-circuit, the synchronous reconcile, and
   the handler tests.
3. **Calibre folders.** `calibre.Writer`, the registered trigger
   functions, the layout, the all-or-nothing transaction, the lock
   refusal, and a fixture that has the triggers a real library has.
4. **The web UI.** An upload form on the library page, shown only where
   the selected folder accepts uploads.

   Two things about it were settled while building it. The browser gets
   at the rules through the same code the JSON route runs — the API's
   `ReceiveUpload`, reached through an interface the way downloads and
   covers already are — so there is one answer to "what may be written
   into a folder" and not a second one that drifts.

   And the form is for any signed-in reader, not for administrators
   alone. The gate is the folder, which an administrator marked, plus a
   session; a per-user upload right would be a third permission model
   beside scopes and the admin flag, for a decision that has already
   been made once. The bound on what that costs is `max_upload_bytes`.

   The CSRF token travels in the query string rather than as a hidden
   field, because reading a form field parses the multipart body, and
   the check that decides whether to accept an upload must not be the
   thing that buffers it first.
5. **Documentation and deployment.** `openapi.yaml`, `AGENTS.md`,
   `DESIGN.md`, `deployment.md`, `README.md`, `compose.yaml`.

## Acceptance criteria

- A folder that did not opt in refuses an upload, and no byte is written
  under it.
- An upload with a scope-less or `library-manage`-only token is refused.
- The same file uploaded twice yields one book, and the second call
  answers with the first book's id.
- A body over the size bound, a file that is not an EPUB, and an EPUB
  that fails the bounded parser are each refused without leaving a temp
  file behind.
- An upload never overwrites a file that was already at the target path.
- A snapshot of a folder that does not accept uploads — names, sizes,
  mtimes, and the mtime of the root itself — is unchanged after an upload
  to a folder that does.
- A book uploaded into a Calibre library is visible in Calibre itself,
  with its title, author, cover and format where Calibre expects them.
- A failed Calibre upload leaves neither a row nor a directory.
- An upload during a reconcile that reports itself incomplete still
  answers success, and the book appears once.
- A client that uploads a book it holds locally can afterwards download
  that book from another device and resume the first device's position.
