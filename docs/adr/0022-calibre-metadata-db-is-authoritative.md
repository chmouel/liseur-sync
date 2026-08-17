# ADR-0022: A Calibre library's `metadata.db` is authoritative

- **Status:** Accepted
- **Date:** 2026-08-17
- **Depends on:** [ADR-0017](0017-folders-not-pipelines.md),
  [ADR-0003](0003-catalog-work-identity.md)

## Context

[ADR-0017](0017-folders-not-pipelines.md) says a book a pass did not
observe is marked `missing` and kept, because the file usually comes
back. That is the right reading of a disk: a share that is slow to
mount, a copy that is half finished, a drive that is not plugged in.
None of those are evidence that anybody deleted anything.

It is the wrong reading of a Calibre library. There the pass does not
read a disk; it reads `metadata.db`, a catalog somebody curates by hand.
A book that is not in it was removed from it, deliberately, by the
person whose library it is. Keeping the row means:

- the row is invisible in every catalog listing, which filters on
  `status = 'active'`, so nobody can see it, find it or delete it;
- but `user_book_works` still points at it, so the reader's work is a
  work whose only book is unservable — and the library page draws
  exactly that as a text tile marked *synced elsewhere*;
- and nothing ever deletes it, because nothing in this server ever
  deleted a missing book. The tile is permanent.

A curator who tidies their Calibre library therefore accumulates a
permanent shadow shelf of books they deleted on purpose.

The second half of the same problem is not deletion at all. Calibre
rewrites a publication whenever it embeds edited metadata, so a title
edit changes the file's bytes. The book keeps its `calibre_id` and its
row (ADR-0017 rule 4 is explicit that in a Calibre folder the curator's
database, not the bytes, is the identity) — but the reader's work was
resolved from a digest that no longer exists anywhere, and the
title/author fingerprint moved with the title. The next device sync
matches nothing and mints a **second** work, which draws as a *synced
elsewhere* tile beside the very book it belongs to.

## Decision

**In a Calibre folder, a book a complete pass did not find in
`metadata.db` is deleted, not flagged.** The row goes, and with it the
relations and the `user_book_works` mappings that hung off it, in the
same transaction, by cascade.

**Everywhere else nothing changes.** A plain folder is a disk, absence
is evidence about a disk, and the book is marked `missing` and kept.

**The two guards in front of the flag are the same two guards in front
of the deletion.** An incomplete pass and a zero-observation pass
conclude nothing, exactly as rules 1 and 2 of ADR-0017 require, and for
the same reason: this is a bigger conclusion than the old one, not a
smaller one.

**A book `metadata.db` still lists but that has no servable file is
observed, not omitted.** The pass reports it as `Unservable` — a book
converted to a format this server does not read, or one whose file is
simply not on the disk — and the store marks it `missing` and keeps it,
row, relations, mappings and all. Only absence *from the catalog* is
deletion. This also stops a single vanished file declaring a whole pass
blind, which under the old code meant no book in that library could ever
be judged again.

**Empty established works are collected; a work with reading behind it
is not.** After a complete, non-empty Calibre pass, every non-pending work
with no book mapping, ops, sessions or rollups is deleted. The sweep is
deliberately broader than the books that pass removed, so it also drains
orphans left by older reconciliation and replacement behavior. Anything
with a single op survives its file, as a work with no book on this server
— which is what *synced elsewhere* is supposed to mean. Pending sync
works are never candidates.

**A book whose bytes changed keeps its readers, and its readers learn
the new digest.** When a Calibre book's `content_sha256` changes, every
work mapped to that book gains an alias and an edition for the new
digest, plus an alias for the new title/author fingerprint. Registration
is **additive**: the old digest still names the same work, so a device
that has not re-synced is unaffected and no op or session has to be
re-pointed at a different edition.

**A digest another work already claims is left alone.** Two works
meeting over one digest is a merge, a merge has reading history on both
sides, and a scan does not make that decision on the reader's behalf.
`POST /v1/works/merge` exists for when they want it.

## Consequences

- A curator's deletions take effect. The shadow shelf drains itself on
  the next pass.
- Reading history is never deleted by a scan. The most a scan does is
  delete a non-pending, unmapped work that had none.
- A metadata edit in Calibre no longer forks a reader's work in two.
  Duplicates already in a database are not healed by this — nothing
  links a work carrying the old title to a book that now has a new one —
  and stay a manual merge.
- `ReconcileResult` grows `Purged` and `Rekeyed`, which is what makes
  both effects visible in the scan log rather than silent.
- The asymmetry between folder kinds is now load-bearing in a second
  place. It is the same asymmetry ADR-0017 already made for identity —
  path for a disk, `calibre_id` for a catalog — applied to absence.
