import { test } from "node:test";
import assert from "node:assert/strict";
import { readerAuth, REOPEN_MESSAGE } from "../static/reader-auth.js";

const json = (data, status = 200) => new Response(JSON.stringify(data), { status });
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
const deferred = () => {
  let resolve;
  const promise = new Promise((r) => { resolve = r; });
  return { promise, resolve };
};
const options = { apiBase: "/proxy/", tokenURL: "/proxy/ui/reader/token", csrf: "csrf" };
const metadata = (account = "a", device = "browser") => json({ account_id: account, device_id: device });

test("measured session duration requires an explicit server capability", async () => {
  for (const advertised of [undefined, false, "true", true]) {
    const auth = readerAuth({
      ...options,
      fetcher: async (url) => url === options.tokenURL ? json({ token: "one" }) :
        json({ account_id: "a", device_id: "browser", session_active_ms: advertised }),
    });
    const identity = await auth.acquire();
    assert.equal(identity.supportsActiveMs, advertised === true);
  }
});

test("concurrent streaming and API calls mint a single credential with Bearer headers", async () => {
  const mint = deferred();
  let mints = 0, inspections = 0;
  const calls = [];
  const auth = readerAuth({
    ...options,
    fetcher: async (url, opts) => {
      calls.push([url, opts]);
      if (url === options.tokenURL) { mints++; return mint.promise; }
      if (url.endsWith("v1/token")) { inspections++; return metadata(); }
      return json({ ok: true });
    },
  });
  const requests = [auth.request("v1/events"), auth.request("v1/works/w/positions")];
  mint.resolve(json({ token: "one", expires_at: "2099-01-01T00:00:00Z" }));
  const responses = await Promise.all(requests);
  assert.equal(mints, 1);
  assert.equal(inspections, 1);
  assert.equal(calls[0][1].credentials, "same-origin");
  assert.equal(String(calls[0][1].body), "csrf=csrf");
  for (const [url, opts] of calls.slice(1)) {
    assert.equal(opts.headers.Authorization, "Bearer one", url);
    assert.equal(opts.credentials, "omit", url);
  }
  responses.forEach((resp) => assert.equal(auth.responseCurrent(resp), true));
  assert.equal(auth.identity().account, "a");
  assert.equal(auth.identity().device, "browser");
});

test("concurrent 401 responses refresh once and late refusal cannot evict the new token", async () => {
  const late = deferred();
  let mints = 0, oldCalls = 0;
  const auth = readerAuth({
    ...options,
    fetcher: async (url, opts) => {
      if (url === options.tokenURL) return json({ token: "t" + ++mints });
      if (url.endsWith("v1/token")) return metadata();
      if (opts.headers.Authorization === "Bearer t1") {
        oldCalls++;
        return oldCalls === 1 ? json({}, 401) : late.promise;
      }
      return json({ ok: true });
    },
  });
  await auth.acquire();
  const first = auth.request("v1/events");
  const second = auth.request("v1/works/w/positions");
  await first;
  late.resolve(json({}, 401));
  const response = await second;
  assert.equal(mints, 2);
  assert.equal(auth.identity().secret, "t2");
  assert.equal(auth.responseCurrent(response), true);
});

test("second 401 after one refresh terminates rather than looping", async () => {
  let mints = 0, exhausted = 0;
  const auth = readerAuth({
    ...options, onExhausted() { exhausted++; },
    fetcher: async (url) => {
      if (url === options.tokenURL) return json({ token: "t" + ++mints });
      if (url.endsWith("v1/token")) return metadata();
      return json({}, 401);
    },
  });
  await assert.rejects(auth.request("v1/events"), (err) => err.terminal && err.message === REOPEN_MESSAGE);
  await assert.rejects(auth.request("v1/events"), (err) => err.terminal);
  assert.equal(mints, 2);
  assert.equal(exhausted, 1);
});

test("detached refusal stops all attempts and retains the reopen message", async () => {
  let calls = 0, exhausted = 0;
  const auth = readerAuth({
    ...options, detached: true, handed: "handed", onExhausted() { exhausted++; },
    fetcher: async (url) => {
      calls++;
      assert.notEqual(url, options.tokenURL);
      return url.endsWith("v1/token") ? metadata() : json({}, 401);
    },
  });
  await assert.rejects(auth.request("v1/events"), (err) => err.terminal && err.message === REOPEN_MESSAGE);
  await assert.rejects(auth.request("v1/events"), (err) => err.terminal);
  assert.equal(calls, 2);
  assert.equal(exhausted, 1);
});

test("expired handed token fails identity lookup without trying the session-cookie endpoint", async () => {
  const calls = [];
  const auth = readerAuth({
    ...options, detached: true, handed: "expired",
    fetcher: async (url) => { calls.push(url); return json({}, 401); },
  });
  await assert.rejects(auth.acquire(), (err) => err.terminal);
  assert.deepEqual(calls, ["/proxy/v1/token"]);
});

test("response generation remains attached through delayed body decoding", async () => {
  let time = 0, mints = 0;
  const changed = [];
  const auth = readerAuth({
    ...options, now: () => time, onChange: (next, old) => changed.push([next, old]),
    fetcher: async (url) => {
      if (url === options.tokenURL)
        return json({ token: "t" + ++mints, expires_at: new Date(time + 3600000).toISOString() });
      if (url.endsWith("v1/token")) return metadata(mints === 1 ? "a" : "b");
      return json({ value: "old" });
    },
  });
  const old = await auth.request("v1/works/w/positions");
  const stamp = auth.responseIdentity(old);
  time = 3600000;
  await auth.acquire();
  await old.json();
  assert.equal(auth.current(stamp), false);
  assert.equal(auth.responseCurrent(old), false);
  assert.equal(changed[1][1].account, "a");
  assert.equal(changed[1][0].account, "b");
});

test("account change can stop before any mutation under the replacement account", async () => {
  let time = 0, mints = 0, writes = 0, auth;
  auth = readerAuth({
    ...options, now: () => time,
    onChange(next, old) { if (old && old.account !== next.account) auth.stop(); },
    fetcher: async (url) => {
      if (url === options.tokenURL) return json({ token: "t" + ++mints, expires_at: new Date(time + 3600000).toISOString() });
      if (url.endsWith("v1/token")) return metadata(mints === 1 ? "a" : "b");
      writes++;
      return json({});
    },
  });
  await auth.acquire();
  time = 3600000;
  await assert.rejects(auth.request("v1/ops", { method: "POST" }), (err) => err.terminal);
  assert.equal(writes, 0);
});

test("stopped generation rejects an in-flight mint without installing it", async () => {
  const held = deferred();
  let changed = 0;
  const auth = readerAuth({
    ...options, onChange() { changed++; },
    fetcher: async (url) => url === options.tokenURL ? held.promise : metadata(),
  });
  const request = auth.acquire();
  auth.stop();
  held.resolve(json({ token: "late" }));
  await assert.rejects(request);
  assert.equal(changed, 0);
  assert.equal(auth.identity(), null);
});

test("an aborted stream waiting for the shared mint makes no event request", async () => {
  const held = deferred();
  let requests = 0;
  const auth = readerAuth({
    ...options,
    fetcher: async (url) => {
      if (url === options.tokenURL) return held.promise;
      if (url.endsWith("v1/token")) return metadata();
      requests++;
      return json({});
    },
  });
  const controller = new AbortController();
  const promise = auth.request("v1/events", { signal: controller.signal });
  controller.abort();
  held.resolve(json({ token: "one" }));
  await assert.rejects(promise, { name: "AbortError" });
  assert.equal(requests, 0);
});

test("cookie auth refusal is terminal, transient mint failure remains retryable", async () => {
  const denied = readerAuth({ ...options, fetcher: async () => json({}, 403) });
  await assert.rejects(denied.acquire(), (err) => err.terminal);
  let calls = 0;
  const auth = readerAuth({
    ...options,
    fetcher: async (url) => {
      if (url.endsWith("v1/token")) return metadata();
      if (++calls === 1) return json({}, 503);
      return json({ token: "one" });
    },
  });
  await assert.rejects(auth.acquire(), /could not obtain/);
  await auth.acquire();
  assert.equal(calls, 2);
});

test("old servers without introspection still allow ordinary requests", async () => {
  const auth = readerAuth({
    ...options,
    fetcher: async (url) => {
      if (url === options.tokenURL) return json({ token: "one", device_id: "browser" });
      if (url.endsWith("v1/token")) return json({}, 404);
      return json({});
    },
  });
  assert.equal((await auth.request("v1/works/w/positions")).ok, true);
  assert.equal(auth.identity().device, "browser");
});
