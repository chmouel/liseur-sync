# ADR-0015: Catalog payloads clients can walk

- **Status:** Proposed
- **Date:** 2026-08-15
- **Depends on:** [ADR-0004](0004-metadata-and-categorization.md),
  [ADR-0006](0006-catalog-api-and-opds.md),
  [ADR-0014](0014-library-sources-and-storage.md)

## Context

ADR-0006 shipped a catalog list payload sized for a list of titles:
`book_id`, `library_id`, `title`, `status`, timestamps, a cover URL and the
optional scalar metadata. That is everything a book *has by itself* and
nothing a book *is related to*.

A client displaying a library needs more than that on every row. Liseur
Android and Liseur Desktop (ADR-0008, ADR-0009) show an author under every
title, group by series, and match books they already hold against the
catalog by digest. None of those three facts is in a list payload, so the
only way to render one page is a detail or metadata request per book. A
catalog walk becomes N+1 requests, and the fields that make a shelf legible
are the ones that cost the most to obtain.

Two smaller holes have the same shape. A file's size is known at ingest but
never returned, so a download UI cannot say how large the download is. And
the digest that *is* returned, in `files[].sha256` of `GET /v1/books/{id}`,
is `BlobSHA256` — the address of the server's own copy in content-addressed
storage. ADR-0014 gave libraries in-place storage, where the server has no
copy and that field is empty. A client matching local books to catalog books
by digest therefore gets nothing for exactly the libraries a migrating user
is most likely to have pointed at their existing files.

## Decision

### One book shape, everywhere

There is a single JSON representation of a catalog book. Every route that
returns books returns it: `GET /v1/libraries/{library}/books`,
`GET /v1/libraries/{library}/search`, the ADR-0004 entity book listings, the
duplicate groups, and `GET /v1/books/{id}`. Detail is the same shape, not a
richer one.

A client that must parse two shapes for one concept will get one of them
wrong, and the one it gets wrong will be the one that appears only in the
listing it renders a thousand times.

The shape gains:

- `contributors`: the book's contributors as `{id, name, role}`, in the
  catalog's stored order. Roles are already normalized (ADR-0004), so a
  client selects authors by `role == "author"` rather than by guessing.
- `series`: the book's series as `{id, name, position}`, `position` omitted
  when unknown. Not a single series: the catalog has always allowed several,
  and a payload that reports one of them silently picks a winner.
- `files`: the available files as `{file_id, media_type, filename, sha256,
  size_bytes}`.

`sha256` is the file's **content** digest — what the bytes are — and is
present for every file whoever owns the bytes. It is not the blob address.
The blob address is a storage detail of the server's own copy and is not in
the API at all; nothing outside the quota, garbage-collection and
reconciliation paths has any business with it.

`size_bytes` is the length those bytes had when the server last read them.
For an in-place file that is a fact about the file the last time it was
seen, not a promise about the file now — the download is still where the
truth is.

### Complete sets, not summaries

The listing carries every contributor and every series row, not just the
authors and not just the first series. A summary field is a decision made on
the client's behalf by the server, and the two clients we have already
disagree about which summary they want. The cost is bounded by the data:
books have a handful of contributors, not a thousand.

Files are the available ones only — the same filter `GET /v1/books/{id}`
already applies. A superseded or missing file is not something a client can
download, and listing it invites a client to try.

### Unconditional, not `?include=`

The new fields are always present. There is no `?include=contributors,series`
parameter.

An opt-in parameter is a second response shape: two paths to test, two
shapes to keep in `docs/openapi.yaml`, and a permanent question about which
one a bug report came from. Every client we know of would pass the parameter
on every request, which makes the cheap default a shape nobody asks for. If
a future caller genuinely needs a title-only listing, that is a decision to
take then, with a real caller to design against.

### The cost moves into the store, and must not become an N+1 there

Enriching a page must not be a per-book metadata read in a loop. The store
gains batch reads that fetch the contributors, series and files for a page
of book IDs in one query per kind, and the handlers use them. A page of
books is a bounded number of rows; a page of books is not a bounded number
of round trips.

`CatalogBookMetadata` stays as it is. It answers a different question — one
book's complete metadata, including identifiers, languages, tags and genres,
read in one transaction for the precedence engine — and the listing is not
that question.

### What does not change

- No reading state in catalog payloads. Position, completion and
  work mapping remain sync-scoped fields (ADR-0006); nothing added here is
  derived from another user's data or from the caller's reading history.
- Every field is subject to the same library ACL as the book that carries
  it. Contributors and series are library-scoped entities already.
- OPDS gains nothing. It is catalog-only by ADR-0006, and series and
  contributors already reach OPDS clients as facets and navigation feeds.
- The cursor stays opaque and the ordering stays as it is. This changes what
  a page contains, not how pages are cut.

Because the server has never shipped, the old payload is replaced outright:
no `include` shim, no versioned response, no deprecation window.

## Consequences

Pages get larger. A listing that used to be a title and a cover URL now
carries the relationships a shelf is drawn from, which is the point; the
alternative was the client fetching the same data one book at a time over
the same connection.

`docs/openapi.yaml` gains one book schema referenced from every book-bearing
response, replacing the per-route inline shapes. That is the contract change
this ADR is really making: not the fields, but the fact that there is one
shape to change next time.

Fixing `sha256` to the content digest changes an existing field's meaning.
Nothing depends on the old meaning — the field is empty for in-place files
and no shipped client reads it — but it is a semantic change, not an
addition, and the tests must say which digest they expect and why.

## Implementation phases

1. **Store batch reads.** Contributors, series and files for a set of book
   IDs within one user's ACL, one query per kind, shared by every listing
   handler. Covered by the shared `internal/store/storetest` suite on both
   backends.
2. **One payload builder.** Replace `catalogBookJSON` with a builder that
   takes a book and its batched relations, and route every book-bearing
   handler through it — list, search, entity listings, duplicates, detail.
   Detail stops assembling its own `files` array.
3. **Digest and size correctness.** `sha256` becomes the content digest and
   `size_bytes` appears, with tests for an in-place library, where the blob
   digest is empty and the content digest is not.
4. **Contract.** `docs/openapi.yaml` gains the shared book schema and
   `docs/integrating.md` describes matching local books to catalog books by
   content digest, in the same commit as the code.

## Acceptance criteria

- One page of a library returns authors, series and per-file digest and size
  with no further requests, and the same page issues a bounded number of
  store queries regardless of page size.
- The book shape returned by list, search, entity listings and detail is
  byte-identical for the same book.
- A book in an in-place library reports a non-empty `sha256`, and no route
  anywhere returns the blob digest.
- Books with no contributors, no series or no available files still render:
  the fields are present and empty rather than absent.
- Catalog payloads still carry no reading state, and a `library-read`-only
  token sees exactly what a multi-scope token sees on these routes.
- OPDS feeds are unchanged.
