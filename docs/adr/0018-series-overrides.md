# ADR-0018: Series a reader can shape

- **Status:** Accepted
- **Date:** 2026-08-16
- **Depends on:** [ADR-0017](0017-folders-not-pipelines.md),
  [ADR-0004](0004-metadata-and-categorization.md),
  [ADR-0006](0006-catalog-api-and-opds.md)

## Context

ADR-0017 made the catalog a projection of what is on disk. A series is
therefore something the server *observed*, never something anyone can
*state*: `series` and `book_series` are written only by `ReconcileFolder`,
from a plain folder's subdirectory names or from a Calibre library's
`books_series_link`, read-only.

That is right for the common case and wrong for every case where the disk
is not the authority on the question. A trilogy split across three
directories is three series. A volume dropped at the folder root is in
none. A Calibre library where book four was never given a `series_index`
sorts it after every numbered volume, forever, because an unplaced book is
an unanswered question. None of these are scan bugs to be fixed by a better
parser — they are places where a person knows something the filesystem does
not say, and today there is nowhere to put that knowledge.

The Liseur Android client has already answered this question locally. It
carries `user_series_name`, `user_series_index` and a `series_override`
flag on every book, offers an assign dialog and drag-reorder, and syncs
none of it. A reader who tidies a series on their phone loses the work on
their tablet. The client is not wrong to have built it; the server is
missing the place to keep it.

The reading side is separately thin. The API does expose series — inline
on every book payload, as folder-wide entities, as a books-in-reading-order
listing, as search facets and as OPDS navigation feeds — but the web UI
spends it on a chip and a plain list, and the Android client reads only the
first series inline and never calls the entity endpoints at all.

## Decision

An **override layer** over the derived catalog, resolved at read time.

`book_series` keeps meaning exactly what it means now: what the folder said
on the last pass. It is still rewritten wholesale by every reconcile, and
nothing about ADR-0017's rules changes — the server writes nothing under a
watched folder, and nothing into `metadata.db`. An override lives in
liseur-sync's own database and is applied when a series is read, never by
mutating what the scan recorded.

### Two layers

Resolution is personal, then shared, then folder.

| layer | written by | seen by |
| --- | --- | --- |
| personal | any signed-in user, for themselves | that user alone |
| shared | admins | everyone with no personal claim |
| folder | `ReconcileFolder` | everyone with no claim at all |

The shared layer is how a library gets corrected once rather than by every
reader in turn. The personal layer is how a reader disagrees with the
correction, and it is also what makes the Android client's existing feature
work for people who are not administrators: without it, "sync my series
tidying" would be a button that silently does nothing for most accounts.

### An override is a whole-book claim

A claim says: *this layer states that this book is in these series, at
these positions.* Not a set of additions and removals over what the scan
found — the whole answer, replacing it.

The empty set is a meaningful claim, and it means "this book is in no
series". That is why the claim is a row of its own with the memberships
hanging off it, rather than being inferred from the presence of membership
rows. Detaching a stray volume from the series its directory implied is a
thing people will want to do on the first day.

Whole-book claims are chosen over a diff because a diff would need to track
three states per membership — added, removed, repositioned — and would then
have to decide what a repositioned membership means when the next pass
moves the book to a different series. A claim has no such question: it
either speaks for the book or it does not. It also matches
`replaceRelationsTx`, which already states a book's metadata wholesale, and
it matches the client's own `series_override` boolean.

### A named series that does not exist is created

Assigning a book to a series by a name nothing has scanned mints the
`series` row through the same path a pass uses, so it folds by normalized
name and is thereafter indistinguishable from a scanned one. This is safe
because nothing collects unreferenced entities and `ReconcileFolder` only
ever touches `book_series`: a pass cannot delete a series that only an
override uses.

There is deliberately no route that creates an empty series. A series with
no books in it is not a thing a reader can point at, and inventing one
would create an object whose only lifecycle question — when does it go
away — has no good answer.

### Overrides live and die with catalog identity

An override is keyed by catalog book. It therefore inherits ADR-0017's
identity rules exactly: a Calibre book keeps its `calibre_id` when the
curator renames it and keeps its overrides, and a plain-folder book whose
bytes changed at a path is a new row by rule 4 of the folder contract and
loses them with everything else attached to the old row.

That is the correct behaviour and not merely the convenient one. Content
change is not identity transfer; a claim about the old file is not a claim
about the new one.

### The catalog becomes partly user-scoped

This is the cost, and it is stated plainly because it contradicts a rule
this repository has enforced until now: reading state is scoped by
`user_id`, the catalog deliberately is not.

The exception is bounded. Only series memberships are affected, only
through the personal layer, and only additively — a personal claim can
never reveal a book, a folder or a path the reader could not already see,
because it is an opinion *about* rows they already read. `root_path` stays
out of every non-admin response, folders stay admin-managed, and the
shared catalog stays shared.

What it does mean is that every catalog read yielding series now depends on
who is asking, and that tenant isolation has a new thing to get wrong. The
scope matrix and isolation tests must cover it: one reader's personal
claim must be invisible to another, and a non-admin must not reach the
shared layer.

### Writing needs a scope of its own

ADR-0017 removed `library-manage` on the grounds that nothing wrote to the
catalog but a pass, and a pass answers to the disk rather than to a token.
That is no longer true, so the scope returns — with a narrower meaning than
it had before uploads were removed. It grants series claims and nothing
else. Writing the shared layer additionally requires an administrator; the
scope alone buys the personal layer.

### OPDS inherits overrides

An OPDS feed is served to an authenticated account, and series membership
is catalog metadata, so the feeds show that account's effective series.
This is what a reader wants — the point of tidying a series is that the
e-reader browsing it is in the right order — and it exposes nothing new:
feeds stay metadata-only and reveal no reading state, as ADR-0006 requires.

### Calibre write-back is not decided here

Pushing a claim back into `metadata.db`, so that Calibre itself shows it,
retires DESIGN §9.2's third rule and needs its own ADR. It is deferred, and
the layers above are built so that it can be added under them rather than
through them: the override is the source of truth, and write-back would be
a projection of it.

Two facts are recorded now because they constrain that future decision.
Calibre allows exactly **one** series per book, while this model allows
several, so write-back cannot round-trip a multi-series claim and must
refuse it rather than silently choose. And a claim is **kept** after
Calibre agrees with it rather than retired, so that a curator editing
Calibre back does not silently win.

## Consequences

A wrong series becomes a thing a person can fix, once, in the place they
noticed it, and the fix follows the account to every device.

The Android client's local override model stops being a dead end and
becomes a cache of the personal layer.

Every catalog read that yields series gains a `userID` argument and reads
through a resolution CTE rather than straight from `book_series`. This is
the largest and least interesting part of the work, and it should land as
its own behaviour-preserving change before any override exists, so that
the commit which introduces overrides is small enough to review.

Series ordering keeps its existing sharp edge: an unplaced book sorts after
every placed one via a sentinel rather than a NULL, because the pagination
cursor must compare against the expression it orders by. Overridden
positions flow through the same expression, so the cursor stays coherent —
but the resolution CTE is now what feeds it, and that is worth a test
rather than a hope.

Renaming a series is *not* possible under this decision, and the model
cannot express it: a rename is a property of the entity, not of a book's
membership in it. It is the obvious next request. When it comes it is a
separate entity-keyed override, and it should be decided rather than
bolted onto this one.

## Implementation phases

1. **Storage and resolution.** The two tables, and a parameterised
   resolution CTE in both backends. Thread `userID` through the catalog
   reads that yield series, with no behaviour change while no override
   exists.
2. **Writing.** Store methods to set, clear and bulk-reorder claims; the
   `library-manage` scope; the three API routes; `source` on read payloads
   so a client can tell a folder series from a claimed one.
3. **Web UI.** The series shelf, a series-aware entity index, and an assign
   dialog on the book page.
4. **Client.** The Liseur Android client reads all series rather than the
   first, and syncs its local overrides as the personal layer.
5. **Calibre write-back.** Deferred to its own ADR.

## Acceptance criteria

- A personal claim wins over a shared one, a shared claim wins over the
  folder, and a claim with no memberships means the book is in no series.
- A reconcile pass does not disturb any claim, and does not delete a series
  that only a claim references.
- A Calibre book whose directory moved keeps its claim; a plain-folder book
  replaced by different bytes at the same path does not.
- One reader's personal claim is invisible to every other reader, in book
  payloads, entity listings, entity book listings, search facets and OPDS.
- A non-admin token with `library-manage` can write the personal layer and
  is refused the shared one. A token without `library-manage` is refused
  both, and is still able to read.
- Books in a series page in reading order across overridden positions, with
  unplaced books last, and the cursor neither skips nor repeats.
- Naming a series that does not exist creates it; there is no route that
  creates an empty one.
- The web UI's assign dialog requires the per-session CSRF token, and
  offers the shared layer only to administrators.
