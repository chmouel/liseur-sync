# ADR-0042: A page count the server does not have

**Status:** Deferred
**Scope:** Later

## Context

A sitting is stored with a start and an end progression — two fractions
of the whole book — and, for native clients, nothing else about length.
Turning that into *pages* needs the book's page count, which lives on the
`editions` row for the sitting's `edition_sha`. `insights.Pages` does
that multiplication, and refuses when the edition is absent:

```go
if s.ReportedPages != nil { return *s.ReportedPages, nil }
if s.EditionSHA == nil || progDelta <= 0 { return 0, nil }
// ... otherwise the edition must be there, or ErrMissingEdition
```

The refusal is deliberate and correct at the point it is made: reporting
0 pages for a sitting that covered 8% of a book would be a lie the reader
cannot detect, and a lie that then accumulates into a rollup that is
never recomputed. Failing closed is the right default for a number that
becomes permanent.

But the refusal propagates all the way out. The rollup job defers the
whole batch — which is right, because a batch that cannot be applied must
not be half-applied — and the read paths return 500. A reader whose
account contains one book the server cannot measure loses the statistics
screen entirely, for every book, until somebody notices.

`internal/insights/diagnostics.go` and the `slog.Warn` on the read paths
exist so the operator can find out *which* edition is missing. That is
the right first move and it is already made. It does not answer what the
reader should see in the meantime.

Today the state is unreachable through ordinary writes: both backends
hold a deferred composite foreign key from a session to its edition, and
the only path that deletes an edition row (the work merge) moves it to
the surviving work first. So this is a decision about what happens when
that invariant is relaxed or breached, not about a bug in flight.

## Decision

*Not decided. What follows is the shape of the choice.*

The question is whether a total that is *known to be short* is better or
worse than no total at all, and the honest answer depends on whether the
reader is told.

### What is not on the table

**A silent 0.** A sitting with real progression contributing nothing, with
nothing said about it, is the one outcome that is clearly wrong: it makes
the number smaller in a way that looks like less reading rather than like
missing data. The gist that preceded this work already ruled it out and
that should stand.

**Changing what a rollup means after the fact.** A bucket is written once
and accumulated into. If a sitting is counted as 0 pages because its
edition was missing, and the edition later appears, nothing recomputes
the bucket. So any graceful degradation at rollup time is permanent in a
way that graceful degradation at *read* time is not.

### The distinction that probably decides it

**The rollup path should keep failing closed.** It writes a number that
is never revisited, under a lock, and deferring costs nothing but a
delay. The sittings are still there; the next run will succeed once the
edition is restored.

**The read path is where the reader is standing.** A statistics screen
that says *3,400 pages, plus one book we cannot measure* is more useful
than a 500, and it is honest in a way a bare 3,400 would not be. That
suggests the read paths should compute what they can, count what they
could not, and say so in the response — a field the client can render as
a footnote rather than an error page.

If that split is taken, the API gains a shape it does not have yet: a
successful statistics response that carries a named incompleteness. That
is the same idea as `SyncOutcome.Incomplete` on the client side, and it
should probably borrow the vocabulary rather than invent one.

## Open questions

- What does the Android stats screen do with "and some reading we could
  not measure"? A footnote, a chip, or nothing visible at all?
- Does the incompleteness name the book, or only its existence? Naming it
  is more useful and leaks slightly more into the response shape.
- Should the web reader's statistics view and the API answer agree here,
  or is the web view allowed to be terser?
- Is there a case for the server *estimating* a page count from the
  file's size or spine when the edition row is missing, or is an estimate
  exactly the undetectable lie this ADR is trying to avoid?
