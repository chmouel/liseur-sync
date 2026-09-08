// Unit tests for the three-way reading-position merge. These mirror the
// Android client's ReadingStateMerge tests: the point of the merge is
// that it decides from *movement away from an agreed baseline*, never
// from a clock and never from whichever number is larger.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  EPSILON, LOCAL_EPSILON, anchorOf, sameSpot, movedFrom, reconcileReadingState,
} from "../static/reader-reconcile.js";

let minted = 0;
const at = (progression, anchor) => ({
  op_id: `op-${++minted}`,
  progression,
  locator: {
    href: "chapter.xhtml",
    locations: {
      totalProgression: progression,
      fragments: anchor ? [`epubcfi(${anchor})`] : [],
    },
  },
});

const decide = (input) => reconcileReadingState(input).decision;

test("an anchor is the epubcfi fragment, and only that", () => {
  assert.equal(anchorOf(at(0.5, "/6/2")), "epubcfi(/6/2)");
  assert.equal(anchorOf(at(0.5)), null);
  assert.equal(anchorOf({ locator: { locations: { fragments: ["page=12"] } } }), null);
  assert.equal(anchorOf({ locator: { locations: { fragments: "epubcfi(/6/2)" } } }), null);
  assert.equal(anchorOf(null), null);
});

test("two positions are the same spot by identity, by anchor, or within tolerance", () => {
  const one = at(0.5, "/6/2");
  assert.equal(sameSpot(one, { ...one }), true);
  assert.equal(sameSpot(at(0.5, "/6/2"), at(0.9, "/6/2")), true, "a named spot outranks a fraction");
  assert.equal(sameSpot(at(0.5, "/6/2"), at(0.5, "/6/4")), false, "two named spots do not blur");
  assert.equal(sameSpot(at(0.5), at(0.5 + EPSILON / 2)), true);
  assert.equal(sameSpot(at(0.5), at(0.5 + EPSILON * 2)), false);
  assert.equal(sameSpot(at(0.5), null), false);
});

test("a corrupt progression is not a position, and cannot be compared as one", () => {
  for (const bad of [null, undefined, NaN, Infinity, -0.1, 1.1, "0.5"]) {
    assert.equal(sameSpot(at(bad), at(0.5)), false);
    assert.equal(movedFrom(at(bad), at(0.5), EPSILON), true);
    assert.equal(movedFrom(at(0.5), at(bad), EPSILON), true);
  }
  assert.equal(sameSpot(at(null), at(undefined)), true, "neither side claims a place");
});

test("nothing has moved away from a baseline that does not exist, so everything has", () => {
  assert.equal(movedFrom(null, at(0.5), EPSILON), true);
  assert.equal(movedFrom(at(0.5), null, EPSILON), false, "an absent position did not move");
});

test("movement is measured against the baseline, with the tolerance it was asked for", () => {
  const base = at(0.5);
  assert.equal(movedFrom(base, at(0.5 + EPSILON / 2), EPSILON), false);
  assert.equal(movedFrom(base, at(0.5 + EPSILON), EPSILON), true);
  // A local write is evidence, so its tolerance is effectively nothing.
  assert.equal(movedFrom(base, at(0.5 + EPSILON / 2), LOCAL_EPSILON), true);
  assert.equal(movedFrom(base, { ...base }, LOCAL_EPSILON), false, "the same op is the same place");
  assert.equal(movedFrom(at(0.5, "/6/2"), at(0.9, "/6/2"), LOCAL_EPSILON), false);
  assert.equal(movedFrom(at(0.5, "/6/2"), at(0.5, "/6/4"), EPSILON), true);
});

test("with nothing to compare there is nothing to say", () => {
  assert.equal(decide({}), "in-sync");
  assert.equal(decide({ local: at(0.5), localDirty: false }), "in-sync");
  assert.equal(decide({ remote: at(0.5) }), "pull");
});

test("an unacknowledged local position with no remote counterpart is owed, not resolved", () => {
  assert.equal(decide({ local: at(0.5), localDirty: true }), "push");
});

test("the four ways two devices can stand relative to what they agreed", () => {
  const baseline = at(0.5);
  const near = at(0.5 + EPSILON / 4);
  const far = at(0.8);
  const further = at(0.9);
  // Neither moved.
  assert.equal(decide({ baseline, local: baseline, remote: near }), "in-sync");
  // Only the other device moved.
  assert.equal(decide({ baseline, local: baseline, remote: far }), "pull");
  // Only this device moved.
  assert.equal(decide({ baseline, local: far, remote: baseline, localDirty: true }), "push");
  // Both moved, and no rule can say which one the reader meant.
  const both = reconcileReadingState({ baseline, local: far, remote: further, localDirty: true });
  assert.equal(both.decision, "conflict");
  assert.equal(both.local, far, "a conflict carries both positions");
  assert.equal(both.remote, further);
});

test("a local position the server already acknowledged is not local movement", () => {
  const baseline = at(0.5);
  // Same numbers as the conflict above; only the queue has changed.
  assert.equal(decide({ baseline, local: at(0.8), remote: at(0.9), localDirty: false }), "pull");
});

test("the furthest position does not win, in either direction", () => {
  const baseline = at(0.5);
  // This device read backwards, on purpose, and owes that page turn.
  assert.equal(decide({ baseline, local: at(0.2), remote: baseline, localDirty: true }), "push");
  // The other device went backwards and this one did not move.
  assert.equal(decide({ baseline, local: baseline, remote: at(0.2) }), "pull");
});

test("no baseline at all is a conflict as soon as both sides have a position", () => {
  assert.equal(decide({ local: at(0.2), remote: at(0.8), localDirty: true }), "conflict");
  assert.equal(decide({ local: at(0.2), remote: at(0.8) }), "pull", "a clean local side simply follows");
  assert.equal(decide({ local: at(0.2), remote: at(0.2), localDirty: true }), "in-sync");
});

test("two devices at the same named spot never disagree, whatever their fractions say", () => {
  const baseline = at(0.1, "/6/2");
  const here = at(0.30, "/6/8");
  const there = at(0.55, "/6/8");
  assert.equal(decide({ baseline, local: here, remote: there, localDirty: true }), "in-sync");
});

test("a clock is never consulted", () => {
  const baseline = at(0.5);
  const old = { ...at(0.8), client_ts: "1999-01-01T00:00:00Z" };
  const fresh = { ...at(0.9), client_ts: "2099-01-01T00:00:00Z" };
  assert.equal(decide({ baseline, local: old, remote: fresh, localDirty: true }), "conflict");
  assert.equal(decide({ baseline, local: fresh, remote: old, localDirty: true }), "conflict");
});
