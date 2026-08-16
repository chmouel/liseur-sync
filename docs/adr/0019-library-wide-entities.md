# ADR-0019: Catalog entities belong to the library, not to a folder

- **Status:** Accepted
- **Date:** 2026-08-16
- **Depends on:** [ADR-0017](0017-folders-not-pipelines.md),
  [ADR-0004](0004-metadata-and-categorization.md),
  [ADR-0018](0018-series-overrides.md)

## Context

Series, contributors and tags are stored per folder:

```sql
CREATE TABLE series (
    id        TEXT PRIMARY KEY,
    folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    ...
    UNIQUE (folder_id, normalized_name)
);
```

The folder key came in with ADR-0004, when a folder was closer to a
library than to a shelf, and survived ADR-0017 unexamined. It says
something that is not true: that a series is a property of the place its
books happen to sit.

It shows. A reader with a Calibre library and a folder of loose EPUBs
holding volumes of the same series gets two series rows, two ids and two
shelves with the same name. ADR-0018 built reading order, gap detection
and next-up on top of one shelf, so each of the two sees half the
volumes and reports the other half as missing — the feature reports a
hole that only the schema believes in. The same author held in two
folders is two authors, and neither page lists all their books.

Nothing about an entity is folder-specific. The folder is where a
*book* was found. "Imperial Radch" is not a fact about a disk.

## Decision

**An entity is library-wide.** `series`, `contributors` and `tags` lose
`folder_id`; `normalized_name` is unique across the library. All three
kinds move together: they share one generic code path (`entityTables`),
so splitting them would buy a second axis of specialness in the one
place that is already special for ADR-0018's resolution CTE.

**Membership keeps its folder.** `book_series`, `book_contributors`,
`book_tags` and `book_series_override_items` keep `folder_id`, because
it carries the composite foreign key to `books(folder_id, id)` and its
cascade. Only the entity side of each key loses the folder.

**Names fold automatically.** Two folders observing the same normalized
name resolve to one row, through the same `resolveEntityTx` a pass
already uses. This is the whole point and it is not free: a *plain*
folder infers its series from directory names (`inferSeries`), so two
unrelated folders each holding an `Essays/` subdirectory become one
shelf. We accept that. Directory names are a weak signal, and a reader
who sees a wrong merge can claim their way out of it (ADR-0018), which
is exactly the layer that exists for the disk being wrong.

The fold is one-way. Nothing records that two merged entities arrived
from different folders, so nothing can split them again. An explicit
merge — where sameness is stated rather than inferred — is the design
this one deliberately does not build.

**An orphan is deleted.** Removing a folder drops its memberships, and
an entity with no memberships left is deleted, at the end of a pass and
on folder removal. An entity kept alive only by a *claim* is not an
orphan: a reader who filed a book into a series they named still has
that series, even when no scan has ever observed it.

**No compatibility is preserved.** This project has never shipped
([ADR-0017](0017-folders-not-pipelines.md)). The baseline schema is
edited in place, the folder-scoped routes are replaced rather than
redirected, and no shim is kept.

Routes become:

| Was | Is |
| --- | --- |
| `GET /v1/folders/{folder}/entities/{kind}` | `GET /v1/entities/{kind}` |
| `GET /v1/folders/{folder}/entities/{kind}/{entity}/books` | `GET /v1/entities/{kind}/{entity}/books` |
| `PUT /v1/folders/{folder}/entities/{kind}/{entity}/order` | `PUT /v1/entities/series/{entity}/order` |
| `GET /ui/folders/{folder}/series/{entity}` | `GET /ui/series/{entity}` |

## Consequences

A shelf spans folders, so a volume on it should name the folder it came
from: the page is now the one place a reader sees books from two disks
side by side.

Search is left folder-scoped. `SearchQuery.FolderID` is required and
every search reads `WHERE b.folder_id = ?`, so a library-wide facet used
inside one folder's search still returns only that folder's books. That
is an inconsistency, not a contradiction, and it is a separate decision.

Full-text search is unchanged: `book_search.subjects` still indexes the
names a pass observed, as it did before ADR-0018.

Series *renaming* remains unsolved. A claim states membership, and a
rename is a property of the entity, so no claim can express one. Making
entities library-wide makes a rename more valuable — it would apply
everywhere at once — without making it expressible.
