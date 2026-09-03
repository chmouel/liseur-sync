# ADR-0030: The web reader counts its sittings

- **Status:** Accepted; implemented
- **Date:** 2026-09-03
- **Amends:** [ADR-0007](0007-web-reader.md), which said the reader
  uses `/v1/sessions` "exactly as the Android and desktop clients do"
  and, until this decision, did not

## Context

Reading statistics are computed from `sessions` rows and their daily
rollups (design §6). A position pushed to `/v1/ops` moves "Continue
reading" and nothing else. The web reader pushed positions only, so an
evening spent reading in a browser advanced the book and added zero
minutes, zero sessions and no day to a streak — a dashboard that
quietly disagreed with the reader who had just used it.

ADR-0007 already lists `/v1/sessions` among the routes the reader uses.
The server side was in place: the route exists, validates the payload
(progressions in `[0,1]`, `ended_at ≥ started_at`, `idle_ms` no longer
than the span), and the reader's short-lived token carries the `sync`
scope it needs. What was missing was the client deciding what a
sitting is in a browser.

A browser is not a phone. There is no foreground/background callback
pair; there is `visibilitychange`, `pagehide` and `beforeunload`, of
which only the first is reliable on mobile. There is no local database
to hold an unsent session across a crash. And there is no way to tell
a difficult page from a tab left open all night behind another one.

## Decision

The reader records a session per sitting, with these rules. The
arithmetic lives in one pure module (`static/reader-session.js`) with
no DOM and no network in it, so the rules are tested by stating a
sequence of moments and reading off the answer.

**A sitting is bounded by visibility.** It opens once the book is on
screen with a work resolved and a position measured, and it closes —
and is sent — when the document becomes hidden, on `pagehide`, or on
`beforeunload`, whichever comes first; the later events find it already
closed and do nothing. Coming back to a visible tab opens a new sitting.
This is what Android does: leaving the foreground finishes a session,
and returning starts one.

**Idle is a cap, not a timer.** A key, a tap or a scroll is activity.
A gap between two moments of activity — or between the last one and the
close — longer than **three minutes** credits three minutes of reading
and counts the rest as idle. KOReader's statistics cap a page's time the
same way. Computed from the sequence of moments, it needs no timer and
cannot drift while a tab is throttled.

A relocate is not activity. The engine relocates on a resize or a font
change as readily as on a page turn, so an untouched page would earn
another three minutes for every layout pass. A relocate updates where
the sitting is; only input says somebody was there.

**Two clocks, as on Android.** The bounds are wall-clock Dates because
that is what the server files a sitting under. Reading time is counted
on `performance.now()`. The server derives active time as the span less
`idle_ms`, so `idle_ms` is sent as the span less the monotonic reading
time — the same arithmetic as the Android client's `finish` — and a
laptop correcting its clock by an hour mid-book puts that hour in idle,
not in reading. Clamped to `[0, span]` because the server refuses
anything else.

**A sitting under ten seconds of reading is not sent.** A mis-click
that opened a book is not a session. Idle time does not count towards
the ten seconds.

**Progressions are what was measured.** `start_progression` is the
first finite fraction seen in the sitting, `end_progression` the last;
a non-finite fraction is never a progression (the same guard as the
position-jumps fix), and a sitting that never measured one is not sent.

**The payload is built once and replayed byte for byte.** The session
id is drawn when the sitting opens; the payload is built at close and
kept until the server confirms it, exactly as the reader already does
for a position op. A `2xx` or a `409` (the id already names this
sitting) settles it; any other `4xx` — an unknown work, say — is dropped,
because replaying the same refused payload cannot help; a server error
or no answer leaves it to be replayed on the next activity or close. The
request is sent with `keepalive` so the close that unloads the page
still delivers.

**Nothing is persisted locally.** A tab that crashes, or whose final
request is lost, loses that sitting. The Android client keeps an open
session in its database and closes it at the last checkpoint on the next
launch; the browser has nowhere as trustworthy to do that, and a stored
half-sitting replayed from `localStorage` a week later would be a guess
wearing a timestamp. This is a known, bounded loss and can be revisited.

`edition_sha` is omitted: the reader resolves a work, not an edition,
and the server treats the field as optional.

## Consequences

- Reading in the browser now counts. Minutes, sessions, streak days and
  pace on the dashboard and under `/v1/insights` include it, with
  `origin: native` and the browser's stable device identity.
- The sessions figure rises with every tab switch, because each
  visible stretch is one sitting. Android behaves the same way.
- `idle_ms` is an estimate with a three-minute floor per page, as
  KOReader's is. A reader who genuinely spends ten minutes on a page is
  credited three. That is the trade the cap makes on purpose.
- A reader who turns pages only through a relocate the page cannot see
  as input — a browser extension scrolling for them, say — is credited
  three minutes per sitting. Every page turn the reader itself offers
  is a key, a tap or a scroll, so this is an edge, not a path.
- Pages stay at zero for a reflowable book read this way unless the
  edition has a page count, per the rule that page numbers are never
  fabricated.
- A crashed tab loses its sitting. Position is unaffected: it was pushed
  on every page turn.
- Testing: the pure module has node unit tests that run in CI without a
  browser (`TestReaderSessionAccounting`); the wiring is proved in a
  real Chromium by `TestReaderRecordsReadingSessions`, which winds both
  clocks forward, fakes the document's visibility, and reads the rows
  back from the store.
