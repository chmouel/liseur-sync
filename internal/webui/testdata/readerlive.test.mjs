import { test } from "node:test";
import assert from "node:assert/strict";
import { eventParser, liveStream, retryDelay } from "../static/reader-live.js";

const encode = (s) => new TextEncoder().encode(s);
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
const frame = 'event: invalidate\r\ndata: {"topics":["positions","annotations","insights","positions"]}\r\n\r\n';

function clock() {
  let now = 0, id = 0;
  const tasks = new Map();
  return {
    now: () => now,
    setTimer(fn, ms) { const key = ++id; tasks.set(key, { at: now + ms, fn }); return key; },
    clearTimer(key) { tasks.delete(key); },
    async tick(ms) {
      const end = now + ms;
      for (;;) {
        const next = [...tasks.entries()].filter(([, v]) => v.at <= end)
          .sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break;
        now = next[1].at;
        tasks.delete(next[0]);
        next[1].fn();
        await flush();
      }
      now = end;
      await flush();
    },
    pending: () => tasks.size,
  };
}

test("split CRLF, one-byte UTF-8, comments and known topics", () => {
  const got = [];
  const parser = eventParser((topics) => got.push(topics));
  for (const byte of encode(": café 📚\r\n\r\n" + frame + "event: other\ndata: bad\n\n")) {
    parser.feed(Uint8Array.of(byte));
  }
  parser.end();
  assert.deepEqual(got, [["positions", "annotations"]]);
});

test("all chunk boundaries produce the same event; EOF never dispatches", () => {
  const bytes = encode(frame);
  for (let cut = 0; cut <= bytes.length; cut++) {
    const got = [];
    const parser = eventParser((topics) => got.push(topics));
    parser.feed(bytes.slice(0, cut));
    parser.feed(bytes.slice(cut));
    parser.end();
    assert.deepEqual(got, [["positions", "annotations"]]);
  }
  const got = [];
  const parser = eventParser((topics) => got.push(topics));
  parser.feed(encode('event: invalidate\ndata: {"topics":["positions"]}\n'));
  parser.end();
  assert.deepEqual(got, []);
});

test("multi-line data, bare CR and unknown fields", () => {
  const got = [];
  const parser = eventParser((topics) => got.push(topics));
  parser.feed(encode('id: ignored\rretry: 0\revent: invalidate\rdata: {"topics":\rdata: ["annotations"]}\r\r'));
  assert.deepEqual(got, [["annotations"]]);
});

test("oversized lines and frames are refused before unbounded accumulation", () => {
  const parser = eventParser(() => {}, 64);
  assert.throws(() => parser.feed(encode(":" + "a".repeat(64))), /large/);
  const multiline = eventParser(() => {}, 64);
  assert.throws(() => multiline.feed(encode("data: a\n".repeat(10))), /large/);
  const separate = eventParser(() => {}, 64);
  separate.feed(encode(": heartbeat\n\n".repeat(1000)));
});

test("malformed payloads and truncated/invalid UTF-8 terminate a connection", () => {
  assert.throws(() => eventParser(() => {}).feed(encode("event: invalidate\ndata: nope\n\n")));
  assert.throws(() => eventParser(() => {}).feed(encode('event: invalidate\ndata: {"topics":1}\n\n')));
  const parser = eventParser(() => {});
  parser.feed(Uint8Array.of(0xc3));
  assert.throws(() => parser.end());
  assert.throws(() => eventParser(() => {}).feed(Uint8Array.of(0xff)));
});

test("jitter is bounded, capped and Retry-After is a minimum", () => {
  assert.equal(retryDelay(0, null, 0, () => 0), 500);
  assert.equal(retryDelay(1, null, 0, () => 1), 2000);
  assert.equal(retryDelay(100, null, 0, () => 1), 60000);
  assert.equal(retryDelay(0, "120", 0, () => 0), 120000);
  assert.equal(retryDelay(0, "Thu, 01 Jan 1970 00:01:00 GMT", 0, () => 0), 60000);
  assert.equal(retryDelay(0, "nonsense", 0, () => 0), 500);
});

test("SSE retry advice is also a minimum, alongside Retry-After", () => {
  assert.equal(retryDelay(0, null, 0, () => 0, 5000), 5000);
  // Retry-After (seconds) and the stream's own retry advice (ms) both
  // floor the delay; the larger of the two wins.
  assert.equal(retryDelay(0, "1", 0, () => 0, 5000), 5000);
  assert.equal(retryDelay(0, "10", 0, () => 0, 5000), 10000);
  // A brief backoff cap can still exceed a small retry advice.
  assert.equal(retryDelay(100, "0", 0, () => 1, 1), 60000);
});

test("the retry field is parsed independently of any invalidate frame", () => {
  const got = [];
  let retryMS = null;
  const parser = eventParser((topics) => got.push(topics), undefined, (ms) => { retryMS = ms; });
  parser.feed(encode("retry: 5000\n\n" + frame));
  assert.equal(retryMS, 5000);
  assert.deepEqual(got, [["positions", "annotations"]]);
});

function socket(signal) {
  let controller;
  const body = new ReadableStream({
    start(c) {
      controller = c;
      signal.addEventListener("abort", () => {
        try { c.error(new DOMException("aborted", "AbortError")); } catch {}
      }, { once: true });
    },
  });
  return {
    response: new Response(body, { headers: { "Content-Type": "text/event-stream" } }),
    send: (s) => controller.enqueue(encode(s)),
    close: () => controller.close(),
  };
}

test("one visible stream, opening hints, heartbeats and 60-second silence watchdog", async () => {
  const time = clock(), got = [], signals = [];
  let connection;
  const live = liveStream({
    ...time, random: () => 0, current: () => true,
    request: async (path, options) => {
      assert.equal(path, "v1/events");
      assert.equal(options.headers.Accept, "text/event-stream");
      signals.push(options.signal);
      connection = socket(options.signal);
      return connection.response;
    },
    onTopics: (topics) => got.push(topics),
  });
  live.start(); live.start();
  await flush();
  assert.equal(signals.length, 1);
  connection.send(frame);
  await flush();
  assert.deepEqual(got, [["positions", "annotations"]]);
  await time.tick(40000);
  connection.send(": heartbeat\n\n");
  await flush();
  await time.tick(40000);
  assert.equal(signals[0].aborted, false);
  await time.tick(20000);
  assert.equal(signals[0].aborted, true);
  await time.tick(500);
  assert.equal(signals.length, 2);
  live.stop();
  await flush();
  assert.equal(signals[1].aborted, true);
  assert.equal(time.pending(), 0);
});

test("quick 200/EOF failures increase delay instead of resetting it", async () => {
  const time = clock();
  let calls = 0;
  const live = liveStream({
    ...time, random: () => 0, current: () => true, onTopics() {},
    request: async () => {
      calls++;
      return new Response("", { headers: { "Content-Type": "text/event-stream" } });
    },
  });
  live.start(); await flush();
  assert.equal(calls, 1);
  await time.tick(500);
  assert.equal(calls, 2);
  await time.tick(999);
  assert.equal(calls, 2);
  await time.tick(1);
  assert.equal(calls, 3);
  live.stop(); await flush();
});

test("unsupported and forbidden streams wait for the next visible boundary", async () => {
  for (const status of [401, 403, 404, 501]) {
    const time = clock();
    let calls = 0;
    const live = liveStream({
      ...time, current: () => true, onTopics() {},
      request: async () => { calls++; return new Response(null, { status }); },
    });
    live.start(); await flush();
    await time.tick(300000);
    assert.equal(calls, 1);
    live.stop(); live.start(); await flush();
    assert.equal(calls, 2);
    live.stop();
  }
});

test("429 respects Retry-After and hidden state cancels retry", async () => {
  const time = clock();
  let calls = 0;
  const live = liveStream({
    ...time, random: () => 0, current: () => true, onTopics() {},
    request: async () => {
      calls++;
      return new Response(null, { status: 429, headers: { "Retry-After": "120" } });
    },
  });
  live.start(); await flush();
  await time.tick(119999);
  assert.equal(calls, 1);
  await time.tick(1);
  assert.equal(calls, 2);
  live.stop();
  await time.tick(300000);
  assert.equal(calls, 2);
});

test("late headers and obsolete credentials cannot deliver queued topics", async () => {
  const time = clock(), got = [];
  let resolve, connection, valid = true;
  const live = liveStream({
    ...time, current: () => valid, onTopics: (t) => got.push(t),
    request: (_, options) => {
      connection = socket(options.signal);
      return new Promise((r) => { resolve = r; });
    },
  });
  live.start();
  live.stop();
  resolve(connection.response);
  await flush();
  assert.deepEqual(got, []);
  live.start();
  resolve(connection.response);
  await flush();
  valid = false;
  connection.send(frame);
  await flush();
  assert.deepEqual(got, []);
  live.stop();
});

test("terminal credential exhaustion does not retry", async () => {
  const time = clock();
  let calls = 0;
  const live = liveStream({
    ...time, current: () => true, onTopics() {},
    request: async () => { calls++; throw Object.assign(new Error("expired"), { terminal: true }); },
  });
  live.start(); await flush();
  await time.tick(300000);
  assert.equal(calls, 1);
  live.stop();
});
