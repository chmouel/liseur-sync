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
// `mode` is "reading" (default) or "listening" (audiobook playback, where
// active time is accumulated strictly between playing() and paused()
// transitions rather than inferred from page-turn idle thresholds).
export function openSession({
  id, workID, startedAt, now, fraction, supportsActiveMs = false, mode = "reading", checkpoint = null,
}) {
  const sessionMode = (checkpoint?.mode || mode) === "listening" ? "listening" : "reading";
  const originalStartedAt = checkpoint?.startedAt
    ? new Date(checkpoint.startedAt)
    : startedAt;
  const sessionID = checkpoint?.id || id;
  const checkpointProgress = checkpoint || {};
  const openedAt = now;
  let activeBase = finite(checkpointProgress.activeMs)
    ? Math.max(0, checkpointProgress.activeMs)
    : 0;
  let last = now;
  let idle = 0;
  let playingSince = null;
  let startProg = finite(checkpointProgress.startProgression)
    ? clamp01(checkpointProgress.startProgression)
    : finite(fraction) ? clamp01(fraction) : null;
  let endProg = finite(checkpointProgress.endProgression)
    ? clamp01(checkpointProgress.endProgression)
    : startProg;
  let closed = null;

  const settle = (at) => {
    if (sessionMode === "listening") {
      if (playingSince !== null && at > playingSince) {
        activeBase += at - playingSince;
        playingSince = at;
      }
      return;
    }
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
    id: sessionID,
    mode: sessionMode,
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
    // playing marks the start (or continuation) of active audio playback
    // for a listening session.
    playing(at, frac) {
      if (closed || sessionMode !== "listening") return;
      note(frac);
      if (playingSince === null) {
        playingSince = at;
      } else if (at > playingSince) {
        activeBase += at - playingSince;
        playingSince = at;
      }
    },
    // paused marks the end of an active audio playback interval (pause,
    // seeking, waiting, stalled, ended, or error) so non-playing spans
    // are excluded from active_ms.
    paused(at, frac) {
      if (closed || sessionMode !== "listening") return;
      note(frac);
      if (playingSince !== null) {
        if (at > playingSince) activeBase += at - playingSince;
        playingSince = null;
      }
    },
    // close ends the sitting and returns the payload to send, or null
    // when there is nothing worth sending: too little reading, or no
    // position ever measured. A second close returns null too, so two
    // unload events cannot produce two sessions.
    close(at, endedAt, frac) {
      if (closed) return null;
      if (sessionMode === "listening") {
        if (playingSince !== null) {
          if (at > playingSince) activeBase += at - playingSince;
          playingSince = null;
        }
      } else {
        settle(at);
      }
      note(frac);
      closed = true;
      const active = sessionMode === "listening"
        ? activeBase
        : activeBase + at - openedAt - idle;
      if (!(active >= MIN_ACTIVE_MS)) return null;
      if (startProg === null) return null;
      const started = originalStartedAt.getTime();
      const ended = Math.max(started, endedAt.getTime());
      // The server works active time out as the span less idle_ms, so
      // idle is what the wall clock saw beyond what the monotonic clock
      // credited — as on Android — and a clock corrected mid-sitting
      // lands in idle, not in reading. Bounded to the span because the
      // server refuses anything else.
      const span = ended - started;
      const idleMs = Math.max(0, Math.min(Math.round(span - active), span));
      const payload = {
        session_id: sessionID,
        work_id: workID,
        started_at: new Date(started).toISOString(),
        ended_at: new Date(ended).toISOString(),
        start_progression: startProg,
        end_progression: endProg,
        idle_ms: idleMs,
      };
      if (sessionMode === "listening") payload.mode = "listening";
      // Capability is captured at open: renewing a token cannot change
      // the payload of a sitting already queued for an immutable retry.
      if (supportsActiveMs) payload.active_ms = Math.round(active);
      return payload;
    },
    checkpoint() {
      if (closed) return null;
      const active = sessionMode === "listening"
        ? activeBase
        : activeBase + Math.max(0, last - openedAt - idle);
      const cp = {
        id: sessionID,
        workID,
        startedAt: originalStartedAt.toISOString(),
        startProgression: startProg,
        endProgression: endProg,
        activeMs: Math.round(active),
        supportsActiveMs,
      };
      if (sessionMode === "listening") cp.mode = "listening";
      return cp;
    },
  };
}

export function resolveListeningCheckpointFraction(checkpoint, fallback) {
  if (checkpoint && finite(checkpoint.endProgression)) return clamp01(checkpoint.endProgression);
  if (checkpoint && finite(checkpoint.startProgression)) return clamp01(checkpoint.startProgression);
  return finite(fallback) ? clamp01(fallback) : undefined;
}

export function resolveSessionEndFraction(session, liveAudioFraction, pageFraction) {
  if (!session) return undefined;
  if (session.mode === "listening") {
    if (finite(liveAudioFraction)) return clamp01(liveAudioFraction);
    return resolveListeningCheckpointFraction(session.checkpoint?.(), pageFraction);
  }
  return finite(pageFraction) ? clamp01(pageFraction) : undefined;
}

export function handleVisibilityHiddenSession({
  session,
  audioSessionOpen,
  audioPlaying,
  now,
  audioFraction,
  onCheckpoint,
  onEndSession,
}) {
  if (session && session.mode === "listening" && audioSessionOpen) {
    if (audioPlaying) {
      session.playing(now, audioFraction);
    }
    onCheckpoint?.();
    return "preserved";
  }
  onEndSession?.();
  return "ended";
}

export function planAudioTicketRefresh({
  expiresAt,
  previousExpiresMs = NaN,
  nowMs = Date.now(),
  preserveState = false,
  forceSwap = false,
}) {
  const expiresMs = typeof expiresAt === "number" ? expiresAt : Date.parse(expiresAt || "");
  if (!finite(expiresMs)) {
    return {
      shouldSwapSrc: !preserveState || forceSwap,
      expiresMs: previousExpiresMs,
      refreshInMs: null,
      authRefreshMarginMs: 5 * 60_000,
    };
  }
  const advanced = !finite(previousExpiresMs) || expiresMs > previousExpiresMs + 1000;
  if (preserveState && !forceSwap && !advanced) {
    return {
      shouldSwapSrc: false,
      expiresMs: previousExpiresMs,
      refreshInMs: null,
      authRefreshMarginMs: 5 * 60_000,
    };
  }
  const ttlMs = Math.max(0, expiresMs - nowMs);
  const leadMs = Math.min(5 * 60_000, Math.floor(ttlMs * 0.2));
  const refreshInMs = ttlMs > 10_000 ? Math.max(10_000, ttlMs - leadMs) : null;
  const authRefreshMarginMs = Math.max(5 * 60_000 + 30_000, leadMs + 30_000);
  return {
    shouldSwapSrc: true,
    expiresMs,
    refreshInMs,
    authRefreshMarginMs,
  };
}
