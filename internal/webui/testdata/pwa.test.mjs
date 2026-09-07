import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { runInNewContext } from "node:vm";
import { webcrypto } from "node:crypto";

const asset = (name) => new URL(`../static/offline/${name}`, import.meta.url);

test("offline manifest is stable and scoped", async () => {
  const manifest = JSON.parse(await readFile(asset("manifest.json"), "utf8"));
  assert.equal(manifest.id, undefined);
  assert.equal(manifest.start_url, "./");
  assert.equal(manifest.scope, "./");
  assert.equal(manifest.display, "standalone");
  assert.deepEqual(manifest.icons.map((icon) => icon.src), ["./icon.svg", "./icon-512.svg"]);
});

test("offline app identities inherit deployment-specific start URLs", async () => {
  const manifest = JSON.parse(await readFile(asset("manifest.json"), "utf8"));
  const identities = ["a", "b"].map(prefix => {
    const start = new URL(manifest.start_url, `https://reader.example/${prefix}/ui/offline/manifest.json`);
    return manifest.id === undefined ? start.href : new URL(manifest.id, start.origin).href;
  });
  assert.deepEqual(identities, ["https://reader.example/a/ui/offline/", "https://reader.example/b/ui/offline/"]);
});

test("offline worker names only the shell assets", async () => {
  const worker = await readFile(asset("sw.js"), "utf8");
  for (const name of ["./shell.html", "./manifest.json", "./offline.css", "./offline.js", "./offline-shelf.js", "./icon.svg", "./icon-512.svg", "./apple-touch-icon.png"]) {
    assert.match(worker, new RegExp(name.replace(".", "\\.")));
  }
  for (const forbidden of ["/v1/", "Authorization", "token", "book"]) {
    assert.doesNotMatch(worker.toLowerCase(), new RegExp(forbidden.toLowerCase()));
  }
  for (const asset of ["./read/", "./assets/offline-account.js", "./assets/offline-storage.js", "./assets/offline-sync.js", "./assets/reader-app.js", "./assets/vendor/readium/readium.js"]) {
    assert.match(worker, new RegExp(asset.replaceAll(".", "\\.")));
  }
  assert.doesNotMatch(worker, /cache\.put/);
});

test("offline shell contains no personalized placeholders", async () => {
  const shell = await readFile(asset("shell.html"), "utf8");
  assert.doesNotMatch(shell, /alice|csrf|token|api|book id/i);
  assert.match(shell, /Offline shelf/);
  assert.match(shell, /offline\.js/);
  assert.match(shell, /offline-shelf\.js/);
  assert.match(shell, /\.\.\/library/);
});

async function workerHarness(root = "/sync/ui/offline/", revision = "new") {
  const listeners = new Map();
  const stores = new Map();
  const deleted = [];
  const prefix = "liseur-sync-offline-shell-" + encodeURIComponent(root) + "-";
  const current = prefix + revision;
  let installation = [];
  let claimed = false;
  const cacheFor = name => {
    if (!stores.has(name)) stores.set(name, new Map());
    return {
      match: async request => stores.get(name).get(request.url || request)?.clone(),
      addAll: async requests => { installation = requests; },
    };
  };
  runInNewContext((await readFile(asset("sw.js"), "utf8"))
    .replaceAll("__OFFLINE_SHELL_REVISION__", revision), {
    self: {
      location: new URL("https://reader.example" + root + "sw.js"),
      addEventListener: (name, handler) => listeners.set(name, handler),
      clients: { claim: async () => { claimed = true; } },
    },
    caches: {
      open: async name => cacheFor(name),
      keys: async () => [...stores.keys()],
      delete: async name => { deleted.push(name); return stores.delete(name); },
    },
    URL, Request, Response, Headers, crypto: webcrypto, Uint8Array, btoa,
  });
  return {
    stores, deleted, prefix, current,
    get installation() { return installation; },
    get claimed() { return claimed; },
    put(name, path, response) {
      cacheFor(name);
      stores.get(name).set("https://reader.example" + path, response);
    },
    async lifecycle(name) {
      let promise;
      listeners.get(name)({ waitUntil: value => { promise = value; } });
      await promise;
    },
    fetch(path, mode = "navigate", method = "GET") {
      let promise;
      listeners.get("fetch")({
        request: { url: "https://reader.example" + path, mode, method },
        respondWith: value => { promise = value; },
      });
      return promise;
    },
  };
}

test("worker installs fresh assets without forcing an active reader to upgrade", async () => {
  const worker = await workerHarness();
  await worker.lifecycle("install");
  assert.ok(worker.installation.length > 20);
  for (const request of worker.installation) {
    assert.equal(request.cache, "reload");
    assert.ok(new URL(request.url).pathname.startsWith("/sync/ui/offline/"));
  }
  assert.equal(worker.claimed, false);
});

test("worker activation cleans only older generations of its deployment", async () => {
  const worker = await workerHarness();
  worker.put(worker.prefix + "old", "/sync/ui/offline/shell.html", new Response("old"));
  worker.put(worker.current, "/sync/ui/offline/shell.html", new Response("new"));
  worker.put("liseur-sync-offline-shell-%2Fother%2Fui%2Foffline%2F-old", "/other/ui/offline/shell.html", new Response("other"));
  await worker.lifecycle("activate");
  assert.deepEqual(worker.deleted, [worker.prefix + "old"]);
  assert.equal(worker.claimed, true);
  assert.equal(worker.stores.size, 2);
});

test("worker serves reader and modules from the same generation with fresh CSP nonces", async () => {
  const worker = await workerHarness();
  const path = "/sync/ui/offline/read/";
  const response = new Response('<script type="module" nonce="original" src="../assets/reader-app.js"></script>', {
    headers: {
      "Content-Security-Policy": "default-src 'none'; script-src 'nonce-original' 'strict-dynamic'",
      "Content-Type": "text/html",
      "Content-Length": "123",
    },
  });
  worker.put(worker.current, path, response);
  worker.put(worker.prefix + "old", "/sync/ui/offline/assets/reader-app.js", new Response("old"));
  worker.put(worker.current, "/sync/ui/offline/assets/reader-app.js", new Response("current"));
  const first = await worker.fetch(path + "?book=one");
  const second = await worker.fetch(path + "?book=two");
  const nonce = first.headers.get("Content-Security-Policy").match(/'nonce-([^']+)'/)[1];
  assert.notEqual(nonce, "original");
  assert.match(await first.text(), new RegExp(`nonce="${nonce.replace(/[+]/g, "\\+")}"`));
  assert.notEqual(first.headers.get("Content-Security-Policy"), second.headers.get("Content-Security-Policy"));
  assert.equal(first.headers.get("Content-Length"), null);
  assert.equal(await (await worker.fetch("/sync/ui/offline/assets/reader-app.js", "cors")).text(), "current");
});

test("worker leaves API, account bootstrap, unrelated navigation and mutations alone", async () => {
  const worker = await workerHarness();
  for (const path of ["/sync/v1/ops", "/sync/ui/offline/account", "/sync/ui/login", "/other/ui/offline/"]) {
    assert.equal(worker.fetch(path), undefined);
  }
  assert.equal(worker.fetch("/sync/ui/offline/shell.html", "cors", "POST"), undefined);
  const response = await worker.fetch("/sync/ui/offline/");
  assert.equal(response.type, "error");
});
