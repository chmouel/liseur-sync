# ADR-0061: Several named mirrors

- **Status:** Accepted
- **Date:** 2026-11-03
- **Depends on:** [ADR-0047](0047-mirroring-reading-to-a-koreader-peer.md),
  [ADR-0048](0048-a-second-protocol-for-the-mirror.md)

## Context

ADR-0047 built one outbound mirror for one account, and ADR-0048 gave it
a second protocol. Both assumed a single peer per deployment, because
that was the case in hand: one library served by one other application
on one machine.

It stopped being the case as soon as people ran both. A reader with a
second liseur-sync, a BookOrbit and a KOReader sync server reaches the
same library three ways, and one `[mirror]` table can only name one of
them. Pointing it at another peer means editing the table and
restarting, which is the same thing as not having the other peers.

Two facts about the code made the change smaller than it looks. Mirror
cursors are already keyed `(user_id, peer, work_id)`, where `peer` is
the entry's name, and each cursor is stamped with a digest of protocol,
URL and remote account, so a stored cursor says which library it is
about. Cursors therefore already support several peers; nothing stores
"the mirror".

The one behaviour that does not survive several entries unchanged is
the rule that a position taken from a peer is forwarded to another.
That rule is wanted: it is how two peers stay in step through this
server. But a peer that stamps a write with its own clock, and a route
that does not carry the writer's device back, leave the mirror nothing
to recognise its own writing by. With two such entries each sees the
other's copy as news, applies it, and the next pass pushes it again —
one op per pass per book, forever.

## Decision

**Keep `[mirror]` exactly as it is and add a `[[mirrors]]` list beside
it. Entries are the same kind of thing: a name, a protocol, a peer
address, a local account and a credential. Each enabled entry starts its
own worker.**

The details that are load-bearing:

- **The single table is never converted.** A saved page edits it where
  it is. Converting it to a list entry would stop every
  `LISEUR_MIRROR_*` variable from applying to it, and a deployment whose
  credential comes from the environment would fail to start after the
  next restart. Its cursors and its `<name>:` device prefix carry over
  untouched, which is what an upgrade costs: nothing.
- **A list entry's name is restricted** to `[a-z0-9][a-z0-9-]{0,31}`.
  The name becomes an environment variable name and a URL query value,
  and the restriction is what makes each mapping one-to-one. Names are
  unique case-insensitively across every entry, so a re-enabled table
  cannot quietly take a list entry's bookkeeping.
- **The credential stays in configuration**, as ADR-0047 decided, and
  the environment may own it. The single entry reads
  `LISEUR_MIRROR_REMOTE_KEY` / `LISEUR_MIRROR_REMOTE_PASSWORD`; a list
  entry named `n` reads `LISEUR_MIRRORS_<N>_REMOTE_KEY` /
  `…_REMOTE_PASSWORD`, with the name upper-cased and its dashes turned
  into underscores. Naming the variable after the entry, rather than by
  position, is what stops a removed entry handing its credential to the
  one that moved up. An environment credential overrides the file, is
  used to test the connection, and is never written to the file. A
  variable that is set but empty refuses the entry rather than falling
  back to a stale one.
- **Any non-secret `LISEUR_MIRROR_*` variable makes the single entry
  read-only in the browser.** The environment would override what a form
  saved at the next start, and a form that reports success for values
  nothing reads is worse than one that says who owns them.
- **There is one mirror per canonical remote account.** The key is
  the peer's host (case-folded, one trailing dot ignored), its port when
  it is not the one the scheme implies, its path with a documented API
  root removed (`/api/v1` or `/api/v1/koreader`, whatever the protocol),
  and the case-folded remote username. So BookOrbit's native API and its
  KOReader route on one installation are one target, as are `:443` and no
  port. It is one mirror whatever local account or protocol is behind it:
  a second route shares one remote reading position, so two local accounts
  would merge their reading through it — a cross-user leak between readers
  of this server — and two routes for one local account would leave two
  workers racing over it with nothing to reconcile them. The duplicate is
  refused before anything is written rather than discovered later. The
  single table is included in this and in the name rule. One reader may
  still mirror to several different peers: those are several remote
  accounts.
  The check sees an address, not a server: a BookOrbit account whose
  KOReader username is a different pair (ADR-0048) still looks like two
  targets, and keeping those apart stays the operator's job.
- **A peer position that agrees with the newest local op is a no-op.**
  Agreement is judged at the finest detail both sides can carry, in both
  directions — a CFI against a CFI, an engine position against an engine
  position, and the fraction only when neither side says more. That is
  what stops a restamped loop without discarding a real move: two places
  at the same percent of a book are not the same place, and a peer that
  names one is still saying something new. When one side names the same
  place more exactly than the other, that difference is reported as such:
  the finer local spot is sent to the peer once (a push appends nothing,
  and the watermark makes it once per op), and a finer peer spot is never
  overwritten by a coarser local copy. A protocol that cannot carry a CFI
  is not asked to echo one. A position at the very start is excluded,
  because there the two protocols disagree about whether it is a place or
  a reset, and the existing rules already answer that.
- **Peers of one account decide one at a time.** A worker asks its peer
  before it knows what the other worker of the same account will do with
  the answer. Once it has an answer, it takes a per-account lock, re-reads
  the newest op and commits its decision before letting go. Network calls
  stay outside the lock, so one slow peer cannot stall the others; the
  lock covers database time only.
- **A configuration change needs a restart.** There is no hot reload.
  Swapping a worker out mid-pass would have an old worker finishing on
  the cursors a new one just started from, and a supervisor to manage
  that is a bigger thing than this page. The admin page says so, and
  reads the file on every request so a saved-and-disabled entry is still
  there to edit while the running process keeps what it booted with.
- **The config file is rewritten surgically.** Only the section that
  changed is rewritten; the rest of the file keeps its bytes, its
  ordering and its comments. The result is decoded and compared against
  the original — mirror fields cleared on both — before it replaces
  anything, which is what turns a line-oriented scan meeting a `[mirror]`
  inside a multi-line string into a refusal instead of a mangled file.
  The write follows symlinks and renames into place. The mode comes from
  what is about to land: a file that will hold a stored credential is
  made owner-only (`0600`), a secretless file keeps the permissions it
  had, and a new file is created `0600`. A save that changes nothing
  still tightens a file that already holds a credential, and a refused
  save changes neither the bytes nor the mode. Comments inside a
  rewritten `[[mirrors]]` section are not preserved: the section is
  written whole, because a field that was switched off has to stop
  existing rather than linger.

## Consequences

- A database migration is not needed, and neither is a new table. The
  only state several entries add is more rows in `mirror_cursors`, keyed
  by name.
- Forwarding between peers is kept. Two peers of one account stay in
  step through this server instead of drifting until a book is opened
  here. The cost is the agreement rule above: a peer position that says
  exactly what this server already holds no longer appends an op, even
  when its timestamp is newer.
- A setup where two peers also sync with each other, and change the
  position on the way, can still loop: this server would see a position
  it does not hold on every pass. The symptom is
  `mirror took a position from the peer` for one work every pass.
  Configuring peers to reach each other directly is not supported.
- **Downgrading is not possible after adding a `[[mirrors]]` section.**
  `Load` refuses a configuration key it does not know, so an older image
  refuses to start on a file that carries the list. This is the price of
  refusing unknown keys, which is itself a rule worth keeping:
  `docs/deployment.md` says so where an operator will read it before
  rolling back.
- Environment secrets are readable by the process and by whoever can
  read its environment, exactly as before; nothing new is exposed. The
  page never renders a credential, and the KOReader client now redacts
  the key from a peer's own error text the way the native protocol
  already did.
- Removing an entry leaves its cursors behind. They are keyed by name,
  so adding the same name and peer back resumes them, and a name that
  never comes back costs a few rows nothing reads.

## Implementation phases

1. **Configuration.** `Mirrors` beside `Mirror`, per-entry defaults,
   name and duplicate-target validation, `EnabledMirrors`, environment
   credentials by name, and the rule that makes an environment-managed
   single entry read-only. Done.
2. **The file writer.** `ReadMirrors` / `UpdateMirrors`, section-level
   rewrite, semantic proof, symlink and permission handling. Done.
3. **The agreement rule and credential redaction.** `samePlace` in the
   syncer, the KOReader client's redaction, one regression test with two
   restamping peers. Done.
4. **One worker per entry.** `mirrorRunners` in `cmd/liseur-sync`, each
   waited for on shutdown. Done.
5. **The admin page.** A list, an add form, an edit form per entry, a
   remove form, admin password re-verification, one audit line per
   mutation. Done.

## Acceptance criteria

- A deployment that upgrades with an existing `[mirror]` table mirrors
  as before, with the same name, the same cursors and the same device
  prefix, and needs no edit.
- Two enabled entries with one peer account are refused before the file
  is written, and the file is unchanged after the refusal.
- A list entry's credential comes from its own variable, is used for the
  connection test, and is never written to the file or rendered.
- A position read here reaches every peer once and stops moving the op
  log after that, even when the peers restamp what they are told.
- A peer position that differs only in the exact spot it names is still
  applied.
- A save rewrites only the section it changed, keeps the file's
  permissions and symlink, and refuses rather than corrupts a file whose
  text contains a table header inside a string.
- A wrong password, a forged CSRF token, or a non-administrator writes
  nothing and leaves one audit line.
