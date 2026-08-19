# ADR-0025: A book may be deleted where a book may be written

- **Status:** Accepted
- **Date:** 2026-08-19
- **Depends on:** [ADR-0017](0017-folders-not-pipelines.md),
  [ADR-0022](0022-calibre-metadata-db-is-authoritative.md),
  [ADR-0023](0023-uploads-land-in-a-folder.md),
  [ADR-0024](0024-deleting-a-work.md)
- **Amends:** ADR-0024, which deliberately left the file alone; and
  ADR-0023's amendment to ADR-0017 rule 3, which allowed one kind of
  write under a folder root and now allows its inverse

## Context

[ADR-0023](0023-uploads-land-in-a-folder.md) gave this server a way to
put a book into a folder. A reader sends one from a phone or from the
browser form, and the bytes land under a root an administrator marked
`accepts_uploads`. Nothing gives it a way back out.

What exists is [ADR-0024](0024-deleting-a-work.md), and it is two
narrower things, neither of which touches a file. A reader deletes their
own *work* — reading, not a book — and only once no catalog book backs
it. An administrator *retires* a catalog row a pass already reported
missing, which is a decision that a file is not coming back.

Both rest on a sentence that was true when it was written: those files
were never this server's to delete
([ADR-0017](0017-folders-not-pipelines.md)). A folder is somebody's
directory, curated with their own tools, and a server that deletes out
of it is a server that has to be trusted with a backup.

Uploads broke the symmetry. A reader who sends the wrong book, or sends
the same book twice under two names, has asked this server to write a
file and has no way to ask it to stop. The recourse is a shell on the
machine — which the phone does not have, and which the browser form's
user may not either. The one place the server *is* the curator is the one
place a reader cannot curate.

## Decision

**A book may be deleted from a folder that accepts uploads, and from no
other folder.** The file goes, and the catalog row with it.

The flag already carries exactly this meaning. `accepts_uploads` is
described in `store.Folder` as "the one place the server is allowed to
write under a folder root", and deleting is a write. A folder without it
is what it always was: read-only to this server, observed by a pass and
never touched. So this needs no new setting and no new decision from an
administrator. The one they already made — *this directory is the
server's to manage* — is the one being honoured.

A book whose file a pass reports **missing** is still not deletable
through this route. Absence is evidence about a disk, not a decision
about a book, and ADR-0024's retire path already covers the case where
somebody has decided otherwise.

### Over the API, a new scope

`library-delete`, beside `library-upload` rather than folded into it.

Uploading is a reader adding their own book. Deleting removes a file
every account on this server can see. They are different questions, and
this is the same reasoning that kept `library-upload` out of
`library-manage`: tidying your own shelves and writing to the server's
disk are not one permission, and neither are adding and destroying.

It is worth being exact about what this scope *is not*. Any account can
mint a token for itself with any non-admin scope, so `library-delete` is
not a privilege boundary between users: it bounds what a **token** may
do, so that a device holding a sync token cannot delete a library by
accident or by compromise. Every account that wants it can have it, and
every holder can delete any book in a writable folder. A server whose
readers should not be able to do that should not mark a folder
`accepts_uploads`.

### In the browser, an administrator

Not the scope, because there is nothing to check it against: a web
`AuthSession` carries an account and a CSRF hash and no capabilities at
all. That is not an oversight but the model — `store.User.IsAdmin` says
it plainly, that a token carries capabilities and the account carries the
role.

So the browser asks the question it can answer, and answers it the way
the sibling control in the same file already does: retiring a missing
book is admin-only, and deleting a live one is no smaller a decision. A
reader who wants to delete from a phone holds a token, and the token
route is the scoped one.

The two surfaces therefore answer *who* differently. That is not a
compromise to apologise for; sessions and tokens genuinely are different
credentials, and pretending a browser login is a scoped token would mean
inventing session scopes for one button.

### The file, and the order it goes in

Removal reuses the discipline `internal/content` already applies to
opening one: a relative path checked, the root opened as a root, a
symlink or a non-regular file refused. A delete is the last place to
join two strings and hope.

Before unlinking, the size and modification time must still match what
the catalog recorded. That is already the change gate a plain folder
uses, so it costs one stat, and with no trash behind this it is the
difference between deleting the book that was described and deleting
whatever replaced it.

**The file first, then the row.** A crash between them leaves a book a
pass will mark missing — a state this server already models, and one an
administrator can retire. The reverse leaves a file with no row, which
the next pass adds straight back, and the reader is told a delete
succeeded that visibly did not.

A **Calibre** folder inverts that, and only it, because it can. There the
library is a transaction: the book's directory is read from
`metadata.db` — authoritative per [ADR-0022](0022-calibre-metadata-db-is-authoritative.md),
and the only place that knows where Calibre has since renamed it to — the
row is deleted, the directory is removed, and the transaction commits.
If the removal fails the delete rolls back, which a plain folder has no
way to do.

Deleting the `books` row is the whole of the database work: Calibre's own
`books_delete_trg` removes the links, formats, comments, identifiers,
annotations, positions, conversion options and plugin data. Enumerating
those here would be a list that drifts out of date the first time Calibre
adds a table.

### The reading is a second question, asked at the same time

ADR-0024 stands: a work is per-user, and only its own reader may remove
it. Deleting a book therefore leaves each reader's reading behind, as a
bookless work that only they can delete.

But the reader doing the deleting is *there*, and has an opinion. So the
delete takes an optional "and forget mine", which removes the caller's
own work and nobody else's. It is offered rather than implied, because
the two are genuinely different decisions — the file is shared, the
reading is yours — and because the answer differs by case: a book
uploaded by mistake wants both, a book being tidied off a full disk while
another device still reads it wants only the first.

One work can back several books; the mapping's key is per user and book.
`DeleteWork` already refuses a work anything still maps, and that refusal
is the guard here too: if another copy of the book remains, the reading
stays with it, and that is an outcome to report rather than an error.

## Consequences

- The first operation in this server that destroys a reader's bytes.
  There is no trash and none is added: ADR-0023 declined one, and adding
  it now would be a store to sweep, expire and account for, to insure
  against a mistake a confirmation already asks about.
- `accepts_uploads` now means more than it did. An administrator who set
  it to let a phone send a book has also allowed that phone to remove
  one. The flag's own comment must say so.
- The API and the browser disagree about who may delete, and always
  will, because they authenticate differently.
- A concurrent pass can still observe a folder between the unlink and the
  commit. The outcome is a book marked missing, which is recoverable and
  already understood, and closing the window would mean a per-folder
  mutation lock this server has no other use for.
- A client paired before this scope existed does not hold it, and cannot
  be granted it silently: widening a token's scopes authenticates with
  the account password, which a client does not keep. Reconnecting is the
  route, and a client should say so rather than hide the feature.

## Acceptance criteria

- A book in a folder that does not accept uploads cannot be deleted, by
  either surface, and the check is made against the folder as it is at
  the moment of deletion.
- A file whose size or modification time no longer matches the catalog is
  refused rather than deleted.
- Deleting a book removes its file and its row, and collects the works
  and entities a pass would have collected.
- A Calibre delete finds the book's directory through `metadata.db`, so a
  directory Calibre renamed since the last pass is still the one removed,
  and a failed removal leaves `metadata.db` unchanged.
- Deleting without asking to forget leaves every reader's work, including
  the caller's. Asking to forget removes the caller's and no other
  reader's, and keeps it when another book still maps it.
- The API route requires `library-delete`; the browser route requires an
  administrator and a CSRF token.
