// Reading-session accounting for the web reader (ADR-0030).
//
// A sitting is what POST /v1/sessions carries: when it began and ended,
// where in the book it began and ended, and how much of that span was
// not reading. This module works those out from a sequence of moments
// and nothing else — no DOM, no fetch, no clock of its own — so the
// rules can be tested by stating a sequence and reading off the answer.
//
// Two clocks are involved, as on Android. The bounds are wall-clock
// Dates because that is what the server files a sitting under; the
// arithmetic is done on a monotonic millisecond count the caller
// supplies (performance.now), so a laptop correcting its clock mid-book
// cannot add or remove reading time.

// A gap between two moments of activity longer than this credits the
// threshold and counts the rest as idle. KOReader's statistics cap a
// page's time the same way: a clock cannot tell a difficult page from a
// tab left open on the sofa, so past a point it stops guessing.
export const IDLE_AFTER_MS = 3 * 60 * 1000;

// A sitting with less reading than this is not sent. A mis-click that
// opened a book is not a session.
export const MIN_ACTIVE_MS = 10 * 1000;

function finite(v) {
  return typeof v === "number" && Number.isFinite(v);
}

function clamp01(v) {
  return Math.max(0, Math.min(1, v));
}

// openSession begins a sitting. `id` is the idempotency key the server
// compares payloads under; `now` is monotonic ms; `startedAt` is the
// wall-clock Date; `fraction` may be non-finite, in which case the start
// progression is the first finite one progress() or activity() reports.
export function openSession({ id, workID, startedAt, now, fraction, supportsActiveMs = false }) {
  let last = now;
  let idle = 0;
  let startProg = finite(fraction) ? clamp01(fraction) : null;
  let endProg = startProg;
  let closed = null;

  const settle = (at) => {
    const gap = at - last;
    if (gap > IDLE_AFTER_MS) idle += gap - IDLE_AFTER_MS;
    if (gap > 0) last = at;
  };
  const note = (frac) => {
    if (!finite(frac)) return;
    const p = clamp01(frac);
    if (startProg === null) startProg = p;
    endProg = p;
  };

  return {
    id,
    // progress records where the reader is without treating it as
    // activity. The engine relocates on a resize or a font change as
    // readily as on a page turn, and an untouched page must not earn
    // another three minutes for it.
    progress(frac) {
      if (closed) return;
      note(frac);
    },
    // activity records a moment the reader did something: a key, a
    // tap, a scroll. Ignored once closed.
    activity(at, frac) {
      if (closed) return;
      settle(at);
      note(frac);
    },
    // close ends the sitting and returns the payload to send, or null
    // when there is nothing worth sending: too little reading, or no
    // position ever measured. A second close returns null too, so two
    // unload events cannot produce two sessions.
    close(at, endedAt, frac) {
      if (closed) return null;
      settle(at);
      note(frac);
      closed = true;
      const active = at - now - idle;
      if (!(active >= MIN_ACTIVE_MS)) return null;
      if (startProg === null) return null;
      const started = startedAt.getTime();
      const ended = Math.max(started, endedAt.getTime());
      // The server works active time out as the span less idle_ms, so
      // idle is what the wall clock saw beyond what the monotonic clock
      // credited — as on Android — and a clock corrected mid-sitting
      // lands in idle, not in reading. Bounded to the span because the
      // server refuses anything else.
      const span = ended - started;
      const idleMs = Math.max(0, Math.min(Math.round(span - active), span));
      const payload = {
        session_id: id,
        work_id: workID,
        started_at: new Date(started).toISOString(),
        ended_at: new Date(ended).toISOString(),
        start_progression: startProg,
        end_progression: endProg,
        idle_ms: idleMs,
      };
      // Capability is captured at open: renewing a token cannot change
      // the payload of a sitting already queued for an immutable retry.
      if (supportsActiveMs) payload.active_ms = Math.round(active);
      return payload;
    },
  };
}
