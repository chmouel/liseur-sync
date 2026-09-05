import { test } from "node:test";
import assert from "node:assert/strict";
import { annotationCFI, annotationRenderer } from "../static/reader-annotations.js";

const mark = (id, color = "yellow", cfi = "epubcfi(/6/2)") => ({
  id, kind: "highlight", color,
  locator: { locations: { fragments: [cfi] } },
});
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };

function fixture() {
  const overlays = new Map(), calls = [];
  let identity = 1, renderer;
  const view = {
    async addAnnotation({ value }) {
      calls.push(["add", value]);
      overlays.set(value, renderer.color(value));
    },
    async deleteAnnotation({ value }) {
      calls.push(["delete", value]);
      overlays.delete(value);
    },
  };
  renderer = annotationRenderer({ getView: () => view, current: (id) => id === identity, changed() {} });
  return { renderer, view, overlays, calls, changeIdentity() { identity++; } };
}

test("replacement removes deletions and recolors existing overlays", async () => {
  const { renderer, overlays, calls } = fixture();
  await renderer.replace([mark("one"), mark("two", "blue", "epubcfi(/6/4)")], 1);
  assert.equal(overlays.size, 2);
  await renderer.replace([mark("one", "pink")], 1);
  assert.deepEqual([...overlays], [["epubcfi(/6/2)", "pink"]]);
  assert.equal(renderer.color("epubcfi(/6/4)"), undefined);
  assert.deepEqual(calls.map(([method]) => method), ["add", "add", "delete", "delete", "add"]);
  await renderer.replace([], 1);
  assert.equal(overlays.size, 0);
  assert.deepEqual(renderer.annotations(), []);
});

test("CFI failure cache is cleared when the server replaces the live set", async () => {
  const { renderer, view } = fixture();
  const add = view.addAnnotation;
  view.addAnnotation = async () => { throw new Error("stale CFI"); };
  await renderer.replace([mark("one")], 1);
  assert.equal(renderer.failed("one"), true);
  view.addAnnotation = add;
  await renderer.replace([mark("one", "green")], 1);
  assert.equal(renderer.failed("one"), false);
});

test("an in-flight old draw cannot recreate an overlay removed by a newer set", async () => {
  const { renderer, view, overlays } = fixture();
  const add = view.addAnnotation;
  let release;
  view.addAnnotation = async (annotation) => {
    await new Promise((r) => { release = r; });
    await add(annotation);
  };
  const first = renderer.replace([mark("old")], 1);
  await flush();
  const removed = renderer.replace([], 1);
  release();
  await Promise.all([first, removed]);
  assert.equal(overlays.size, 0);
  assert.equal(renderer.color("epubcfi(/6/2)"), undefined);
});

test("superseded token/work rendering is discarded and clear removes in-flight overlays", async () => {
  const { renderer, view, overlays, changeIdentity } = fixture();
  const add = view.addAnnotation;
  let release;
  view.addAnnotation = async (annotation) => {
    await new Promise((r) => { release = r; });
    await add(annotation);
  };
  const old = renderer.replace([mark("old"), mark("later", "blue", "epubcfi(/6/4)")], 1);
  await flush();
  changeIdentity();
  const cleared = renderer.clear();
  release();
  await Promise.all([old, cleared]);
  assert.equal(overlays.size, 0);
  await renderer.replace([mark("stale")], 1);
  assert.equal(overlays.size, 0);
  assert.deepEqual(renderer.annotations(), []);
});

test("overlapping chapter draw requests are serialized", async () => {
  const { renderer, view } = fixture();
  let active = 0, maximum = 0;
  view.addAnnotation = async () => {
    active++;
    maximum = Math.max(maximum, active);
    await Promise.resolve();
    active--;
  };
  await renderer.replace([mark("one")], 1);
  await Promise.all([renderer.draw(), renderer.draw(), renderer.draw()]);
  assert.equal(maximum, 1);
});

test("non-drawable notes/bookmarks stay in the replacement set without overlays", async () => {
  const { renderer, overlays } = fixture();
  const rows = [
    { id: "note", kind: "note", body: "A standalone note" },
    { ...mark("bookmark"), kind: "bookmark" },
    { ...mark("unknown"), locator: {} },
  ];
  await renderer.replace(rows, 1);
  assert.equal(overlays.size, 0);
  assert.deepEqual(renderer.annotations(), rows);
  assert.equal(annotationCFI({ locator: { locations: { fragments: "not an array" } } }), null);
});
