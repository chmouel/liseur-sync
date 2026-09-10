// What syncing one book by hand should do about the two positions it
// found. A port of the Android app's BookSyncChoice, and a decision and
// only a decision: page numbers need the book laid out, which is the
// reader's business rather than this one's.
//
// This is deliberately *not* how a sync running on its own behalf
// decides. Those take whichever side has read further, because nobody
// is watching and a question with no one to answer it is a stall. The
// button is somebody asking, so it gets asked back — which is why even
// agreement is reported rather than passed over in silence.

import { EPSILON, sameSpot, reconcileReadingState } from "./reader-reconcile.js";

/**
 * Reads the two sides and says what to do about them.
 *
 * The baseline is what makes this answerable at all. Without it, this
 * reader's own last position coming back from the server looks exactly
 * like another device: same shape, same fields, a different page from
 * wherever the reader has since got to. The reconciler already knows
 * the difference — movement is measured *from what the two sides last
 * agreed on* — so the same three-way merge that decides an automatic
 * sync decides this one, and the two can never disagree about which
 * side moved.
 *
 * `resolvable` is whether the remote position can actually be opened in
 * this copy of the book. It is asked before the sides are weighed: a
 * position this reader cannot navigate to cannot be adopted however far
 * ahead it looks, and offering it would put a button on screen that
 * does nothing.
 *
 * A missing local position is not a local position of zero. It means
 * this device has nothing to offer, so offering it as a choice would be
 * inventing a side; the server's answer is simply taken.
 *
 * @param {{local: object|null, remote: object|null, baseline?: object|null,
 *   localDirty?: boolean, resolvable?: boolean}} sides
 * @returns {{verdict: string, relation?: string}}
 */
export function decideBookSync({
  local, remote, baseline = null, localDirty = false, resolvable = true,
}) {
  // Nothing on the server. What that means depends on whether this
  // device has read anything: a page waiting to go up is a different
  // answer from a book neither side has opened, and telling a reader
  // their page is on its way when there is no page is a small lie.
  if (!remote) return { verdict: local ? "no-remote" : "no-position" };
  if (local && sameSpot(local, remote)) return { verdict: "in-step" };
  const { decision } = reconcileReadingState({ local, remote, baseline, localDirty });
  // Nothing has moved away from what was agreed, whatever the two
  // pages say: the difference is one this reader has already answered.
  if (decision === "in-sync") return { verdict: "in-step" };
  // Only this device moved, so there is one position and the server has
  // an older copy of it. Nothing was preserved to adopt, and the only
  // true answer is to send.
  if (decision === "push") return { verdict: "owed" };
  if (!resolvable) return { verdict: "unreadable" };
  if (!local) return { verdict: "no-local" };
  return { verdict: "ask", relation: relationOf(local, remote) };
}

/**
 * How the server's position sits against this device's.
 *
 * `same-page` is neither ahead nor behind: two progressions within
 * rounding of each other while the anchors disagree outright. Rare, and
 * worth its own answer — two nearly identical positions over two
 * different buttons is a riddle, and the excerpt is the only thing that
 * tells the sides apart. Whether that rounding really is one page is a
 * question for the edition's page table, which the reader holds and
 * this module deliberately does not.
 */
function relationOf(local, remote) {
  const here = fractionOf(local), there = fractionOf(remote);
  if (there - here >= EPSILON) return "ahead";
  if (here - there >= EPSILON) return "behind";
  return "same-page";
}

function fractionOf(op) {
  const value = Number(op?.progression);
  return Number.isFinite(value) ? value : 0;
}
