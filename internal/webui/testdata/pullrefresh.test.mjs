// Unit tests for the pull gesture and the refresh runner.
//
// The gesture's whole job is to decide, from points alone, whether a
// drag belonged to it — so it is tested from points alone, with no
// browser anywhere near it. The runner's job is that two asks are one
// run, and that a spinner belongs only to a reader who asked.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  PULL_THRESHOLD, PULL_MAX, PULL_SLOP, distance, pullGesture, refreshRunner,
} from "../static/pull-refresh.js";

const touch = (y, x = 0) => [{ clientX: x, clientY: y }];
const none = [];

const gestureAt = (options = {}) =>
  pullGesture({ atTop: () => true, enabled: () => true, ...options });

test("a pull past the threshold refreshes", () => {
  const pull = gestureAt();
  assert.equal(pull.start(touch(0)), null);
  const moved = pull.move(touch(PULL_THRESHOLD));
  assert.equal(moved.kind, "move");
  assert.equal(moved.armed, true);
  assert.deepEqual(pull.end(), { kind: "release", refresh: true });
});

test("a pull let go short of the threshold does nothing", () => {
  const pull = gestureAt();
  pull.start(touch(0));
  const moved = pull.move(touch(PULL_SLOP + 10));
  assert.equal(moved.kind, "move");
  assert.equal(moved.armed, false);
  assert.deepEqual(pull.end(), { kind: "release", refresh: false });
});

test("travel under the slop is still the page's to scroll", () => {
  const pull = gestureAt();
  pull.start(touch(0));
  assert.equal(pull.move(touch(PULL_SLOP)), null);
  assert.equal(pull.active, false);
  assert.equal(pull.end(), null);
});

test("a scroller not at the top never yields the gesture", () => {
  const pull = gestureAt({ atTop: () => false });
  pull.start(touch(0));
  assert.equal(pull.move(touch(200)), null);
  assert.equal(pull.active, false);
});

test("being at the top is asked when the finger lands, not later", () => {
  let top = false;
  const pull = gestureAt({ atTop: () => top });
  pull.start(touch(0));
  top = true;
  assert.equal(pull.move(touch(200)), null, "a list scrolled a moment ago is not at the top");
});

test("a disarmed gesture leaves the browser its own", () => {
  const pull = gestureAt({ enabled: () => false });
  pull.start(touch(0));
  assert.equal(pull.move(touch(200)), null);
});

test("an upward drag is not a pull", () => {
  const pull = gestureAt();
  pull.start(touch(300));
  assert.equal(pull.move(touch(200)), null);
  assert.equal(pull.active, false);
});

test("a drag more sideways than down belongs to whatever scrolls sideways", () => {
  const pull = gestureAt();
  pull.start(touch(0, 0));
  assert.equal(pull.move(touch(30, 60)), null);
  assert.equal(pull.active, false);
});

test("a drag straighter than sideways is a pull", () => {
  const pull = gestureAt();
  pull.start(touch(0, 0));
  const moved = pull.move(touch(80, 20));
  assert.equal(moved.kind, "move");
});

test("a second finger abandons a claimed pull", () => {
  const pull = gestureAt();
  pull.start(touch(0));
  pull.move(touch(80));
  assert.deepEqual(pull.move([...touch(80), ...touch(120, 40)]), { kind: "cancel" });
  assert.equal(pull.active, false);
  assert.equal(pull.end(), null);
});

test("a second finger before the gesture was claimed says nothing", () => {
  const pull = gestureAt();
  pull.start(touch(0));
  assert.equal(pull.move([...touch(4), ...touch(6, 40)]), null);
});

test("two fingers landing at once are not a pull", () => {
  const pull = gestureAt();
  assert.equal(pull.start([...touch(0), ...touch(0, 40)]), null);
  assert.equal(pull.move(touch(200)), null);
});

test("a cancelled touch takes a claimed pull with it", () => {
  const pull = gestureAt();
  pull.start(touch(0));
  pull.move(touch(80));
  assert.deepEqual(pull.cancel(), { kind: "cancel" });
  assert.equal(pull.cancel(), null);
});

test("a finger dragged back above where it started refreshes nothing", () => {
  const pull = gestureAt();
  pull.start(touch(100));
  pull.move(touch(200));
  const back = pull.move(touch(90));
  assert.equal(back.distance, 0);
  assert.equal(back.armed, false);
  assert.deepEqual(pull.end(), { kind: "release", refresh: false });
});

test("a new touch forgets the last one", () => {
  const pull = gestureAt();
  pull.start(touch(0));
  pull.move(touch(200));
  pull.start(touch(0));
  assert.equal(pull.active, false);
  assert.equal(pull.end(), null);
});

test("moves and ends without a touch are ignored", () => {
  const pull = gestureAt();
  assert.equal(pull.move(touch(200)), null);
  assert.equal(pull.end(), null);
  assert.equal(pull.move(none), null);
});

test("the indicator follows the finger, then resists", () => {
  assert.equal(distance(-20), 0);
  assert.equal(distance(0), 0);
  assert.equal(distance(40), 40);
  assert.equal(distance(PULL_THRESHOLD), PULL_THRESHOLD);
  const far = distance(PULL_THRESHOLD + 200);
  assert.ok(far > PULL_THRESHOLD, "past the threshold it still moves");
  assert.ok(far < PULL_MAX, "but never reaches the end of its room");
  assert.ok(distance(1000) < PULL_MAX);
});

// ------------------------------------------------------- the runner

const deferred = () => {
  let settle, fail;
  const promise = new Promise((resolve, reject) => { settle = resolve; fail = reject; });
  return { promise, settle, fail };
};

test("a second ask joins the run already under way", async () => {
  let runs = 0;
  const gate = deferred();
  const runner = refreshRunner({ work: () => { runs++; return gate.promise; } });
  const first = runner.ask();
  const second = runner.ask();
  assert.equal(runs, 1);
  assert.equal(runner.busy, true);
  gate.settle();
  assert.deepEqual(await Promise.all([first, second]), [true, true]);
  assert.equal(runner.busy, false);
  await runner.ask();
  assert.equal(runs, 2, "once it is over, asking again runs again");
});

test("an ask spins, and says when it is over", async () => {
  const states = [];
  const runner = refreshRunner({
    work: async () => {}, onState: (state) => states.push(state),
  });
  await runner.ask();
  assert.deepEqual(states, ["refreshing", "done"]);
});

test("a refresh that failed is reported, not thrown", async () => {
  const states = [];
  const boom = new Error("no");
  const runner = refreshRunner({
    work: async () => { throw boom; },
    onState: (state, error) => states.push([state, error]),
  });
  assert.equal(await runner.ask(), false);
  assert.deepEqual(states, [["refreshing", undefined], ["failed", boom]]);
});

test("work that half succeeded names its own outcome", async () => {
  const states = [];
  const runner = refreshRunner({
    work: async () => "partial", onState: (state) => states.push(state),
  });
  assert.equal(await runner.ask(), true);
  assert.deepEqual(states, ["refreshing", "partial"]);
});

test("nobody asked, so nothing spins", async () => {
  const states = [];
  const runner = refreshRunner({
    work: async () => {}, onState: (state) => states.push(state),
  });
  assert.equal(await runner.quietly(), true);
  assert.deepEqual(states, []);
});

test("a quiet refresh is debounced; asking is not", async () => {
  let runs = 0, clock = 1_000_000;
  const runner = refreshRunner({
    work: async () => { runs++; }, now: () => clock, quietInterval: 60_000,
  });
  await runner.quietly();
  assert.equal(runs, 1);
  clock += 30_000;
  assert.equal(await runner.quietly(), false);
  assert.equal(runs, 1);
  await runner.ask();
  assert.equal(runs, 2, "a reader asking is never debounced");
  clock += 31_000;
  await runner.quietly();
  assert.equal(runs, 3);
});

test("asking over a quiet run raises the spinner for the run in flight", async () => {
  const states = [];
  const gate = deferred();
  const runner = refreshRunner({
    work: () => gate.promise, onState: (state) => states.push(state),
  });
  const quiet = runner.quietly();
  const asked = runner.ask();
  assert.deepEqual(states, ["refreshing"]);
  gate.settle();
  await Promise.all([quiet, asked]);
  assert.deepEqual(states, ["refreshing", "done"]);
});

test("a quiet refresh over an ask does not lower the spinner early", async () => {
  const states = [];
  const gate = deferred();
  const runner = refreshRunner({
    work: () => gate.promise, onState: (state) => states.push(state),
  });
  const asked = runner.ask();
  const quiet = runner.quietly();
  gate.settle();
  await Promise.all([asked, quiet]);
  assert.deepEqual(states, ["refreshing", "done"]);
});
