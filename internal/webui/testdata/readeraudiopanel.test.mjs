import { test } from "node:test";
import assert from "node:assert/strict";
import { audioPanel, panelPreferences, panelPosition, panelPlacement } from "../static/reader-audio-panel.js";

const bounds = { left: 12, top: 60, right: 640, bottom: 400 };
test("preferences reject malformed records and clamp finite coordinates", () => {
  const fallback = { size: "mini", edge: "bottom-right", x: 1, y: 1 };
  for (const value of [null, [], {}, { size: "expanded", edge: "free", x: Infinity, y: 1 }]) {
    assert.deepEqual(panelPreferences(value), fallback);
  }
  assert.deepEqual(panelPreferences({ size: "expanded", edge: "free", x: -3, y: 5 }),
    { size: "expanded", edge: "free", x: 0, y: 1 });
});
test("positions remain bounded and retain proportional docking offsets", () => {
  assert.deepEqual(panelPosition({ edge: "right", x: 0, y: .5 }, bounds), { x: 640, y: 230 });
  assert.deepEqual(panelPosition({ edge: "free", x: -5, y: 8 }, bounds), { x: 12, y: 400 });
  for (const [edge, point] of Object.entries({ left: [15, 200], right: [630, 200], top: [200, 65], bottom: [200, 390] })) {
    const placement = panelPlacement({ x: point[0], y: point[1] }, bounds);
    assert.equal(placement.edge, edge);
    assert.equal(panelPosition(placement, bounds)[["left", "right"].includes(edge) ? "x" : "y"], bounds[edge]);
  }
  assert.equal(panelPlacement({ x: 200, y: 200 }, bounds).edge, "free");
  assert.deepEqual(panelPlacement({ x: -99, y: 900 }, { left: 12, right: 12, top: 60, bottom: 60 }), { edge: "left", x: 0, y: 0 });
});

class Element extends EventTarget {
  hidden = false;
  dataset = {};
  attributes = new Map();
  properties = new Map();
  style = { setProperty: (k, v) => this.properties.set(k, v), removeProperty: k => this.properties.delete(k) };
  textContent = "";
  children = [];
  constructor(doc, height = 44) { super(); this.ownerDocument = doc; this.height = height; }
  getBoundingClientRect() {
    const width = Math.min(this.dataset.audioSize === "expanded" ? 400 : 336, parseFloat(this.style.maxWidth) || 1000);
    return { width, height: this.dataset.audioSize === "expanded" ? 280 : this.height, bottom: this.height };
  }
  setAttribute(k, v) { this.attributes.set(k, v); }
  removeAttribute(k) { this.attributes.delete(k); }
  focus() { this.ownerDocument.activeElement = this; }
  contains(el) { return this === el || this.children.includes(el); }
  append(el) { this.children.push(el); el.parent = this; }
  remove() { this.parent.children = this.parent.children.filter(el => el !== this); }
  setPointerCapture(id) { this.capture = id; }
  hasPointerCapture(id) { return this.capture === id; }
  releasePointerCapture() { this.capture = null; }
  fire(type, values = {}) {
    const event = new Event(type, { cancelable: true });
    Object.assign(event, values);
    this.dispatchEvent(event);
    return event;
  }
}
function fixture(stored, denied = false) {
  const doc = new EventTarget();
  doc.body = new Element(doc);
  doc.createElement = () => new Element(doc);
  const win = new EventTarget();
  const frames = new Map();
  let id = 0;
  Object.assign(win, {
    innerWidth: 1000, innerHeight: 800,
    requestAnimationFrame(fn) { frames.set(++id, fn); return id; },
    cancelAnimationFrame(id) { frames.delete(id); },
    getComputedStyle: () => ({ getPropertyValue: () => "0px" }),
    ResizeObserver: class { observe() {} disconnect() {} },
    localStorage: { getItem() { if (denied) throw Error("denied"); return stored; },
      setItem(key, value) { if (denied) throw Error("denied"); stored = value; } },
  });
  const panel = new Element(doc, 116);
  const controls = Object.fromEntries(["[data-audio-handle]", "[data-audio-size]", "[data-audio-details]", ".reader-audio-transport", ".reader-audio-heading", "[data-audio-announcement]"].map(k => [k, new Element(doc)]));
  const docks = ["top", "left", "right", "bottom", "bottom-right"].map(edge => {
    const el = new Element(doc); el.dataset.audioDock = edge; return el;
  });
  panel.querySelector = key => controls[key];
  panel.querySelectorAll = () => docks;
  const stage = new Element(doc), toolbar = new Element(doc), footer = new Element(doc, 16), toggle = new Element(doc);
  const api = audioPanel(panel, { win, stage, toolbar, footer, toggle });
  api.setOpen(true);
  return { api, win, doc, panel, stage, toggle, controls, docks,
    handle: controls["[data-audio-handle]"], size: controls["[data-audio-size]"], details: controls["[data-audio-details]"],
    stored: () => JSON.parse(stored),
    flush() { const pending = [...frames.values()]; frames.clear(); pending.forEach(fn => fn()); },
  };
}

test("mini and expanded share the same controls and preserve media state", () => {
  const f = fixture();
  const media = { src: "signed-ticket", currentTime: 200, playbackRate: 1.5, paused: false };
  f.panel.children.push(media);
  const before = { ...media };
  assert.equal(f.panel.dataset.audioSize, "mini");
  assert.equal(f.details.hidden, true);
  f.size.fire("click");
  assert.equal(f.panel.dataset.audioSize, "expanded");
  assert.equal(f.size.attributes.get("aria-expanded"), "true");
  f.details.focus();
  f.size.fire("click");
  assert.equal(f.doc.activeElement, f.size);
  assert.equal(f.panel.children[0], media);
  assert.deepEqual(media, before);
  assert.equal(f.stored().size, "mini");
});
test("dragging snaps, cancellation restores, and shields always get removed", () => {
  const f = fixture();
  const start = f.panel.style.left;
  f.handle.fire("pointerdown", { button: 0, isPrimary: true, pointerId: 1, clientX: 650, clientY: 650 });
  f.handle.fire("pointermove", { pointerId: 1, clientX: 648, clientY: 650 });
  assert.equal(f.doc.body.children.length, 0);
  f.handle.fire("pointermove", { pointerId: 1, clientX: 15, clientY: 400 });
  assert.equal(f.doc.body.children.length, 1);
  f.handle.fire("pointercancel");
  assert.equal(f.panel.style.left, start);
  assert.equal(f.doc.body.children.length, 0);
  f.handle.fire("pointerdown", { button: 0, isPrimary: true, pointerId: 2, clientX: 650, clientY: 650 });
  f.handle.fire("pointermove", { pointerId: 2, clientX: 15, clientY: 400 });
  f.handle.fire("pointerup", { pointerId: 2 });
  assert.equal(f.stored().edge, "left");
  assert.equal(f.doc.body.children.length, 0);
});
test("escape, lost capture, close and teardown clean up active drags", () => {
  for (const end of ["escape", "lostpointercapture", "close", "destroy", "resize"]) {
    const f = fixture();
    const start = f.panel.style.left;
    f.handle.fire("pointerdown", { button: 0, isPrimary: true, pointerId: 3, clientX: 650, clientY: 650 });
    f.handle.fire("pointermove", { pointerId: 3, clientX: 400, clientY: 450 });
    assert.equal(f.doc.body.children.length, 1);
    if (end === "escape") { const e = new Event("keydown"); Object.assign(e, { key: "Escape" }); f.doc.dispatchEvent(e); }
    if (end === "lostpointercapture") f.handle.fire(end);
    if (end === "close") { f.panel.hidden = true; f.api.setOpen(false); }
    if (end === "destroy") f.api.destroy();
    if (end === "resize") { f.win.dispatchEvent(new Event("resize")); f.flush(); }
    assert.equal(f.doc.body.children.length, 0, end);
    if (end !== "close") assert.equal(f.panel.style.left, start, end);
  }
});

test("keyboard movement, resize, docking, and reset stay in bounds", () => {
  const f = fixture();
  const x = parseFloat(f.panel.style.left);
  assert.equal(f.handle.fire("keydown", { key: "ArrowLeft" }).defaultPrevented, true);
  assert.equal(parseFloat(f.panel.style.left), x - 10);
  f.handle.fire("keydown", { key: "ArrowLeft", shiftKey: true });
  assert.equal(parseFloat(f.panel.style.left), x - 50);
  f.docks[0].fire("click");
  assert.equal(f.stored().edge, "top");
  assert.equal(f.docks[0].attributes.get("aria-pressed"), "true");
  assert.equal(f.docks[4].attributes.get("aria-pressed"), "false");
  f.win.innerWidth = 390;
  f.win.dispatchEvent(new Event("resize")); f.flush();
  assert.equal(f.panel.dataset.audioLayout, "dock");
  assert.equal(f.handle.hidden, true);
  assert.ok(f.stage.properties.has("--reader-audio-inset"));
  f.win.innerWidth = 1000;
  f.win.dispatchEvent(new Event("resize")); f.flush();
  assert.equal(f.panel.dataset.audioLayout, "floating");
  assert.equal(f.stored().edge, "top");
  f.docks[4].fire("click");
  assert.equal(f.stored().edge, "bottom-right");
  assert.equal(f.stage.properties.size, 0);
});
test("restoration, denied storage, close and teardown are safe", () => {
  const f = fixture(JSON.stringify({ size: "expanded", edge: "left", x: 0, y: .5 }));
  assert.equal(f.panel.dataset.audioSize, "expanded");
  assert.equal(f.panel.style.left, "12px");
  f.panel.hidden = true; f.api.setOpen(false);
  assert.equal(f.doc.activeElement, f.toggle);
  f.api.destroy();
  const denied = fixture("invalid JSON", true);
  denied.size.fire("click");
  assert.equal(denied.panel.dataset.audioSize, "expanded");
  denied.api.destroy();
  denied.size.fire("click");
  assert.equal(denied.panel.dataset.audioSize, "expanded");
});
