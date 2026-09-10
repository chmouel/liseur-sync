import { test } from "node:test";
import assert from "node:assert/strict";
import { catchupState, candidateID, topicRefresh, latestReadablePosition, positionAcknowledged } from "../static/reader-sync.js";

test("position reads skip corrupt heads without inventing a zero", () => {
  const good = { op_id: "good", work_id: "work", progression: 0.7 };
  for (const progression of [null, undefined, NaN, Infinity, -0.1, 1.1, "bad"]) {
    const bad = { ...good, op_id: "bad", progression };
    assert.deepEqual(latestReadablePosition([bad, good], "work"), good);
    assert.equal(latestReadablePosition([bad], "work"), null);
  }
  assert.equal(latestReadablePosition([{ ...good, work_id: "other" }], "work"), null);
  assert.equal(latestReadablePosition([{ ...good, locator: { locations: { totalProgression: null } } }, good], "work").op_id, "good");
  assert.equal(latestReadablePosition([{ ...good, progression: 0 }, good], "work").progression, 0);
});

test("only an acknowledgement of this exact operation settles a position", () => {
  const op = { op_id: "mine" };
  for (const status of ["applied", "duplicate"]) {
    assert.equal(positionAcknowledged({ results: [{ op_id: "mine", status }] }, op), true);
    assert.equal(positionAcknowledged({ results: [{ op_id: "other", status }] }, op), false);
  }
  for (const status of ["conflict", "invalid", undefined])
    assert.equal(positionAcknowledged({ results: [{ op_id: "mine", status }] }, op), false);
  assert.equal(positionAcknowledged(null, op), false);
});

// Distinct operations sit at distinct places. Reconciliation compares
// positions now, not identities, so two ops at the same spot are the
// same news however they are named. Pass a progression explicitly when
// that sameness is the point.
const places = new Map();
const place = (id) => {
  if (!places.has(id)) places.set(id, 0.1 + places.size * 0.05);
  return places.get(id);
};
const op = (id, device = "phone", progression = place(id)) => ({
  op_id: id, device_id: device, work_id: "work", progression,
  locator: {
    href: "chapter.xhtml",
    locations: { totalProgression: progression, fragments: [`epubcfi(/6/${Math.round(progression * 1000)})`] },
  },
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

test("a local page turn disagrees with the remote position instead of discarding it", () => {
  const s = state();
  s.observe(op("remote"));
  s.hide(); s.resume();
  assert.equal(s.offer().kind, "pull");
  s.local(op("mine"), true);
  s.moved();
  assert.equal(s.offer(), null); // never mid-page
  s.hide(); s.resume();
  const offer = s.offer();
  assert.equal(offer.kind, "conflict");
  assert.equal(offer.op.op_id, "remote");
  assert.equal(offer.local.op_id, "mine");
});

test("an acknowledged local position is agreement, so a matching remote echo says nothing", () => {
  const s = state();
  const mine = op("mine");
  s.local(mine, true);
  s.settled(mine);
  s.observe(mine);
  s.hide(); s.resume();
  assert.equal(s.offer(), null);
});

test("only this side moving is a push, and asks the reader nothing", () => {
  const s = state();
  s.local(op("mine"), true);
  s.observe(op("opening"));
  s.hide(); s.resume();
  assert.equal(s.offer(), null);
});

test("a conflict answered stays answered across a reload of the same state", () => {
  const s = state();
  s.local(op("mine"), true);
  s.observe(op("remote"));
  s.hide(); s.resume();
  const offer = s.offer();
  assert.equal(offer.kind, "conflict");
  assert.deepEqual(s.accept(offer).op_id, "remote");
  // Accepting agrees on the remote position for both sides, so the
  // same news arriving again is no longer news.
  s.observe(op("remote"));
  s.hide(); s.resume();
  assert.equal(s.offer(), null);
});

test("staying put answers the disagreement without moving the baseline of this device", () => {
  const s = state();
  s.local(op("mine"), true);
  s.observe(op("remote"));
  s.hide(); s.resume();
  assert.equal(s.offer().kind, "conflict");
  s.dismiss();
  s.observe(op("remote"));
  s.hide(); s.resume();
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

// Telling this reader's own stale copy on the server apart from another
// device having moved is what the baseline is for, so the baseline has
// to be readable by whoever is doing the telling.
test("the agreed baseline can be read back", () => {
  const s = state();
  assert.equal(s.agreed().op_id, "opening");
  const answered = op("answered");
  s.observe(answered);
  s.hide(); s.resume();
  s.offer();
  s.dismiss();
  assert.equal(s.agreed().op_id, "answered", "staying agrees the position stayed away from");
  const taken = op("taken");
  s.observe(taken);
  s.hide(); s.resume();
  assert.deepEqual(s.accept(s.offer()), taken);
  assert.equal(s.agreed().op_id, "taken");
});

// A page written to the durable queue clears the in-memory dirty flag
// long before anyone delivers it, so whoever asks whether this device
// owes the server a position has to ask the queue.
test("a page still queued counts as owed", () => {
  const s = state();
  assert.equal(s.pending(), false);
  s.local(op("written"));
  assert.equal(s.pending(), true);
  s.settled(op("written"));
  assert.equal(s.pending(), false, "an acknowledgement is the end of owing it");
});

test("a book with nothing agreed yet says so rather than inventing one", () => {
  const s = catchupState();
  s.bind("account", "work", "browser");
  assert.equal(s.agreed(), null);
});
