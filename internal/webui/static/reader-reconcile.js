// Three-way reconciliation of reading positions, the way the Android
// client does it (`domain/ReadingStateMerge.kt`).
//
// Two positions that differ are not evidence of anything on their own.
// What decides the outcome is which side moved *away from the position
// both sides last agreed on*: the baseline. Without it, a browser that
// has read on for ten pages and a phone that has not moved since
// yesterday look exactly like a browser that has not moved and a phone
// that read on — and picking either one silently loses somebody's
// evening.
//
// Nothing here consults a clock. A wall clock is not evidence about
// reading: two devices disagree about it, and the later write is not
// the one the reader meant. The local side is known to have moved
// because this browser wrote it; the remote side is compared by
// distance.

/**
 * EPSILON is the cross-device tolerance. Another client reports a
 * progression it computed from its own layout and rounding, so a
 * difference smaller than this is agreement, not movement.
 */
export const EPSILON = 0.005;

/**
 * LOCAL_EPSILON is deliberately almost zero. A local write is durable
 * evidence that the reader turned a page here, and one page of a long
 * book is a far smaller fraction than EPSILON.
 */
export const LOCAL_EPSILON = 1e-9;

const fraction = value =>
  typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 1 ? value : null;

/** anchorOf is the exact pointer in a position, when it carries one. */
export function anchorOf(state) {
  const fragments = state?.locator?.locations?.fragments;
  if (!Array.isArray(fragments)) return null;
  return fragments.find(value => typeof value === "string" && value.startsWith("epubcfi(")) || null;
}

// Two exact anchors either name the same spot or they do not; there is
// no tolerance to apply. Null means neither side offered one, so only
// the fractions can speak.
function exactAgreement(one, two) {
  const here = anchorOf(one), there = anchorOf(two);
  if (!here || !there) return null;
  return here === there;
}

export function sameSpot(one, two) {
  if (!one || !two) return false;
  if (one.op_id && one.op_id === two.op_id) return true;
  const exact = exactAgreement(one, two);
  if (exact !== null) return exact;
  const here = fraction(one.progression), there = fraction(two.progression);
  if (here === null || there === null) return here === there;
  return Math.abs(here - there) < EPSILON;
}

/**
 * movedFrom asks whether a position has left the agreed baseline.
 *
 * No baseline at all means everything is movement: nothing has been
 * agreed, so nothing can be said to have stayed put. An exact pointer
 * is the strongest evidence either way and is consulted before the
 * fraction, which is only ever an approximation of a layout.
 */
export function movedFrom(baseline, state, tolerance) {
  if (!baseline) return true;
  if (!state) return false;
  if (baseline.op_id && baseline.op_id === state.op_id) return false;
  const exact = exactAgreement(baseline, state);
  if (exact !== null) return !exact;
  const agreed = fraction(baseline.progression);
  const now = fraction(state.progression);
  if (agreed === null) return now !== null;
  if (now === null) return true;
  return Math.abs(agreed - now) >= tolerance;
}

/**
 * reconcileReadingState compares this device's position, another
 * device's position and the baseline the two last agreed on.
 *
 * - `pull`: only the other side moved. The reader is offered the trip.
 * - `push`: only this side moved. The queued op is the whole answer.
 * - `conflict`: both moved. Both positions are kept and the reader
 *   chooses; nothing here decides for them.
 * - `in-sync`: nothing to do.
 *
 * `localDirty` says this browser has a position the server has not
 * acknowledged. It comes from the durable queue, not from a timestamp,
 * so it survives a reload and a crash.
 */
export function reconcileReadingState({ local, remote, baseline, localDirty = false } = {}) {
  const settled = { decision: "in-sync", local: local || null, remote: remote || null };
  if (!local && !remote) return settled;
  if (!local) return { decision: "pull", local: null, remote };
  if (!remote) return localDirty ? { decision: "push", local, remote: null } : settled;
  if (sameSpot(local, remote)) return settled;

  const localMoved = localDirty && movedFrom(baseline, local, LOCAL_EPSILON);
  const remoteMoved = movedFrom(baseline, remote, EPSILON);
  if (!localMoved && remoteMoved) return { decision: "pull", local, remote };
  if (localMoved && !remoteMoved) return { decision: "push", local, remote };
  if (localMoved && remoteMoved) return { decision: "conflict", local, remote };
  // Two positions that differ, with neither of them away from what both
  // sides agreed: rounding, not reading. Saying anything here would be
  // asking the reader about a difference they cannot see.
  return settled;
}
