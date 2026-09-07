# ADR-0035: Naming a shelf is one request

- **Status:** Accepted
- **Date:** 2026-09-07
- **Depends on:** [ADR-0003](0003-catalog-work-identity.md)
- **Amends:** [ADR-0003](0003-catalog-work-identity.md), which gave
  catalog resolution one route for one book. It gets a second shape for
  many, deciding nothing differently.

## Context

Positions and sessions attach to a work, never to a catalog book, so a
book with no work on this server can neither send a position nor receive
one. `POST /v1/books/{id}/resolve` (ADR-0003) gives a book its work, one
book per request.

That is the right shape for the case it was designed for: a reader
downloads a book and the client names it. It is the wrong shape for the
other case, which is every device's first day. A phone signing in to an
existing account has a name here for *none* of its books, so a library of
four hundred needs four hundred requests before anything syncs at all.

Clients therefore ration it, and rationing has a cost the ration was
never meant to pay. Liseur named twenty-five books a run, and a run
happened when the reader pulled to refresh. Each refresh named the next
twenty-five, fetched their history, and put it on the shelf — so the
shelf arrived in batches over several refreshes, each batch landing on
top of the last. That is [chmouel/liseur#180], and while the ordering
half of it was the client's own bug, the batching half is this route's
shape.

A client cannot fix it by asking faster. Four hundred requests is four
hundred round trips against a server that may be a Raspberry Pi behind a
domestic uplink, and the reader is watching.

## Decision

`POST /v1/books/resolve` resolves up to 500 books in one request.

**It decides nothing that the single route does not.** Both go through
`resolveBookWork`, which is the whole of what resolving means: the same
evidence read off the catalog, the same `DecideWorkResolution`, the same
per-user mapping. A batch route that could reach a different answer would
be a second protocol wearing the first one's name.

**Each book is resolved in its own transaction, and takes its own
answer.** One book the server cannot place settles nothing about the
others. This is the load-bearing decision and it is what makes the route
worth having: a client naming four hundred books must not lose the three
hundred and ninety-eight that worked because two did not.

**So the response is `200` with per-item results, not a status code.**
There is no single status that honestly describes five hundred answers.
`error` is the per-item spelling of what the single route answers with —
`not_found` for its `404`, `ambiguous` (with the works) for its `409` —
and a doubtful title/author match is *not* an error: it comes back with
`confidence: "low"` and nothing stored, exactly as it does one at a time.

**A failure that is the server's, rather than a book's, fails the
request.** A database that has gone away would otherwise be reported as a
permanent refusal of each book in turn, which a client cannot tell from
"this book will never resolve" — and would file away as a settled
answer. Only `ErrNotFound` and `ErrConflict` become items.

**The same scope pair, `library-read` + `sync`.** The reasoning is
ADR-0003's unchanged: it reads the catalog and writes the caller's work
graph. A batch route with a weaker gate would be a way around the check
the single route exists to enforce.

**`confirmed` applies to the whole batch**, and the documentation says to
send it only when there is no reader to ask. Accepting a title/author
guess is a question about one book; answering it five hundred at a time
is how two different books quietly become one reading history, which is
what ADR-0003 exists to prevent. The field is kept rather than dropped
because a migration tool has nobody to ask and knows what it is holding.

**500 ids**, refused above that with `batch_too_large` and the limit, the
shape `POST /v1/ops` and `POST /v1/sessions` already use and clients
already know how to cut a request against. The bound is not about the
body — a list of ids is small — but about the work behind it, which is
five hundred short transactions.

### What is *not* here

`GET /v1/heads` already answers "where does each of my works stand" for a
whole account, and a freshly named library is exactly that question. It
needs no server change; it needed a client that knew to ask it. The
sequence — batch resolve, then heads, then the ordinary delta feed — is
now written down in `docs/integrating.md`, because every client will want
it and none of it was spelled out.

The cursor does not move for a heads seed. `snapshot_seq` belongs to the
resync protocol, and the rule that a cursor advances only in the
transaction that stores the page it covers is unchanged.

## Consequences

- A fresh device names its whole catalog in one request and learns where
  every book stands in one more. Two round trips instead of four hundred.
- A client that predates the route, or a server that does, keeps working:
  the batch answers `404` and the per-book route is still there. Clients
  are told to remember the probe rather than repeat it.
- The single route stays. It is the right shape for a book the reader
  just downloaded, and it is the only place a doubtful match can be
  confirmed for one book.
- Per-item errors mean a client must read the results rather than the
  status. The alternative — a batch that is all-or-nothing — was
  rejected: one unplaceable book in a library of four hundred is
  ordinary, and it must not be able to stop the other three hundred and
  ninety-nine.

## Acceptance criteria

- A batch of good ids names each book, once, with the catalog's own
  evidence, and replays to the same works without creating more.
- A mixed batch returns the good books' works alongside `not_found` and
  `ambiguous` for the ones that failed, and neither refusal leaves
  anything in the work graph.
- A doubtful match returns `confidence: "low"` and stores no mapping.
- `library-read`-only and `sync`-only tokens are both refused, and a
  refused batch maps nothing.
- Over 500 ids answers `batch_too_large` with the limit; empty and
  malformed bodies are `400`, never `5xx`.

[chmouel/liseur#180]: https://github.com/chmouel/liseur/issues/180
