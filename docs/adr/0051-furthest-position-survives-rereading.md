# 51. The furthest position survives rereading

Status: Accepted

## Context

A reader can see a server position at 70%, keep reading from 30%, and
upload 31%. The latest operation then correctly says 31%, but the clients'
single remote candidate no longer provides a reliable way back to 70%.
Recent history is bounded, and compaction keeps daily last operations and
device heads rather than historical maxima.

## Decision

Retain the original maximum-progression operation per user, work, edition,
and origin alias. Equal fractions retain the earliest sequence. The edition
and alias groups match the ownership boundaries of a work split, so splitting
one edition off cannot lose its former maximum. A work's overall furthest
position is the greatest progression among its candidates.

Compaction preserves these operations alongside its existing survivors.
The existing log remains authoritative; no second payload table, synthetic
operation, or new sequence is needed. Merges and splits move the original
operations, and work deletion removes them. Compaction takes the work-graph
lock used by appends and identity changes.

The native per-work positions response and heads snapshot add a separate
`furthest` array with full operation payloads, read in the same transaction
as the existing response data. History limits do not limit this array.
The changes feed and legacy kosync response keep their existing semantics.

Clients retain every observed maximum before replacing pending state or
collapsing a feed page to its newest operation. Locally authored peaks
survive offline coalescing too. An explicit action restores the chosen
historical destination using the existing edition-aware locator ladder.
It does not become the current position merely because it is farther.

Navigation jumps count. Rereading, marking unread, and declining a sync
offer do not lower the maximum. There is no reset operation. Preserve the
clients' existing current-position conflict policies, including Android's
automatic farther-side opening with a way back.

## Consequences

The retained maximum survives the retention window with its exact original
payload. Cross-edition navigation can still be approximate, and the server
cannot recover history an older version already compacted. Operations
never recorded or delivered are also outside the server's guarantee.

Server retention grows by at most one maximum per ownership group beyond
the existing heads and daily snapshots. An index supports grouped maximum
reads. Clients on older servers can retain local observations, but missing
`furthest` support does not certify complete server history.

## Implementation phases

1. Server retention, snapshot fields, migration index, and ownership tests.
2. Android and web durable maxima with explicit candidate-bound navigation.
3. Cross-client handoff, offline replay, and compaction regression coverage.

## Acceptance criteria

- After 70% is seen and 31% is uploaded, latest remains 31% and an explicit
  furthest action can still restore the original 70% destination.
- More than 200 lower writes, compaction, and snapshot recovery preserve it.
- Equal fractions, zero, small advances, and edition/alias splits retain
  deterministic whole records without crossing users.
- Declining an offer and ordinary current-position reconciliation keep
  their previous meaning.
