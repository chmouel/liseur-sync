import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";
import {
  openSession, resolveListeningCheckpointFraction, resolveSessionEndFraction,
  planAudioTicketRefresh,
} from "../static/reader-session.js";

const source = readFileSync(new URL("../static/reader-app.js", import.meta.url), "utf8");
const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};
const flush = async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
};

class Element {
  hidden = false;
  value = "1";
  classList = { toggle() {} };
  listeners = new Map();
  setAttribute() {}
  addEventListener(type, fn, options = {}) {
    const entries = this.listeners.get(type) || [];
    entries.push({ fn, once: options.once });
    this.listeners.set(type, entries);
  }
  removeEventListener(type, fn) {
    this.listeners.set(type, (this.listeners.get(type) || []).filter(entry => entry.fn !== fn));
  }
  async emit(type) {
    for (const entry of [...(this.listeners.get(type) || [])]) {
      if (entry.once) this.removeEventListener(type, entry.fn);
      await entry.fn();
    }
  }
}

function player() {
  const audio = new Element();
  Object.assign(audio, {
    currentTime: 200, duration: 1000, paused: true, ended: false, seeking: false,
    readyState: 0, playbackRate: 1, playCalls: 0,
    async play() { this.playCalls++; this.paused = false; },
    pause() { this.paused = true; },
  });
  let src = "";
  Object.defineProperty(audio, "src", {
    get: () => src,
    set(value) { src = value; audio.currentTime = 0; audio.readyState = 0; },
  });
  const clock = { now: 0 };
  const navigations = [];
  const errors = [];
  let scheduledPushes = 0;
  const context = createContext({
    openSession, resolveListeningCheckpointFraction, resolveSessionEndFraction,
    planAudioTicketRefresh, AbortController, URL, console,
    setTimeout: () => 1, clearTimeout() {},
    performance: { now: () => clock.now },
    document: { hidden: false },
    window: { location: { href: "https://books.test/ui/reader/b" } },
    cfg: { bookID: "b", apiBase: "https://books.test/", offline: false },
    workID: "w", ready: true, restoring: false, offlineInvalidated: false,
    offlineContext: null, sessionOwner: false, offlineCheckpoint: null,
    here: { fraction: 0.8 }, readingDirty: false, activityGeneration: 0,
    interactionPending: false, leaving: false,
    finite: Number.isFinite, opID: () => "session",
    checkpointSession() {}, pushSession() {}, schedulePush() { scheduledPushes++; },
    catchup: { moved() {} }, hideCatchup() {},
    say: message => errors.push(message),
    auth: { identity: () => ({ supportsActiveMs: true }), refreshBefore() {}, responseCurrent: () => true },
    api: async () => ({
      ok: true,
      json: async () => ({
        stream_url: "/v1/books/b/audio/stream/t",
        expires_at: new Date(Date.now() + 3600000).toISOString(),
      }),
    }),
    editionSHA: () => "edition",
    startCandidates: op => [op.locator],
    withAnchor: locator => locator,
    bookTitle: () => "Book",
    sectionProgression: location => location.sectionFraction || 0,
    view: {
      book: { sections: [{ id: "page.xhtml" }] },
      resolveNavigation: async target => target,
      renderer: { goTo: async target => navigations.push(target) },
    },
    audioEl: audio,
  });
  for (const name of [
    "audioToggleBtn", "audioBar", "audioPlayBtn",
    "audioBackBtn", "audioFwdBtn", "audioChapterLabel", "audioCurrentLabel",
    "audioDurationLabel", "audioSeekInput", "audioRateSelect", "audioFollowBtn",
    "audioFromPageBtn", "audioToAudioBtn", "audioCloseBtn",
  ]) context[name] = new Element();
  const run = script => runInContext(script, context);
  run(source.slice(source.indexOf("function locatorFor(location)"), source.indexOf("// withAnchor describes")));
  run(source.slice(source.indexOf("let session = null;"), source.indexOf("function checkpointSession()")));
  run(source.slice(source.indexOf("function noteActivity()"), source.indexOf("// noteProgress follows")));
  run(source.slice(source.indexOf("async function endSession("), source.indexOf("let sessionsInFlight")));
  run(source.slice(source.indexOf("let audioTicketTimer = null;")));
  run(`audioInfo = {
    audio_time: 200,
    mapping: {
      segments: [{ a0: 0, a1: 1000, b0: 0, b1: 1, href: "chapter.xhtml" }],
      resources: [{ href: "chapter.xhtml", start: 0, end: 1 }],
      full_narrative: true
    }
  };`);
  return { audio, clock, context, navigations, errors, run, get scheduledPushes() { return scheduledPushes; } };
}

test("a manual page turn cancels an audio navigation still resolving", async () => {
  const p = player();
  const held = deferred();
  p.context.view.resolveNavigation = () => held.promise;
  const navigation = p.run('navigateToAudioTarget({ href: "chapter.xhtml" }, 0.2)');
  p.run("noteNavigation()");
  held.resolve({ href: "chapter.xhtml" });
  assert.equal(await navigation, false);
  assert.equal(p.run("followAudio"), false);
  assert.equal(p.navigations.length, 0);
});

test("ticket replacement ignores the temporary zero position until metadata restores the playhead", async () => {
  const p = player();
  const mint = p.run("mintAudioTicket(true)");
  await flush();
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(p.navigations.length, 0, "loading a replacement source must not navigate to the beginning");
  p.audio.readyState = 1;
  await p.audio.emit("loadedmetadata");
  assert.equal(await mint, true);
  assert.equal(p.audio.currentTime, 200);
});

test("stalled network fetch does not stop accounting while buffered audio is still playing", async () => {
  const p = player();
  p.audio.paused = false;
  await p.audio.emit("play");
  assert.equal(p.context.audioPlayBtn.textContent, "Pause");
  await p.audio.emit("playing");
  p.clock.now = 10000;
  await p.audio.emit("stalled");
  p.clock.now = 20000;
  await p.audio.emit("waiting");
  assert.equal(p.run("session.checkpoint().activeMs"), 20000);
  await p.audio.emit("pause");
  assert.equal(p.context.audioPlayBtn.textContent, "Play");
});

test("the player uses API segment keys and resource coordinates rather than chapter coordinates", () => {
  const p = player();
  p.run(`audioInfo.mapping.segments = [
    { a0: 0, a1: 500, b0: 0, b1: 0.5, href: "chapter.xhtml" },
    { a0: 500, a1: 1000, b0: 0.5, b1: 1, href: "chapter.xhtml" }
  ];`);
  let mapped = p.run("audioTimeToBook(750)");
  assert.equal(mapped.progression, 0.75);
  assert.equal(mapped.chapterProgress, 0.75);
  p.run(`audioInfo.mapping.resources = [
    { href: "chapter.xhtml", start: 0, end: 0.6 },
    { href: "continuation.xhtml", start: 0.6, end: 1 }
  ];`);
  mapped = p.run("audioTimeToBook(800)");
  assert.equal(mapped.href, "continuation.xhtml");
  assert.ok(Math.abs(mapped.chapterProgress - 0.5) < 1e-10);
  assert.equal(p.run("audioTimeToBook(1100)"), null);
});

test("closing the player finalizes listening and resumes reading accounting", async () => {
  for (const control of ["audioCloseBtn", "audioToggleBtn"]) {
    const p = player();
    p.audio.paused = false;
    await p.audio.emit("play");
    await p.audio.emit("playing");
    assert.equal(p.run("session.mode"), "listening");
    p.clock.now = 15000;
    await p.context[control].emit("click");
    await flush();
    assert.equal(p.run("session && session.mode"), "reading", control);
    assert.equal(p.run("unsent.length"), 1, control);
    assert.equal(p.run("unsent[0].mode"), "listening", control);
  }
});

test("closing the player while a replacement loads prevents playback from resuming", async () => {
  const p = player();
  p.audio.paused = false;
  p.run("audioWantsPlay = true");
  const mint = p.run("mintAudioTicket(true)");
  await flush();
  await p.context.audioCloseBtn.emit("click");
  p.audio.readyState = 1;
  await p.audio.emit("loadedmetadata");
  assert.equal(await mint, true);
  assert.equal(p.audio.playCalls, 0);
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(p.navigations.length, 0);
});

test("listening with followAudio disabled still records mapped playback positions without moving the page", async () => {
  const p = player();
  p.run("setFollowAudio(false)");
  p.audio.paused = false;
  p.audio.currentTime = 350;
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(p.navigations.length, 0, "text view must not navigate while followAudio is disabled");
  assert.equal(p.run("readingDirty"), true);
  assert.equal(p.scheduledPushes, 1);
  assert.deepEqual(JSON.parse(p.run("JSON.stringify(pendingPositionLocator())")), {
    href: "chapter.xhtml",
    type: "application/xhtml+xml",
    title: "Book",
    locations: {
      fragments: [],
      progression: 0.35,
      totalProgression: 0.35,
    },
  });

  // Pausing while followAudio is disabled refines and records the audio locator without moving the page.
  p.context.api = async (url) => {
    if (url.endsWith("/audio/locate")) {
      return {
        ok: true,
        json: async () => ({
          progression: 0.355,
          locator: {
            href: "chapter.xhtml",
            type: "application/xhtml+xml",
            text: { highlight: "spoken words" },
            locations: { progression: 0.355, totalProgression: 0.355 },
          },
        }),
      };
    }
    throw new Error("unexpected url " + url);
  };
  p.audio.paused = true;
  await p.audio.emit("pause");
  await flush();
  assert.equal(p.navigations.length, 0);
  assert.equal(p.scheduledPushes, 2);
  assert.deepEqual(JSON.parse(p.run("JSON.stringify(pendingPositionLocator())")), {
    href: "chapter.xhtml",
    type: "application/xhtml+xml",
    title: "Book",
    text: { highlight: "spoken words" },
    locations: {
      fragments: [],
      progression: 0.355,
      totalProgression: 0.355,
    },
  });
  assert.equal(p.run("locatorMatchesCurrentPosition(pendingPositionLocator())"), true);

  // A subsequent manual page turn clears the audio locator so pushPosition records the rendered page.
  p.run("audioSyncLocator = null");
  assert.deepEqual(JSON.parse(p.run("JSON.stringify(pendingPositionLocator())")), {
    href: "page.xhtml",
    type: "application/xhtml+xml",
    title: "Book",
    locations: {
      fragments: [],
      progression: 0,
      totalProgression: 0.8,
      position: 1,
    },
  });
});

test("an expired reading credential cannot install a delayed stream ticket", async () => {
  const p = player();
  const held = deferred();
  p.context.api = () => held.promise;
  const mint = p.run("mintAudioTicket()");
  p.context.ready = false;
  held.resolve({ ok: true, json: async () => ({ stream_url: "/audio" }) });
  assert.equal(await mint, false);
  assert.equal(p.audio.src, "");
});
