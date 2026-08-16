# ADR-0021: Merging and splitting a series

- **Status:** Accepted
- **Date:** 2026-08-16
- **Depends on:** [ADR-0018](0018-series-overrides.md),
  [ADR-0019](0019-library-wide-entities.md),
  [ADR-0020](0020-series-renaming.md)

## Context

Two shelves that should be one — `Metro` and `Metro 2033`, `The Expanse`
and `Expanse, The` — and one shelf that should be two: two folders that
each happen to hold an `Essays/` directory, folded into a single shelf by
[ADR-0019](0019-library-wide-entities.md). Neither is expressible.
ADR-0020 refuses a rename onto an occupied name with a `409` precisely
because that request is a merge, and nothing at all speaks to a split.

The constraint is the one that shaped ADR-0020: **the fold key belongs to
the disk.** `resolveEntityTx` turns an observed name into an entity
through the globally unique `series.normalized_name`.

- Merge `Metro 2033` into `Metro` by repointing memberships and deleting
  the absorbed row, and the next pass observing `Metro 2033` finds
  nothing, mints it again and pulls its books back off the shelf. The
  merge undoes itself.
- Split folder F's books onto a new shelf, and the next pass over F
  observes `Essays`, resolves it globally to the original shelf and puts
  them back.

Both failures are one failure: a decision made in the database that the
disk has never heard of, against a resolver that only listens to the
disk.

## Decision

Say the missing thing out loud — *what an observed name means here* —
and let the resolver read it:

```sql
CREATE TABLE series_bindings (
    id              TEXT PRIMARY KEY,
    folder_id       TEXT REFERENCES folders(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    series_id       TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    created_at      TEXT NOT NULL,
    created_by      TEXT NOT NULL
);
CREATE UNIQUE INDEX series_bindings_key
    ON series_bindings (COALESCE(folder_id, ''), normalized_name);
```

`name` is kept beside its key so that a shelf can say which names it
absorbed in the words they were written in; `normalized_name` is what
the resolver matches. A NULL `folder_id` means everywhere. `resolveEntityTx` gains one lookup
before the one it does today: the binding for this folder, else the
binding for everywhere, else the current match on
`series.normalized_name`. ADR-0019's automatic fold becomes the default
rule this table overrides.

One table is both features.

**Merge B into A** is a global binding from B's normalized name to A,
B's memberships and claims repointed to A, and B deleted. The absorbed
row is genuinely gone, so nothing has to remember to filter dead entities
out of listings, facets, OPDS or counts — the binding, not a tombstone,
is what keeps the name resolving. In one transaction:

1. `book_series`: `series_id = B` → A, `ON CONFLICT DO NOTHING`, so a
   book already on both shelves keeps A's position.
2. `book_series_override_items`: the same repoint. A reader's claim
   naming B has to follow B, or their book falls off the shelf.
3. `series_name_overrides` for B go with B. The survivor's name is the
   name; a merge that silently renamed A for a reader who was never
   asked would be worse than one that forgets B's nickname.
4. Bindings already pointing at B are repointed to A, so a binding always
   names a live series and the resolver never chases a chain. Merging
   into a series that is itself absorbed is refused rather than
   flattened.
5. Nothing else: a series appears in no op, no position and no work
   mapping, so **a merge cannot touch reading state.** That is what makes
   it a safe operation rather than a migration.

**Split folder F out of shelf S** is a new series S2, a folder-scoped
binding from the observed name to S2, and F's memberships moved onto it.
The next pass over F re-observes the same name and now agrees.

**Unmerge is deleting the binding.** The next pass observes the absorbed
name, resolves it to nothing, mints the series again and refills it from
what the disk says. The disk is the backup — with the caveat below, which
is the one place that stops being true.

**No renumbering.** Two shelves each with a volume 1 stay that way.
Inventing an order across two series is a guess, and ADR-0018's personal
reorder already exists for a reader who wants one.

### Shared, and written by an admin

A merge is a statement about the library's shape, not about one reader's
view, and the automatic fold it corrects is global too. A per-reader
merge would mean a resolve-time redirect on every series-bearing read,
entity listings that fold two rows into one per reader, and per-reader
book counts: a large machine in the hottest path, to express something a
reader can already reach with a personal claim and a personal rename.

Write-back turns that from a preference into a constraint — see below.

### What split does not need

Splitting a shelf a *single* folder produced — an omnibus directory
holding two series — needs a reader to say which volumes go where. That
is a claim per book, which ADR-0018 already expresses, and an admin's
shared claim makes it everyone's. The only new machinery is the
folder-wise case; the per-book case is a bulk affordance in the web UI at
most.

### What Calibre write-back changes

Write-back remains its own milestone and its own ADR, but a binding is a
long-lived record and getting its meaning wrong now would be expensive
later, so the question is answered here.

It does not change the mechanism, and it argues for it. ADR-0018 already
fixed the shape: the override is the source of truth and write-back is a
projection of it, kept afterwards rather than retired so that a curator
editing Calibre back does not silently win. A binding is the same kind of
thing one level up, and its shape is what a write-back would consume — a
folder-scoped binding is "rename this series in this Calibre library", a
global one is "in every folder that has this name".

Three things follow, and they are decided now.

**A binding is never garbage-collected**, even when no folder observes
its name any more. It records a decision rather than caching one, and it
is what makes a merge stick after somebody renames the series back in
Calibre: the pass observes the old name again and the binding folds it
again. The alternative is a merge that quietly comes undone the day a
curator edits their library.

**Only a shared decision can ever be written back.** A personal claim or
a personal rename is by definition not the library's truth, and pushing
one into `metadata.db` would publish one reader's opinion to everybody.
Merge and split being admin-and-shared is therefore not only cheaper to
resolve; it is the only version of them a write-back milestone could ever
act on.

**Unmerge stops being free once a merge is written back.** "Delete the
binding and let the next pass restore it" holds only while the disk is
read-only. Once the absorbed name is gone from `metadata.db` there is
nothing left to re-observe. That is a requirement on the write-back ADR —
it must record what it overwrote if it wants an undo — and it is stated
here rather than left as a promise a later milestone silently breaks.

Finally, the seam ADR-0018 already recorded is inherited: Calibre allows
exactly one series per book, so a merge that leaves a book on two shelves
cannot be written back and must be refused there rather than resolved by
guessing.

## Consequences

Searching for an absorbed name finds the shelf only through what the last
pass wrote into `book_search`. A binding is a resolver rule, not an index
entry, and per-reader search rows were already refused in ADR-0020 for
the same reason.

Unmerge restores what the disk says, not what readers claimed: a claim
that named the absorbed series was repointed and stays repointed, and a
series that only ever existed as a claim has no disk to come back from.

A folder-scoped binding lives and dies with its folder. Removing the
folder removes the split, which is right — the split was a statement
about that folder's books.

ADR-0020's `409` becomes an affordance rather than a dead end: rename,
refused, naming the holder, and then "merge them instead?".

The fold stops being a pure function of the disk. That is the point, and
it is the first time it has been true, so the binding table is the first
place to look when a shelf holds books whose folders disagree with it.

## Implementation phases

1. **Schema and resolution.** `series_bindings` in both baseline schemas;
   `resolveEntityTx` consults folder binding, then global binding, then
   the name.
2. **Writing.** `MergeSeries`, `SplitSeriesFolder`, `SeriesBindings` and
   `DeleteSeriesBinding` in the store, with their refusals; the API
   routes behind the admin gate.
3. **Web UI.** Merge offered from the refusal a rename returns; a
   folder-wise split offered on a shelf holding books from more than one
   folder; the names a shelf absorbs listed with an undo.
4. **Client.** Nothing, as expected. The Android client folds shelves
   from each book's series name rather than being sent them, so a merge
   arrives as the books having been renamed and they regroup by
   themselves; the absorbed shelf's key then matches nothing, which
   closes that screen and leaves the reader in the library with their
   books on the survivor. Pinned by a test there rather than left as a
   claim.

All four are implemented.

## Acceptance criteria

- A merge survives a full rescan: the pass that re-observes the absorbed
  name puts its books on the surviving shelf.
- A folder-wise split survives a full rescan: the pass over that folder
  keeps its books on the new shelf, and the other folder's stay put.
- Deleting a binding restores the shelf on the next pass, from the disk.
- A reader's claim naming the absorbed series follows it, and a personal
  reorder is not renumbered by the merge.
- Merging into an absorbed series is refused; merging a series into
  itself is refused.
- A merge changes no op, no position and no work mapping.
- Removing a folder removes its bindings and no others.
