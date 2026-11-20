# ADR-0068: Vocabulary Builder words and their reviews sync

- **Status:** Accepted
- **Date:** 2026-11-20
- **Depends on:** [ADR-0034](0034-live-notifications-say-only-that-something-changed.md)

## Context

Liseur keeps a Vocabulary Builder: words collected while reading, with
a definition, notes, the sentences they were seen in, and a
spaced-repetition schedule driven by review ratings. It lived on one
phone. A reader with a phone and a tablet saw two unrelated lists and
two schedules that disagreed about what was due.

A word is edited (definition, notes, sentences) and a schedule is
appended to (one review per rating). Merging those two the same way
loses one or the other: a last-writer-wins word drops a concurrent
review, and a log of word snapshots cannot say which device's rating
came first.

## Decision

**Words are compare-and-set documents; reviews are immutable events.**
Both are per-user reading state under the `sync` scope.

- A word is keyed by `(language, normalized)` and carries `rev` for
  compare-and-set, and `generation`, which starts at 1 and increments
  only when a tombstone is written over by a live word. A delete
  tombstones the word and, in the same transaction, every live review
  for it.
- A review is keyed by a client-chosen `review_id` and names the word
  and the generation it was written against. A live review is accepted
  only while that word is live at that generation, so a rating made
  before a delete cannot attach to the word collected again afterwards.
  An undo is a review tombstone. A retry is answered from the review id
  before the word is checked, so it survives the word's deletion.
- Live schedule state (due date, streak, interval) is never stored: each
  client derives it from the review events. An `imported_baseline` on
  the word carries a schedule that predates the events (a word imported
  from a file, or reviewed before sync existed).
- `GET /v1/vocabulary/changes` reads words and reviews in one per-user
  `seq` order from one snapshot, bounded by a byte budget and always
  emitting at least one item. A third counter in `seq_counters` keeps
  this feed independent of the op and annotation logs.
- Word and review writes are batched and never atomic: one result per
  item (`applied`, `duplicate`, `conflict`, `invalid`). Both run under
  the per-user work-graph lock.
- Changes are announced on the `vocabulary` live topic.

## Consequences

- Concurrent reviews on two devices both survive; the schedule is the
  same on every device once both have pulled.
- A conflicting word write returns the server's copy; the client merges
  field by field and retries against the new `rev`.
- Words are bounded (`ops.vocabulary_max_words`, 50,000 by default), as are
  every text field, the occurrence count and the item size.

## Acceptance criteria

- Shared store tests cover the lifecycle, delete cascade, resurrection,
  undo, stale generations, paging and the byte budget on SQLite and
  PostgreSQL.
- Liseur pulls, merges and pushes vocabulary over these routes.
