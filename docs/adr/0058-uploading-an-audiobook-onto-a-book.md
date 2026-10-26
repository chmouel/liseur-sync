# ADR-0058: Uploading an audiobook onto a book

- **Status:** Accepted
- **Date:** 2026-10-26
- **Depends on:** [ADR-0023](0023-uploads-land-in-a-folder.md),
  [ADR-0057](0057-native-watched-folder-audiobooks.md)
- **Amends:** the write rule of [ADR-0023](0023-uploads-land-in-a-folder.md);
  the pairing rules of [ADR-0057](0057-native-watched-folder-audiobooks.md)

## Context

ADR-0057 indexes `.m4b` files found in watched folders and pairs them
with books. Getting one there meant copying it by hand, and a plain
folder that keeps its EPUBs side by side at the root had no pairing rule
that would match it: the dedicated-subdirectory rule needs one book per
directory, and title matching is the last resort.

## Decision

**An audiobook is uploaded onto a book, not into a folder.**
`POST /v1/books/{id}/audiobook` (and the details page's form, `POST
/ui/books/{id}/audiobook`) takes one `.m4b` and writes it into the
book's own folder. It is allowed exactly where a book upload is: the
folder has `accepts_uploads`, the caller holds a grant on it, and the
token carries `library-upload`. The body is spooled to
`content.cache_dir`, hashed, and must read as an M4B with a duration
before anything under the folder is touched. The size limit is
`content.max_audiobook_upload_bytes`, 4 GiB by default.

**Where it lands.** In a plain folder the file is `<epub stem>.m4b` in
the EPUB's directory. In a Calibre folder it is a new `M4B` format on the
existing Calibre book: a `data` row and a file in the book's directory,
named like its EPUB. No existing Calibre row is changed, not even
`last_modified`. Either way the folder's pass then indexes the file and
pairs it, so the route writes no catalog row, as in ADR-0023.

**This amends ADR-0023's write rule** by one case: the server may now
create a file beside an existing book, and in Calibre a `data` row on an
existing book. It still only creates. A book that already has an
audiobook, a Calibre book that already has an `M4B`, a target file that
already exists, and bytes already in the caller's visible catalog are
all refused with `409`. Nothing is replaced and there is no idempotent
retry answer: a retry after the first attempt landed sees the
audiobook and gets `409`.

**A new pairing rule** sits second in ADR-0057's list: same folder, same
relative path without the extension, 1:1. It pairs what an upload
writes, and also a file somebody named the same way by hand.

## Consequences

- A folder that accepts uploads must have room for multi-gigabyte files,
  and so must `content.cache_dir`. A reverse proxy must allow bodies as
  large as the limit.
- The stem rule outranks the dedicated-subdirectory rule, so a book and
  an audiobook sharing a name are paired even where that directory holds
  other files.
- Deleting a book (ADR-0025) in a plain folder leaves an audiobook
  written beside it, indexed and unpaired. In a Calibre folder the
  audiobook is a format of that book, so it goes with the book.

## Implementation phases

1. Calibre `AddFormat`, plain-folder placement, the stem rule, the
   upload route and the web form. Done.

## Acceptance criteria

- An upload into a folder without `accepts_uploads`, onto an ungranted
  or missing book, or of a file that is not an M4B, writes nothing.
- A plain-folder upload is paired with its book by the next pass; a
  Calibre upload is paired by `calibre_id`.
- A second upload onto the same book is refused and leaves the first in
  place.
