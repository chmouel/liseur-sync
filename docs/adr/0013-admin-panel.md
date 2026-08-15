# ADR-0013: The admin panel

- **Status:** Accepted
- **Date:** 2026-08-14
- **Depends on:** [ADR-0002](0002-library-storage-and-ownership.md),
  [ADR-0011](0011-web-ui-revamp.md)

## Context

`/ui/admin` today is two tables: every user's name and id, and the
invite codes this admin created. Everything else an operator does is a
subcommand of `liseur-sync admin` — creating users, minting and revoking
tokens for them, creating managed and watched libraries, granting and
revoking access to those libraries, setting a library's filename layout,
working the review queue, backfilling works, verifying a backup.

That split is not a design; it is where the code stopped. It has three
costs.

**The panel lies by omission.** It shows a list of users and offers
nothing to do with one. An operator who reads it concludes the server
has no user administration, when in fact it has nine subcommands of it.

**Shell access is required for routine work.** Adding a household member
means SSH, the binary, the config file, and a TTY for the password
prompt — for an operation the server already exposes over HTTP to the
person's own browser during invite registration. On a container host
this is `docker exec` with the right config path, which is exactly the
sort of thing people get wrong at the moment they need it.

**Nothing reports state.** The server runs five periodic workers and
holds ingest jobs, a review queue, trashed books awaiting purge and a
blob inventory. None of it is visible anywhere except by reading the
database or grepping logs. "Is it working?" has no answer short of
uploading a book and watching.

One property of the existing code makes this cheaper than it looks:
cross-user *account* administration needs no new store concept.
`ListTokens`, `RevokeToken`, `UpdateUserPassword` and the rest already
take the target `user_id` as an argument, which is how the CLI acts on
another account today.

One property makes it more expensive, and this ADR's first decision is
about it. "Is an admin" is currently *"holds an active admin-scope
token"* (`auth.Service.IsAdmin`), and administration is not a
credential.

## Decision

Make `/ui/admin` the operator surface for the instance: everything a
self-hosted administrator does routinely, done in a browser, with the
CLI kept as the equal-capability path for the cases where a browser is
the wrong tool (bootstrapping the first user, running from cron,
verifying a backup) and as the recovery path when the UI is what is
broken.

### Admin becomes an account property, not a token

`users.is_admin` (boolean, default false) replaces "holds a live
admin-scope token" as the definition of an administrator. The
token-derived definition cannot survive this panel:

- Granting admin would have to mint a bearer credential and hand it to
  the *granting* administrator, who does not need it and now has one.
- Revoking admin would mean revoking every admin-scope token the user
  holds — destroying unrelated scopes on multi-scope tokens, and racing
  with any concurrent mint.
- Any "is there another admin left?" check is a scan of every user's
  tokens, which cannot be made atomic against a concurrent revoke, an
  expiry, or the existing `POST /ui/tokens/{id}/scopes` route.
- An admin token that expires silently demotes its holder.

The two ideas are separated: the **account** carries the role, a
**token** carries capabilities. `store.ScopeAdmin` stays exactly as it
is for API authorization; what changes is who may hold it.

- `auth.Service.IsAdmin(ctx, userID)` reads `users.is_admin` and returns
  false for a disabled user. It remains the single definition, and every
  caller (`requireAdmin`, `CheckScopeGrant`, the API's admin gate) is
  unchanged in shape.
- `CheckScopeGrant` still refuses an admin-scope token to a non-admin —
  now meaning a non-admin *account*, so the escalation rule gets
  stronger rather than weaker.
- `store.SetUserAdmin(ctx, userID string, admin bool) error` is the only
  way the flag moves. Every role and disable transition runs in one
  transaction that first takes an instance-wide lock — a
  `pg_advisory_xact_lock` on a constant on PostgreSQL, where a bare
  conditional `UPDATE` under `READ COMMITTED` would permit write skew
  and let two transactions demote the last two admins simultaneously;
  SQLite's single writer gives it for free. Inside that transaction the
  last-admin rule applies: clearing the flag fails with `ErrLastAdmin`
  unless another enabled, non-disabled admin exists. `SetUserDisabled`
  takes the same lock and the same guard.
- **Minting an admin-scoped token takes the same lock.** A role check
  in `CheckScopeGrant` followed by a separate insert can interleave with
  a demotion: the check passes, the demotion revokes the tokens that
  exist, and the insert then lands an admin-scoped token that lies
  dormant until the account is promoted again. So the *store* is where
  the rule lives: `CreateToken` and `UpdateTokenScopes`, when the scope
  set contains `admin`, take the same instance lock and re-read
  `users.is_admin` inside the transaction that writes, failing with
  `ErrAdminGrantRequiresAdmin`. `auth.Service.CheckScopeGrant` stays as
  the early check that produces a good error message, but it is no
  longer what makes the invariant true.
- **Demotion revokes admin capability, not just the role.**
  `store.ScopeAdmin` implies every other scope (`ScopeSet.Allows`), so
  an admin-scope token outliving its owner's role would keep full API
  authority. In the same transaction, `SetUserAdmin(false)` revokes
  every unrevoked token of that user whose scopes contain `admin`. As
  defence in depth — and to cover a token minted through a path that
  forgets this — `auth.Service.AuthenticateToken` rejects an
  admin-scoped token whose owner is not an enabled admin.
- The migration adds `users.is_admin` **and** `users.disabled_at`
  together, in Phase 1, even though nothing disables an account until
  Phase 6: `IsAdmin`, the last-*enabled*-admin guard and the token check
  all say "enabled admin", and a phase that writes those semantics
  against a column that does not exist yet is a phase that has to be
  rewritten when it does. The backfill sets `is_admin = true` for any
  user currently holding a live admin-scope token — exactly the set
  `IsAdmin` returns true for today — and `disabled_at` starts null for
  everyone. Appended to the `migrations` slice in both backends; no
  shipped migration is edited.
- The CLI gains `grant-admin <user>`, `revoke-admin <user>`,
  `disable-user <user>` and `enable-user <user>`. Because the flag is not
  a credential, the CLI recovers from every lockout the panel can
  produce — but recovering a *disabled* last administrator takes
  `enable-user` first and `grant-admin` only if the role was also
  cleared; `grant-admin` alone does not reactivate a disabled account.
  `mint-token -scope admin` for a non-admin account now fails and says
  to run `grant-admin` first, rather than silently creating the role as
  a side effect.

### The panel administers accounts, not their contents

An admin may create a user, reset their password, see and revoke their
tokens and devices, grant them access to a library, and cut off their
access entirely. An admin may **not** browse another user's library,
reading history, statistics or positions from the panel, and no page
added here renders a book title, an author, a filename, a source path or
an error message drawn from another user's data. Those are the private
contents of an account; they are not needed to operate the server.

This is the reason the maintenance page is **aggregate-only** (below),
and it is the boundary that lets the panel be built without weakening a
single tenant-isolation test.

A *library* is the one shared thing: infrastructure with an ACL, not a
private shelf. The panel shows which libraries exist, their kind, owner,
watched root and grants — never the books inside.

### Instance administration does not impersonate an owner

Library mutations are ACL-checked against the actor
(`GrantLibraryAccess(ctx, actorUserID, ...)` requires the actor to own
or manage that library). An instance admin does not automatically hold
that role, and the panel must not paper over it by passing the owner's
id as the actor: that would log the wrong actor and would make the ACL
check meaningless while appearing to pass.

Instead, the deliberate bypass is explicit and named. Each of these is a
new store method that takes the acting admin's id for the record and
skips the ACL check by construction, documented in the interface beside
`ListUsers` with the same justification, and none of them reads book
rows:

| Method | Purpose |
|---|---|
| `AdminListLibraries(ctx, after string, limit int) ([]Library, error)` | every library on the instance, paginated |
| `AdminUserLibraries(ctx, userID, after string, limit int) ([]AccessibleLibrary, error)` | the libraries one user owns or was granted, paginated |
| `AdminLibraryGrants(ctx, libraryID string, limit int) ([]LibraryGrant, error)` | who can read or manage one library |
| `AdminSetLibraryAccess(ctx, actorUserID, libraryID, userID string, role *LibraryRole, at time.Time) error` | grant, or revoke when `role` is nil |
| `AdminSetLibraryConfig(ctx, actorUserID, libraryID string, configJSON []byte, at time.Time) error` | filename layout |
| `AdminLibraryByID(ctx, libraryID string) (Library, error)` | one library, for the two writes that rewrite a record they must read whole |
| `AdminCounts(ctx) (AdminCounts, error)` | the aggregate numbers behind the overview and maintenance pages, in one round trip |

`AdminCounts` returns only integers and timestamps: users (total,
disabled, admin), libraries by kind, catalog books by status, blobs and
total bytes, ingest jobs by state, oldest job in each non-terminal
state, books in review, books in trash and the next expiry. No names, no
paths, no ids of other people's things.

That is the complete list of global reads this ADR adds — six methods.
**No global read of reading data** — no cross-user ops, sessions,
rollups, positions or works — is added, now or later, under this ADR.

### Naming a server path, and what it costs

Creating a root-backed library names a server filesystem path. Exposing
that to a browser hands a remote administrator a filesystem-existence
oracle and a way to make the scanner ingest any readable EPUB tree on
the host — a material privilege increase over "can administer this
application", and one that survives a stolen admin session.

This ADR originally concluded from that that the panel would create
managed libraries only, and that `add-library` would stay a subcommand.
That is now amended. The argument establishes what the privilege costs,
not that nobody may buy it: the operator who has to find a shell to
attach the Calibre library the instance exists to serve is an operator
who ends up running a shell as root, which is strictly worse than the
thing being avoided. So the panel offers it, priced:

- the acting administrator re-types **their own password**, on the two
  independent budgets (per account, per address) every high-impact
  action here uses, so a stolen session alone cannot probe the
  filesystem;
- `content.library_roots`, when an operator sets it, is the only place
  a root may be — the form becomes a choice among trees they already
  meant to serve rather than the whole disk;
- every attempt, refused or not, is one audit line, and no refusal
  repeats what the server found on disk beyond the path that was typed;
- a **Test this folder** button exists so the common case — a typo
  in a path — is answered without creating anything.

Creating a **managed** library from the panel needs none of that: it
names no path.

### Disabling an account, and every credential it holds

Deleting an account means cascading across the op log, sessions,
rollups, works, catalog rows, blobs whose last reference just went away,
libraries the user owns and grants they were given — with a retention
model built around append-only tables. It is a real feature, it is not
this one, and getting it wrong destroys the data that is the point of
the server. Out of scope, explicitly.

What an operator needs at the moment of need is to *stop* an account:
`users.disabled_at`, one nullable timestamp. The enforcement is the part
worth designing, because the server has eight ways to arrive
authenticated and a check in seven of them is a feature that does not
exist:

| # | Path | Where it resolves today |
|---|---|---|
| 1 | web session cookie | `webui.Server.session` → `AuthSessionByHash` |
| 2 | web login form | `auth.Service.Login` |
| 3 | `POST /v1/login` | `auth.Service.Login` |
| 4 | login credential (token management) | `auth.Service.AuthenticateLogin` → `AuthSessionByHash` |
| 5 | API/OPDS bearer token | `auth.Service.AuthenticateToken` → `TokenByHashGlobal` |
| 6 | kosync device key | `kosync` → `KosyncDeviceByKey` |
| 7 | kosync pairing redemption | `kosync` → `RedeemPairingCode` |
| 8 | koplugin capability URL | `koplugin` → `KopluginDeviceByToken` |

Six of these bypass `auth.Service` entirely or load no user row, so
"check it in the service" would leave holes. **The refusal goes in the
store, in the credential lookups themselves.** `AuthSessionByHash`,
`TokenByHashGlobal`, `KosyncDeviceByKey`, `RedeemPairingCode` and
`KopluginDeviceByToken` exist for no purpose but authentication; each
gains a join against `users` and returns its not-found error when
`disabled_at IS NOT NULL`. A handler cannot forget a check it does not
make, both backends implement the same clause, and the shared
`storetest` suite asserts it once per lookup for SQLite and Postgres
alike. `auth.Service.Login` gets the one explicit check, since
`UserByName` must keep returning disabled users for the panel to render
them.

Disabling also revokes the account's web and login sessions, in the same
transaction as the flag — `SetUserDisabled` does the guard, the flag and
`RevokeAuthSessionsForUser` together, so there is no state where the
account is disabled but a session survives, and a failure is reported as
a failure rather than as a half-done disable. An open browser tab stops
working immediately rather than at cookie expiry.

Re-enabling clears `disabled_at` and nothing else. Precisely: API
tokens, kosync slots and koplugin capabilities resume working, because
they were never revoked; **web sessions do not**, because they were, and
the user signs in again. The panel says this next to the button.

### What a password reset does to other credentials

An admin resetting another account's password revokes that account's web
and login sessions, and leaves API tokens, kosync slots and koplugin
capabilities alone — they are separate credentials that a leaked
password did not expose, and silently revoking a household's e-readers
is a bad surprise. Where the password is *known* compromised the
operator wants everything gone, so the user page carries a separate,
explicit "revoke every credential" action.

Both are single store operations, because a credential change that
half-succeeded is worse than one that failed:

- `SetUserPassword(ctx, userID, argon2Hash)` writes the hash and revokes
  the account's auth sessions in one transaction.
- `RevokeAllUserCredentials(ctx, userID)` revokes, in one transaction,
  every API token, kosync device slot, koplugin capability, auth session
  and unredeemed pairing code the account holds. Anything less leaves a
  pairing code that mints a fresh device slot minutes after the operator
  was told everything was gone.

Neither reports success on a partial result.

### High-impact actions re-verify the admin's own password

Reset password, grant or revoke admin, disable or enable an account, and
revoke every credential each require the acting admin to type their own
password in the form, verified with `auth.CheckPassword` before the
mutation. A seven-day session cookie is not a good enough authorization
for taking over another account, and this needs no new auth mechanics —
the verifier already exists and is used by the change-password form.

A verifier reachable from a stolen session is an online password oracle,
so these forms are rate-limited on two independent budgets, both of
which must allow the attempt: one keyed on the acting admin's user id,
so an attacker who moves between addresses still runs out, and one keyed
on the remote address, so one account cannot spend the instance's whole
budget. Both are stricter than the login limiter. Exhausting either
renders the page with a message, like the login form does, and a failed
re-verification logs the same structured line as any other admin action.

### Config is shown, never edited, and never leaks a secret

Configuration is a TOML file read once at startup; there is no reload
path and this ADR does not add one. The overview shows the settings an
operator needs in order to interpret the server's behaviour — listen
address, database driver, content root, retention windows, upload and
quota limits, worker intervals, which adapters are on, open
registration, `insecure_http`, trusted proxies — as read-only facts,
labelled as coming from the config file and taking effect at restart.

`database.url` is **never rendered**: it carries the Postgres password.
The driver is shown; nothing else about the connection is. The card is
written as an explicit list of named fields, never as a walk over the
config struct, so that the next secret added to that struct does not
appear on a web page for free. A test asserts the rendered page contains
neither the DSN nor a password fixture planted in it.

### Workers are reported by their evidence, not by telemetry

The five periodic workers keep no run history, and inventing one (a
`worker_runs` table, heartbeats, a health rollup) is a bigger change
than the panel it would decorate. The maintenance page shows the
aggregate state the database already knows — ingest jobs by state with
the age of the oldest, jobs abandoned mid-flight, books held in review,
books in trash and the next expiry, blob count and bytes — which is
enough to answer "is it stuck?" without naming anybody's book.

There are no "run now" buttons. Every worker is idempotent and periodic;
a button would add a way to start a second concurrent pass over the same
rows, to solve a problem that waiting solves.

### One implementation of each operation, shared with the CLI

Calling the same store method from two places is not enough to keep the
CLI and the panel honest: the rules that matter (password strength, the
watched-root checks, layout parsing, what a grant means, the last-admin
guard's error text) live today in unexported helpers of
`internal/admin`. Those become exported, backend-neutral operations in
that package — `CreateUser`, `SetPassword`, `SetAdmin`, `SetDisabled`,
`CreateManagedLibrary`, `SetLibraryAccess`, `SetLibraryLayout` — taking
a `store.Store`, a context and plain arguments, returning typed errors.
The CLI subcommands become argument parsing over those functions, and
the web handlers become form parsing over the same ones. Validation
lives at the edge as the conventions require; the edge is now shared.

### Admin actions are logged, structured, to `slog`

A persisted audit table needs a migration, a retention policy and a page
of its own, and would be the third retention window in this codebase; it
is deferred. Every cross-user mutation logs one structured line — actor
id, target id, action, outcome — at `INFO`. Secrets never appear in it.

### Shape of the pages

`/ui/admin` becomes a section with sub-navigation. The rail keeps one
Admin entry, shown only to admins.

| Route | Purpose |
|---|---|
| `GET /ui/admin` | Overview: instance facts, counts, config |
| `GET /ui/admin/users` | Users (paginated) and invites |
| `GET /ui/admin/users/{id}` | One user: tokens, devices, libraries, actions |
| `GET /ui/admin/libraries` | Every library, its kind, owner, root and grants |
| `GET /ui/admin/libraries/{id}/review` | One library's review queue, by book id |
| `GET /ui/admin/maintenance` | Aggregate ingest, review, trash, blob state |

Mutations, each `POST` with the per-session CSRF token, redirecting back
with a flash:

`/ui/admin/users`, `/ui/admin/users/{id}/password`,
`/ui/admin/users/{id}/admin`, `/ui/admin/users/{id}/disabled`,
`/ui/admin/users/{id}/credentials/revoke`,
`/ui/admin/users/{id}/tokens/{tokenID}/revoke`,
`/ui/admin/users/{id}/kosync/{slot}/revoke`,
`/ui/admin/users/{id}/koplugin/{deviceID}/revoke`,
`/ui/admin/users/{id}/tokens`, `/ui/admin/users/{id}/pairing`,
`/ui/admin/users/{id}/koplugin`, `/ui/admin/users/{id}/backfill`,
`/ui/admin/libraries`,
`/ui/admin/libraries/{id}/access`, `/ui/admin/libraries/{id}/layout`,
`/ui/admin/libraries/{id}/refresh`,
`/ui/admin/libraries/{id}/review/{bookID}/clear`,
`/ui/admin/maintenance/verify`, and the two existing invite routes.

The panel is a superset of the `admin` subcommands, not a subset of
them: every subcommand has a control here, and the ones that mint a
credential for somebody else — `mint-token`, `pairing-code`,
`koplugin-device` — go through the same re-verification as a password
reset, since each hands out a working way into another account. The
shared implementation lives in `internal/admin` as plain functions over
a `store.Store`, so a library attached from a browser is the same row as
one attached from a shell.

Server-rendered HTML, no new JavaScript, htmx only where a fragment
genuinely beats a reload. The CSP is unchanged, so no inline styles and
no inline scripts.

Lists are bounded two ways, by nature.

Users and libraries grow with the instance, so they are cursor-paginated
in SQL: `AdminListLibraries`, `AdminUserLibraries` and the user list
take a limit, are asked for one row more than they display, and report
`has_more` from that extra row rather than from a second counting query.

A single account's tokens, kosync slots, koplugin devices and an admin's
invites grow with what one person did — bounded by their own devices and
invitations, indexed by `user_id`, tens of rows in practice. Those are
read with the existing per-user methods in full and rendered newest-first
under a stated cap of 200 with the exact remainder, which the handler
knows because it holds the slice. This is a deliberate exception to
"never read what you do not display", taken because six new bounded
query methods to save fifty rows would be machinery bought with
interface surface, and it is stated rather than assumed: if any of these
lists is ever unbounded by construction, the exception is wrong and the
method takes a limit. `AdminLibraryGrants` is the one such list whose
size is set by an *administrator's* choices rather than by one user's,
so it takes a limit and reports `has_more` like the paginated ones.

## Consequences

- The CLI stays, is not deprecated, and gains the four subcommands the
  panel needs to be recoverable from (`grant-admin`, `revoke-admin`,
  `disable-user`, `enable-user`). Both surfaces call one implementation
  in `internal/admin`, so they cannot drift.
- Two schema migrations (`users.is_admin` with its backfill,
  `users.disabled_at`) land in both backends. They are additive and
  appended; no shipped migration is edited. Every `SELECT`, `INSERT` and
  row scan of `users` in both backends must carry the new columns —
  the `storetest` suite is what catches a missed one.
- `IsAdmin` stops meaning "holds an admin token". A token with
  `ScopeAdmin` still authorizes the API's admin operations, but only
  while its owner is an enabled admin account; demotion revokes those
  tokens and authentication rejects any that survive. Existing tests
  that mint an admin token to unlock the UI change to setting the flag.
- The number of hand-enumerated routes in the secure-transport and scope
  matrix tests roughly doubles. Every route above is added to both in
  the commit that adds it.
- `docs/openapi.yaml` documents `/v1` and `/healthz` only; this ADR adds
  no API route, so the contract file is untouched. `docs/deployment.md`
  gains what is now doable without a shell, and the disabled-account
  semantics.
- Version stamping is added for the container image (`.ko.yaml`) as well
  as the static binaries (`build.yaml`), or the overview reports "dev"
  for exactly the deployment most likely to be asking.

## Implementation phases

Each phase is shippable on its own and leaves the panel coherent.

**Phase 1 — Admin as an account property.** The migration adding
`users.is_admin` and `users.disabled_at` with the admin backfill,
`SetUserAdmin` (advisory-locked transaction, last-enabled-admin guard,
revocation of the demoted account's admin-scope tokens), the same lock
and in-transaction role re-read on `CreateToken` and
`UpdateTokenScopes`, `IsAdmin` reading the column, `AuthenticateToken`
rejecting an admin-scoped token whose owner is not an enabled admin, the
CLI's `grant-admin`/`revoke-admin`, and the `mint-token -scope admin`
refusal. No UI, and nothing yet sets `disabled_at`. This phase changes
an existing definition and is first so that nothing is built on the old
one.

**Phase 2 — The section and the overview.** Sub-navigation, the overview
page, `AdminCounts`, the read-only config card with `database.url`
withheld, and build information (`internal/buildinfo`: a `Version`
stamped by `-ldflags -X` in `build.yaml` and `.ko.yaml`, falling back to
`runtime/debug.ReadBuildInfo`'s VCS revision). Invites move to the users
page unchanged.

**Phase 3 — Users.** The shared operations in `internal/admin`, the
paginated user list, the per-user page, create user, reset password
(`SetUserPassword`), grant and revoke admin, revoke a token, kosync slot
or koplugin device, and `RevokeAllUserCredentials` — the high-impact
ones behind the admin's own password and the dedicated limiter.

**Phase 3b — First-run setup.** A one-time onboarding page, added after
Phase 3 at the request of the operator this is being built for. Until
the instance holds any account at all, `/ui/setup` is open and `/ui/`
and `/ui/login` send an anonymous visitor to it; it creates one account
with the admin flag already set and signs it in. Every guarantee here
rests on `CreateFirstAdmin`, a store operation that answers "is this
instance empty?" inside the same locked transaction that inserts, so
that several people opening the page of a fresh instance at the same
moment produce exactly one administrator; the handler's own check
decides only which page to render. There is no CSRF token, because
there is no session to bind one to and no user to act on behalf of; the
POST is rate limited exactly like sign-in, since it is open and hashes
a password. Once anybody has an account the page is gone — it is not a
registration form, and invites remain the only way to add accounts from
outside the admin panel.

**Phase 4 — Libraries.** `AdminListLibraries`, `AdminUserLibraries`,
`AdminLibraryGrants`, `AdminSetLibraryAccess`, `AdminSetLibraryConfig`,
the paginated library list, create managed library, grants, and layout.
Watched libraries are listed but created only by the CLI.

**Phase 5 — Maintenance.** The aggregate page, read-only, no identifying
strings.

**Phase 6 — Disable an account.** The five store-level credential
refusals, the `Login` check, `SetUserDisabled` (guard, flag and session
revocation in one transaction), the toggle on the user page, the CLI's
`disable-user`/`enable-user`, and a test per authentication path. The
column already exists from Phase 1.

## Acceptance criteria

- Every new route is under `requireAdmin`; a non-admin gets the rendered
  403 page on `GET` and a 403 on `POST`, asserted per route. Every new
  route appears in the secure-transport matrix and is rejected over
  plain HTTP when `insecure_http` is false.
- Every new mutation rejects a missing or wrong CSRF token; every
  high-impact mutation rejects a missing or wrong admin password and is
  rate-limited, asserted per route.
- `SetUserAdmin` refuses to clear the last enabled admin, and a
  concurrent test — two goroutines demoting the two remaining admins —
  leaves exactly one admin standing on both backends, PostgreSQL
  included, where the advisory lock is what makes that true.
- Demoting an admin revokes their admin-scope tokens in the same
  transaction, and an admin-scoped token whose owner is not an enabled
  admin is rejected by `AuthenticateToken` even if one survives. A
  concurrent test mints an admin-scoped token while a demotion runs and
  asserts that whichever order they land in, no usable admin-scoped
  token belongs to a non-admin afterwards.
- Disabling the last enabled admin fails the same way. Recovery from a
  disabled administrator is `enable-user`, then `grant-admin` only if
  the role was cleared too; a CLI test walks that path.
- `SetUserPassword`, `SetUserDisabled` and `RevokeAllUserCredentials`
  are atomic: a test that fails the transaction mid-way asserts no
  partial effect, and `RevokeAllUserCredentials` leaves no unredeemed
  pairing code behind.
- Each of the eight authentication paths rejects a disabled user, one
  test each. Re-enabling restores paths 2, 3, 5, 6 and 8; paths 1 and 4
  require a fresh sign-in, which the test asserts rather than assumes.
- No page renders `database.url`, a password hash, a token hash, a
  filesystem path other than a library root (one an operator configured,
  or one they just typed into the attach form), or any string drawn from
  another user's books. A test plants a Postgres DSN with a password and
  a distinctively titled book in another user's library, then asserts
  neither appears anywhere under `/ui/admin`.
- No new store method reads another user's ops, sessions, rollups,
  positions or works. The six new `Admin*` methods are the complete list
  of global reads added, each documented in the interface.
- The user and library lists are paginated in SQL and report `has_more`
  from one extra fetched row; a test with more rows than one page
  asserts the second page and that the first query did not return
  everything. The capped per-account lists render their cap and exact
  remainder rather than silently truncating.
- The re-verification limiter is exhausted independently by user id and
  by address: a test spends the per-user budget from many addresses and
  asserts the next attempt is still refused.
- The panel and the CLI produce the same row: a test creates a managed
  library and a user through the panel and asserts the stored rows match
  what `create-library` and `create-user` write.
