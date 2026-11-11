// What the screen lock is asked for, taken from the reader itself.
//
// The rule is intent, not the momentary flag: `waiting` and `seeking`
// take audioActivelyPlaying down through every stall, and a page that
// drops the lock there goes dark mid-chapter. So the states below are
// the element's own — paused, ended, error — and not the flag.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";

const source = readFileSync(new URL("../static/reader-app.js", import.meta.url), "utf8");
const start = source.indexOf("function audioPlaybackIntended()");
const end = source.indexOf("function syncWakeLock()");
assert.ok(start >= 0 && end > start, "cannot slice the wake lock rule");

const doc = { hidden: false };
const ctx = createContext({
  document: doc,
  ready: true,
  audioBar: { hidden: false },
  followAudio: true,
  audioWantsPlay: false,
  audioEl: null,
});
runInContext(source.slice(start, end), ctx);
const wants = () => runInContext("wantsScreenWakeLock()", ctx);

function element(over = {}) {
  return { paused: false, ended: false, error: null, currentTime: 12, ...over };
}

test("a playing element keeps the screen on", () => {
  ctx.audioEl = element();
  ctx.audioWantsPlay = true;
  assert.equal(wants(), true);
});

test("buffering and seeking keep the lock the element still means to use", () => {
  ctx.audioWantsPlay = true;
  ctx.audioEl = element();
  // `waiting` paused nothing: the element is late, not stopped.
  assert.equal(wants(), true);
  ctx.audioEl = element({ seeking: true });
  assert.equal(wants(), true);
});

test("a pause, an end, a failed element and a hidden page release it", () => {
  ctx.audioEl = element();
  ctx.audioWantsPlay = false;
  assert.equal(wants(), false, "a pause drops the intent to play");
  ctx.audioWantsPlay = true;
  ctx.audioEl = element({ paused: true });
  assert.equal(wants(), false, "a paused element is not playing");
  ctx.audioEl = element({ ended: true });
  assert.equal(wants(), false, "an ended element is not playing");
  ctx.audioEl = element({ error: new Error("decode") });
  assert.equal(wants(), false, "a broken element is not playing");
  ctx.audioEl = element();
  doc.hidden = true;
  assert.equal(wants(), false, "a hidden page cannot hold the lock");
  doc.hidden = false;
  assert.equal(wants(), true);
});

test("the lock is the follow-along panel's, and only its", () => {
  ctx.audioEl = element();
  ctx.audioWantsPlay = true;
  ctx.followAudio = false;
  assert.equal(wants(), false);
  ctx.followAudio = true;
  ctx.audioBar = { hidden: true };
  assert.equal(wants(), false, "a closed panel is not a page to watch");
  ctx.audioBar = { hidden: false };
  ctx.ready = false;
  assert.equal(wants(), false);
  ctx.ready = true;
  ctx.audioEl = null;
  assert.equal(wants(), false);
});
