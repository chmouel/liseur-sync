import { test } from "node:test";
import assert from "node:assert/strict";
import { catchupState, candidateID, topicRefresh } from "../static/reader-sync.js";

const op = (id, device = "phone", progression = 0.6) => ({
  op_id: id, device_id: device, work_id: "work", progression,
  locator: { href: "chapter.xhtml", locations: { totalProgression: progression, fragments: ["epubcfi(/6/2)"] } },
});
function state() {
  const s = catchupState();
  s.bind("account", "work", "browser");
  s.baseline(op("opening"));
  return s;
}

test("live positions are held while reading and offered only on resume", () => {
  const s = state(), remote = op("new");
  s.observe(remote);
  assert.equal(s.offer(), null);
  s.resume(); // duplicate visible/focus-like event is not a resume
  assert.equal(s.offer(), null);
  s.hide(); s.resume();
  const offer = s.offer();
  assert.equal(offer.op.op_id, "new");
  assert.deepEqual(s.accept(offer), remote);
  assert.equal(s.offer(), null);
});

test("resume may await its snapshot, but later visible events do not nag", () => {
  const s = state();
  s.hide(); s.resume();
  s.observe(op("first"));
  assert.equal(s.offer().op.op_id, "first");
  s.dismiss();
  s.observe(op("second"));
  assert.equal(s.offer(), null);
  s.hide(); s.resume();
  assert.equal(s.offer().op.op_id, "second");
});

test("incoming updates never retarget a displayed offer; stale acceptance is refused", () => {
  const s = state();
  s.observe(op("one")); s.hide(); s.resume();
  const offer = s.offer();
  s.observe(op("two"));
  assert.equal(s.offer(), offer);
  assert.equal(offer.op.op_id, "one");
  assert.equal(s.accept(offer), null);
});

test("local movement, account/work/token replacement and hidden state invalidate acceptance", () => {
  for (const invalidate of [
    (s) => s.moved(),
    (s) => s.bind("other", "work", "browser"),
    (s) => s.bind("account", "other-work", "browser"),
    (s) => s.bind("account", "work", "browser"),
    (s) => s.hide(),
  ]) {
    const s = state();
    s.observe(op("one")); s.hide(); s.resume();
    const offer = s.offer();
    invalidate(s);
    assert.equal(s.accept(offer), null);
  }
});

test("new local page does not rediscover the discarded remote candidate", () => {
  const s = state();
  s.observe(op("remote"));
  s.moved();
  s.hide(); s.resume();
  s.observe(op("remote"));
  assert.equal(s.offer(), null);
});

test("dismissed and baseline operations do not reappear", () => {
  const s = state();
  s.observe(op("opening")); s.hide(); s.resume();
  assert.equal(s.offer(), null);
  s.observe(op("one")); s.hide(); s.resume();
  s.offer(); s.dismiss();
  s.hide(); s.resume(); s.observe(op("one"));
  assert.equal(s.offer(), null);
});

test("self-authorship requires actual op/device, not shared browser device alone", () => {
  const s = state();
  s.wrote(op("mine", "browser"));
  s.observe(op("mine", "browser")); s.hide(); s.resume();
  assert.equal(s.offer(), null);
  s.observe(op("other-tab", "browser")); s.hide(); s.resume();
  assert.equal(s.offer().op.op_id, "other-tab");
  s.dismiss();
  s.observe(op("mine", "phone")); s.hide(); s.resume();
  assert.equal(s.offer().op.device_id, "phone");
});

test("self op identity survives a same-account credential renewal", () => {
  const s = state();
  s.wrote(op("mine", "browser"));
  s.bind("account", "work", "browser");
  s.observe(op("mine", "browser")); s.hide(); s.resume();
  assert.equal(s.offer(), null);
});

test("credential renewal during a resume keeps its owed offer, not an old target", () => {
  const s = state();
  s.observe(op("old")); s.hide(); s.resume();
  s.bind("account", "work", "browser");
  s.observe(op("fresh"));
  assert.equal(s.offer().op.op_id, "fresh");
});

test("full locator and identity survive snapshots without mutable aliases", () => {
  const s = state(), remote = op("id");
  s.observe(remote);
  remote.locator.href = "mutated.xhtml";
  s.hide(); s.resume();
  const offer = s.offer();
  assert.equal(offer.id, candidateID("account", "work", op("id")));
  assert.equal(s.accept(offer).locator.href, "chapter.xhtml");
  assert.notEqual(candidateID("account", "work", op("id")), candidateID("other", "work", op("id")));
  assert.notEqual(candidateID("account", "work", op("id")), candidateID("account", "work", op("id", "other")));
});

const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
function queue(refresh) {
  let scheduled = null;
  const q = topicRefresh({
    refresh,
    setTimer(fn) { scheduled = fn; return 1; },
    clearTimer() { scheduled = null; },
  });
  return {
    q,
    async run() { const f = scheduled; scheduled = null; f?.(); await flush(); },
    pending: () => !!scheduled,
  };
}

test("topic bursts coalesce with at most one follow-up for each held topic", async () => {
  const calls = [];
  let release;
  const { q, run, pending } = queue(async (topic) => {
    calls.push(topic);
    if (calls.length === 1) await new Promise((r) => { release = r; });
    return true;
  });
  q.owe(["positions", "annotations", "insights"]); q.start();
  await run();
  for (let i = 0; i < 100; i++) q.owe(["positions", "annotations"]);
  assert.deepEqual(calls, ["positions"]);
  release(); await flush();
  assert.deepEqual(calls, ["positions", "annotations"]);
  assert.equal(pending(), true);
  await run();
  assert.deepEqual(calls, ["positions", "annotations", "positions", "annotations"]);
  assert.equal(pending(), false);
});

test("failed topic stays owed without another event and does not lose other topics", async () => {
  const calls = [];
  const { q, run, pending } = queue(async (topic) => {
    calls.push(topic);
    if (calls.length === 1) throw new Error("offline");
    return true;
  });
  q.start(); q.owe(["positions", "annotations"]);
  await run();
  assert.deepEqual(calls, ["positions", "annotations"]);
  assert.equal(pending(), true);
  await run();
  assert.deepEqual(calls, ["positions", "annotations", "positions"]);
});

test("hidden stops reads; restart preserves debt; account reset drops it", async () => {
  const calls = [];
  const { q, run, pending } = queue(async (topic) => { calls.push(topic); return true; });
  q.start(); q.owe(["positions"]); q.stop();
  await run();
  assert.deepEqual(calls, []);
  q.start(); await run();
  assert.deepEqual(calls, ["positions"]);
  q.owe(["annotations"]); q.reset();
  await run();
  assert.equal(pending(), false);
  assert.deepEqual(calls, ["positions"]);
});

test("failure from a previous account does not requeue on the new account", async () => {
  let reject;
  const calls = [];
  const { q, run, pending } = queue(async (topic) => {
    calls.push(topic);
    await new Promise((_, r) => { reject = r; });
  });
  q.start(); q.owe(["positions", "annotations"]); await run();
  q.reset();
  reject(new Error("old response")); await flush();
  assert.equal(pending(), false);
  assert.deepEqual(calls, ["positions"]);
});
