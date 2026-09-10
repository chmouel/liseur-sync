# ADR-0040: Asking, rather than waiting to be asked

**Status:** Accepted
**Scope:** Now
**Amends:** [ADR-0037](0037-durable-online-reading.md)

## Context

The web reader reconciles two reading positions the same way the Android
app does — `reader-reconcile.js` is a port of `ReadingStateMerge.kt` —
and when it finds a disagreement it raises a small panel at a quiet
moment: *Continue there* or *Stay here*. That panel is good at what it
does, and everything it does is on its own initiative.

Two things a reader can do on the phone had no equivalent here.

**Pulling down to mean "look again".** The Android library refreshes on
a pull: scan the folders, refresh the catalog, sync the positions, with
a spinner that only the gesture shows — a quiet startup scan never
spins. In a browser there was no way at all to say *look again* short of
reloading the page, and a reload on the offline shelf is the one thing a
reader there cannot rely on.

**Asking about one book.** `BookSyncDialog` shows *both* positions —
page or percent, how long ago, the passage that was on screen — and
offers three answers: take theirs, keep mine, cancel. Nothing in the web
reader could be asked. It could only be waited for, and what it said
about a position was a single percentage: 42%. A percentage is not
something a reader can check. The page in their hand is.

## Decision

### A pull is a command, and so is a button

The library page and the offline shelf answer a pull-down, and both also
carry a visible refresh button. They are not a gesture with a fallback
bolted on: they are one command, run through one single-flight runner,
so a second pull joins the run already under way rather than starting a
second one. That is the same mutex Android's `LibraryRefresh` holds, for
the same reason.

The command is: **deliver what this browser read offline, then draw what
the server has now.** In that order, so a page turned offline is on the
server before the shelf is drawn from it. It does *not* ask the server
to walk its folders — the library page already has a Scan button for
that, and a gesture that quietly starts a filesystem pass would be a
different and much larger promise. Both halves are best-effort: a failed
drain still lets the page redraw, and says so.

The spinner belongs to the gesture, exactly as on the phone. A refresh
nothing asked for must not spin.

**The browser's own pull-to-reload is the obstacle, and CSS is the only
reliable switch.** Chrome on Android and Safari from 15 reload the page
on the same drag, and they commit to it before the first cancellable
`touchmove` arrives; `preventDefault()` alone does not stop them.
`overscroll-behavior-y: contain` does. So the gesture is armed only
where `CSS.supports('overscroll-behavior-y', 'contain')`, on a coarse
pointer, and the suppression is written onto the page's own scrolling
element from JavaScript while it is armed — not dropped on `html` in the
stylesheet, where it would change scroll chaining on every `/ui` page on
every desktop for no gain. Where it is not supported (older iOS Safari),
the native reload is left alone: a reload is a coarse but honest
refresh, and the button remains the precise path.

The reader is deliberately excluded. The paginator binds the same touch
stream, and competing for one drag is how a page turn becomes a refresh.

### A position is a place, and a reader can be asked about one

One presenter (`reader-place.js`) describes a side, and both surfaces
use it, so the panel and the dialog cannot drift apart about what a
position *is*:

- **A page**, counted from Readium positions, which is the same number
  the phone shows for the same spot. *Page 212 of 480* when the op named
  a resource this copy also has — that lands in the right chapter
  whoever wrote it. *Near page 212* when only a whole-book fraction
  survived, because clients do not compute that fraction the same way
  and a page derived from it is an interpolation. A percentage when the
  book has not been measured. An interpolated page presented as an exact
  one is a lie with consequences, so the two are never spelled alike.
- **How long ago.** A clock is not evidence about which position is
  right — that is decided by movement away from an agreed baseline, and
  never here — but it is what lets a reader recognise their own evening.
- **The passage that was on screen**, from `locator.text.highlight`,
  which `reader-anchor.js` already writes in exactly the shape Android's
  `ExactLocatorAnchor` reads. It is another device's text: it is set as
  a text node, capped by the presenter and clamped to two lines, because
  a partner sending a chapter must not push the buttons off the screen.

**The catch-up panel gains those three facts** and changes nothing else:
it still appears only at a quiet moment, and either answer still settles
the baseline durably.

**The reader can now ask.** A sync button in the reader's top bar, with
`s` as its shortcut and a row in the shortcuts dialog, reads the newest
position from the server and always opens a dialog — even when the two
sides agree, because the reader asked, and a button that silently does
nothing is worse than one that says *nothing to do*.

Its verdicts are a port of `BookSyncChoice`, in
`reader-sync-choice.js`, decided on two positions and a flag with no
navigator, database or server in sight:

- nothing on the server;
- in step, including a difference the reader has already answered;
- **owed** — only this device has moved, so there is one position and
  the server has an older copy of it: nothing was preserved to adopt,
  and the only true answer is to send;
- **unreadable** — the server's position cannot be opened in this copy
  of the book, so there is nothing to adopt however far ahead it looks;
- only the server has a position, so there is nothing to choose between;
- a real choice, with the relation named: the other device is ahead,
  this one is, or it is the same page but not the same spot.

That last one is worth its own words. Two identical page numbers over
two different buttons is a riddle, and the excerpt is the only thing
that tells the sides apart.

**The agreed baseline is what makes this answerable.** This reader's own
last position, read back from the server, has exactly the shape of
another device: the same fields, a different page from wherever the
reader has since got to. The only thing that tells them apart is which
side moved away from what the two of them last agreed on — which is what
the three-way merge already decides. So the button asks the same
reconciler an automatic sync asks, and the two can never disagree about
which side moved. Deciding it on the device id instead would have been
wrong in a subtler way: a device id is a label two credentials may
share, and the question is about movement, not about hardware.

The three answers are the phone's. *Take theirs* runs the same journey
the panel's *Continue there* runs — settle, withdraw this device's
undelivered pages, end the sitting, then travel — because it is the same
journey. *Keep mine* settles the other position as agreed and sends this
page, so the other device learns it. **Cancel changes nothing**: not the
page, not what the two sides have agreed on. It is deliberately not "not
now" — an ordinary sync will settle the same disagreement later exactly
as it would have done, so cancelling buys no delay and pretends to none.

A second side is shown only when there is one to compare with. This
reader's own position sitting on the server is not another device, and
putting it under *Another device* would say something untrue.

## Consequences

- No new dependency and no build step: plain ES modules served from this
  origin, which is what the `/ui` CSP requires. The indicator's
  transform is written through CSSOM, which the policy does not govern —
  it blocks `<style>` and `style=` in markup.
- Both pages keep working with JavaScript off. A page still refreshes by
  being loaded, and a position still reconciles on open.
- `overscroll-behavior-y: contain` also stops scroll chaining out of the
  page's scroller while the gesture is armed. That is wanted, and it is
  why it is scoped rather than global.
- The catch-up panel says more than it used to, so the browser suite
  reads the whole panel rather than one element: the question names a
  page, and the detail line under it carries the percentage every client
  agrees on.

## Implementation and acceptance

- [x] A pure gesture state machine and a single-flight refresh runner,
      unit-tested without a DOM: a drag that claims a sideways swipe or
      an ordinary scroll is one that has taken something from the
      reader.
- [x] Arm it on the library page and the offline shelf, with a visible
      refresh button raising the same spinner, and suppress the native
      pull only where the gesture is armed.
- [x] Drain the offline outbox before redrawing, both halves
      best-effort.
- [x] One presenter for a side — exact page, near page, or percent, plus
      age and a capped excerpt — unit-tested.
- [x] The catch-up panel uses it, and shows the other device's passage
      as text.
- [x] The verdicts, unit-tested — including that this device's own older
      page on the server is owed rather than offered, and that a
      difference already answered is not asked again.
- [x] The top-bar button, its `s` shortcut, its help row and the dialog,
      with take, keep and cancel wired to the paths the panel already
      uses.
- [x] Browser coverage: the dialog's answers, that cancelling moves
      nothing, that a cancelled question can be asked again, and that
      answering in the dialog does not leave the same question open in
      the panel.
