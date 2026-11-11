import { test } from "node:test";
import assert from "node:assert/strict";
import { seekScrubber } from "../static/audio-seek.js";

function range(value) {
  const listeners = new Map();
  return {
    value,
    commit: 0,
    addEventListener(event, fn) {
      if (!listeners.has(event)) listeners.set(event, []);
      listeners.get(event).push(fn);
    },
    removeEventListener(event, fn) {
      listeners.set(event, (listeners.get(event) || []).filter(entry => entry !== fn));
    },
    emit(event) {
      for (const fn of listeners.get(event) || []) fn();
    },
  };
}

test("a tap that fires input and pointerup but no change still seeks", () => {
  const seek = range("1000");
  const commits = [];
  const scrubber = seekScrubber(seek, { onCommit: target => commits.push(target) });
  seek.emit("input");
  assert.equal(scrubber.isScrubbing(), true);
  seek.emit("pointerup");
  assert.deepEqual(commits, [1000]);
  assert.equal(scrubber.isScrubbing(), false, "the latch must clear without a change event");
});

test("touchend commits the same gesture and a later change does not seek twice", () => {
  const seek = range("500");
  const commits = [];
  seekScrubber(seek, { onCommit: target => commits.push(target) });
  seek.emit("input");
  seek.emit("touchend");
  seek.emit("change");
  assert.deepEqual(commits, [500]);
});

test("change alone commits, and keyup commits keyboard scrubbing", () => {
  const seek = range("42");
  const commits = [];
  seekScrubber(seek, { onCommit: target => commits.push(target) });
  seek.emit("input");
  seek.emit("change");
  seek.emit("input");
  seek.emit("keyup");
  assert.deepEqual(commits, [42, 42]);
});

test("a cancelled gesture clears the latch without seeking", () => {
  for (const event of ["pointercancel", "touchcancel", "blur"]) {
    const seek = range("900");
    const commits = [];
    let cancels = 0;
    const scrubber = seekScrubber(seek, {
      onCommit: target => commits.push(target),
      onCancel: () => { cancels++; },
    });
    seek.emit("input");
    seek.emit(event);
    assert.deepEqual(commits, [], event);
    assert.equal(cancels, 1, event);
    assert.equal(scrubber.isScrubbing(), false, event);
  }
});

test("a gesture end without a preceding input is ignored", () => {
  const seek = range("10");
  const commits = [];
  const scrubber = seekScrubber(seek, { onCommit: target => commits.push(target) });
  seek.emit("pointerup");
  seek.emit("change");
  assert.deepEqual(commits, []);
  assert.equal(scrubber.isScrubbing(), false);
});

test("detach removes every listener, and a missing element is inert", () => {
  const seek = range("1");
  const scrubber = seekScrubber(seek, { onCommit() {} });
  scrubber.detach();
  seek.emit("input");
  seek.emit("pointerup");
  assert.equal(scrubber.isScrubbing(), false);
  const none = seekScrubber(null);
  assert.equal(none.isScrubbing(), false);
  assert.equal(none.commit(), undefined);
});
