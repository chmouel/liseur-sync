import test from "node:test";
import assert from "node:assert/strict";
import { coalesceFrameHide } from "./framehide.js";

test("overlapping frame hides share the acknowledgement", async () => {
  let finish, calls = 0;
  class Frame {
    setCSSProperties() { calls += 10; }
    async load() { calls += 100; }
    async show() {}
    hide() {
      calls++;
      return new Promise(resolve => { finish = resolve; });
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
    show() { return new Promise(resolve => { finish = resolve; }); }
    setCSSProperties() { calls.push("style"); }
    async load() { calls.push("load"); }
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
