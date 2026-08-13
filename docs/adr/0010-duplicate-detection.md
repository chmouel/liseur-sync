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

Report both kinds, resolve neither, and keep them clearly apart: one is
a finding, the other is a question.

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

The same route also reports the weaker kind: books whose normalized
titles match and which name at least one contributor in common. The
normalization is `store.NormalizeTitle`, which is the folding search
already uses — case, diacritics and punctuation go, nothing else does. A
librarian who found two books with one query therefore sees the same two
books called possible duplicates, and the rule needs no second
vocabulary to explain.

The contributor requirement is what makes it worth reading. Title alone
groups every "Selected Poems" in a library, and a report that is wrong
most of the time is one a librarian learns to ignore, which is worse than
having none. Contributors are compared by entity id rather than by name,
because the library already decided which spellings are one person and
this rule has no business deciding it again. A book naming nobody is
never matched.

Two subtractions:

- Books sharing a digest are dropped from it, because the exact report
  already names them and saying it twice makes the weaker one look
  unreliable.
- Books at *different* recorded positions in one series are different
  volumes that happen to share a name — the failure this ADR named when
  it deferred the feature. An unplaced book is not excluded: nobody said
  where it goes, so nobody said it was a different book.

Deliberately not folded: leading articles and edition words. "The Tower"
and "Tower" stay two books, because stripping articles works in English
and mangles other languages, and "revised" is exactly the difference
somebody may have meant to keep.

Rejected alternatives:

- **Refusing a duplicate upload.** It contradicts ADR-0005, and the file
  is already stored by the time the digest is known.
- **Merging automatically.** Choosing which entry survives destroys
  metadata somebody may have edited, to save one click. This applies
  doubly to the similarity report, which is a guess.
- **Matching on title alone**, for the reason above.

## Consequences

An import of a folder holding the same file twice still produces two
books, and now says so. Storage is unaffected either way.

A user with two builds of one novel is now asked about them, and still
has two books until they say otherwise. The similarity report will miss
pairs whose titles differ by an article or an edition word, which is the
price of a rule that can be explained in one sentence.

## Phases

1. **Exact-content detection.** Store query, API route, books page
   section. Implemented.
2. **Similarity detection.** Implemented. The rule is
   `store.GroupSimilarBooks`, shared by both backends for the same reason
   the search folding is: the two must not disagree about what a
   duplicate is. `GET /v1/libraries/{library}/duplicates` gained a
   `similar` array beside the untouched `duplicates` one, and the books
   page a section that links rather than asserts.

## Acceptance criteria

- Two books holding one digest are reported as a group; a book whose
  bytes nothing else shares is not.
- Deleting one member resolves the group; restoring it brings the group
  back.
- Duplicates never cross a library boundary, even though blobs do.
- A group truncated by the limit is omitted rather than reported with one
  book in it.
- The similarity report never contains a pair the exact report already
  contains, never groups two numbered volumes of one series, and never
  groups books that name no contributor in common.
- Both backends return the same groups for the same library.
