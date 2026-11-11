// The offline shell's status bar, decided from the appearance cookie
// the cached shell never saw.
//
// The shelf's palette is the cookie's; the shell is cached, so the
// media-queried theme-color pair it ships is only right when the cookie
// asks for "system". Anything else — including no cookie at all, where
// the stylesheet falls back to light — has to be settled here, or a
// dark-system device paints dark browser chrome around a light shelf.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";

const source = readFileSync(new URL("../static/offline/offline.js", import.meta.url), "utf8");

function meta(id, content) {
  return {
    id,
    content,
    media: "(prefers-color-scheme: light)",
    removed: false,
    removeAttribute(name) { if (name === "media") this.media = null; },
    setAttribute(name, value) { if (name === "content") this.content = value; },
    remove() { this.removed = true; },
  };
}

function shell(cookie) {
  const light = meta("offline-theme-color", "#ffffff");
  const dark = meta("offline-theme-color-dark", "#18181a");
  const elements = new Map([[light.id, light], [dark.id, dark]]);
  const document = {
    cookie,
    documentElement: { dataset: {} },
    getElementById: id => elements.get(id) || null,
  };
  const ctx = createContext({ document, navigator: {}, console });
  runInContext(source, ctx);
  return { ctx, light, dark };
}

test("no appearance cookie leaves a light shelf with a light status bar", () => {
  const page = shell("");
  assert.equal(page.ctx.document.documentElement.dataset.theme, undefined, "the stylesheet's light default stands");
  assert.equal(page.light.media, null, "the media pair would follow the system, not the shelf");
  assert.equal(page.light.content, "#ffffff");
  assert.equal(page.dark.removed, true);
});

test("an unrecognised appearance is the light shelf too", () => {
  const page = shell("liseur_ui=sepia.grid");
  assert.equal(page.light.content, "#ffffff");
  assert.equal(page.light.media, null);
  assert.equal(page.dark.removed, true);
});

test("a named theme settles on its own colour", () => {
  const page = shell("liseur_ui=dark");
  assert.equal(page.ctx.document.documentElement.dataset.theme, "dark");
  assert.equal(page.light.content, "#18181a");
  assert.equal(page.light.media, null);
  assert.equal(page.dark.removed, true);
});

test("an explicit system choice keeps the media-queried pair", () => {
  const page = shell("liseur_ui=system");
  assert.equal(page.ctx.document.documentElement.dataset.theme, "system");
  assert.equal(page.light.media, "(prefers-color-scheme: light)");
  assert.equal(page.light.content, "#ffffff");
  assert.equal(page.dark.removed, false);
});
