# ADR-0020: Renaming a series

- **Status:** Accepted
- **Date:** 2026-08-16
- **Depends on:** [ADR-0018](0018-series-overrides.md),
  [ADR-0019](0019-library-wide-entities.md)

## Context

A series gets its name from whatever observed it first: Calibre's
`series` column, or a plain folder's directory name. The name is wrong
often enough to matter — `Metro` for `Метро`, `The Expanse (Books)` for
`The Expanse`, `03 - Something` where a directory carried its volume
number — and today nothing can correct it.

[ADR-0018](0018-series-overrides.md) gave a reader a claim layer, but a
claim states *membership*: which series a book is in. A name is a
property of the series itself, so no claim can express one. ADR-0019
made entities library-wide, which raises the value of a rename — it
would land everywhere at once — without making it expressible.

The load-bearing constraint is the fold. `resolveEntityTx` resolves an
observed name to an entity through `series.normalized_name`, which is
globally unique. If a rename rewrote that column, the next pass that
observed the original name would find nothing, mint a second series and
silently move every book onto it: a rename would quietly undo itself and
lose reading order on the way.

## Decision

**A rename is a display layer over a fold key that never moves.**
`series.name` and `series.normalized_name` keep meaning "what a scan
said", and stay the only thing a pass resolves against. The reader's
name lives in a new table keyed by the entity:

```sql
CREATE TABLE series_name_overrides (
    series_id       TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    scope_user      TEXT NOT NULL,
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    PRIMARY KEY (series_id, scope_user)
);
```

The override carries its own `normalized_name` because that is what the
listing orders and pages by, and what a collision is checked against.
The scanned one it shadows is left exactly as the pass wrote it.

Everything that displays a series name resolves it: personal override,
then shared override, then the scanned name. That is the same ladder,
the same `scope_user` sentinels and the same `SeriesSource` type as a
claim, so the resolution helper ADR-0018 already parameterises gains a
second caller rather than a sibling.

The consequence worth stating plainly: a second folder observing `Metro`
still joins the shelf a reader renamed to `Метро`. The fold follows the
disk, the name follows the reader, and those are allowed to disagree.

**Series only.** Contributors and tags share the `entityTables` code
path, so generalising is tempting, and one polymorphic
`entity_name_overrides(kind, entity_id, …)` table would serve all three
— at the cost of the foreign key, because a polymorphic column cannot
have one, which would leave orphan cleanup enforced by care in a file
whose whole design is to be enforced by keys. A tag rename is also
mostly a request to *merge* two tags, and an author's name wants an
authority record rather than a nickname. Neither is this ADR.

**Renaming onto an occupied name is refused.** If the target name
normalises to one already visible in the caller's scope, the write fails
with `409` naming the series that holds it. Renaming `Metro 2033` to
`Metro` when `Metro` exists is a request to merge two shelves, and
merging is a decision this ADR does not make (below). Refusing keeps
"one visible name, one shelf" true, and leaves an exact place for merge
to land.

**A name does not keep a series alive.** Orphan collection counts
claims, because a claim asserts a membership somebody made; a rename
asserts nothing about which books exist. An entity with no memberships
and no claims is deleted, and the cascade takes its names with it.

Clearing the override reverts to the scanned name. An empty or
whitespace-only name is a `400`, not a way to hide a shelf.

## Consequences

Two readers can see different names for the same shelf, and a shared
rename by an admin changes a name for everyone who has not overridden it
themselves. That is exactly ADR-0018's bargain, applied to a field
instead of a relation.

Sort order becomes the effective name's, so entity listings resolve
before they order, and the paging cursor — today `normalized_name` —
must move with it or paging will skip renamed rows.

Full-text search keeps indexing what a pass observed. `book_search`
belongs to the catalog, not to a reader, and per-reader FTS rows are a
cost far beyond a rename's value. Searching `Метро` finds the shelf by
its scanned name only; the effective name is a display concern.

The Android client displays the effective name for free, since it
renders what the API returns. Renaming *from* the app is a phase of its
own, and it has the same offline hole every write has there: no outbox,
so a rename made offline is dropped.

### What this defers: merge and split

Two shelves that should be one is the next request, and this ADR
deliberately stops short of it. Recorded so the next design starts from
the right place:

- **The refusal above is the hook.** A merge is a rename onto an
  occupied name, plus consent. The API shape probably already exists.
- **Merge needs an alias, not a rewrite.** The absorbed series' key must
  keep resolving, or the next pass observing its name mints it again —
  the same trap a rename would fall into. That means a surviving row
  pointing at its successor, and a fold that follows the pointer.
- **Today's fold is already a silent merge**, and it has no record and
  no undo: two folders each holding an `Essays/` directory become one
  shelf with nothing saying why. An explicit merge should produce the
  record the automatic one never did.
- **Split is not merge's inverse.** Membership keeps `folder_id`
  (ADR-0019), so provenance survives and a folder-wise split is
  possible; splitting a shelf a single folder produced needs a reader to
  say which volumes go where, which is a claim per book — something
  ADR-0018 can already express.
- **Scope is the open question.** A merge changes everyone's shelves,
  which argues for shared-only and admin-only; a wrong automatic fold
  hurts one reader, which argues for personal. Deciding that is most of
  the design.

## Implementation phases

1. **Storage and resolution.** Implemented. A `series_names` CTE
   resolves the name beside the claim layer's `eff_series`, so a read
   that shows a series never asks twice; the entity listing orders and
   pages on the resolved name.
2. **Writing.** Implemented. `SetSeriesName`/`ClearSeriesName`, the
   collision refusal, and `PUT`/`DELETE
   /v1/entities/series/{entity}/name` under `library-manage`. Entity
   payloads carry `scanned_name` and `name_source`.
3. **Web UI.** Implemented, as a disclosure on the shelf that redirects
   back to it: the name is in the title, the heading and the
   breadcrumb, so reloading is the honest way to show it changed.
4. **Client.** A rename action in the Liseur Android client.

## Acceptance criteria

- A personal name wins over a shared one, a shared name over the
  scanned one, and clearing an override reverts to what the scan said.
- A rename survives a full rescan, including a Calibre pass that
  re-observes the original name, with the shelf's books and order
  intact.
- A second folder observing the original name joins the renamed shelf
  rather than creating a new one.
- Renaming onto a name already visible in that scope is refused with a
  `409` that names the holder; the same name in another reader's
  personal layer does not collide.
- One reader's rename is invisible to every other reader in entity
  listings, book payloads, search facets and OPDS.
- Deleting the last folder holding a renamed series deletes the series
  and its names; a series kept alive by a claim keeps both.
- Paging an entity listing that contains renamed rows returns every row
  exactly once.
