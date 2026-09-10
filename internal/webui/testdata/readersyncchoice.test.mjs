// Tests for the on-demand sync decision (ADR-0040), the port of the
// Android app's BookSyncChoice. Two doubles and a flag, which is what
// makes it worth testing without a navigator, a database or a server.

import test from "node:test";
import assert from "node:assert/strict";

import { decideBookSync } from "../static/reader-sync-choice.js";

const at = (fraction, cfi) => ({
  progression: fraction,
  locator: cfi ? { locations: { fragments: [cfi] } } : {},
});

test("no position on the server is not a choice", () => {
  assert.deepEqual(decideBookSync({ local: at(0.4), remote: null }), { verdict: "no-remote" });
  assert.deepEqual(decideBookSync({ local: null, remote: null }), { verdict: "no-remote" });
});

test("the same spot is reported rather than passed over", () => {
  assert.deepEqual(
    decideBookSync({ local: at(0.4), remote: at(0.4) }),
    { verdict: "in-step" },
  );
  // Within rounding of each other, with neither side offering an anchor.
  assert.equal(decideBookSync({ local: at(0.4), remote: at(0.401) }).verdict, "in-step");
});

test("two exact anchors settle agreement outright", () => {
  const cfi = "epubcfi(/6/4!/4/2/2:0)";
  assert.equal(decideBookSync({ local: at(0.4, cfi), remote: at(0.9, cfi) }).verdict, "in-step");
  // The same page, different spots: a choice, and the excerpt is the
  // only thing that tells the sides apart.
  const other = "epubcfi(/6/4!/4/2/8:0)";
  assert.deepEqual(
    decideBookSync({ local: at(0.4, cfi), remote: at(0.4, other) }),
    { verdict: "ask", relation: "same-page" },
  );
});

test("a position this copy cannot open is never offered", () => {
  // Asked before the sides are weighed: an answer that cannot be
  // adopted must not become a button that does nothing.
  assert.deepEqual(
    decideBookSync({ local: at(0.1), remote: at(0.9), resolvable: false }),
    { verdict: "unreadable" },
  );
  // Even with nothing on this side.
  assert.deepEqual(
    decideBookSync({ local: null, remote: at(0.9), resolvable: false }),
    { verdict: "unreadable" },
  );
});

// The case the baseline exists for. This reader's own last position
// coming back from the server has exactly the shape of another device:
// the only thing that tells them apart is which side moved away from
// what the two of them last agreed on.
test("the server holding this device's own older page is not a second side", () => {
  const stale = at(0.4, "epubcfi(/6/4!/4/2/2:0)");
  const moved = at(0.6, "epubcfi(/6/4!/4/2/8:0)");
  assert.deepEqual(
    decideBookSync({ local: moved, remote: stale, baseline: stale, localDirty: true }),
    { verdict: "owed" },
  );
  // And with no baseline to go on it is a genuine choice, which is why
  // the baseline is passed rather than assumed.
  assert.equal(decideBookSync({ local: moved, remote: stale }).verdict, "ask");
});

test("a difference already answered is not asked again", () => {
  const answered = at(0.9, "epubcfi(/6/8!/4/2/2:0)");
  const mine = at(0.4, "epubcfi(/6/4!/4/2/2:0)");
  // Neither side has moved since they agreed on the other device's
  // page; the reader stayed here and said so.
  assert.deepEqual(
    decideBookSync({ local: mine, remote: answered, baseline: answered }),
    { verdict: "in-step" },
  );
});

test("the other device moving away from what was agreed is a question", () => {
  const agreed = at(0.4, "epubcfi(/6/4!/4/2/2:0)");
  const moved = at(0.9, "epubcfi(/6/8!/4/2/2:0)");
  assert.deepEqual(
    decideBookSync({ local: agreed, remote: moved, baseline: agreed }),
    { verdict: "ask", relation: "ahead" },
  );
});

test("a missing local position is not a local position of zero", () => {
  assert.deepEqual(decideBookSync({ local: null, remote: at(0.9) }), { verdict: "no-local" });
  // Which is the point: at(0) would have made this a choice.
  assert.deepEqual(
    decideBookSync({ local: at(0), remote: at(0.9) }),
    { verdict: "ask", relation: "ahead" },
  );
});

test("the relation names which side read further", () => {
  assert.equal(decideBookSync({ local: at(0.1), remote: at(0.9) }).relation, "ahead");
  assert.equal(decideBookSync({ local: at(0.9), remote: at(0.1) }).relation, "behind");
});

test("behind is the case an ordinary sync says nothing about", () => {
  // An automatic sync keeps the further-read side and never asks; the
  // button exists so this one can be answered the other way.
  const decision = decideBookSync({ local: at(0.8), remote: at(0.2) });
  assert.deepEqual(decision, { verdict: "ask", relation: "behind" });
});

test("a missing or unreadable progression counts as the start", () => {
  const cfi = "epubcfi(/6/4!/4/2/2:0)";
  const other = "epubcfi(/6/8!/4/2/2:0)";
  const decision = decideBookSync({
    local: { locator: { locations: { fragments: [cfi] } } },
    remote: at(0.5, other),
  });
  assert.deepEqual(decision, { verdict: "ask", relation: "ahead" });
});

test("the same op on both sides is the same spot", () => {
  const op = { op_id: "op-1", progression: 0.3 };
  assert.equal(decideBookSync({ local: op, remote: { ...op, progression: 0.9 } }).verdict, "in-step");
});
