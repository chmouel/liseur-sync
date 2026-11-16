import test from "node:test";
import assert from "node:assert/strict";
import { coalesceFrameHide } from "./framehide.js";

const tick = () => new Promise((resolve) => setImmediate(resolve));

test("overlapping frame hides share the acknowledgement", async () => {
  let finish,
    calls = 0;
  class Frame {
    setCSSProperties() {
      calls += 10;
    }
    async load() {
      calls += 100;
    }
    async show() {}
    hide() {
      calls++;
      return new Promise((resolve) => {
        finish = resolve;
      });
    }
  }
  coalesceFrameHide(Frame);
  const frame = new Frame();
  const first = frame.hide();
  const second = frame.hide();
  assert.equal(first, second);
  assert.equal(calls, 1);
  frame.setCSSProperties({});
  const loading = frame.load();
  assert.equal(calls, 1);
  finish();
  await Promise.all([first, second, loading]);
  assert.equal(calls, 111);
  const next = frame.hide();
  assert.equal(calls, 112);
  finish();
  await next;
});

test("frame style and load updates wait for show", async () => {
  let finish;
  const calls = [];
  class Frame {
    async hide() {}
    show() {
      return new Promise((resolve) => {
        finish = resolve;
      });
    }
    setCSSProperties() {
      calls.push("style");
    }
    async load() {
      calls.push("load");
    }
  }
  coalesceFrameHide(Frame);
  const frame = new Frame();
  const showing = frame.show();
  frame.setCSSProperties({});
  const loading = frame.load();
  assert.deepEqual(calls, []);
  finish();
  await Promise.all([showing, loading]);
  assert.deepEqual(calls, ["style", "load"]);
});

// A frame whose calls resolve only when the test opens their gate, so the
// order in which Readium's calls actually run is observable.
function gatedFrame(log) {
  const gates = [];
  const gate = (name) =>
    new Promise((resolve) =>
      gates.push(() => {
        log.push(`${name}-done`);
        resolve();
      }),
    );
  class Frame {
    hide() {
      log.push("hide");
      return gate("hide");
    }
    show() {
      log.push("show");
      return gate("show");
    }
    hideImmediately() {}
    setCSSProperties() {
      log.push("style");
    }
    async load() {}
  }
  coalesceFrameHide(Frame);
  return { frame: new Frame(), gates };
}

test("a hide after a show is its own turn, not the earlier hide", async () => {
  // hide, show, hide: the middle show is a different intent, so the last
  // hide cannot reuse the first hide's acknowledgement and let the show
  // land last.
  const log = [];
  const { frame, gates } = gatedFrame(log);
  const first = frame.hide();
  const showing = frame.show();
  const last = frame.hide();
  assert.notEqual(last, first, "the hide after a show is a new request");
  assert.deepEqual(log, ["hide"]);
  gates.shift()();
  await first;
  await tick();
  assert.deepEqual(log, ["hide", "hide-done", "show"]);
  gates.shift()();
  await showing;
  await tick();
  assert.deepEqual(log, ["hide", "hide-done", "show", "show-done", "hide"]);
  assert.notEqual(last, first);
  gates.shift()();
  await last;
  await tick();
  assert.deepEqual(log, ["hide", "hide-done", "show", "show-done", "hide", "hide-done"]);
});

test("a style write waits for a hide queued after a show", async () => {
  // Readium's CSS setter can interrupt a frame's acknowledgement, so a
  // style write asked for after a hide must not run while that hide is
  // still waiting for the show in front of it.
  const log = [];
  const { frame, gates } = gatedFrame(log);
  const showing = frame.show();
  const hiding = frame.hide();
  frame.setCSSProperties({});
  assert.deepEqual(log, ["show"]);
  gates.shift()();
  await showing;
  await tick();
  assert.deepEqual(log, ["show", "show-done", "hide"]);
  assert.equal(log.includes("style"), false);
  gates.shift()();
  await hiding;
  await tick();
  assert.deepEqual(log, ["show", "show-done", "hide", "hide-done", "style"]);
});
