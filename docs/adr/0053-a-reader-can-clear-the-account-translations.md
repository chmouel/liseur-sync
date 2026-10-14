# ADR-0053: A reader can clear the account's translations

- **Status:** Accepted; implemented
- **Date:** 2026-10-13
- **Scope:** Client
- **Amends:** [ADR-0052](0052-a-translation-cache-the-account-keeps.md),
  [DESIGN §5](../DESIGN.md#57-the-translation-cache)

## Context

ADR-0052 put the sentence translation cache on the account and left
clearing it as a follow-up: a device's Clear dropped its own rows and
nothing else, so the account's copy only ever shrank through the byte
budget, a work deleted with its reading, or a work a folder scan
collected. A reader who removes a book, finishes it, or clears the
cache in Liseur's settings had no way to say the account should stop
keeping those translations too.

## Decision

**Two clears under the `sync` scope.**

- `DELETE /v1/works/{id}/translations` drops one work's rows.
- `DELETE /v1/translations` drops the whole account's.

Both answer `200 {"deleted": n}` and are idempotent: clearing an empty
cache answers the same as the first clear, so a client retrying after
a lost answer needs no special case. A work the user does not have is
the pull's `404` with a `work not found` JSON body. A client reads it
as "nothing left to clear" and tells it apart from an older server's
bare `404` for a route it never had. Both run under the work-graph
lock a push takes, so a clear cannot interleave with a merge moving the
same rows to another work.

**A clear is the account's, not every device's.** The server stops
serving the rows; a device that already pulled them keeps its local
copy, and nothing tells it otherwise. There is no tombstone and no
feed entry for a clear: the cache holds nothing a device needs to
forget, and a stale local hit is still a correct translation of its
sentence. A device that translated sentences it has not pushed yet
pushes them after the clear like any other new row.

**The feed keeps moving forward.** Stamps come from the same per-user
high-water as before, so a row pushed after a clear lands behind any
cursor a device held from before it.

## Consequences

- Liseur's remove-from-library dialog, its finish hook, its settings
  Clear and its reader menu can ask the account to forget a book's
  translations, or all of them. The client's side, including how it
  keeps a clear owed while offline, is in
  [its ADR](https://github.com/chmouel/liseur/blob/main/docs/adr/0050-clearing-translations-reaches-the-account.md).
- A second device keeps serving its own copy of a cleared book until
  it clears it too. Accepted: the point of the clear is the account's
  storage, and no device is ever shown a wrong translation.
