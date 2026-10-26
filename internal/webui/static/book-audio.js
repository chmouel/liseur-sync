// The audiobook on a book's details page (ADR-0057, ADR-0058).
//
// The player is the reader's (audioplayer.templ), rendered inline and
// without the controls that need the text on screen. The <audio> element
// streams through the cookie route; what this adds is where the reader
// left off, and filing where they got to as an ordinary reading position
// and a listening session, the same records the reader writes. Nothing
// is fetched until the player is used, so looking at a book does not
// mint a credential.

import { readerAuth } from "./reader-auth.js";
import { openSession, resolveListeningCheckpointFraction, resolveSessionEndFraction } from "./reader-session.js";
import { uploadSessions } from "./reader-session-upload.js";
import { readingSync } from "./offline-sync.js";
import {
  accountVersion, claimOfflineReader, clearOfflineSessionCheckpoint, finishOfflineSession,
  getOfflineSessionCheckpoint, removeOfflineOutbox, saveOfflineSessionCheckpoint,
  setActiveAccount, storagePartition,
} from "./offline-storage.js";

const SYNC_EVERY_MS = 30000;
const RATE_KEY = "liseur.listen.rate";

// chapterAt is the index of the chapter playing at time t, or -1 before
// the first one.
export function chapterAt(chapters, t) {
  let at = -1;
  for (let i = 0; i < chapters.length; i++) {
    if (chapters[i] <= t) at = i;
    else break;
  }
  return at;
}

// bookAt is the book's totalProgression for audio time t, as the
// server's audiomap.Mapping.ToBook computes it, or NaN outside every
// segment. It gives a sitting its starting place before any locate.
export function bookAt(mapping, t) {
  const segs = mapping?.segments || [];
  for (let i = 0; i < segs.length; i++) {
    const s = segs[i];
    const last = i === segs.length - 1;
    if (t >= s.a0 && (t < s.a1 || (last && t <= s.a1 + 1))) {
      const at = Math.min(t, s.a1);
      let p = s.a1 > s.a0 ? s.b0 + ((at - s.a0) / (s.a1 - s.a0)) * (s.b1 - s.b0) : s.b0;
      if (!mapping.full_narrative) p = Math.min(p, 0.998);
      return p;
    }
  }
  return NaN;
}

function uuid() {
  if (crypto.randomUUID) return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = [...b].map((x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

function clock(seconds) {
  const s = Math.max(0, Math.floor(seconds));
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
  const mm = String(m).padStart(h ? 2 : 1, "0"), ss = String(r).padStart(2, "0");
  return h ? `${h}:${mm}:${ss}` : `${mm}:${ss}`;
}

function player(section) {
  const audio = section.querySelector("#reader-audio-el");
  if (!audio) return;
  const bookID = section.dataset.bookAudio;
  const $ = (id) => section.querySelector("#" + id);
  const panel = $("reader-audio-bar");
  const details = $("reader-audio-details");
  const sizeButton = panel.querySelector("[data-audio-size]");
  const playButton = $("reader-audio-play");
  const chapterLabel = $("reader-audio-chapter");
  const currentLabel = $("reader-audio-current");
  const durationLabel = $("reader-audio-duration");
  const seek = $("reader-audio-seek");
  const rateSelect = $("reader-audio-rate");
  const chapterSelect = $("reader-audio-chapters");
  const status = $("reader-audio-stt-status");
  const chapters = chapterSelect
    ? [...chapterSelect.options].map((o) => ({ start: Number(o.value), title: o.textContent.replace(/^[\d:]+ · /, "") }))
    : [];
  const starts = chapters.map((c) => c.start);
  const knownDuration = Number(section.dataset.duration) || 0;
  const say = (text) => { if (status) status.textContent = text; };
  const auth = readerAuth({
    apiBase: section.dataset.apiBase,
    tokenURL: section.dataset.tokenUrl,
    csrf: section.dataset.csrf,
    onExhausted: () => say("Your session expired. Reload the page to keep saving your place."),
  });
  const api = (path, options) => auth.request(path, options);
  const path = (rest) => "v1/books/" + encodeURIComponent(bookID) + rest;
  let prepared = null;
  let workID = null;
  let positioned = false; // the listener chose a place before the resume point arrived
  let lastSynced = -1;
  let syncing = null;
  let timer = null;
  let session = null;
  let fraction = NaN;
  let mapping = null;
  let rolling = false; // sound is coming out: not paused, buffering or seeking
  let leaving = false;
  let context = null; // this browser's reading queue, when it has one
  let coordinator = null;
  let owner = false; // holds this book's checkpoint, as the reader would
  let leftover = null;
  let checkpointedAt = 0;
  let unsent = [];

  // The reader's queue (offline-sync.js): a finished sitting is written
  // to this browser's outbox before anything is sent, and checkpointed
  // while it runs, so a refused post or a phone that kills the tab
  // mid-chapter still leaves it to deliver later. Without IndexedDB the
  // sitting is posted straight and retried while the page lives.
  const durable = async () => {
    const identity = auth.identity();
    if (context || !identity?.account) return;
    try {
      const partition = storagePartition();
      const epoch = await setActiveAccount(partition, identity.account, await accountVersion(partition));
      context = { partition, account: identity.account, epoch, deviceID: identity.device };
    } catch {
      return;
    }
    coordinator = readingSync({
      context, request: api, bookID,
      onStatus: (message) => { if (message) say(message); },
    });
    // One page at a time keeps a book's checkpoint. A reader open on it
    // elsewhere keeps it, and sittings here are still queued at the end.
    const release = await claimOfflineReader(context, bookID).catch(() => null);
    if (!release) return;
    owner = true;
    leftover = await getOfflineSessionCheckpoint({ ...context, bookID }).catch(() => null);
  };

  const prepare = () => prepared ??= (async () => {
    const [infoResp, workResp] = await Promise.all([
      api(path("/audio/info")),
      api(path("/resolve"), {
        method: "POST", headers: { "Content-Type": "application/json" }, body: "{}",
      }),
    ]);
    if (!infoResp.ok) throw new Error("this audiobook cannot be played right now");
    const info = await infoResp.json();
    if (workResp.ok) workID = (await workResp.json()).work_id || null;
    if (Number.isFinite(info.progression)) fraction = info.progression;
    mapping = info.mapping || null;
    if (workID) await durable();
    return info;
  })();

  const resume = async () => {
    let info;
    try {
      info = await prepare();
    } catch (err) {
      prepared = null;
      say(err.message || "This audiobook could not be prepared.");
      return;
    }
    if (!positioned && Number.isFinite(info.audio_time) && info.audio_time > 1) {
      audio.currentTime = info.audio_time;
      say("Resumed where you left off, at " + clock(info.audio_time) + ".");
    }
    positioned = true;
  };

  // A position is the book's place for this second of audio, as the
  // server's alignment says, filed exactly as a page turn is.
  const sync = (keepalive = false) => {
    if (syncing || !workID) return syncing;
    const t = audio.currentTime;
    if (!Number.isFinite(t) || Math.abs(t - lastSynced) < 2) return null;
    syncing = (async () => {
      try {
        const resp = await api(path("/audio/locate"), {
          method: "POST", keepalive,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ audio_time: t }),
        });
        if (!resp.ok) return;
        const at = await resp.json();
        if (!Number.isFinite(at.progression) || !at.locator) return;
        const edition = section.dataset.edition;
        const op = {
          op_id: uuid(), work_id: workID, client_ts: new Date().toISOString(),
          progression: at.progression, locator: at.locator,
          ...(edition ? { edition_sha: edition } : {}),
        };
        const pushed = await api("v1/ops", {
          method: "POST", keepalive,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ ops: [op] }),
        });
        if (pushed.ok) lastSynced = t;
      } catch {
        // Offline or expired: the next tick tries again from wherever
        // playback has got to by then.
      } finally {
        syncing = null;
      }
    })();
    return syncing;
  };

  // Where the book is at this second of audio, worked out here from the
  // alignment rather than waiting on a locate, so a sitting starts and
  // ends at the playhead and not wherever the last push landed.
  const here = () => {
    const p = bookAt(mapping, audio.currentTime);
    if (Number.isFinite(p)) fraction = p;
    return fraction;
  };

  const checkpoint = () => {
    if (!session || !context || !owner) return;
    checkpointedAt = performance.now();
    const cp = session.checkpoint();
    if (cp) saveOfflineSessionCheckpoint({ ...context, bookID, checkpoint: cp }).catch(() => {});
  };

  // Straight to the server: on the way out, the queue may not get
  // another turn, and without a queue it is the only way.
  const post = async (batch) => {
    if (!batch.length) return;
    try {
      await uploadSessions(batch, {
        canSend: () => true,
        send: (body) => api("v1/sessions", {
          method: "POST", keepalive: true,
          headers: { "Content-Type": "application/json" }, body,
        }),
        responseCurrent: (resp) => auth.responseCurrent(resp),
        accepted: async (sent) => {
          unsent = unsent.filter((p) => !sent.includes(p));
          if (!context) return;
          for (const p of sent) {
            await removeOfflineOutbox({ ...context, kind: "session", id: p.session_id }).catch(() => {});
          }
        },
        refused: (item) => { unsent = unsent.filter((p) => p !== item); },
        deferred: () => {},
      });
    } catch {
      // Still queued, or still in unsent: the next tick tries again.
    }
  };

  const settle = async (payload, sessionID) => {
    if (context) {
      try {
        if (payload) await finishOfflineSession({ ...context, bookID, payload });
        else await clearOfflineSessionCheckpoint({ ...context, bookID, sessionID });
        if (payload && leaving) void post([payload]);
        else coordinator?.trigger();
        return;
      } catch {
        // Not on disk: keep it in memory instead.
      }
    }
    if (!payload) return;
    unsent.push(payload);
    void post(unsent.slice());
  };

  const startSession = () => {
    if (session || !workID) return;
    const cp = leftover;
    leftover = null;
    const continues = cp?.mode === "listening" && cp.workID === workID;
    if (cp && !continues) {
      // A sitting a page left open when it died ends where it was last
      // seen, so this listen does not inherit its reading.
      const stale = openSession({
        id: cp.id, workID: cp.workID || workID, startedAt: new Date(), now: performance.now(),
        supportsActiveMs: cp.supportsActiveMs === true, checkpoint: cp,
      });
      void settle(stale.close(performance.now(), new Date(), resolveListeningCheckpointFraction(cp)), cp.id);
    }
    session = openSession({
      id: uuid(), workID, startedAt: new Date(), now: performance.now(),
      fraction: here(), supportsActiveMs: !!auth.identity()?.supportsActiveMs, mode: "listening",
      checkpoint: continues ? cp : null,
    });
    checkpoint();
  };

  const endSession = async () => {
    if (!session) return;
    const id = session.id;
    const end = resolveSessionEndFraction(session, bookAt(mapping, audio.currentTime), fraction);
    const payload = session.close(performance.now(), new Date(), end);
    session = null;
    await settle(payload, id);
  };

  const tick = () => {
    sync();
    if (unsent.length) void post(unsent.slice());
  };

  // The first play waits for the resume point with the sound off, so
  // the gesture that started playback is still the one that started it
  // (browsers refuse play() once a fetch has come between) and nobody
  // hears the opening seconds of chapter one first. A listener who
  // picked a place first skips the resume, not the preparation: the
  // sitting still needs its work.
  audio.addEventListener("play", async () => {
    if (!positioned) {
      audio.muted = true;
      await resume();
      audio.muted = false;
    } else {
      await prepare().catch((err) => {
        prepared = null;
        say(err.message || "This audiobook could not be prepared.");
      });
    }
    if (!workID || audio.paused) return;
    startSession();
    if (rolling) session?.playing(performance.now(), here());
    clearInterval(timer);
    timer = setInterval(tick, SYNC_EVERY_MS);
  });

  // Listening time is what was heard: it runs from "playing" and stops
  // on anything that silences the sound, buffering and seeking included.
  audio.addEventListener("playing", () => {
    rolling = !audio.paused && !audio.seeking && !audio.ended;
    if (!rolling || !session) return;
    session.playing(performance.now(), here());
    checkpoint();
  });
  const halt = () => {
    rolling = false;
    if (!session) return;
    session.paused(performance.now(), here());
    checkpoint();
  };
  for (const ev of ["waiting", "seeking", "error"]) audio.addEventListener(ev, halt);
  audio.addEventListener("timeupdate", () => {
    if (!rolling || !session || audio.paused || audio.seeking) return;
    const now = performance.now();
    session.playing(now, here());
    if (now - checkpointedAt >= 5000) checkpoint();
  });
  const stopped = () => {
    halt();
    clearInterval(timer);
    timer = null;
    sync();
  };
  audio.addEventListener("pause", () => {
    stopped();
    if (document.hidden) void endSession();
  });
  audio.addEventListener("ended", () => { stopped(); void endSession(); });
  audio.addEventListener("seeking", () => { if (audio.paused) positioned = true; });

  // Background listening keeps the sitting open; a page put away while
  // nothing plays ends it, because a phone may never say pagehide.
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState !== "hidden") return;
    sync(true);
    if (audio.paused) {
      void endSession();
      return;
    }
    if (rolling) session?.playing(performance.now(), here());
    checkpoint();
  });
  window.addEventListener("pagehide", () => {
    leaving = true;
    coordinator?.leave();
    sync(true);
    void endSession();
  });
  window.addEventListener("pageshow", (e) => {
    if (!e.persisted) return;
    leaving = false;
    coordinator?.resume();
  });

  // Warm the credential and the resume point as soon as the listener
  // reaches for the player, so the first play has less to wait for.
  section.addEventListener("pointerenter", () => { prepare().catch(() => { prepared = null; }); }, { once: true });
  section.addEventListener("focusin", () => { prepare().catch(() => { prepared = null; }); }, { once: true });

  const skipBy = (by) => {
    positioned = true;
    const end = Number.isFinite(audio.duration) ? audio.duration : Infinity;
    audio.currentTime = Math.max(0, Math.min(end, audio.currentTime + by));
  };
  $("reader-audio-back")?.addEventListener("click", () => skipBy(-15));
  $("reader-audio-fwd")?.addEventListener("click", () => skipBy(30));
  playButton?.addEventListener("click", () => {
    if (audio.paused) audio.play().catch(() => {});
    else audio.pause();
  });
  const showPlaying = (playing) => {
    if (!playButton) return;
    playButton.dataset.playing = String(playing);
    playButton.setAttribute("aria-label", playing ? "Pause audiobook" : "Play audiobook");
  };
  audio.addEventListener("play", () => showPlaying(true));
  audio.addEventListener("pause", () => showPlaying(false));

  // The same size toggle as the reader's panel, without its placement.
  if (sizeButton && details) {
    const renderSize = (size) => {
      panel.dataset.audioSize = size;
      details.hidden = size === "mini";
      sizeButton.setAttribute("aria-expanded", String(!details.hidden));
      sizeButton.setAttribute("aria-label", details.hidden ? "Expand audiobook player" : "Minimize audiobook player");
      sizeButton.title = details.hidden ? "Expand player" : "Minimize player";
    };
    sizeButton.addEventListener("click", () => {
      const size = panel.dataset.audioSize === "mini" ? "expanded" : "mini";
      if (size === "mini" && details.contains(document.activeElement)) sizeButton.focus();
      renderSize(size);
    });
    renderSize(panel.dataset.audioSize || "mini");
  }

  let scrubbing = false;
  const timeline = () => {
    const cur = Number.isFinite(audio.currentTime) ? audio.currentTime : 0;
    const dur = Number.isFinite(audio.duration) && audio.duration > 0 ? audio.duration : knownDuration;
    if (currentLabel && !scrubbing) currentLabel.textContent = clock(cur);
    if (durationLabel && dur > 0) durationLabel.textContent = clock(dur);
    if (seek && !scrubbing && dur > 0) {
      seek.max = String(Math.ceil(dur));
      seek.value = String(Math.min(dur, cur));
    }
    const i = chapterAt(starts, cur);
    if (chapterLabel) chapterLabel.textContent = chapters[Math.max(0, i)]?.title || section.dataset.title || "Audiobook";
    if (chapterSelect && i >= 0 && chapterSelect.selectedIndex !== i) chapterSelect.selectedIndex = i;
  };
  for (const ev of ["timeupdate", "durationchange", "loadedmetadata", "seeked"]) audio.addEventListener(ev, timeline);
  timeline();

  seek?.addEventListener("input", () => {
    scrubbing = true;
    const target = Number(seek.value);
    if (currentLabel && Number.isFinite(target)) currentLabel.textContent = clock(target);
  });
  seek?.addEventListener("change", () => {
    scrubbing = false;
    const target = Number(seek.value);
    if (!Number.isFinite(target)) return;
    positioned = true;
    audio.currentTime = target;
    timeline();
  });

  chapterSelect?.addEventListener("change", () => {
    positioned = true;
    audio.currentTime = Number(chapterSelect.value);
    timeline();
    if (audio.paused) audio.play().catch(() => {});
  });

  if (rateSelect) {
    const saved = Number(localStorage.getItem(RATE_KEY));
    if (saved > 0 && [...rateSelect.options].some((o) => Number(o.value) === saved)) {
      rateSelect.value = [...rateSelect.options].find((o) => Number(o.value) === saved).value;
    }
    const apply = () => { audio.playbackRate = Number(rateSelect.value) || 1; };
    apply();
    rateSelect.addEventListener("change", () => {
      apply();
      localStorage.setItem(RATE_KEY, rateSelect.value);
    });
    // Changing src resets the rate in some engines.
    audio.addEventListener("loadedmetadata", apply);
  }

  if ("mediaSession" in navigator) {
    navigator.mediaSession.metadata = new MediaMetadata({
      title: section.dataset.title || document.title,
      artist: section.dataset.artist || "",
      artwork: section.dataset.cover ? [{ src: section.dataset.cover }] : [],
    });
    navigator.mediaSession.setActionHandler("seekbackward", () => skipBy(-15));
    navigator.mediaSession.setActionHandler("seekforward", () => skipBy(30));
  }

  if (location.hash === "#listen") playButton?.focus();
}

// The audiobook upload: one large file, so it goes through XHR for a
// progress bar, and answers in JSON (uploadDone) rather than a redirect.
function uploadForm(form) {
  const bar = form.querySelector("[data-upload-progress]");
  const status = form.querySelector(".uploadstatus");
  const button = form.querySelector("button[type=submit]");
  form.addEventListener("submit", (e) => {
    const input = form.querySelector("input[type=file]");
    if (!input?.files?.length) return;
    e.preventDefault();
    const xhr = new XMLHttpRequest();
    xhr.open("POST", form.action);
    xhr.setRequestHeader("Accept", "application/json");
    xhr.responseType = "json";
    if (button) button.disabled = true;
    if (bar) { bar.hidden = false; bar.value = 0; }
    xhr.upload.addEventListener("progress", (ev) => {
      if (!ev.lengthComputable) return;
      const pct = Math.round((ev.loaded / ev.total) * 100);
      if (bar) bar.value = pct;
      if (status) {
        status.textContent = pct < 100
          ? "Uploading… " + pct + "%"
          : "Checking the file and pairing it with this book…";
      }
    });
    const fail = (text) => {
      if (button) button.disabled = false;
      if (bar) bar.hidden = true;
      if (status) status.textContent = text;
    };
    xhr.addEventListener("load", () => {
      const body = xhr.response || {};
      if (xhr.status >= 200 && xhr.status < 300 && body.ok) {
        const url = new URL(location.href);
        url.search = "?notice=" + encodeURIComponent(body.notice || "audiobook added");
        url.hash = "listen";
        location.replace(url.href);
        return;
      }
      fail(body.error || "The upload failed (the server answered " + xhr.status + ").");
    });
    xhr.addEventListener("error", () => fail("The upload was interrupted. Check your connection and try again."));
    xhr.send(new FormData(form));
  });
}

if (typeof document !== "undefined") {
  document.querySelectorAll("[data-book-audio]").forEach(player);
  document.querySelectorAll("[data-audiobook-upload]").forEach(uploadForm);
}
