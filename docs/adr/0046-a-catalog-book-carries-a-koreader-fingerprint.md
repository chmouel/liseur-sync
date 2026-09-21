# ADR-0046: A catalog book carries a KOReader fingerprint

- **Status:** Accepted
- **Date:** 2026-09-02
- **Depends on:** [ADR-0003](0003-catalog-work-identity.md),
  [ADR-0017](0017-folders-not-pipelines.md)

## Context

[ADR-0003](0003-catalog-work-identity.md) gave this server a list of
identifiers a reading work can be recognised by, in order of how much
each one is worth: `sha256`, `partial-md5`, `source`, `dc`, `ta`. The
second of those is KOReader's own document fingerprint, and it is the
only name KOReader, kosync and every server that speaks kosync knows a
file by.

The catalog has never computed it. A folder pass reads a file once,
records its `content_sha256`, its size, its mtime and its metadata, and
that is the whole of what it knows about the bytes. The `partial-md5`
alias exists in the resolver, but the only way a value ever entered the
database was a KOReader client sending one.

That asymmetry has a cost that is already being paid. A reader with
KOReader syncing against this server sends a document fingerprint for a
book the catalog is already serving, from the very file on disk the
catalog read. Nothing matches, so the resolver does the only honest
thing it can and creates a *pending* work: reading with no book behind
it. The book and the reading of it sit in the same database, about the
same file, unable to see each other. Nothing joins them until somebody
notices and merges them by hand.

It has a second cost that is about to be paid. Talking to any other
server that speaks KOReader's protocol means naming a document in the
only vocabulary that protocol has, which is this fingerprint. Without it
there is no sentence to say.

The fingerprint itself is not ours and not a choice. It is
`util.partialMD5` from KOReader: an MD5 over twelve one-kilobyte samples
taken at offsets that double in span, so a large file is named without
being read. The offsets are computed in a language with 32-bit shift
semantics, and the first one overflows to zero, which is how the first
sample lands at the start of the file. That overflow is not a bug to be
corrected; it is the definition. An implementation that computes the
"right" offsets agrees with nobody.

## Decision

**A folder pass computes the KOReader fingerprint of every book it
reads, and the catalog stores it beside the content digest.**

1. `books.partial_md5` is added by migration, `NOT NULL DEFAULT ''`,
   with a partial index over the non-empty values. It is additive:
   nothing an existing deployment could already see changes, and an
   empty value means *not yet known*, never *none*.

2. The pass computes it from the same open file the content digest is
   taken from, through a positioned read so the sequential digest is
   undisturbed. It costs twelve kilobytes of reading on a file already
   being read in full.

3. **A fingerprint is filled where it is missing and never replaced.**
   A pass recognises an unchanged file by its stat and does not reopen
   it, so every book catalogued before this ADR would stay anonymous
   forever if the value could only be taken on the read path. The
   unchanged path therefore fingerprints a book that has none, and only
   a book that has none. An existing deployment heals itself on its next
   pass without an administrator running anything, and a `touch` still
   cannot change what the catalog says about a book.

4. **A book whose bytes changed gets a new fingerprint, because it is a
   new book.** That is already the rule
   ([ADR-0017](0017-folders-not-pipelines.md)): changed bytes at a path
   are a different catalog row, not the same one updated. The
   fingerprint follows the bytes it names.

5. `workident.ForCatalogBook` offers the fingerprint as resolution
   evidence for an active book, below `sha256`. A KOReader digest now
   resolves onto the catalog work for that file.

## Consequences

- A reader syncing KOReader against this server stops accumulating
  pending works for books the catalog already has. This is worth doing
  on its own, independent of anything it enables.
- Twelve samples of a file is not a content digest and can collide.
  KOReader accepts that; so does every server that speaks to it. Any
  code that resolves *by fingerprint alone* must be prepared for more
  than one book to answer, and must refuse rather than guess.
- The fingerprint is a name, not a secret, but it is still a fact about
  a reader's library. It is treated like `content_sha256`: internal
  evidence, not part of a catalog payload.
- The 32-bit overflow has to survive future maintenance. It is pinned by
  a test against a known value and documented where it is written.

## Acceptance criteria

- A pass over a folder records a fingerprint that byte-for-byte matches
  what KOReader computes for the same file.
- A book catalogued without a fingerprint has one after the next pass,
  and the pass does not reopen the file to get anything else.
- A fingerprint already recorded is not recomputed or overwritten by a
  later pass.
- A KOReader client syncing a book already in the catalog appends to the
  catalog work rather than creating a pending one.
- The migration leaves every existing row readable and every existing
  book visible.
