# ADR-0052: A translation cache the account keeps

- **Status:** Accepted; implemented
- **Date:** 2026-10-12
- **Scope:** Client
- **Amends:** [DESIGN §5](../DESIGN.md#57-the-translation-cache)

## Context

Liseur's page translation caches every translated sentence on the
phone, keyed by a SHA-256 over the translation identity — the service,
the model, the languages, the prompt version, and the server address
when a server is used — and the sentence itself. The key is documented
on the client as written to be kept across versions, and it is stable
across devices that translate with the same service, model and
languages. A reader who reads the same book on a second device pays
for every sentence a second time, which for a model on a self-hosted
server is the same book translated twice at the same cost.

The cache is a cache: nothing in it is a record of reading, and the
per-book "left translated" mode beside it is a choice about how this
device opens a book, not data.

## Decision

**The account keeps the sentence cache, per work.** Two routes under
the `sync` scope, `POST /v1/works/{id}/translations` and
`GET /v1/works/{id}/translations`, store and serve the cached
translations of the work's sentences in a `work_translations` table
keyed by `(user_id, work_id, key)`.

**The key is a hash, and that is the privacy bargain — within its
limits.** The client sends only `key` and `translation`; the sentence
itself never reaches the server, and the server treats the key exactly
as it treats an `op_id` — an opaque idempotency key it does not
interpret. A key is 64 hex characters, validated at the handler edge.
The hash is for matching, not for secrecy: a translation says what its
sentence means, and an operator who holds the book can test whether a
given sentence was translated. The bargain is that the original text is
not handed over and nothing is derivable beyond what the translation
already discloses, which is the same disclosure a translation service
already receives; protection from the server's own operator was never
part of it.

**Last write wins, and an identical push is a no-op.** A push is a
batched, atomic upsert: a row is inserted if absent and updated —
translation, `device_id` and `updated_at`, all stamped by the server
from the token and the clock — only when the text differs. An
identical push writes nothing, so `updated_at` does not move and no
puller ever re-downloads a row it already holds. There is no conflict
and no rev: two devices translating the same sentence differently
settle it the way the append-only log settles everything, by whoever
wrote last, and a sentence's translation is worth exactly one answer
whichever it is.

**Pull is a per-work delta feed.** Rows are ordered by
`(updated_at, key)` and paged by an opaque cursor the client stores
per book. The first pull is the whole work; later pulls are the delta.
The order carries the key beside the timestamp because a batch lands
under one stamp, and a cursor that cut at the timestamp alone would
skip or repeat rows sharing it. A cursor that does not parse is
refused, and the client's recovery is to drop its cursor and pull from
the beginning — re-reading, never skipping.

**Caps and eviction.** The push shares `ops.max_batch` with ops: the
same shape, a bounded atomic batch. Per value the bound is
`ops.translation_max_value_bytes` (8 KiB, above the 2,000 characters
the client itself refuses to translate), one pull page is
`ops.translation_max_pull` (500), and the whole cache is bounded in
bytes per user by `ops.translation_budget_bytes` (32 MiB). Over
budget, the oldest-`updated_at` rows are evicted first, inside the
push transaction — a cache, not an archive, and what leaves is what a
pull can fetch again anyway.

**Sentences only.** The scope is the page-translation sentence cache.
The per-book "left translated" mode stays on the device, where
ADR-0050 already put every other reading preference; a book the reader
left translated on one device is a choice about that device's screen.
The web reader's translation and any selection-sheet passage are not
part of this either: the sheet is never cached locally, so there is
nothing to share.

**A local Clear does not clear the server.** The client's Clear drops
the local rows and its pull cursors, so the next pull re-fetches what
the account holds. That is accepted: the Clear is about this phone's
storage, not about the account, and a `DELETE
/v1/works/{id}/translations` route is noted as a possible follow-up
rather than built now.

## Consequences

- A second device translating a page it has never read pulls its
  sentences from the account and calls no translation service for
  them.
- Two devices that spell the same server's address differently — a LAN
  IP on one, a domain on the other — derive different keys, because
  the address is part of the translation identity. The result is a
  cache miss, never a wrong hit; the sentences are simply translated
  twice. Accepted.
- A device translator's translations sync too: the identity names the
  service, not the engine build, so a hit on another device may show
  that device's own device-translation. Accepted, for the same reason.
- A work deleted from the account takes its cache with it (the
  foreign key cascades). Naming one meets the same refusal and the
  same recovery ops have — re-resolve the book, retry under the fresh
  work id — though each route says it its own way: the push answers
  `400 unknown_work` with the `work_id`, the pull a `404` whose JSON
  body says `work not found` (a body the client also uses to tell a
  dropped work from an older server with no route at all).
- No `/v1/events` topic: the feed is pulled on the client's existing
  sync cadence, and a cache a run late is still a cache.

## Implementation

- `work_translations` table and store methods in both backends
  (`PushWorkTranslations`, `PullWorkTranslations`), shared through
  `internal/store/storetest`.
- Handlers in `internal/api/translations.go`, registered under the
  `sync` scope; validation at the edge names the item (`bad_key`,
  `too_large`, `bad_value`, `missing_field`) and refuses the batch
  whole, like an ops batch.
- Liseur's side is the `LiseurSyncTranslations` pass, folded into its
  position sync run like the annotation pass.
