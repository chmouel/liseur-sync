# ADR-0029: A folder somebody can read

- **Status:** Accepted
- **Date:** 2026-09-04
- **Amends:** [ADR-0027](0027-explicit-per-user-folder-access.md), which
  said a new folder is created with no grants and that migration 4
  creates none

## Context

ADR-0027 made catalog visibility a row in `user_folders`, and left every
route that creates a folder writing no such row. The result was reported
as issue #13: an administrator adds a folder from the Settings page, the
scan finds the books, the admin Folders page lists the folder and its
root, and the Library page one click away says the server watches no
folders.

Two separate holes produced it, and a third made it unreadable.

Creating a folder granted nobody. Neither `POST /ui/admin/folders` nor
`admin add-folder` wrote a grant, so the account that had just added a
folder could not see it, and no page said why.

Migration 4 backfilled nobody. ADR-0027 chose that deliberately and
wrote the consequence down ("Deployments upgrading to migration 4 must
assign existing accounts' folders"), but nothing in the product said so
and no release note carried it. A working single-user deployment lost
its whole library on upgrade.

The Library page then reported the wrong cause. It branched on the
viewer's granted folders and rendered a sentence about the server. That
same branch wrapped the entire shelf, so a reader with no grant also
lost sight of their own reading history, which ADR-0027 explicitly
promised revocation would not touch.

## Decision

Keep ADR-0027's boundary. Grants stay explicit, a folder still has no
owner, and administrator status still confers no reading access. What
changes is that the boundary stops defaulting to nobody in silence.

### A new folder is granted to one named account

Folder creation and the grant are one transaction, through
`CreateFolderGranting(ctx, folder, grantUserID)`. A grantee that does
not exist, a duplicate root or a failed grant rolls the folder insert
back, so no half-granted folder is ever committed. `CreateFolder` stays
as the ungranted form and is now defined as `CreateFolderGranting` with
an empty grantee.

From the admin panel the grantee is the administrator who submitted the
form, and nobody else. The CLI has no session, so `add-folder` takes an
optional `-assign <user>`; without it the folder is created ungranted
and the subcommand prints the `assign-folder` command that would make it
visible. The parameter is named for the grantee rather than the creator
because `-assign` may name somebody else.

### Migration 6 repairs the state migration 4 left, and only that state

One statement, the same in both backends:

```sql
INSERT INTO user_folders (user_id, folder_id)
SELECT u.id, f.id FROM users u CROSS JOIN folders f
WHERE NOT EXISTS (SELECT 1 FROM user_folders);
```

The subquery makes it conditional and idempotent at once. An install
where anybody holds any grant is left alone, so a deliberately separated
library stays separated and a revocation stays revoked. A fresh install
runs it against empty tables. A second run finds the table non-empty and
does nothing.

Two policies the SQL implies and this ADR states outright:

- **Disabled accounts are included.** A disabled account is refused
  every credential, so the grant grants nothing while it stays disabled.
  Excluding it would mean re-enabling an account later produced an empty
  library with no explanation. This restores what such an account saw
  before migration 4 and nothing more.
- **Migration-time folders only.** Grants are written for the folders
  that exist when the migration runs. A folder created afterwards is
  granted to its creator alone, so the tenant boundary holds from the
  upgrade onward.

**One state is an accepted security risk, not a limitation.** An
operator who revoked every grant on purpose, leaving `user_folders`
empty, gets every grant back. The guard cannot distinguish that database
from the broken one. Version-aware gating, backfilling only a database
that was below migration 4 when the run began, would close it and was
rejected: the installs this repairs are already at migration 4 or 5, so
gating on version would leave the reported bug in place. The release
note carries the warning prominently and before the upgrade, not as a
recovery afterwards, and the remedy is to revoke again once upgraded.

### The Library page names which situation it is in

`LibraryView` carries two booleans and no other new information: whether
this account holds any grant, and whether the server watches any folder
at all. The second is a global bit disclosed to an unprivileged account,
and this ADR authorizes that disclosure deliberately: it buys a reader
an accurate instruction instead of a false one, and no folder name,
root path, id or count crosses with it.

Folder state decides the wording of an empty catalog and an explanatory
notice. It never decides whether the shelf renders. The `all`, `reading`
and `finished` filters show the reader's own works with no grant at all,
restating ADR-0027's promise that revoking a grant removes no reading
state. The default `here` filter is genuinely catalog-only and reports
either that no folder is assigned to this account or that the server
watches none.

A work whose catalog book is now hidden renders through the existing
orphan text-tile path: title, author and progress from the work, with no
book id, folder id, folder name, root path, cover, download or reader
URL.

That path carried a trap worth recording. `WorkBookIDs` is
grant-filtered, so a revoked reader's work looked unmapped, and the page
offered a delete that ADR-0024 refuses while a catalog book still maps
the work. `WorksWithCatalogMappings(ctx, userID, workIDs)` therefore
answers the ungranted question, returning existence booleans and nothing
else, and `Bookless` is set from it. `WorkBookIDs` keeps its own job of
choosing which book id this reader may follow.

### The admin Folders page marks a folder nobody can read

`FoldersWithGrants(ctx, folderIDs)` answers one grouped query and
returns presence booleans: no user ids, no counts. ADR-0027 ruled out a
user-count field on folders and that stands; a boolean on an admin-only
page that already renders root paths is the narrowest thing that makes
the situation discoverable. There is no folder-centric grant UI and this
decision does not add one, so the marker links to the admin Users view
and says grants are edited from an account's page.

## Consequences

- An administrator who adds a folder can read it immediately, because a
  row says so and not because they are an administrator.
- An install where nothing was ever assigned is repaired by upgrading.
  An install where anything was assigned is untouched.
- An operator who revoked every grant must revoke again after upgrading.
- A reader with no grant still sees their own reading history and is
  told, accurately, which of two situations they are in.
- The store gains four read-only or narrowly scoped methods rather than
  a widened folder shape.

## Acceptance criteria

- A folder created from the panel is readable by its creator and by no
  other administrator.
- `add-folder -assign` grants exactly the named account, rejects an
  unknown one before touching the filesystem, and leaves no folder
  behind when the grant fails.
- `add-folder` without `-assign` writes no grant and prints the command
  that would.
- Migration 6 grants every account every folder when no grant exists
  anywhere, changes nothing when one does, and is a no-op on a second
  run and on a fresh database.
- A reader whose last grant is revoked still sees their works under
  `all` and `reading`, with no catalog identifier or URL and no delete
  form.
- The Library page distinguishes a server with no folders from an
  account with no grants, and discloses nothing else about folders.
- The admin Folders page marks a folder no account can read.
