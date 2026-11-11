// The page-turn gesture as the reader actually wires it.
//
// classifySwipe is unit-tested next door, and it rejects a cancelled
// gesture. What it cannot see is its callers: a touchend handler that
// passes `cancelled: false` hands a gesture the browser took back to
// the classifier as a live one, and a cancelled swipe would turn a
// page. So these dispatch the sequence a browser sends when a scroll
// or a pinch interrupts a finger — pointerdown, pointercancel,
// touchend — at the listeners, not at the decision.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";
import { classifySwipe } from "../static/reader-place.js";

const source = readFileSync(new URL("../static/reader-app.js", import.meta.url), "utf8");
const slice = (from, to) => {
  const start = source.indexOf(from);
  const end = source.indexOf(to);
  assert.ok(start >= 0 && end > start, `cannot slice ${from}`);
  return source.slice(start, end);
};
const TAP_SLOP_PX = Number(source.match(/const TAP_SLOP_PX = (\d+);/)[1]);

class Target {
  listeners = new Map();
  addEventListener(type, fn) {
    const list = this.listeners.get(type) || [];
    list.push(fn);
    this.listeners.set(type, list);
  }
  fire(type, event = {}) {
    for (const fn of this.listeners.get(type) || []) fn({ target: this, ...event });
  }
}

function context(extra = {}) {
  const ctx = createContext({
    classifySwipe, TAP_SLOP_PX, console, URL, Date,
    performance: { now: () => 0 },
    setTimeout: () => 1,
    clearTimeout: () => {},
    ...extra,
  });
  runInContext(slice("function newGesture()", "function wireChapterPointer(doc)"), ctx);
  return ctx;
}

// A chapter document: its coordinates are its own, and the frame sits
// at the origin, so a viewport coordinate is a document coordinate.
function frameDocument() {
  const doc = new Target();
  doc.body = { style: { setProperty() {} } };
  doc.documentElement = {};
  doc.defaultView = { frameElement: { getBoundingClientRect: () => ({ left: 0, top: 0, width: 800, height: 600 }), offsetWidth: 800 }, getComputedStyle: () => ({}) };
  doc.getSelection = () => ({ rangeCount: 0, isCollapsed: true });
  doc.caretRangeFromPoint = () => null;
  return doc;
}

function chapterReader() {
  const turns = [];
  const doc = frameDocument();
  const ctx = context({
    turn: (dir) => turns.push(dir),
    chromeVisible: () => false,
    armChrome: () => {},
    CHROME_IDLE_MS: 1000,
    tapAt: () => {},
    toggleTOC: () => {},
    TAP_MAX_MS: 500,
    settingsPanel: null,
    tocPanel: null,
    noteActivity: () => {},
    noteNavigation: () => {},
    stage: { getBoundingClientRect: () => ({ left: 0, top: 0 }) },
  });
  runInContext(slice("function frameOffset(doc)", "function wireChapterPointer(doc)"), ctx);
  runInContext(slice("function wireChapterPointer(doc)", "// The stage should match the chosen theme"), ctx);
  ctx.doc = doc;
  runInContext("wireChapterPointer(doc)", ctx);
  return { ctx, doc, turns };
}

const down = (over = {}) => ({
  pointerId: 1, pointerType: "touch", clientX: 700, clientY: 300, button: 0, detail: 1, ...over,
});
const touchEnd = (x) => ({
  touches: [],
  changedTouches: [{ clientX: x, clientY: 300 }],
  target: {},
});

test("a finger the browser cancelled never turns a page from the chapter frame", () => {
  const reader = chapterReader();
  reader.doc.fire("pointerdown", down());
  reader.doc.fire("pointercancel", down());
  reader.doc.fire("touchend", touchEnd(200));
  assert.deepEqual(reader.turns, [], "a cancelled gesture is not a swipe");
});

test("the same drag uncancelled still turns the page", () => {
  const reader = chapterReader();
  reader.doc.fire("pointerdown", down());
  reader.doc.fire("touchend", touchEnd(200));
  assert.deepEqual(reader.turns, [1]);
});

// The stage outside the chapter frame runs the same sequence through
// its own listener, and had the same literal.
function stageReader() {
  const turns = [];
  const stageArea = new Target();
  const ctx = context({
    stageArea,
    turn: (dir) => turns.push(dir),
    chromeVisible: () => false,
    armChrome: () => {},
    CHROME_IDLE_MS: 1000,
    tapAt: () => {},
    toggleTOC: () => {},
    TAP_MAX_MS: 500,
    tocPanel: null,
    view: {},
    stageTouchAt: 0,
    stageGesture: { id: null, x: 0, y: 0, vx: 0, vy: 0, lastVx: 0, lastVy: 0, startedAt: 0, spoiled: true, cancelled: false, multiTouch: false, hadSelection: false, exempt: false },
    noteActivity: () => {},
    bookHasSelection: () => false,
    stageSurface: () => true,
  });
  runInContext(slice('const spoilStage = (e) => {', 'stageArea.addEventListener("click"'), ctx);
  return { ctx, stageArea, turns };
}

test("a finger the browser cancelled never turns a page from the stage", () => {
  const stage = stageReader();
  stage.stageArea.fire("touchstart", {
    touches: [{ clientX: 700, clientY: 300 }],
  });
  assert.equal(stage.ctx.stageGesture.id, "touch", "the finger opened a gesture");
  stage.stageArea.fire("pointercancel", { pointerId: "touch" });
  assert.equal(stage.ctx.stageGesture.cancelled, true, "the stage spoils a cancelled pointer");
  stage.stageArea.fire("touchend", touchEnd(200));
  assert.deepEqual(stage.turns, []);
});
