# ADR-0024: Deleting is a reader forgetting, or an administrator retiring

- **Status:** Accepted
- **Date:** 2026-08-18
- **Depends on:** [ADR-0017](0017-folders-not-pipelines.md),
  [ADR-0022](0022-calibre-metadata-db-is-authoritative.md)
- **Amends:** the append-only rule for ops and sessions, for one case

## Context

The library page shows two kinds of dead entry, and neither had a way
out.

The first is a work no catalog book backs: reading that arrived from a
device holding a file this server has never seen, or held once and no
longer lists. It renders as a text tile marked "synced elsewhere", and
it stays there for good — a book finished on a Kobo two years ago, a
book tried for ten pages, a file whose hash changed when it was
re-downloaded and which therefore resolved to a second work beside the
first.

The second is a catalog row whose file is gone. A plain folder keeps a
missing book on purpose: absence is evidence about a disk, not a
decision about a book, and the row is what lets the reading position
survive an unplugged drive. But a book that is genuinely not coming
back — deleted on purpose, moved to another library, a folder
reorganised around it — leaves a row nothing will ever collect, because
the collector only runs where a pass observed the folder as complete and
the book as absent, and a plain folder never purges at all.

The two look alike on the page and are nothing alike underneath. One is
per-user reading state whose only owner is the reader. The other is a
shared catalog row nobody owns and only an administrator manages.

The obstacle is the invariant: **the op log and sessions are append-only
within their retention windows.** That rule is about *history not being
edited* — a position that was reported was reported, a session that
happened happened, and no later write silently rewrites either. It was
never a promise that a reader cannot ask this server to forget a book.

## Decision

### A reader deletes a work; the unit is the whole work

`DeleteWork(userID, workID)` removes one work and everything that hangs
off it — editions, aliases, ops, sessions, supersessions, rollups, the
book mapping — in one transaction, by cascade.

It is the whole work or nothing. There is no deleting one op, one
session or one day: that would be editing history, which is what
append-only forbids and what this does not do. Forgetting a book is a
different act from rewriting what happened while reading it.

### Only a work no catalog book backs

`DeleteWork` refuses, with `ErrInvalidInput`, a work any
`user_book_works` row still maps. A mapping cannot outlive its book —
the composite foreign key cascades it — so a mapping means this server
still lists the book, whether or not the last pass could find the file.

This is the load-bearing half of the decision. A missing book is the
common case of a disk that is not where it was, and the row exists
precisely so the reading survives until the disk comes back. A delete
offered there would be a button that trades a permanent loss for a
temporary absence. On the library page such a work looks bookless — it
already renders as a text tile, because a cover that resolves to 410 is
worse than a tile — so the page distinguishes *bookless* from *book
missing* and offers the control only for the first.

The way to delete the reading of a book whose file is gone for good is
therefore two decisions by two people: an administrator retires the
catalog row, and then each reader may forget what they read.

### An administrator retires a missing catalog row

`DeleteMissingBook(bookID)` deletes a book already marked missing, and
then runs exactly what a pass that dropped a book runs: collect the
works nothing maps and nobody has read, then collect the entities no
book names any more (ADR-0019). An active book is `ErrInvalidInput` —
the next pass would re-add it, so deleting one would be theatre.

Readers' works are not deleted with it. A work with reading behind it
survives as a bookless work, which is the entry only its own reader can
remove. Nothing under the folder root is touched: those files were never
this server's to delete (ADR-0017).

In a Calibre folder the control is not offered and the handler refuses
it, because `metadata.db` is authoritative there (ADR-0022): a book it
still lists is put back by the next pass, and an unservable book is
marked missing on purpose.

### Deletion is local and has no tombstone

Nothing records that a work was deleted. `seq` is not renumbered, no
delete record enters the op log, and a delta sync has no way to say a
work is gone, so other devices never learn of the deletion at all.

A replay does not resurrect the deleted work itself: an op or a session
naming a work id this account no longer has is refused as an unknown
work, and the alias that named it went with it. What happens instead is
that a device still holding the book resolves it again, gets a *new*
work, and syncs into that — so the book returns, with its history
starting from what the device sends now.

That is accepted rather than overlooked. The alternative is a per-user
graveyard of blocked aliases, which turns "forget this book" into "never
sync this book again" — a different and worse promise, and one nothing
in the UI would be able to undo later. The UI says what actually
happens: *a device still syncing this book will send it back.*

## Consequences

- The first user-initiated destruction of reading state in this server.
  Every other path that removes ops or sessions is a scheduled window
  (compaction, retention), and they remain the only ones.
- `session_tombstones` is untouched by a delete, so a session that was
  already compacted stays deduplicated by its fingerprint. A session
  still held in full goes with the work and leaves no tombstone; sending
  it again is refused as an unknown work rather than silently restored.
- The library page needs to know the difference between a work with no
  book and a work whose book is missing, which is one extra answer from
  a lookup it already does.
- Retiring a plain folder's missing book is the only way a plain folder
  ever loses a row without losing its folder.

## Acceptance criteria

- Deleting a work removes its ops, sessions, rollups, editions, aliases
  and mapping, and one reader can never delete another's work.
- A work any catalog book maps is refused, missing file or not, in the
  store as well as in the page.
- Deleting a missing book collects the works and entities a pass would
  have collected, keeps works with reading behind them, and refuses an
  active book.
- The delete control appears on a bookless work and on an admin's view
  of a missing book in a plain folder, and nowhere else.
