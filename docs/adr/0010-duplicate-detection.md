# ADR-0010: Duplicate detection

- **Status:** Accepted
- **Date:** 2026-08-13
- **Depends on:** [ADR-0005](0005-upload-and-ingestion.md)

## Context

Content is deduplicated by digest, so uploading the same file twice costs
no extra storage. ADR-0005 decided that a second upload may still create a
second catalog entry, on the grounds that a user may mean it — the same
file filed under two books is a thing people do on purpose.

What that decision left out is that the user is never told. Importing a
download folder that accumulated two copies of the same book produces two
identical-looking rows and no explanation, which reads as a bug in the
importer rather than a faithful record of what was uploaded.

Two entries can be duplicates in two quite different ways:

- **Same bytes.** One digest, two books. Certain, cheap to find, and
  impossible to argue with.
- **Same book, different bytes.** Two EPUB builds of one title, or the
  same novel from two shops. Deciding this needs title and author
  normalization and is a judgement call the server can get wrong.

## Decision

Report exact-content duplicates; do not resolve them, and do not guess at
the second kind.

`GET /v1/libraries/{library}/duplicates` groups a library's active books
by digest and returns the groups holding more than one book. The books
page shows the same groups to librarians. Both are reads: a group is
resolved by deleting an entry, which is an ordinary delete with its own
trash and restore, and no separate merge exists.

Only active books count. A trashed book still references its blob —
that is what makes restore a relink — but reporting it would tell the
user to resolve something they resolved by deleting it.

Grouping happens server-side. The result depends on the digest ordering
of the underlying query, and a client that had to reproduce that rule
would eventually show a book duplicating itself.

Rejected alternatives:

- **Refusing a duplicate upload.** It contradicts ADR-0005, and the file
  is already stored by the time the digest is known.
- **Merging automatically.** Choosing which entry survives destroys
  metadata somebody may have edited, to save one click.
- **Title matching now.** The interesting cases need author and edition
  normalization; a naive match on title alone would group the volumes of
  a series that share a name.

## Consequences

An import of a folder holding the same file twice still produces two
books, and now says so. Storage is unaffected either way.

Similar-but-not-identical duplicates remain undetected and invisible. A
user with two builds of one novel sees two books, which is what they
have.

## Phases

1. **Exact-content detection.** Store query, API route, books page
   section. Implemented.
2. **Similarity detection.** Deferred until there is a normalization rule
   worth defending. It would extend the same route with a weaker kind of
   group, not replace it.

## Acceptance criteria

- Two books holding one digest are reported as a group; a book whose
  bytes nothing else shares is not.
- Deleting one member resolves the group; restoring it brings the group
  back.
- Duplicates never cross a library boundary, even though blobs do.
- A group truncated by the limit is omitted rather than reported with one
  book in it.
