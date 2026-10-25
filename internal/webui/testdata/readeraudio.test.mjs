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

test("live Whisper refinement runs periodically while following audio and compensates for elapsed playback", async () => {
  const p = player();
  p.run("audioInfo.stt_enabled = true;");
  const locateCalls = [];
  const held = deferred();
  p.context.api = async (url, opts) => {
    if (url.endsWith("/audio/locate")) {
      locateCalls.push(JSON.parse(opts.body));
      return held.promise;
    }
    throw new Error("unexpected url " + url);
  };

  p.audio.paused = false;
  p.audio.currentTime = 200;
  p.clock.now = 5000;
  await p.audio.emit("play");
  await p.audio.emit("playing");
  await flush();
  assert.equal(locateCalls.length, 1);
  assert.deepEqual(locateCalls[0], { audio_time: 200, refine_stt: true });

  // While the first request is in flight, timeupdate must not launch a duplicate request.
  p.audio.currentTime = 203;
  p.clock.now = 7000;
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(locateCalls.length, 1);
  assert.equal(p.navigations.at(-1).locations.progression, 0.203);

  // Resolve the STT response 3s later; the progression is adjusted by the 3s chapter delta (+0.003).
  held.resolve({
    ok: true,
    json: async () => ({
      stt_matched: true,
      progression: 0.25,
      locator: {
        href: "chapter.xhtml",
        type: "application/xhtml+xml",
        text: { before: 'whispered, "', highlight: "Don't stop, John", after: '," and hurried' },
        locations: { progression: 0.25, totalProgression: 0.25 },
      },
    }),
  });
  await flush();
  const lastNav = p.navigations.at(-1);
  assert.equal(lastNav.href, "chapter.xhtml");
  assert.equal(lastNav.text.highlight, "Don't stop, John");
  assert.ok(Math.abs(lastNav.locations.progression - 0.253) < 1e-9);
  assert.ok(Math.abs(lastNav.locations.totalProgression - 0.253) < 1e-9);

  for (const time of [204, 205, 210]) {
    p.audio.currentTime = time;
    p.clock.now = 7000 + (time - 203) * 1000;
    await p.audio.emit("timeupdate");
    await flush();
    const progression = 0.253 + (time - 203) / 1000;
    assert.ok(Math.abs(p.navigations.at(-1).locations.progression - progression) < 1e-9);
    assert.ok(Math.abs(p.run("liveAudioFraction()") - progression) < 1e-9);
    assert.equal(locateCalls.length, 1);
  }

  // After 15s of playback, timeupdate triggers the next live STT refinement.
  p.context.api = async (url, opts) => {
    if (url.endsWith("/audio/locate")) {
      locateCalls.push(JSON.parse(opts.body));
      return {
        ok: true,
        json: async () => ({
          stt_matched: true,
          progression: 0.27,
          locator: {
            href: "chapter.xhtml",
            type: "application/xhtml+xml",
            text: { highlight: "next spoken phrase" },
            locations: { progression: 0.27, totalProgression: 0.27 },
          },
        }),
      };
    }
    throw new Error("unexpected url " + url);
  };
  p.audio.currentTime = 218;
  p.clock.now = 21000;
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(locateCalls.length, 2);
  assert.deepEqual(locateCalls[1], { audio_time: 218, refine_stt: true });
  assert.equal(p.run("audioSTTAnchor.progression"), 0.27);
  p.audio.currentTime = 219;
  p.clock.now = 22000;
  await p.audio.emit("timeupdate");
  await flush();
  assert.ok(Math.abs(p.navigations.at(-1).locations.progression - 0.271) < 1e-9);
});

for (const result of ["HTTP failure", "network failure", "no match"]) {
  test(`mapped following continues during repeated live STT requests with ${result}`, async () => {
    const p = player();
    p.run("audioInfo.stt_enabled = true;");
    const requests = [];
    p.context.api = () => {
      const held = deferred();
      requests.push(held);
      return held.promise;
    };
    p.audio.paused = false;
    await p.audio.emit("play");
    await p.audio.emit("playing");
    await flush();
    assert.equal(requests.length, 1);

    for (let round = 0; round < 3; round++) {
      p.audio.currentTime = 215 + round * 16;
      p.clock.now = 16000 + round * 16000;
      await p.audio.emit("timeupdate");
      await flush();
      assert.equal(requests.length, round + 1);
      assert.equal(p.navigations.at(-1).locations.progression, p.audio.currentTime / 1000);
      requests[round].resolve(result === "HTTP failure" ? { ok: false } : {
        ok: true,
        json: async () => {
          if (result === "network failure") throw Error("connection lost");
          return { stt_matched: false, progression: 0.9, locator: { href: "chapter.xhtml" } };
        },
      });
      await flush();
      assert.equal(p.run("sttAbort"), null);
      p.audio.currentTime++;
      p.clock.now += 1000;
      await p.audio.emit("timeupdate");
      await flush();
      assert.equal(requests.length, round + 2);
      assert.equal(p.navigations.at(-1).locations.progression, p.audio.currentTime / 1000);
    }
    requests.at(-1).resolve({ ok: false });
    await flush();
  });
}

const sttResponse = progression => ({
  ok: true,
  json: async () => ({
    stt_matched: true,
    progression,
    locator: { href: "chapter.xhtml", locations: { progression, totalProgression: progression } },
  }),
});

test("live STT corrections are replaced by a newer refinement and cleared by seeking", async () => {
  const p = player();
  p.run("audioInfo.stt_enabled = true;");
  p.audio.paused = false;
  p.context.api = async () => sttResponse(0.25);
  assert.equal(await p.run("refineAudioSTT(200, { allowPlaying: true })"), true);
  p.audio.currentTime = 205;
  p.clock.now = 5000;
  p.context.api = async () => sttResponse(0.3);
  assert.equal(await p.run("refineAudioSTT(205, { allowPlaying: true })"), true);
  p.audio.currentTime = 206;
  await p.audio.emit("timeupdate");
  await flush();
  assert.ok(Math.abs(p.navigations.at(-1).locations.progression - 0.301) < 1e-9);

  p.audio.currentTime = 400;
  await p.audio.emit("seeking");
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(p.navigations.at(-1).locations.progression, 0.4);
});

test("live STT corrections expire when playback leaves the mapped chapter", async () => {
  const p = player();
  p.run(`audioInfo.stt_enabled = true;
    audioInfo.mapping.segments = [
      { a0: 0, a1: 300, b0: 0, b1: 0.3, href: "chapter.xhtml" },
      { a0: 300, a1: 1000, b0: 0.3, b1: 1, href: "next.xhtml" }
    ];
    audioInfo.mapping.resources = [
      { href: "chapter.xhtml", start: 0, end: 0.3 },
      { href: "next.xhtml", start: 0.3, end: 1 }
    ];`);
  p.audio.paused = false;
  p.context.api = async () => sttResponse(0.25);
  await p.run("refineAudioSTT(200, { allowPlaying: true })");
  p.audio.currentTime = 290;
  p.clock.now = 5000;
  const held = deferred();
  p.context.api = () => held.promise;
  const refinement = p.run("refineAudioSTT(290, { allowPlaying: true })");
  p.audio.currentTime = 301;
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(p.navigations.at(-1).href, "next.xhtml");
  assert.ok(Math.abs(p.navigations.at(-1).locations.totalProgression - 0.301) < 1e-9);
  assert.equal(p.run("audioSTTAnchor"), null);
  held.resolve(sttResponse(0.29));
  assert.equal(await refinement, false);
  assert.equal(p.run("audioSTTAnchor"), null);
  assert.equal(p.navigations.at(-1).href, "next.xhtml");
});

test("manual navigation cancels a live STT result while its target is resolving", async () => {
  const p = player();
  p.run("audioInfo.stt_enabled = true;");
  p.audio.paused = false;
  p.context.api = async () => sttResponse(0.25);
  const held = deferred();
  p.context.view.resolveNavigation = () => held.promise;
  const refinement = p.run("refineAudioSTT(200, { allowPlaying: true })");
  await flush();
  assert.ok(p.run("audioSTTAnchor"));
  p.run("noteNavigation();");
  held.resolve({ href: "chapter.xhtml" });
  assert.equal(await refinement, false);
  assert.equal(p.navigations.length, 0);
  assert.equal(p.scheduledPushes, 0);
  assert.equal(p.run("audioSTTAnchor"), null);
});

test("a live STT result survives overlap with an ordinary mapped navigation", async () => {
  const p = player();
  p.run("audioInfo.stt_enabled = true;");
  p.audio.paused = false;
  const response = deferred();
  p.context.api = () => response.promise;
  const refinement = p.run("refineAudioSTT(200, { allowPlaying: true })");
  const navigation = deferred();
  p.context.view.resolveNavigation = () => navigation.promise;
  p.audio.currentTime = 201;
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(p.run("audioNavInProgress"), 1);
  response.resolve(sttResponse(0.25));
  assert.equal(await refinement, true);
  navigation.resolve({ href: "chapter.xhtml", locations: { progression: 0.201 } });
  await flush();
  p.context.view.resolveNavigation = async target => target;
  p.audio.currentTime = 202;
  await p.audio.emit("timeupdate");
  await flush();
  assert.ok(Math.abs(p.navigations.at(-1).locations.progression - 0.252) < 1e-9);
});

test("following audio turns pages on fine chapter progression deltas even in multi-hour books", async () => {
  const p = player();
  // Simulate a 20-chapter book where this chapter spans only 5% of total book progression,
  // and the audio segment is 1000s long (so 12s of audio is 0.0006 of the chapter, < 0.003 of the book).
  p.run(`audioInfo.mapping = {
    segments: [{ a0: 0, a1: 1000, b0: 0.10, b1: 0.15, href: "chapter.xhtml" }],
    resources: [{ href: "chapter.xhtml", start: 0.10, end: 0.15 }],
    full_narrative: true
  };`);
  p.audio.paused = false;
  p.audio.currentTime = 100;
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(p.navigations.length, 1);

  // Advance by 12s: totalProgression delta is only 0.0006 (< 0.003), but chapterProgress delta is 0.012 (>= 0.0005).
  p.audio.currentTime = 112;
  await p.audio.emit("timeupdate");
  await flush();
  assert.equal(p.navigations.length, 2);
});
