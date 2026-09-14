# ADR-0044: Not asking about the page in your hand

**Status:** Accepted
**Scope:** Now
**Amends:** [ADR-0040](0040-asking-rather-than-waiting-to-be-asked.md)

## Context

The catch-up panel asked *Continue from Page 571 of 761 read on another
device?* while the footer said **571 of 761** and the passage it quoted
was the text on screen. The reader was offered a trip to the page in
their hand.

The merge is not wrong. `reconcileReadingState` is a port of Android's
`ReadingStateMerge.kt`, and it compares positions anchor first: two
exact anchors either name the same spot or they do not, and only when
one side has no anchor does it fall back to comparing fractions within
a tolerance. Two clients sitting on one Readium position almost never
spell a CFI the same way, so the anchors differ, the fraction
comparison is never reached, and the other device reads as having moved
away from the agreed baseline. The verdict is `pull`, or `conflict`
when this device also owes a page. That is the right answer to *what
should be stored*: the spots really are different, and a merge that
guessed otherwise would silently drop a position.

It is the wrong answer to *what should be asked*. On open it is worse
than redundant: a `pull` makes the book open **at** the remote position
and then raises the panel about it anyway.

The honest test already existed a few lines away. ADR-0040's presenter
resolves a position to a numbered page through the rendered page table,
and the *Sync this book* dialog refuses to call two positions the same
page unless that table says so on both sides. The panel never asked it.

## Decision

**A question whose answer is the page already on screen is not asked.**
When the offered position and the page the reader is looking at resolve
to the same numbered page, the panel is not shown.

- **The page table settles it, not the anchors.** `samePage()` in
  `reader-place.js` is the one definition, shared with the dialog's
  wording: both sides present, both **exact**, page and total positive
  integers, totals equal, pages equal.
- **An interpolated page is not a match.** *Near page 571* is this
  reader's arithmetic across a fraction, not a page either side
  rendered, and two clients disagree about the whole-book fraction by
  more than the merge's tolerance while sitting on one page — which is
  precisely why the question arises. A guess must not be allowed to
  silence one.
- **The premise is what is painted.** The comparison uses the location
  the view last rendered, never the position an op *claims*.
- **Both variants.** A `pull` and a `conflict` on one page are the same
  non-question.
- **The reconciler is untouched.** A page table is the reader's
  business; the merge decides what to store.

### A suppressed offer is answered, not dropped

It goes through `keepHere()` — the path the *Stay here* button takes —
so the other device's position becomes the agreed baseline and is never
raised again. Dropping it instead would re-ask on the next resume and
every reload.

What that costs on the wire is what was owed already: `keepHere()`
writes no position when this device's page is clean, so a suppressed
`pull` is silent. A `conflict` is dirty by definition, and there the
reader's own page is written down first. Written down, not delivered:
the durable queue is the guarantee, so an answer may rest on a page
that is queued and waiting for the network, and that page is not lost
by being answered on top of. What is refused is settling on a page
recorded nowhere, which is why a failed flush answers nothing and
leaves the question for a quieter moment.

The answer keeps `settled: false`: this device's own position stays its
own. We are on that page, not at that spot.

The premise is re-read at the end rather than trusted from the start,
because a flush takes as long as its storage takes and a resize, a font
change or a reflow moves the reader without a page turn. An offer
refused after its reason has gone would be exactly the silent answer
this decision exists to prevent. That recheck is defensive: with the
durable queue in front of it the window it guards is small, and no
browser test here can prove which of the two guards closed a given
question, so none claims to.

### What ADR-0040 keeps

ADR-0040 treats "the same page, but not the same spot" as a real answer
worth its own wording, and it stays one **when the reader asks**. *Sync
this book* still opens, still shows both passages, still offers *Take
theirs*. Suppressing the unprompted question does give up the other
device's sub-page spot as something the reader might have taken without
asking for it — that is the trade, stated rather than glossed: what
goes is an interruption about a difference the reader cannot see, and
the way to that spot is still one button away.

## Consequences

- A same-page disagreement is settled without the reader seeing it, so
  the panel stops being noise on every resume between two clients
  reading in step.
- The suppression is presentational. Ops, the op log and the merge are
  unchanged, and no route or payload moves.
- Because the answer settles the remote position as the baseline, a
  later position from that device is still offered normally; only the
  one already on screen is spent.
- A question can come to be about the page in the reader's hand after it
  has been drawn. Every page turn takes the panel down with it, but a
  resize, a font change or a reflow moves the reader without one, so an
  offer can end up pointing at the page underneath it with its buttons
  still live. The panel is taken down when the silent answer is
  recorded, and only then: taken down first, it would have to be put
  back whenever the page would not go, and putting it back runs the same
  check again, which flickers the question for as long as the network is
  down. An unanswerable question left standing is one the reader can
  answer themselves.

## Implementation and acceptance

- [x] `samePage()` in `reader-place.js`, unit-tested, and the dialog's
      same-page wording reads through it.
- [x] The panel answers instead of asking, guarded per candidate and
      with the premise re-checked before anything durable is written.
- [x] `catchupState()` coverage that a refused candidate becomes the
      baseline and is never raised again, and that a newer remote
      position is not swallowed by an older one being answered.
- [x] Browser coverage: a same-page `pull` is silent, durable and sends
      nothing new; a same-page `conflict` is answered on a page that is
      queued but undeliverable, and that page is still owed afterwards
      and still becomes the agreed one when it lands; a question already
      on screen comes down when a relocate nobody asked for carries the
      reader onto the page it names; a different page and a *near* page
      of the same number are both still asked; and a book opened cold at
      the other device's position asks nothing.
