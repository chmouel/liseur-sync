import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFile, mkdir, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { resolve } from "node:path";
import { gzipSync, brotliCompressSync } from "node:zlib";

async function annotationChecks() {
  const { ReaderEngine } = await import("/reader-engine.js");
  const CFI = await import("/vendor/foliate/epubcfi.js");
  const check = (value, message) => { if (!value) throw new Error(message); };
  const engine = new ReaderEngine();
  engine.packageHref = "OEBPS/package.opf";
  engine.packageDocument = new DOMParser().parseFromString(`
    <package xmlns="http://www.idpf.org/2007/opf">
      <metadata/><manifest>
        <item id="one" href="one.xhtml"/>
        <item id="nav" href="nav.xhtml"/>
        <item id="two" href="two%20words.xhtml"/>
      </manifest><spine>
        <itemref idref="one"/><itemref idref="nav" linear="no"/>
        <itemref id="chapter-two" idref="two"/>
      </spine>
    </package>`, "application/xml");
  engine.book = { sections: [{ id: "OEBPS/one.xhtml" }, { id: "OEBPS/two words.xhtml" }] };
  engine.resources = { resolveKey: path => decodeURIComponent(path) };
  const doc = new DOMParser().parseFromString('<html xmlns="http://www.w3.org/1999/xhtml"><head/><body><p>Selected chapter text.</p></body></html>', "application/xhtml+xml");
  doc.documentElement.dataset.readerHref = "OEBPS/two words.xhtml";
  const range = doc.createRange();
  range.setStart(doc.querySelector("p").firstChild, 0);
  range.setEnd(doc.querySelector("p").firstChild, 8);
  for (const collapsed of [false, true]) {
    if (collapsed) range.collapse(true);
    const cfi = engine.getCFIForRange(doc, range);
    const { href, parsed } = engine.cfiHref(cfi);
    check(href === "OEBPS/two words.xhtml", "annotation CFI must resolve past the non-linear spine entry");
    check(CFI.toElement(engine.packageDocument, CFI.collapse(parsed)[0]).getAttribute("idref") === "two",
      "package CFI must identify the actual itemref");
    const local = Array.isArray(parsed) ? [parsed.at(-1)] : { ...parsed, parent: [parsed.parent.at(-1)] };
    const restored = CFI.toRange(doc, local);
    check(restored.toString() === range.toString() && restored.collapsed === collapsed,
      "highlights and bookmarks must round-trip through the package CFI");
  }
  engine.packageDocument.querySelector('itemref[idref="two"]').remove();
  let error;
  try { engine.getCFIForRange(doc, range); } catch (caught) { error = caught; }
  check(error, "missing spine items must not produce fabricated CFIs");
  return "annotation checks passed";
}

async function storageChecks() {
  const s = await import("/offline-storage.js");
  const { drainOfflineOutbox, offlineSync } = await import("/offline-sync.js");
  const check = (value, message) => { if (!value) throw new Error(message); };
  const rejected = async (fn, message) => {
    let error;
    try { await fn(); } catch (caught) { error = caught; }
    check(error, message);
  };
  const partition = location.origin + "/sync/";
  const account = "reader", deviceID = "device";
  await s.setActiveAccount(partition, account);
  let context = { ...await s.accountContext(partition, account), deviceID };
  const bookID = "book", workID = "work";
  const root = "/v1/books/book/publication/digest/";
  const initialNote = { id: "server-note", rev: 1, kind: "note", body: "server", work_id: workID };
  let remoteAnnotations = [initialNote];
  let remotePosition = { op_id: "server-position", work_id: workID, progression: 0.4 };
  const reply = (body, url, status = 200) => {
    const response = new Response(JSON.stringify(body), {
      status, headers: { "Content-Type": "application/json" },
    });
    if (url) Object.defineProperty(response, "url", { value: url });
    return response;
  };
  const publicationRequest = async path => {
    check(path.startsWith("v1/"), "transport must strip deployment prefix exactly once");
    check(!path.includes("must-not-download"), "discarded publication code must not become a download dependency");
    check(!path.includes("#"), "resource fragments must not enter transport paths");
    const url = partition + path;
    if (path.endsWith("manifest.json")) return reply({
      metadata: { title: "A book" },
      links: [
        { rel: "package", href: root + "resources/package.opf" },
        { rel: "http://readium.org/position-list", href: root + "positions.json" },
      ],
      readingOrder: [{ href: root + "resources/chapter.xhtml", type: "application/xhtml+xml" }],
    }, url);
    if (path.endsWith("positions.json")) return reply({ positions: [] }, url);
    if (path.includes("/positions?")) return reply({ ops: [remotePosition] }, url);
    if (path.endsWith("/annotations")) return reply({ annotations: remoteAnnotations }, url);
    const response = new Response(`<html xmlns="http://www.w3.org/1999/xhtml"><head>
      <script src="/must-not-download/script.js"></script>
      <link rel="preload" href="/must-not-download/preload.js"/>
      </head><body>Book
      <iframe src="/must-not-download/frame.xhtml"></iframe>
      <object data="/must-not-download/object.xhtml"><img src="/must-not-download/fallback.jpg"/></object>
      <a href="/must-not-download/navigation">Not a resource dependency</a>
      <svg xmlns="http://www.w3.org/2000/svg"><use href="chapter.xhtml#fragment"/></svg>
      </body></html>`,
      { headers: { "Content-Type": "application/xhtml+xml" } });
    Object.defineProperty(response, "url", { value: url });
    return response;
  };
  await s.downloadPublication({ ...context, bookID, workID, request: publicationRequest });
  let book = await s.getReadySnapshot({ ...context, bookID });
  check(book.resourceHrefs.length === 2 && book.resourceHrefs.every(href => !href.includes("#")),
    "fragment references share the existing archive resource");
  check(book.localPosition.progression === 0.4, "download seeds server reading position");
  check((await s.listOfflineAnnotations({ ...context, bookID }))[0].body === "server",
    "download seeds server annotations");

  for (const encoding of ["gzip", "br", "identity"]) {
    await s.downloadPublication({ ...context, bookID, workID, request: path =>
      path.includes("/resources/")
        ? fetch(partition + path, { headers: { "X-Test-Encoding": encoding } })
        : publicationRequest(path) });
    check(await s.getReadySnapshot({ ...context, bookID }), `${encoding} resources must complete the download`);
  }
  for (const encoding of [null, "identity"]) {
    await rejected(() => s.downloadPublication({ ...context, bookID, workID, request: async path => {
      const response = await publicationRequest(path);
      if (path.includes("/resources/")) {
        response.headers.set("Content-Length", "99999");
        if (encoding) response.headers.set("Content-Encoding", encoding);
      }
      return response;
    } }), "identity resource length mismatches must still reject the download");
  }

  const queue = async (id, body, queueID, deleted = false, rev = 0) => {
    const annotation = { id, work_id: workID, kind: "note", body, rev, pending: true, deleted };
    await s.queueOfflineAnnotation({ ...context, bookID, annotation, queueID,
      payload: deleted ? { method: "delete", id, rev }
        : { method: "write", annotation: { ...annotation, base_rev: rev } } });
  };
  const pending = () => s.listOfflineOutbox({ ...context, kind: "annotation" });
  await queue("note", "first", "create");
  let attempted = await s.attemptOfflineRecord(context, (await pending())[0].key);
  await queue("note", "newer", "edit");
  check((await pending()).find(row => row.id === "create").payload.annotation.body === "first",
    "in-flight payload stays immutable after edit");
  await s.acknowledgeOfflineAnnotation(context, attempted, { id: "note", body: "first", rev: 1, seq: 1 });
  let local = (await s.listOfflineAnnotations({ ...context, bookID })).find(row => row.id === "note");
  check(local.body === "newer" && local.pending && local.rev === 1, "ack preserves newer edit");
  check((await pending())[0].payload.annotation.base_rev === 1, "successor advances revision");
  attempted = await s.attemptOfflineRecord(context, (await pending())[0].key);
  await queue("note", "newer", "delete", true, 1);
  await s.acknowledgeOfflineAnnotation(context, attempted, { id: "note", body: "newer", rev: 2, seq: 2 });
  check((await pending())[0].payload.rev === 2, "pending deletion advances revision");
  local = (await s.listOfflineAnnotations({ ...context, bookID })).find(row => row.id === "note");
  check(local.deleted && local.pending, "ack must not resurrect deleted note");
  await drainOfflineOutbox(context, async (path, options) => {
    check(options.method === "DELETE" && path.endsWith("rev=2"), "delete uses acknowledged revision");
    return reply({ rev: 3, seq: 3 });
  });
  await queue("creating", "first", "creating");
  attempted = await s.attemptOfflineRecord(context, (await pending())[0].key);
  await queue("creating", "", "delete-creating", true);
  await s.acknowledgeOfflineAnnotation(context, attempted, { id: "creating", body: "first", rev: 1 });
  check((await pending())[0].payload.rev === 1, "deleting in-flight create retains deletion intent");
  await drainOfflineOutbox(context, async () => reply({ rev: 2 }));
  await queue("unsent", "unsent", "unsent");
  await queue("unsent", "", "discard-unsent", true);
  check(!(await pending()).length, "unattempted create/delete needs no invalid rev-zero request");
  await rejected(() => queue("empty", "  ", "empty"), "empty standalone note rejected locally");

  const payload = { session_id: "session", work_id: workID, active_ms: 1000 };
  await s.saveOfflineSessionCheckpoint({ ...context, bookID, checkpoint: { id: "session" } });
  await s.finishOfflineSession({ ...context, bookID, payload });
  await rejected(() => s.finishOfflineSession({ ...context, bookID, payload: { ...payload, active_ms: 2000 } }),
    "finalized payload cannot be overwritten");
  await queue("bad", "bad", "bad");
  await queue("invalid", "invalid", "invalid");
  await queue("good", "good", "good");
  const sent = [];
  await drainOfflineOutbox(context, async (path, options) => {
    sent.push(path);
    if (path === "v1/sessions") return reply({ accepted: 1 });
    const annotation = JSON.parse(options.body).annotations[0];
    return annotation.id === "bad" ? reply({ error: "invalid note" }, "", 400)
      : annotation.id === "invalid" ? reply({ results: [{ id: annotation.id, status: "invalid", reason: "a note requires a body" }] })
      : reply({ results: [{ id: annotation.id, status: "applied", rev: 1 }] });
  });
  check(sent.includes("v1/sessions") && (await pending()).length === 0,
    "permanent annotation error does not stop sessions or other annotations");
  await rejected(() => s.finishOfflineSession({ ...context, bookID, payload: { ...payload, active_ms: 2000 } }),
    "delivered session retains immutable identity");
  await queue("retryable", "retryable", "retryable");
  await drainOfflineOutbox(context, async () => reply({ error: "temporary failure" }, "", 500));
  const retryable = (await pending()).find(row => row.id === "retryable");
  check(retryable?.state === "pending" && retryable.error === "temporary failure",
    "unexpected server errors stay retryable with an error");
  await drainOfflineOutbox(context, async () =>
    reply({ results: [{ id: "retryable", status: "applied", rev: 1 }] }));
  check(!(await pending()).some(row => row.id === "retryable"),
    "retryable outbox records recover after a later success");
  await s.saveOfflineSessionCheckpoint({ ...context, bookID, checkpoint: { id: "session" } });
  check(!await s.getOfflineSessionCheckpoint({ ...context, bookID }), "late checkpoint cannot recreate finalized session");
  await s.saveOfflineSessionCheckpoint({ ...context, bookID, checkpoint: { id: "next-session" } });
  await s.finishOfflineSession({ ...context, bookID, payload });
  check((await s.getOfflineSessionCheckpoint({ ...context, bookID })).id === "next-session",
    "late finalization cannot clear a newer sitting checkpoint");

  await queue("local", "local pending", "local");
  remoteAnnotations = [{ ...initialNote, id: "fresh", body: "remote update" }];
  remotePosition = { ...remotePosition, progression: 0.8 };
  await s.saveOfflinePosition({ ...context, bookID, op: { op_id: "local-position", progression: 0.6 } });
  await s.reconcileOfflineBook(context, book, publicationRequest);
  local = await s.listOfflineAnnotations({ ...context, bookID });
  check(local.some(row => row.id === "local") && local.some(row => row.id === "fresh") &&
    !local.some(row => row.id === "server-note"), "pull merges pending writes and removes absent server notes");
  check((await s.getReadySnapshot({ ...context, bookID })).localPosition.progression === 0.6,
    "pull never overwrites queued local position");
  await new Promise(resolve => setTimeout(resolve, 5));
  const latestPosition = { op_id: "a-newer-position", progression: 0.2,
    locator: { href: "chapter.xhtml", locations: { progression: 0.2 } } };
  await s.saveOfflinePosition({ ...context, bookID, op: latestPosition });
  await s.removeBookSnapshots({ ...context, bookID });
  check(!await s.getReadySnapshot({ ...context, bookID }), "removal discards publication snapshots");
  await s.downloadPublication({ ...context, bookID, workID, request: publicationRequest });
  book = await s.getReadySnapshot({ ...context, bookID });
  check(JSON.stringify(book.localPosition) === JSON.stringify(latestPosition),
    "replacement snapshot restores the latest queued position, including its locator");
  check((await s.listOfflineOutbox({ ...context, kind: "position" })).length === 2,
    "restoring a replacement snapshot preserves queued operations");

  const positionPosts = [];
  await drainOfflineOutbox(context, async (path, options) => {
    if (path !== "v1/ops") return reply({ results: [] });
    positionPosts.push(JSON.parse(options.body).ops[0].op_id);
    return reply({ error: "temporary failure" }, "", 503);
  });
  check(positionPosts.join(",") === "local-position", "a deferred page blocks newer pages of the same work");
  const firstPosition = (await s.listOfflineOutbox({ ...context, kind: "position" }))[0];
  await s.saveOfflinePosition({ ...context, bookID, op: firstPosition.payload });
  check((await s.listOfflineOutbox({ ...context, kind: "position" }))[0].attempted,
    "saving a retry does not erase its transmission evidence");
  check((await s.getReadySnapshot({ ...context, bookID })).localPosition.op_id === latestPosition.op_id,
    "saving an older retry cannot replace the local head");
  await rejected(() => s.saveOfflinePosition({ ...context, bookID,
    op: { ...firstPosition.payload, progression: 0.9 } }), "queued position IDs have immutable payloads");
  await drainOfflineOutbox(context, async (path, options) => {
    if (path !== "v1/ops") return reply({ results: [] });
    const op = JSON.parse(options.body).ops[0];
    positionPosts.push(op.op_id);
    return reply({ results: [{ op_id: op.op_id, status: "applied" }] });
  });
  check(positionPosts.join(",") === "local-position,local-position,a-newer-position",
    "recovery sends the old position before the latest, including a deliberate backward move");
  const originalNow = Date.now;
  try {
    Date.now = () => 100;
    await s.saveOfflinePosition({ ...context, bookID, op: { ...latestPosition, op_id: "z-first" } });
    Date.now = () => 50;
    await s.saveOfflinePosition({ ...context, bookID, op: { ...latestPosition, op_id: "a-last" } });
  } finally { Date.now = originalNow; }
  check((await s.listOfflineOutbox({ ...context, kind: "position" })).map(row => row.id).join(",") === "z-first,a-last",
    "a clock moving backwards cannot reorder page turns");

  // The coordinator uses actual IndexedDB, Web Locks, account/token handshakes
  // and transport-bound identity checks, with only HTTP replaced by fixtures.
  await s.clearOfflineAccount(partition, account);
  await s.setActiveAccount(partition, account);
  context = { ...await s.accountContext(partition, account), deviceID };
  const originalFetch = globalThis.fetch;
  let signedIn = false, redirectedLogin = false, actualAccount = account, actualDevice = deviceID, posts = 0;
  globalThis.fetch = async (url, options) => {
    if (url.endsWith("/ui/offline/account")) {
      if (redirectedLogin) {
        const response = new Response("<html>Sign in</html>");
        Object.defineProperty(response, "redirected", { value: true });
        return response;
      }
      return reply({ account: actualAccount, csrf: "csrf" }, "", signedIn ? 200 : 401);
    }
    if (url.endsWith("/ui/reader/token")) return reply({ token: "secret" });
    if (url.endsWith("/v1/token")) return reply({ account_id: actualAccount, device_id: actualDevice });
    if (url.endsWith("/v1/annotations")) {
      posts++;
      const annotation = JSON.parse(options.body).annotations[0];
      return reply({ results: [{ id: annotation.id, status: "applied", rev: 1 }] });
    }
    throw new Error("Unexpected request: " + url);
  };
  let syncStatus = "";
  const sync = offlineSync({ context, base: partition, onStatus: message => { syncStatus = message; } });
  await queue("reconnect", "pending", "reconnect");
  await sync.trigger();
  check(posts === 0, "signed-out coordinator does not send");
  redirectedLogin = true;
  await sync.trigger();
  check(posts === 0 && syncStatus === "Sign in again to sync offline changes.",
    "redirected login HTML is handled as expired authentication, not parsed as JSON");
  redirectedLogin = false;
  signedIn = true;
  await sync.trigger();
  check(posts === 1 && !(await pending()).length, "manual retry recovers after reauthentication");
  await queue("later", "later", "later");
  s.notifyOfflineChange("write", partition);
  for (let i = 0; i < 50 && posts < 2; i++) await new Promise(resolve => setTimeout(resolve, 20));
  check(posts === 2, "local writes wake running coordinator");
  await queue("online-event", "later", "online-event");
  globalThis.dispatchEvent(new Event("online"));
  await sync.trigger();
  check(posts === 3, "online event drains new local work");
  await queue("foreground-event", "later", "foreground-event");
  document.dispatchEvent(new Event("visibilitychange"));
  await sync.trigger();
  check(posts === 4, "foreground event drains new local work");
  sync.stop();
  actualDevice = "other-device";
  const other = offlineSync({ context, base: partition });
  await queue("bound", "bound", "bound");
  await other.trigger();
  check(posts === 4, "different device cannot mutate queued records");
  other.stop();
  actualDevice = deviceID;
  actualAccount = "other-account";
  const switched = offlineSync({ context, base: partition });
  await switched.trigger();
  check(posts === 4, "different account cannot mutate queued records");
  switched.stop();
  actualAccount = account;
  signedIn = false;
  const originalTimeout = globalThis.setTimeout;
  const scheduled = [];
  globalThis.setTimeout = (callback, delay) => {
    scheduled.push({ callback, delay });
    return 0;
  };
  const bounded = offlineSync({ context, base: partition });
  try {
    await bounded.trigger();
    for (let i = 0; i < 5; i++) await scheduled[i].callback();
    check(scheduled.map(item => item.delay).join(",") === "1000,3000,10000,30000,30000,30000",
      "foreground retries continue at a bounded rate through a long outage");
    signedIn = true;
    await scheduled[5].callback();
    check(posts === 5, "recovery needs neither a page turn nor a new online event");
  } finally {
    bounded.stop();
    globalThis.setTimeout = originalTimeout;
  }
  globalThis.fetch = originalFetch;

  const old = context;
  const version = await s.accountVersion(partition);
  let releaseDownload, downloading;
  const blocked = new Promise(resolve => { releaseDownload = resolve; });
  const started = new Promise(resolve => { downloading = resolve; });
  const lateDownload = s.downloadPublication({ ...old, bookID, workID, request: async path => {
    if (path.includes("/resources/")) { downloading(); await blocked; }
    return publicationRequest(path);
  } });
  await started;
  await s.clearOfflineAccount(partition, account);
  await rejected(() => s.setActiveAccount(partition, account, version),
    "an old account handshake cannot undo logout");
  await s.setActiveAccount(partition, account);
  releaseDownload();
  await rejected(() => lateDownload, "in-flight download cannot commit after logout and relogin");
  await rejected(() => s.queueOfflineAnnotation({ ...old, bookID,
    annotation: { id: "late", kind: "note", body: "private" },
    payload: { method: "write", annotation: { id: "late" } } }), "old reader cannot write after relogin");
  await rejected(() => s.finishOfflineSession({ ...old, bookID, payload }), "late session cannot repopulate logout");
  await rejected(() => s.acknowledgeOfflineAnnotation(old, attempted, { id: "creating", rev: 1 }),
    "late acknowledgement cannot repopulate logout");
  await rejected(() => s.downloadPublication({ ...old, bookID, request: publicationRequest }),
    "late download cannot repopulate logout");
  check(!(await s.listOfflineAnnotations({ ...old, bookID })).length &&
    !(await s.listReadySnapshots(partition, account)).length, "logout leaves no private rows");
  return "IndexedDB publication, mutation, session, reconciliation, coordinator and logout checks passed";
}

test("offline state invariants in real Chromium IndexedDB", {
  skip: !process.env.SMOKE_CHROME, timeout: 80000,
}, async () => {
  const staticRoot = resolve(import.meta.dirname, "../static");
  const server = createServer(async (req, res) => {
    if (req.url === "/") { res.end("<!doctype html><title>Offline tests</title>"); return; }
    if (req.url.startsWith("/sync/v1/books/book/publication/digest/resources/")) {
      const body = Buffer.from(`<html xmlns="http://www.w3.org/1999/xhtml"><head/><body>${"Book text. ".repeat(150)}</body></html>`);
      const encoding = req.headers["x-test-encoding"];
      const encoded = encoding === "gzip" ? gzipSync(body) : encoding === "br" ? brotliCompressSync(body) : body;
      res.writeHead(200, { "Content-Type": "application/xhtml+xml", "Content-Encoding": encoding,
        "Content-Length": encoded.length });
      res.end(encoded);
      return;
    }
    const path = resolve(staticRoot, "." + req.url);
    if (!path.startsWith(staticRoot + "/")) { res.writeHead(404).end(); return; }
    try { res.setHeader("Content-Type", "text/javascript"); res.end(await readFile(path)); }
    catch { res.writeHead(404).end(); }
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const profile = resolve(import.meta.dirname, ".offline-chrome-" + process.pid);
  await mkdir(profile);
  const proc = spawn(process.env.SMOKE_CHROME, [
    "--headless=new", "--no-sandbox", "--disable-gpu", "--remote-debugging-port=0",
    "--user-data-dir=" + profile, "about:blank",
  ], { stdio: ["ignore", "ignore", "pipe"] });
  let ws;
  try {
    const endpoint = await new Promise((resolve, reject) => {
      let output = "";
      proc.once("error", reject);
      proc.stderr.on("data", data => {
        output += data;
        const match = output.match(/ws:\/\/[^\s]+/);
        if (match) resolve(match[0]);
      });
    });
    ws = new WebSocket(endpoint);
    await once(ws, "open");
    let id = 0;
    const waiting = new Map();
    ws.addEventListener("message", event => {
      const message = JSON.parse(event.data);
      if (!waiting.has(message.id)) return;
      const { resolve, reject } = waiting.get(message.id);
      waiting.delete(message.id);
      message.error ? reject(new Error(JSON.stringify(message.error))) : resolve(message.result);
    });
    const send = (method, params, sessionId) => new Promise((resolve, reject) => {
      waiting.set(++id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params, sessionId }));
    });
    const tab = async () => {
      const { targetId } = await send("Target.createTarget", { url: "about:blank" });
      const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
      await send("Page.navigate", { url: `http://127.0.0.1:${server.address().port}/` }, sessionId);
      for (let i = 0; i < 100; i++) {
        const result = await send("Runtime.evaluate", { expression: "document.title", returnByValue: true }, sessionId);
        if (result.result.value === "Offline tests") return sessionId;
        await new Promise(resolve => setTimeout(resolve, 20));
      }
      throw new Error("browser did not navigate");
    };
    const evaluate = async (session, expression) => {
      const result = await send("Runtime.evaluate", {
        expression, awaitPromise: true, returnByValue: true,
      }, session);
      assert.equal(result.exceptionDetails, undefined, JSON.stringify(result.exceptionDetails));
      return result.result.value;
    };
    const first = await tab();
    assert.match(await evaluate(first, `(${annotationChecks.toString()})()`), /checks passed/);
    assert.match(await evaluate(first, `(${storageChecks.toString()})()`), /checks passed/);
    const second = await tab();
    await evaluate(first, `(async () => {
      const s = await import("/offline-storage.js");
      window.release = await s.claimOfflineReader({ partition: location.origin, account: "locks" }, "book");
    })()`);
    assert.equal(await evaluate(second, `(async () => {
      const s = await import("/offline-storage.js");
      try { await s.claimOfflineReader({ partition: location.origin, account: "locks" }, "book"); return false; }
      catch { return true; }
    })()`), true, "second tab cannot restore a session owned by first tab");
    await evaluate(first, "window.release()");
    assert.equal(await evaluate(second, `(async () => {
      const s = await import("/offline-storage.js");
      const release = await s.claimOfflineReader({ partition: location.origin, account: "locks" }, "book");
      release(); return true;
    })()`), true, "reader claim can transfer after release");
  } finally {
    ws?.close();
    const exited = once(proc, "exit");
    proc.kill("SIGTERM");
    await exited;
    server.close();
    await rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
  }
});
