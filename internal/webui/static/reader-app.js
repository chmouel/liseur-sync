// The reader page's controller: open the book with foliate-js, put it
// on screen, and keep the reading position in step with every other
// Liseur client.
//
// The rendering engine is vendored (ADR-0007, renderer revised by
// ADR-0012). foliate-js paginates with CSS multi-column inside the
// frame's own viewport, so there is nothing for this file to measure
// and no sizing hook to get wrong — the failure mode that ended both
// the hand-written renderer and epub.js.
//
// Sync is unchanged and deliberately so: this file talks to the same
// /v1 routes Android and desktop use, with the short-lived token from
// POST /ui/reader/token. It gets no special treatment from the server.

import "./vendor/foliate/view.js";
import { Overlayer } from "./vendor/foliate/overlayer.js";
import { openSession } from "./reader-session.js";
import { positionTable, pageAt, pageLocation } from "./reader-positions.js";
import { readerAuth } from "./reader-auth.js";
import { liveStream } from "./reader-live.js";
import { catchupState, topicRefresh } from "./reader-sync.js";
import { annotationCFI, annotationRenderer } from "./reader-annotations.js";

const el = document.getElementById("reader-config");
// Every URL is relative, computed server-side, so the reader keeps
// working when the UI is served under a stripped proxy subpath.
const cfg = {
  bookID: el.dataset.book,
  csrf: el.dataset.csrf,
  tokenURL: el.dataset.tokenUrl,
  downloadURL: el.dataset.downloadUrl,
  apiBase: el.dataset.apiBase,
  detached: el.dataset.detached === "1",
  handed: null,
};

// On the separate reader origin (ADR-0007 phase 3) there is no session
// and no CSRF token, because there is no cookie on this hostname at
// all. The credential was handed over in the URL fragment, which the
// browser sent to nobody; the addresses it works against were in the
// query, checked by the server, and are already in the page.
if (cfg.detached) {
  cfg.handed = new URLSearchParams(location.hash.slice(1)).get("t");
  // Out of the address bar, out of the history entry, out of anything
  // the user might paste to somebody. It stays in this module, which
  // is where a credential belongs.
  history.replaceState(null, "", location.pathname + location.search);
}
const stage = document.getElementById("reader-view");
const stageArea = stage.closest(".reader-stage") || stage;
const status = document.getElementById("reader-status");
const progressBar = document.getElementById("reader-progress-bar");
const progressText = document.getElementById("reader-progress-text");
const chapterText = document.getElementById("reader-chapter");
const pageText = document.getElementById("reader-page");
const footer = document.getElementById("reader-footer");
const gotoDialog = document.getElementById("reader-goto");
const gotoForm = document.getElementById("reader-goto-form");
const gotoTitle = document.getElementById("reader-goto-title");
const gotoLabel = document.getElementById("reader-goto-label");
const gotoInput = document.getElementById("reader-goto-input");
const gotoUnit = document.getElementById("reader-goto-unit");
const gotoCancel = document.getElementById("reader-goto-cancel");
let gotoKind = "percent";
const titleText = document.getElementById("reader-title-text");
const tocPanel = document.getElementById("reader-toc");
const tocList = document.getElementById("reader-toc-list");
const tocButton = document.getElementById("reader-toc-button");
const fullscreenBtn = document.getElementById("reader-fullscreen");

let view = null;
let workID = null;
let pending = null;
let here = null;
let faviconObjectURL = null;
let ready = false;
let readingDirty = false;
let interactionPending = false;
let restoring = true;
let lifecycle = 0;
let activityGeneration = 0;
let syncExpired = false;
const catchup = catchupState();
const catchupPanel = document.getElementById("reader-catchup");
const catchupText = document.getElementById("reader-catchup-text");
const catchupAccept = document.getElementById("reader-catchup-accept");
const catchupDismiss = document.getElementById("reader-catchup-dismiss");
// The book's positions, counted once when it opens. Null for a book
// this recipe cannot measure, which leaves the engine's own locations
// to say what page it is.
let positions = null;

function say(message, isError) {
  status.textContent = message;
  status.classList.toggle("problem", !!isError);
  status.hidden = !message;
}

// ------------------------------------------------------------ auth

const auth = readerAuth({
  ...cfg,
  handed: cfg.handed,
  onChange(identity, previous) {
    catchup.bind(identity.account, workID, identity.device);
    hideCatchup();
    if (!previous) return;
    refreshes.reset();
    live.stop();
    annotationDrawing.clear().catch(() => {});
    if (previous.account !== identity.account) {
      // A same-account renewal already invalidates old-credential
      // responses through auth.current(); only an actual account
      // switch needs the page lifecycle to advance, or a write settled
      // under the fresh token would be requeued as if it never landed.
      lifecycle++;
      auth.stop(); // A page opened for one account never writes under another.
      return;
    }
    if (ready && !document.hidden) startLive();
  },
  onExhausted(err) {
    syncExpired = true;
    ready = false;
    lifecycle++;
    workID = null;
    session = null;
    unsent = [];
    retryOp = null;
    readingDirty = false;
    cancelScheduledPush();
    refreshes.stop();
    live.stop();
    catchup.bind(null, null, null);
    hideCatchup();
    annotationDrawing.clear().catch(() => {});
    say(err.message, true);
  },
});
cfg.handed = null;
const api = (path, options) => auth.request(path, options);

function snapshot() {
  return { identity: auth.identity(), work: workID, view, lifecycle };
}
function current(stamp) {
  return stamp && auth.current(stamp.identity) && stamp.work === workID &&
    stamp.view === view && stamp.lifecycle === lifecycle;
}

// ------------------------------------------------------------ sync

async function resolveWork() {
  const resp = await api(
    "v1/books/" + encodeURIComponent(cfg.bookID) + "/resolve",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    },
  );
  if (!resp.ok) return null;
  const data = await resp.json();
  return auth.responseCurrent(resp) ? data.work_id || null : null;
}

async function lastPosition() {
  if (!workID) return { ok: false };
  const work = workID;
  const stamp = snapshot();
  const resp = await api(
    "v1/works/" + encodeURIComponent(work) + "/positions?limit=1",
  );
  if (!resp.ok) return { ok: false };
  stamp.identity = auth.responseIdentity(resp);
  const ops = (await resp.json()).ops || [];
  if (work !== workID || !current(stamp)) return { ok: false };
  return { ok: true, op: ops.length ? ops[0] : null };
}

// ------------------------------------------------------------- live

function hideCatchup() {
  if (!catchupPanel) return;
  const focused = catchupPanel.contains(document.activeElement);
  catchupPanel.hidden = true;
  if (focused) {
    stage.tabIndex = -1;
    stage.focus({ preventScroll: true });
  }
}

function showCatchup() {
  const offer = catchup.offer();
  if (!offer || !catchupPanel) return;
  const fraction = offer.op.progression;
  catchupText.textContent = finite(fraction)
    ? `Continue from ${Math.round(fraction * 100)}% read on another device?`
    : "Continue from the position read on another device?";
  catchupPanel.hidden = false;
}

const refreshes = topicRefresh({
  async refresh(topic) {
    if (!ready || document.hidden || !workID) return false;
    const run = lifecycle;
    const activity = activityGeneration;
    if (topic === "annotations") return loadAnnotations();
    const result = await lastPosition();
    if (!result.ok || document.hidden || run !== lifecycle ||
        activity !== activityGeneration) return false;
    catchup.observe(result.op);
    showCatchup(); // The state allows this only after hidden -> visible.
    return true;
  },
});
const live = liveStream({
  request: api,
  current: (resp) => ready && !document.hidden && auth.responseCurrent(resp),
  onTopics: (topics) => refreshes.owe(topics),
});

function startLive() {
  if (!workID) return;
  refreshes.start();
  // This also refreshes on resume against a server without /v1/events.
  refreshes.owe(["positions", "annotations"]);
  live.start();
}

catchupDismiss?.addEventListener("click", () => {
  catchup.dismiss();
  hideCatchup();
});
catchupPanel?.addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    event.preventDefault();
    catchup.dismiss();
    hideCatchup();
  }
});
catchupAccept?.addEventListener("click", async () => {
  const shown = catchup.shown();
  const activity = activityGeneration;
  hideCatchup();
  if (!shown || !view) return;
  cancelScheduledPush();
  await settlePosition();
  // A failed flush leaves the local page dirty on purpose, so adopting
  // the remote offer now would lose it for good the moment retryOp and
  // readingDirty are cleared below.
  if (readingDirty) return;
  const stamp = snapshot();
  const op = current(stamp) ? catchup.accept(shown) : null;
  if (!op || !view) return;
  retryOp = null;
  readingDirty = false;
  interactionPending = false;
  // Close the old sitting at its actual page, not at the remote destination.
  endSession();
  restoring = true;
  try {
    for (const target of startCandidates(op)) {
      if (!current(stamp) || document.hidden || activity !== activityGeneration) break;
      try {
        const resolved = await view.resolveNavigation(target);
        if (!current(stamp) || document.hidden || activity !== activityGeneration) break;
        // goTo catches anchor failures internally; use the renderer so a stale
        // CFI actually descends to the existing fraction/href fallback.
        await view.renderer.goTo(resolved);
        break;
      } catch { /* try the coarser locator */ }
    }
  } finally {
    restoring = false;
    // A restored page starts accounting only when the reader next interacts.
  }
});

// ------------------------------------------------------ annotations

// View-only annotation rendering (ADR-0028). The live set comes from
// the same sync API every client uses; highlights whose CFI the engine
// can anchor draw through the vendored overlayer, and the rest — every
// bookmark, every note, and any highlight whose locator no longer
// resolves against this copy — degrade to a sidebar entry at their
// progression. Best-effort display, never an error.
const annPanel = document.getElementById("reader-annotations");
const annList = document.getElementById("reader-annotations-list");

// The palette is tokens on the wire, always; this table is the only
// place a token becomes CSS, so nothing a client pushed reaches the
// overlayer as a style.
const ANNOTATION_COLORS = {
  yellow: "#ffd54f",
  green: "#81c784",
  blue: "#64b5f6",
  pink: "#f06292",
  purple: "#ba68c8",
  orange: "#ffb74d",
};

const annotationDrawing = annotationRenderer({
  getView: () => view,
  current,
  changed: buildAnnotationList,
});

// buildAnnotationList fills the sidebar with the entries that do not
// draw over the text. Excerpts and bodies are set as text nodes only.
function buildAnnotationList() {
  if (!annPanel || !annList) return;
  annList.textContent = "";
  const listed = annotationDrawing.annotations().filter(
    (a) =>
      a.kind !== "highlight" || !annotationCFI(a) || annotationDrawing.failed(a.id),
  );
  annPanel.hidden = !listed.length;
  if (!listed.length) return;
  const ul = document.createElement("ul");
  for (const a of listed) {
    const li = document.createElement("li");
    const cfi = a.kind === "highlight" ? null : annotationCFI(a);
    const canNavigate =
      !!cfi || (typeof a.progression === "number" && a.progression >= 0);
    const entry = document.createElement(canNavigate ? "a" : "span");
    if (canNavigate) {
      entry.href = "#";
      entry.addEventListener("click", (e) => {
        e.preventDefault();
        if (!noteNavigation()) return;
        toggleTOC(false);
        if (!view) return;
        const fall = () => {
          if (typeof a.progression === "number") {
            view.goToFraction(Math.min(0.999, a.progression)).catch(() => {});
          }
        };
        if (cfi) view.goTo(cfi).catch(fall);
        else fall();
      });
    }
    const kind = document.createElement("span");
    kind.className = "reader-ann-kind";
    kind.textContent = a.kind;
    entry.append(kind);
    const text = document.createElement("span");
    text.className = "reader-ann-text";
    text.textContent =
      a.excerpt ||
      a.body ||
      (typeof a.progression === "number"
        ? Math.round(a.progression * 100) + "%"
        : "");
    entry.append(text);
    li.append(entry);
    ul.append(li);
  }
  annList.append(ul);
}

// loadAnnotations is best-effort like every other sync call: a reader
// on a server without the routes, or offline, still reads the book.
async function loadAnnotations() {
  if (!workID || !view) return false;
  const work = workID;
  const stamp = snapshot();
  const resp = await api("v1/works/" + encodeURIComponent(work) + "/annotations");
  // An old server without annotation support is not a failing refresh.
  if ([404, 501].includes(resp.status)) return true;
  if (!resp.ok) return false;
  stamp.identity = auth.responseIdentity(resp);
  const data = await resp.json();
  if (!current(stamp) || work !== workID || document.hidden) return false;
  if (!Array.isArray(data.annotations)) return false;
  await annotationDrawing.replace(data.annotations, stamp);
  return current(stamp);
}

// bookTitle is read from the package document rather than from the
// catalog, so the page says what the file says even when the two have
// drifted. foliate-js keeps a title either as a string or as a
// language map, and either way one string comes out.
function bookTitle() {
  const raw =
    view && view.book && view.book.metadata && view.book.metadata.title;
  if (!raw) return "";
  if (typeof raw === "string") return raw;
  const values = Object.values(raw);
  return values.length ? String(values[0]) : "";
}

// finite guards a value that must be a real number the reader can act
// on. `typeof NaN === "number"` is true and `JSON.stringify(NaN)` emits
// null, so a bare typeof check would let a NaN fraction reach the wire
// as a "position unknown" the server records as the start of the book.
function finite(v) {
  return typeof v === "number" && Number.isFinite(v);
}

// locatorFor builds the Readium Locator the sync protocol carries. The
// server stores it verbatim and never reads it, so the shape is a
// promise to the other clients rather than to the server.
//
// The CFI goes in `fragments`, which is where Readium puts a format's
// own pointer, and `totalProgression` is beside it because that is the
// one field every client can act on: a phone that has never heard of a
// CFI still opens in the right place.
//
// `location` here is what foliate-js hands the relocate event: a total
// fraction, the section index, and a CFI. The engine estimates the
// fraction from section sizes it already knows, so there is no separate
// "generate locations" pass and no moment when progress is unknown.
//
// A non-finite fraction yields null: there is no position to push, and
// the caller asks the engine to remeasure rather than record a wrong one.
function locatorFor(location) {
  if (!finite(location.fraction)) return null;
  const section = location.section || {};
  const index = typeof section.current === "number" ? section.current : 0;
  const sections = (view.book && view.book.sections) || [];
  return {
    href: (sections[index] && sections[index].id) || "",
    type: "application/xhtml+xml",
    title: bookTitle(),
    locations: {
      fragments: location.cfi ? [location.cfi] : [],
      progression: sectionProgression(location),
      totalProgression: location.fraction,
      position: index + 1,
    },
  };
}

// sectionProgression recovers the fraction within the current section
// from the total fraction and the section boundaries, because Readium's
// `progression` is within-resource and foliate-js reports the total.
function sectionProgression(location) {
  const fractions = view.getSectionFractions ? view.getSectionFractions() : [];
  const section = location.section || {};
  const index = typeof section.current === "number" ? section.current : 0;
  const lo = fractions[index];
  const hi = fractions[index + 1];
  if (!finite(location.fraction)) return 0;
  const total = location.fraction;
  if (!finite(lo) || !finite(hi) || hi <= lo) return 0;
  return Math.max(0, Math.min(1, (total - lo) / (hi - lo)));
}

// push records where we are. Failure is deliberately quiet: losing a
// position update is a smaller harm than an error banner over the page
// every time a laptop lid closes, and the next page turn retries.
//
// The op log is append-only and idempotent by op id, so a retry of the
// same position must replay the same op — the whole op, byte for byte,
// because a fresh client_ts under an old id is a different payload and
// the server rightly calls that a conflict. The op is therefore built
// once and kept until the server confirms it holds it; a different
// position is a different op and gets a new id. The server answers 200
// with a status per op: "applied" and "duplicate" both mean the log
// has it, and "conflict" means this id already belongs to another
// payload, where replaying is the one thing that cannot help.
let retryOp = null;
let positionInFlight = null;
let fractionRetryTimer = null;
let fractionRetryAttempt = 0;
const FRACTION_RETRY_DELAYS = [250, 750, 1500, 3000, 6000];
const FRACTION_WAIT_MESSAGE =
  "Position sync is paused until the reader can measure this page.";
let fractionWaitShown = false;

function cancelScheduledPush() {
  clearTimeout(pending);
  pending = null;
}

function clearFractionRetry() {
  clearTimeout(fractionRetryTimer);
  fractionRetryTimer = null;
  fractionRetryAttempt = 0;
  if (fractionWaitShown && status.textContent === FRACTION_WAIT_MESSAGE) {
    say("");
  }
  fractionWaitShown = false;
}

function scheduleFractionRetry() {
  if (!view || !here || finite(here.fraction) || fractionRetryTimer) return;
  if (document.hidden) return;
  if (fractionRetryAttempt >= FRACTION_RETRY_DELAYS.length) {
    fractionWaitShown = true;
    say(FRACTION_WAIT_MESSAGE, true);
    return;
  }
  const delay = FRACTION_RETRY_DELAYS[fractionRetryAttempt++];
  fractionRetryTimer = setTimeout(() => {
    fractionRetryTimer = null;
    if (!view || !here || finite(here.fraction)) return;
    if (document.hidden) return;
    const retry = () => {
      const renderer = view && view.renderer;
      if (renderer && typeof renderer.render === "function") renderer.render();
      if (!fractionRetryTimer && !finite(here && here.fraction)) {
        scheduleFractionRetry();
      }
    };
    requestAnimationFrame(retry);
  }, delay);
}

async function push() {
  if (!workID || !here || !readingDirty || restoring) return;
  const stamp = snapshot();
  const locator = locatorFor(here);
  // A non-finite fraction has no position to record. Return without
  // touching retryOp; the layout retry below will prod the engine for
  // a new relocate, and the next finite one goes through the normal
  // debounce.
  if (!locator) {
    scheduleFractionRetry();
    return;
  }
  const key =
    (locator.locations.fragments[0] || "") +
    "@" +
    locator.locations.totalProgression;
  const op =
    retryOp && retryOp.key === key
      ? retryOp.op
      : {
          op_id: opID(),
          work_id: workID,
          client_ts: new Date().toISOString(),
          progression: locator.locations.totalProgression,
          locator: locator,
        };
  retryOp = { key, op };
  catchup.wrote(op);
  const request = api("v1/ops", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      // keepalive lets the final flush outlive the page: without it a
      // position pushed from beforeunload is cancelled mid-flight.
      keepalive: true,
      body: JSON.stringify({ ops: [op] }),
    });
  positionInFlight = request;
  try {
    const resp = await request;
    stamp.identity = auth.responseIdentity(resp) || stamp.identity;
    if (!resp.ok || !current(stamp) || !auth.responseCurrent(resp)) return;
    const out = await resp.json().catch(() => null);
    if (!current(stamp) || !auth.responseCurrent(resp)) return;
    const status =
      out && out.results && out.results[0] && out.results[0].status;
    if (
      status !== "applied" &&
      status !== "duplicate" &&
      status !== "conflict"
    ) {
      return;
    }
    // Only this op's own outcome may clear it: a slower response
    // arriving after the reader has moved on must not discard the op
    // a newer push is still responsible for.
    if (retryOp && retryOp.op === op) {
      retryOp = null;
      // A newer local page still needs its own push.
      if (locatorFor(here)?.locations.fragments[0] === locator.locations.fragments[0] &&
          here.fraction === locator.locations.totalProgression) readingDirty = false;
    }
  } catch (err) {
    /* offline: the next page turn replays this exact op */
  } finally {
    if (positionInFlight === request) positionInFlight = null;
  }
}

async function settlePosition() {
  if (readingDirty && !restoring && !positionInFlight) await push();
  if (positionInFlight) await positionInFlight;
}

// opID is a v4 UUID. crypto.randomUUID exists only in a secure
// context, and the reader origin may be plain HTTP on a LAN, so the
// bytes are drawn directly — getRandomValues has no such restriction
// — rather than letting sync fail quietly where TLS is absent.
function opID() {
  if (crypto.randomUUID) return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const hex = [...b].map((n) => n.toString(16).padStart(2, "0")).join("");
  return [
    hex.slice(0, 8),
    hex.slice(8, 12),
    hex.slice(12, 16),
    hex.slice(16, 20),
    hex.slice(20),
  ].join("-");
}

function schedulePush() {
  if (!readingDirty || restoring) return;
  cancelScheduledPush();
  pending = setTimeout(push, 1500);
}

// ------------------------------------------------------- sessions

// A sitting (ADR-0030) is bounded the way Android bounds one: it opens
// when the book is on screen with a position measured, and closes when
// the tab is hidden or unloaded, which is the last moment a browser
// reliably lets a page speak. Coming back opens a new one. The
// arithmetic — idle cap, minimum, clamps — lives in reader-session.js.
let session = null;
// Closed sittings the server has not yet confirmed. Each is kept whole
// and replayed byte for byte, like retryOp: the server compares
// payloads under the session id, and a figure that moved between
// attempts would be refused as a different session wearing the same
// name.
let unsent = [];

function beginSession() {
  if (session || !workID || document.hidden) return;
  if (!here || !finite(here.fraction)) return;
  session = openSession({
    id: opID(),
    workID: workID,
    startedAt: new Date(),
    now: performance.now(),
    fraction: here.fraction,
  });
}

function noteActivity() {
  activityGeneration++;
  if (document.hidden || restoring) return;
  interactionPending = true;
  if (session) {
    session.activity(performance.now(), here && here.fraction);
  } else {
    beginSession();
  }
  if (unsent.length) pushSession();
}

function noteNavigation() {
  if (!view || restoring || document.hidden) return false;
  noteActivity();
  catchup.moved();
  hideCatchup();
  return true;
}

// noteProgress follows a relocate. It is not activity: the engine
// relocates on a resize or a font change too, and only a key, a tap or
// a scroll says somebody was there.
function noteProgress() {
  if (session) session.progress(here && here.fraction);
}

function endSession() {
  if (!session) return;
  const payload = session.close(
    performance.now(),
    new Date(),
    here && here.fraction,
  );
  session = null;
  if (payload) unsent.push(payload);
  pushSession(true);
}

let sessionInFlight = false;

// pushSession sends every unconfirmed sitting in one batch. A retry
// prodded by activity waits for the request already out; a close does
// not, because the page may not be here when that one comes back, and
// the server holds the same payload twice as once.
async function pushSession(closing) {
  if (!unsent.length || (sessionInFlight && !closing)) return;
  const batch = unsent.slice();
  const stamp = snapshot();
  sessionInFlight = true;
  try {
    const resp = await api("v1/sessions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      keepalive: true,
      body: JSON.stringify({ sessions: batch }),
    });
    stamp.identity = auth.responseIdentity(resp) || stamp.identity;
    // 2xx: filed (re-posting the same payload is idempotently a 2xx too).
    // 409: this session_id was already used with a *different* payload —
    // a collision, not a repeat of this sitting — and replaying the same
    // batch cannot fix that. Any other 4xx is a payload the server will
    // refuse however often it is sent — an unknown work, say. Only a
    // server error or no answer at all leaves the batch to try again.
    if (current(stamp) && auth.responseCurrent(resp) &&
        (resp.ok || (resp.status >= 400 && resp.status < 500))) {
      unsent = unsent.filter((p) => !batch.includes(p));
    }
  } catch (err) {
    /* offline: the next activity or close replays these exact sittings */
  } finally {
    sessionInFlight = false;
  }
}

document.addEventListener("visibilitychange", () => {
  lifecycle++;
  if (document.hidden) {
    live.stop();
    refreshes.stop();
    catchup.hide();
    hideCatchup();
    cancelScheduledPush();
    push();
    endSession();
    return;
  }
  catchup.resume();
  if (ready) startLive();
  beginSession();
  if (here && !finite(here.fraction)) {
    fractionRetryAttempt = 0;
    scheduleFractionRetry();
  }
});
window.addEventListener("pagehide", () => {
  lifecycle++;
  live.stop();
  refreshes.stop();
  catchup.hide();
  hideCatchup();
  endSession();
});

function cfiOf(op) {
  const fragments =
    (op &&
      op.locator &&
      op.locator.locations &&
      op.locator.locations.fragments) ||
    [];
  for (const fragment of fragments) {
    if (typeof fragment === "string" && fragment.indexOf("epubcfi(") === 0) {
      return fragment;
    }
  }
  return null;
}

// startCandidates lists where to try opening, best pointer first. It
// prefers what the writing client actually said — a CFI from this
// reader, or the resource another one named — and keeps the fraction
// every client agrees on as the fallback, which is why a book started
// on a phone opens in roughly the right place here. A stored pointer
// is only offered after the engine confirms it resolves against *this*
// copy of the book; but resolution here checks only the spine step, and
// a CFI's path inside the chapter is walked lazily after the chapter
// loads, where a pointer from another engine or edition can still fail.
// That is why this returns a ladder for the caller to descend rather
// than a single answer.
function startCandidates(op) {
  if (!op) return [];
  const resolves = (target) => {
    try {
      const resolved = view.resolveNavigation(target);
      return (
        resolved != null &&
        typeof resolved.index === "number" &&
        !!view.book.sections[resolved.index]
      );
    } catch (err) {
      return false;
    }
  };
  const out = [];
  const cfi = cfiOf(op);
  if (cfi && resolves(cfi)) out.push(cfi);
  const locations = (op.locator && op.locator.locations) || {};
  const fraction =
    typeof locations.totalProgression === "number"
      ? locations.totalProgression
      : op.progression;
  if (finite(fraction) && fraction >= 0) {
    out.push({ fraction: Math.min(0.999, fraction) });
  }
  const href = op.locator && op.locator.href;
  if (href && resolves(href)) out.push(href);
  return out;
}

// ------------------------------------------------------ appearance

// Reader appearance, Komga-style: theme, font, size, spacing, layout.
// All of it is a browser preference — stored in localStorage, applied
// through the engine's user stylesheet and layout attributes, never
// sent to the server. The default for everything is "what the
// publisher said": a fresh reader renders the book exactly as shipped,
// and every override below exists only once the user asks for it.
const SETTINGS_KEY = "liseur.reader.settings";
const SETTINGS_DEFAULTS = Object.freeze({
  theme: "original",
  font: "publisher",
  size: 100,
  spacing: "0",
  justify: false,
  hyphenate: false,
  flow: "paginated",
  columns: "auto",
  margin: "normal",
  autohide: false,
  footer: "chapter",
});
// What the footer's middle slot shows; a click on the footer walks
// this ring, the way a tap does in the app.
const FOOTER_MODES = ["chapter", "time-chapter", "time-book", "empty"];
const THEMES = {
  light: { bg: "#ffffff", fg: "#1b1b1f", link: "#1a63c4", scheme: "light" },
  sepia: { bg: "#f6ecd9", fg: "#5b4636", link: "#8a5a2b", scheme: "light" },
  dark: { bg: "#202124", fg: "#cfcfd4", link: "#8ab4f8", scheme: "dark" },
  "tokyo-night": {
    bg: "#1a1b26",
    fg: "#c0caf5",
    link: "#7aa2f7",
    scheme: "dark",
  },
  "rose-pine": {
    bg: "#191724",
    fg: "#e0def4",
    link: "#c4a7e7",
    scheme: "dark",
  },
  black: { bg: "#000000", fg: "#ababae", link: "#7aa2d8", scheme: "dark" },
};
const FONTS = {
  serif: 'Georgia, "Times New Roman", "Liberation Serif", serif',
  sans: 'system-ui, -apple-system, "Segoe UI", Roboto, "Liberation Sans", sans-serif',
};
const MARGINS = { narrow: "16px", normal: "48px", wide: "72px" };
// The column gap follows the margin choice: "narrow" should mean the
// text gets the window, not just that the outer edge moved.
const GAPS = { narrow: "3%", normal: "7%", wide: "11%" };

let settings = loadSettings();

function loadSettings() {
  try {
    const stored = JSON.parse(localStorage.getItem(SETTINGS_KEY));
    return { ...SETTINGS_DEFAULTS, ...(stored || {}) };
  } catch (err) {
    return { ...SETTINGS_DEFAULTS };
  }
}

function saveSettings() {
  try {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings));
  } catch (err) {
    /* private browsing: the settings last as long as the page */
  }
}

// chapterCSS builds the user stylesheet the engine injects after the
// publication's own. Only what differs from the defaults appears, so
// with everything at "publisher" the sheet is one safety rule and the
// book's own design is untouched — which the browser test relies on
// when it reads the publication's colour back out of the page.
function chapterCSS(s) {
  const rules = ["pre { white-space: pre-wrap !important; }"];
  const theme = THEMES[s.theme];
  if (theme) {
    rules.push(
      `html { color-scheme: ${theme.scheme}; }`,
      `html, body { background: ${theme.bg} !important; color: ${theme.fg} !important; }`,
      `body * { background-color: transparent !important; color: ${theme.fg} !important; }`,
      `a:any-link { color: ${theme.link} !important; }`,
    );
  }
  if (FONTS[s.font]) {
    rules.push(
      `body, body :not(pre):not(code):not(kbd):not(samp) { font-family: ${FONTS[s.font]} !important; }`,
    );
  }
  const size = Number(s.size);
  if (size && size !== 100) {
    // Both rules matter: scaling the root handles publications that
    // size text in rem/em, and forcing the body and paragraphs back to
    // 1rem overrides the ones that pin type in px or CSS keywords
    // (small, medium...), which would otherwise ignore the slider.
    rules.push(
      `html { font-size: ${size}% !important; }`,
      "body { font-size: 1rem !important; }",
      "p, li, blockquote, dd, dt, table, td, th { font-size: 1rem !important; }",
      "h1 { font-size: 1.8rem !important; }",
      "h2 { font-size: 1.5rem !important; }",
      "h3 { font-size: 1.3rem !important; }",
      "h4 { font-size: 1.15rem !important; }",
      "h5 { font-size: 1rem !important; }",
      "h6 { font-size: 0.9rem !important; }",
      ".lettrine, .dropcap, .first-letter { font-size: 2.5rem !important; line-height: 1 !important; }",
      // A moved slider is the user taking over the typography, and
      // that has to include the measure: books that cap their own
      // text width (max-width on the body or a wrapper at any depth)
      // would otherwise keep a ribbon of the old width pinned to the
      // left of the wider column the reader lays out for the bigger
      // type, and the growth would arrive as blank page instead of
      // longer lines. Caps are lifted on wrappers however deep —
      // width as well as max-width, since publishers use either to
      // pin the measure — but side spacing is only zeroed at the top
      // level: deeper margins and padding are indentation, not page
      // geometry. The engine's own max-inline-size still bounds the
      // line length.
      "body, body div, body section, body article {" +
        " max-width: none !important; max-inline-size: none !important;" +
        " width: auto !important; inline-size: auto !important; }",
      "body, body > div, body > section, body > article {" +
        " margin-left: 0 !important; margin-right: 0 !important;" +
        " padding-left: 0 !important; padding-right: 0 !important; }",
    );
  }
  const spacing = Number(s.spacing);
  if (spacing) {
    rules.push(`p, li, blockquote, dd { line-height: ${spacing} !important; }`);
  }
  if (s.justify) {
    rules.push(
      "p, li, blockquote, dd { text-align: justify; }",
      '[align="left"] { text-align: left; } [align="right"] { text-align: right; }',
      '[align="center"] { text-align: center; }',
    );
  }
  if (s.hyphenate) {
    rules.push(
      "p, li, blockquote, dd { -webkit-hyphens: auto; hyphens: auto; }",
    );
  }
  return rules.join("\n");
}

function applySettings() {
  document.body.dataset.readerTheme = settings.theme;
  document.body.dataset.readerFlow =
    settings.flow === "scrolled" ? "scrolled" : "paginated";
  document.body.dataset.readerFooter = FOOTER_MODES.includes(settings.footer)
    ? settings.footer
    : SETTINGS_DEFAULTS.footer;
  // The footer lives in the bottom margin the engine leaves under the
  // text, so its height is that margin, whatever the setting says.
  document.body.style.setProperty(
    "--reader-margin",
    MARGINS[settings.margin] || MARGINS.normal,
  );
  applyChrome();
  if (here) chapterText.textContent = footerMiddle(here);
  if (!view || !view.renderer) return;
  const renderer = view.renderer;
  renderer.setAttribute(
    "flow",
    settings.flow === "scrolled" ? "scrolled" : "paginated",
  );
  renderer.setAttribute(
    "max-column-count",
    settings.columns === "auto" ? "2" : settings.columns,
  );
  renderer.setAttribute("margin", MARGINS[settings.margin] || MARGINS.normal);
  renderer.setAttribute("gap", GAPS[settings.margin] || GAPS.normal);
  // The line-length cap grows with the type: a bigger font on a wide
  // window should mean a wider column with the same characters per
  // line, not the same 720px ribbon with more empty page around it.
  const scale = (Number(settings.size) || 100) / 100;
  renderer.setAttribute(
    "max-inline-size",
    Math.round(720 * Math.max(1, scale)) + "px",
  );
  if (renderer.setStyles) renderer.setStyles(chapterCSS(settings));
}

const settingsPanel = document.getElementById("reader-settings");
const settingsForm = document.getElementById("reader-settings-form");
const sizeOut = document.getElementById("reader-size-out");

function syncSettingsForm() {
  if (!settingsForm) return;
  for (const field of settingsForm.elements) {
    if (!field.name || !(field.name in settings)) continue;
    if (field.type === "radio")
      field.checked = field.value === String(settings[field.name]);
    else if (field.type === "checkbox") field.checked = !!settings[field.name];
    else field.value = String(settings[field.name]);
  }
  if (sizeOut) sizeOut.textContent = settings.size + "%";
}

function readSettingsForm() {
  const data = new FormData(settingsForm);
  settings = {
    theme: String(data.get("theme") || SETTINGS_DEFAULTS.theme),
    font: String(data.get("font") || SETTINGS_DEFAULTS.font),
    size: Number(data.get("size")) || SETTINGS_DEFAULTS.size,
    spacing: String(data.get("spacing") || SETTINGS_DEFAULTS.spacing),
    justify: data.has("justify"),
    hyphenate: data.has("hyphenate"),
    flow: String(data.get("flow") || SETTINGS_DEFAULTS.flow),
    columns: String(data.get("columns") || SETTINGS_DEFAULTS.columns),
    margin: String(data.get("margin") || SETTINGS_DEFAULTS.margin),
    autohide: data.has("autohide"),
    footer: String(data.get("footer") || SETTINGS_DEFAULTS.footer),
  };
  if (sizeOut) sizeOut.textContent = settings.size + "%";
}

if (settingsForm) {
  syncSettingsForm();
  settingsForm.addEventListener("input", () => {
    readSettingsForm();
    saveSettings();
    applySettings();
  });
  document
    .getElementById("reader-settings-reset")
    .addEventListener("click", () => {
      settings = { ...SETTINGS_DEFAULTS };
      syncSettingsForm();
      saveSettings();
      applySettings();
    });
  document.addEventListener("click", (e) => {
    if (settingsPanel.open && !settingsPanel.contains(e.target)) {
      settingsPanel.open = false;
    }
  });
}
// -------------------------------------------------------- fullscreen

const MAXIMIZE_PATH = "M8 3H5a2 2 0 00-2 2v3m18 0V5a2 2 0 00-2-2h-3M3 16v3a2 2 0 002 2h3m10 0h3a2 2 0 002-2v-3";
const MINIMIZE_PATH = "M4 14h3a2 2 0 012 2v3m6 0v-3a2 2 0 012-2h3M20 10h-3a2 2 0 01-2-2V5m-6 0v3a2 2 0 01-2 2H4";

function toggleFullscreen() {
  if (!document.fullscreenElement) {
    document.documentElement.requestFullscreen().catch(() => {});
  } else {
    document.exitFullscreen();
  }
}

if (fullscreenBtn) {
  fullscreenBtn.addEventListener("click", toggleFullscreen);
  document.addEventListener("fullscreenchange", () => {
    const p = fullscreenBtn.querySelector("path");
    if (document.fullscreenElement) {
      p.setAttribute("d", MINIMIZE_PATH);
      fullscreenBtn.title = "Exit full screen (f)";
    } else {
      p.setAttribute("d", MAXIMIZE_PATH);
      fullscreenBtn.title = "Full screen (f)";
    }
  });
}

// ---------------------------------------------- chrome and tap zones

// The bar and the two arrows float over the book and step aside while
// nobody is reaching for them. What replaces them is the tap model
// here: the sides of the window turn pages wherever they are clicked —
// blank margin or the text itself — and the middle asks for the chrome
// back. A phone has no hover and no arrows to aim at once they have
// faded, so without this there would be no way left to turn a page.
//
// The zones are a share of the window rather than a fixed ribbon: a
// third of a phone is a thumb, a third of a desktop window is most of
// the column.
const CHROME_IDLE_MS = 2200;
const CHROME_TOUCH_MS = 4000;
const TAP_SLOP_PX = 10;

let chromePinned = false;
let chromeTimer = null;

function chromeAuto() {
  return !!settings.autohide && !chromePinned;
}

function chromeVisible() {
  return document.body.dataset.readerChrome !== "hidden";
}

function setChrome(visible) {
  document.body.dataset.readerChrome = visible ? "visible" : "hidden";
}

// chromeBusy is every reason the bar cannot be taken away right now:
// something it owns is open, an error is on screen — a failure must
// never hide behind an invisible bar — or the keyboard is in it.
function chromeBusy() {
  if (gotoDialog && gotoDialog.open) return true;
  if (tocPanel && !tocPanel.hidden) return true;
  if (settingsPanel && settingsPanel.open) return true;
  if (status && !status.hidden) return true;
  const help = document.getElementById("reader-help");
  if (help && help.open) return true;
  const active = document.activeElement;
  return !!(
    active &&
    active.closest &&
    active.closest(".reader-bar, .reader-turn")
  );
}

function armChrome(delay) {
  clearTimeout(chromeTimer);
  if (!chromeAuto()) return;
  chromeTimer = setTimeout(() => {
    if (chromeBusy()) {
      armChrome(delay);
      return;
    }
    setChrome(false);
  }, delay);
}

function revealChrome(delay) {
  setChrome(true);
  armChrome(delay || CHROME_IDLE_MS);
}

function applyChrome() {
  if (!chromeAuto()) {
    clearTimeout(chromeTimer);
    setChrome(true);
    return;
  }
  revealChrome();
}

function sideWidth() {
  const w = window.innerWidth || 1;
  return Math.max(64, Math.min(w * (w < 700 ? 0.3 : 0.22), 260));
}

function tapZone(x) {
  const w = window.innerWidth || 1;
  const side = sideWidth();
  if (x <= side) return "prev";
  if (x >= w - side) return "next";
  return "chrome";
}

function inTopZone(y) {
  return y <= Math.max(56, (window.innerHeight || 0) * 0.1);
}

// tapAt is the whole click model, in viewport coordinates: the stage,
// the engine's margins and every chapter document funnel here so that
// one rule covers the page whatever the click happened to land on.
function tapAt(x, y) {
  const zone = tapZone(x);
  if (zone !== "chrome") {
    turn(zone === "prev" ? -1 : 1);
    if (chromeVisible()) armChrome(CHROME_IDLE_MS);
    return true;
  }
  if (!settings.autohide) {
    setChrome(true);
    return true;
  }
  if (chromeVisible()) {
    clearTimeout(chromeTimer);
    setChrome(false);
  } else {
    chromePinned = false;
    revealChrome(CHROME_TOUCH_MS);
  }
  return true;
}

function toggleChrome() {
  if (chromeVisible()) {
    chromePinned = false;
    clearTimeout(chromeTimer);
    setChrome(false);
  } else {
    chromePinned = true;
    clearTimeout(chromeTimer);
    setChrome(true);
  }
}

function pointerMoved(x, y, pointerType) {
  if (pointerType === "touch") return; // a finger reveals by tapping
  if (!chromeAuto()) return;
  if (inTopZone(y)) revealChrome();
  else if (chromeVisible()) armChrome(CHROME_IDLE_MS);
}

document.addEventListener(
  "pointermove",
  (e) => pointerMoved(e.clientX, e.clientY, e.pointerType),
  { passive: true },
);
document.addEventListener("focusin", () => {
  if (chromeAuto() && chromeBusy()) revealChrome();
});

// A chapter is a document of its own, so nothing that happens over the
// text reaches this page: pointer and click handling has to be wired
// per chapter, the way the reading keys already are. Its coordinates
// are the frame's, and the frame knows where it sits.
// A coordinate inside a chapter is the chapter's, and the reader thinks
// in the window's. The frame says where it sits — and, for a
// fixed-layout publication, how much the engine scaled it: those frames
// carry a CSS transform, so the rectangle is in rendered pixels while
// the coordinate inside it is in the document's own. A reflowable book
// scales by 1 and passes through unchanged.
function frameOffset(doc) {
  try {
    const el = doc.defaultView && doc.defaultView.frameElement;
    if (el) {
      const box = el.getBoundingClientRect();
      const scale = el.offsetWidth ? box.width / el.offsetWidth : 1;
      return { left: box.left, top: box.top, scale: scale || 1 };
    }
  } catch (err) {
    /* the frame was replaced between events */
  }
  const box = stage.getBoundingClientRect();
  return { left: box.left, top: box.top, scale: 1 };
}

function toViewport(doc, x, y) {
  const box = frameOffset(doc);
  return [box.left + x * box.scale, box.top + y * box.scale];
}

// Everything in a publication that is already something when it is
// clicked: a control, a link, a media player, an image map, a frame.
// Clicking one of those is doing that thing, not turning a page.
const TAP_EXEMPT =
  "a,button,input,textarea,select,summary,label,audio,video,area[href]," +
  "iframe,embed,object,[contenteditable],[role='button'],[role='link']";
// Long enough to cover the usual platform double-click intervals
// (Windows and macOS both default to about half a second).
const DOUBLE_CLICK_MS = 500;
// How long after a tap the browser's synthesized click may still turn
// up. It is the same tap, and it has already been answered.
const TOUCH_CLICK_MS = 700;

// overText answers the only question that makes a mouse click
// ambiguous: is there a word under the pointer? A click on a line of
// text might be the first half of a double-click that selects it, so it
// has to wait; a click on a margin, a gutter or the space below the
// last line cannot be selecting anything and turns the page at once.
// caretRangeFromPoint alone is not enough — it answers with the
// *nearest* caret position even when the point is nowhere near it — so
// the character it names is measured and the point has to be inside it.
function overText(doc, x, y) {
  const find = doc.caretRangeFromPoint || doc.caretPositionFromPoint;
  if (!find) return true; // no way to tell: assume the careful answer
  try {
    const hit = find.call(doc, x, y);
    if (!hit) return false;
    const node = hit.startContainer || hit.offsetNode;
    const offset = hit.startOffset ?? hit.offset ?? 0;
    if (!node || node.nodeType !== 3 || !node.length) return false;
    const at = Math.min(offset, node.length - 1);
    const range = doc.createRange();
    range.setStart(node, at);
    range.setEnd(node, at + 1);
    const box = range.getBoundingClientRect();
    return (
      x >= box.left - 2 &&
      x <= box.right + 2 &&
      y >= box.top - 2 &&
      y <= box.bottom + 2
    );
  } catch (err) {
    return true;
  }
}

// A gesture is one pointer, from its own down to its own up. A second
// finger, a cancelled pointer or a stolen capture ends it: what happens
// next is a pinch, a scroll or the browser's business, never a tap.
function newGesture() {
  return { id: null, x: 0, y: 0, spoiled: true, hadSelection: false };
}

function wireChapterPointer(doc) {
  let gesture = newGesture();
  let mouse = false;
  let touchAt = 0;
  let deferred = null;
  const cancelDeferred = () => {
    clearTimeout(deferred);
    deferred = null;
  };
  const selected = () => {
    const sel = doc.getSelection && doc.getSelection();
    return !!(sel && sel.rangeCount && !sel.isCollapsed);
  };
  // Everything a tap has to not be, in one place: a drag, a gesture
  // that was never ours to finish, a second click, a control — or a
  // click that is putting a selection away rather than making one,
  // which is why the selection is remembered from pointerdown: the
  // browser collapses it before the click arrives, and by then the
  // evidence is gone.
  const isTap = (e) => {
    if (gesture.spoiled) return false;
    if (typeof e.button === "number" && e.button !== 0) return false;
    if (e.detail > 1) return false;
    if (gesture.hadSelection || selected()) return false;
    return !(e.target && e.target.closest && e.target.closest(TAP_EXEMPT));
  };
  doc.addEventListener(
    "pointermove",
    (e) => {
      pointerMoved(...toViewport(doc, e.clientX, e.clientY), e.pointerType);
    },
    { passive: true },
  );
  // A scroll is the one way to move through a book that is neither a
  // key nor a tap, so it counts as activity for the sitting.
  doc.addEventListener("wheel", () => noteNavigation(), { passive: true });
  doc.addEventListener(
    "pointerdown",
    (e) => {
      noteActivity();
      if (settingsPanel && settingsPanel.open) settingsPanel.open = false;
      if (gesture.id !== null && gesture.id !== e.pointerId) {
        gesture.spoiled = true; // a second finger: not a tap any more
        return;
      }
      mouse = e.pointerType !== "touch" && e.pointerType !== "pen";
      gesture = {
        id: e.pointerId,
        x: e.clientX,
        y: e.clientY,
        spoiled: false,
        hadSelection: selected(),
      };
    },
    { passive: true },
  );
  const spoil = (e) => {
    if (gesture.id === null || gesture.id === e.pointerId) gesture.spoiled = true;
  };
  doc.addEventListener("pointercancel", spoil, { passive: true });
  doc.addEventListener("lostpointercapture", spoil, { passive: true });
  // A finger is answered here rather than on the click that should
  // follow it: the engine snaps the page on every touchend, and a
  // scroll between touchend and the synthesized click cancels that
  // click outright. Waiting for it would mean tapping a phone and
  // watching nothing happen.
  doc.addEventListener(
    "pointerup",
    (e) => {
      if (gesture.id !== null && gesture.id !== e.pointerId) {
        gesture.spoiled = true;
        return;
      }
      if (
        Math.abs(e.clientX - gesture.x) > TAP_SLOP_PX ||
        Math.abs(e.clientY - gesture.y) > TAP_SLOP_PX
      )
        gesture.spoiled = true;
      gesture.id = null;
      if (e.pointerType !== "touch" && e.pointerType !== "pen") return;
      touchAt = Date.now();
      if (tocPanel && !tocPanel.hidden) {
        toggleTOC(false); // the drawer cannot hear a tap inside the book
        gesture.spoiled = true;
        return;
      }
      if (!isTap(e)) return;
      tapAt(...toViewport(doc, e.clientX, e.clientY));
    },
    { passive: true },
  );
  // Double-clicking a word selects it, and the reader who did that
  // wants the word, not the next page — but the first click of the
  // pair arrives while the selection is still empty and looks exactly
  // like a tap. A mouse click *on a word* therefore waits out the
  // double-click interval and is cancelled if a second click, a
  // selection or a dblclick follows. A mouse click anywhere else, and
  // any touch or pen tap, turns the page immediately: there is nothing
  // there to select, so there is nothing to wait for. (Beyond a
  // half-second double-click interval the first click does turn a
  // page; the reader turns back, and nothing is lost.)
  doc.addEventListener("dblclick", cancelDeferred);
  doc.addEventListener("selectstart", cancelDeferred);
  // A tap on the text turns the page; a drag, a selection, a link or
  // any other control is not a tap and is left entirely alone.
  doc.addEventListener("click", (e) => {
    if (e.detail > 1) {
      cancelDeferred();
      return;
    }
    // The tap this click belongs to was already answered on pointerup.
    if (Date.now() - touchAt < TOUCH_CLICK_MS) return;
    if (tocPanel && !tocPanel.hidden) {
      toggleTOC(false);
      gesture.spoiled = true;
      return;
    }
    if (!isTap(e)) return;
    const [x, y] = toViewport(doc, e.clientX, e.clientY);
    if (!mouse || !overText(doc, e.clientX, e.clientY)) {
      tapAt(x, y);
      return;
    }
    cancelDeferred();
    deferred = setTimeout(() => {
      deferred = null;
      const live = doc.getSelection && doc.getSelection();
      if (live && live.rangeCount && !live.isCollapsed) return;
      tapAt(x, y);
    }, DOUBLE_CLICK_MS);
  });
}

// The stage should match the chosen theme before the book arrives, so
// a dark reader does not open with a white flash.
applySettings();

// ---------------------------------------------------------- render

function paint(location) {
  here = location;
  // Leave the bar where it was when the fraction is unusable, so a
  // transient NaN does not flash the progress back to 0%.
  if (finite(location.fraction)) {
    const fraction = location.fraction;
    progressBar.style.width = (fraction * 100).toFixed(1) + "%";
    progressText.textContent = Math.round(fraction * 100) + "%";
  }
  // The page is a Readium position: a fixed slice of the book as it is
  // stored, so the count does not move when the font does and it is the
  // same number the app shows for the same spot. A book the recipe
  // cannot measure falls back to the engine's own locations. The same
  // rule as the fraction — an unusable value leaves the last one up.
  const page = readerPage(location);
  if (page) pageText.textContent = page;
  const hasPageTotal = !!(
    (positions && positions.total > 0) ||
    (location.location && finite(location.location.total) && location.location.total > 0)
  );
  pageText.disabled = !hasPageTotal;
  chapterText.textContent = footerMiddle(location);
  markTOC(location.tocItem);
}

// readerPage is the footer's "n of m" for this spot, or null when
// neither the positions nor the engine can name one.
function readerPage(location) {
  const section = location.section || {};
  const n = pageAt(positions, section.current, location.sectionFraction);
  if (n) return n + " of " + positions.total;
  const loc = location.location || {};
  if (finite(loc.current) && finite(loc.total) && loc.total > 0) {
    return (
      Math.min(Math.max(1, Math.floor(loc.current) + 1), loc.total) +
      " of " +
      loc.total
    );
  }
  return null;
}

// footerMiddle is what the middle slot says for this spot under the
// chosen mode. A slot with nothing honest to say stays empty rather
// than inventing something.
function footerMiddle(location) {
  const mode = FOOTER_MODES.includes(settings.footer)
    ? settings.footer
    : SETTINGS_DEFAULTS.footer;
  const time = location.time || {};
  switch (mode) {
    case "time-chapter":
      return finite(time.section)
        ? durationText(time.section) + " left in chapter"
        : "";
    case "time-book":
      return finite(time.total) ? durationText(time.total) + " left in book" : "";
    case "empty":
      return "";
    default: {
      // The chapter title the book itself gives this spot, falling back
      // to a plain count when the navigation has no entry covering it.
      const tocItem = location.tocItem;
      if (tocItem && tocItem.label) return tocItem.label.trim();
      const section = location.section || {};
      if (typeof section.current === "number" && section.total) {
        return "Chapter " + (section.current + 1) + " of " + section.total;
      }
      return "";
    }
  }
}

// durationText spells minutes the way the app does: "45 mins",
// "2 hrs 5 mins", or a friendly line for nearly nothing left.
function durationText(minutes) {
  const total = Math.round(minutes);
  if (total < 1) return "Less than a minute";
  const hours = Math.floor(total / 60);
  const rest = total % 60;
  const mins = rest + (rest === 1 ? " min" : " mins");
  if (hours === 0) return mins;
  const hrs = hours + (hours === 1 ? " hr" : " hrs");
  return rest === 0 ? hrs : hrs + " " + mins;
}

// A click on the footer cycles the middle slot, and only that: the
// footer is not a stage surface, so the page under it stays put.
function cycleFooter() {
  const i = FOOTER_MODES.indexOf(settings.footer);
  settings = {
    ...settings,
    footer: FOOTER_MODES[(i + 1) % FOOTER_MODES.length],
  };
  saveSettings();
  syncSettingsForm();
  applySettings();
}

async function goToPage(page) {
  if (!view) return;
  if (positions) {
    const loc = pageLocation(positions, page);
    if (loc && view.renderer) {
      try {
        await view.renderer.goTo(loc);
        const landedIndex = view.lastLocation?.section?.current;
        const state =
          landedIndex === loc.index && view.lastLocation?.cfi
            ? view.lastLocation.cfi
            : loc.index;
        if (view.history && view.history.pushState) {
          view.history.pushState(state);
        }
        return;
      } catch (e) {
        /* fall through to engine fraction fallback */
      }
    }
  }
  const loc = here && here.location;
  if (loc && finite(loc.total) && loc.total > 0) {
    const frac = Math.min(Math.max((page - 0.5) / loc.total, 0), 1);
    await view.goToFraction(frac).catch(() => {});
  }
}

function openGoto(kind) {
  if (!gotoDialog || !gotoInput) return;
  gotoKind = kind;
  if (kind === "percent") {
    if (gotoTitle) gotoTitle.textContent = "Go to percentage";
    if (gotoLabel) gotoLabel.textContent = "Percentage";
    gotoInput.min = "0";
    gotoInput.max = "100";
    gotoInput.step = "1";
    gotoInput.value =
      here && finite(here.fraction) ? String(Math.round(here.fraction * 100)) : "";
    if (gotoUnit) gotoUnit.textContent = "%";
  } else {
    const total = positions
      ? positions.total
      : here && here.location && finite(here.location.total) && here.location.total > 0
        ? here.location.total
        : null;
    if (!total) return;
    if (gotoTitle) gotoTitle.textContent = "Go to page";
    if (gotoLabel) gotoLabel.textContent = "Page number";
    gotoInput.min = "1";
    gotoInput.max = String(total);
    gotoInput.step = "1";
    let current = null;
    if (positions && here && here.section) {
      current = pageAt(positions, here.section.current, here.sectionFraction);
    } else if (
      here &&
      here.location &&
      finite(here.location.current) &&
      finite(here.location.total)
    ) {
      current = Math.min(
        Math.max(1, Math.floor(here.location.current) + 1),
        here.location.total,
      );
    }
    gotoInput.value = current !== null ? String(current) : "";
    if (gotoUnit) gotoUnit.textContent = "of " + total;
  }
  gotoDialog.showModal();
  gotoInput.select();
}

if (footer) {
  footer.addEventListener("click", (e) => {
    const btn = e.target && e.target.closest ? e.target.closest("button") : null;
    if (btn === progressText) {
      openGoto("percent");
    } else if (btn === pageText) {
      openGoto("page");
    } else {
      cycleFooter();
    }
  });
}

if (gotoForm) {
  gotoForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    const val = parseFloat(gotoInput.value);
    if (!Number.isFinite(val)) return;
    if (!noteNavigation()) return;
    if (gotoKind === "percent") {
      const pct = Math.min(Math.max(val, 0), 100);
      if (view) await view.goToFraction(pct / 100).catch(() => {});
    } else {
      await goToPage(val);
    }
    if (gotoDialog && gotoDialog.open) gotoDialog.close();
  });
}

if (gotoCancel) {
  gotoCancel.addEventListener("click", () => {
    if (gotoDialog && gotoDialog.open) gotoDialog.close();
  });
}

if (gotoDialog) {
  gotoDialog.addEventListener("click", (e) => {
    if (e.target !== gotoDialog) return;
    const rect = gotoDialog.getBoundingClientRect();
    const inDialog =
      rect.top <= e.clientY &&
      e.clientY <= rect.top + rect.height &&
      rect.left <= e.clientX &&
      e.clientX <= rect.left + rect.width;
    if (!inDialog) gotoDialog.close();
  });
}

// ------------------------------------------------------- contents

// buildTOC turns the publication's navigation into the drawer's list.
// Labels are set as text, never as markup — the TOC is publication
// content like everything else. Entries without an href (bare section
// headings) render as labels that cannot be followed.
function buildTOC(items) {
  tocList.textContent = "";
  if (!items || !items.length) {
    if (tocButton) tocButton.hidden = true;
    return;
  }
  const make = (list) => {
    const ul = document.createElement("ul");
    for (const item of list || []) {
      const li = document.createElement("li");
      if (item.href) {
        const a = document.createElement("a");
        a.href = "#";
        a.textContent = (item.label || "").trim() || item.href;
        a.dataset.href = item.href;
        li.append(a);
      } else {
        const span = document.createElement("span");
        span.textContent = (item.label || "").trim();
        li.append(span);
      }
      if (item.subitems && item.subitems.length) li.append(make(item.subitems));
      ul.append(li);
    }
    return ul;
  };
  tocList.append(make(items));
}

function markTOC(tocItem) {
  if (!tocList) return;
  const before = tocList.querySelector("a.current");
  if (before) before.classList.remove("current");
  if (!tocItem || !tocItem.href) return;
  for (const a of tocList.querySelectorAll("a")) {
    if (a.dataset.href === tocItem.href) {
      a.classList.add("current");
      break;
    }
  }
}

function toggleTOC(open) {
  const want = typeof open === "boolean" ? open : tocPanel.hidden;
  tocPanel.hidden = !want;
  if (want) {
    // The drawer belongs to the button in the bar, so the bar comes
    // back with it however the drawer was opened.
    revealChrome();
    const current =
      tocList.querySelector("a.current") || tocList.querySelector("a");
    if (current) current.focus();
    if (current) current.scrollIntoView({ block: "center" });
  }
}

if (tocButton) tocButton.addEventListener("click", () => toggleTOC());
// A click anywhere outside the drawer puts it away, the way a drawer
// behaves everywhere else.
document.addEventListener("click", (e) => {
  if (!tocPanel || tocPanel.hidden) return;
  if (tocPanel.contains(e.target)) return;
  if (tocButton && tocButton.contains(e.target)) return;
  toggleTOC(false);
});
tocList.addEventListener("click", (e) => {
  const a = e.target && e.target.closest && e.target.closest("a[data-href]");
  if (!a) return;
  e.preventDefault();
  if (!noteNavigation()) return;
  toggleTOC(false);
  if (view) view.goTo(a.dataset.href).catch(() => {});
});

// stripScripts removes the publication's own code from every resource
// before the engine turns it into a blob URL. The page CSP is what
// actually stops a book's script from running — a blob document
// inherits this page's policy, and only the nonce this server minted
// for its own module tag passes script-src — so this is the second
// fence, and it also keeps the console clear of the browser announcing
// refusals.
//
// Markup is stripped by parsing it, not by pattern-matching it: a
// regex misses a script element in an SVG island, a namespace prefix,
// or markup broken in just the way a parser would quietly repair. The
// document is parsed exactly as the engine will parse it, every script
// element in any namespace is removed, and what is serialized back is
// what the parser saw — there is no second interpretation for hostile
// markup to aim between. If the resource does not parse at all, the
// engine will not render it either; it is replaced with the parse
// error rather than passed through unexamined.
function stripScripts(book) {
  if (!book || !book.transformTarget) return;
  const parser = new DOMParser();
  const serializer = new XMLSerializer();
  const strip = (data, mime) => {
    const doc = parser.parseFromString(data, mime);
    if (doc.querySelector("parsererror")) {
      return serializer.serializeToString(doc);
    }
    for (const el of [...doc.getElementsByTagName("script")]) el.remove();
    for (const el of [...doc.getElementsByTagNameNS("*", "script")])
      el.remove();
    return serializer.serializeToString(doc);
  };
  book.transformTarget.addEventListener("data", (event) => {
    const detail = event.detail;
    const type = detail.type || "";
    if (/\b(x-)?(javascript|ecmascript)\b/.test(type)) {
      detail.data = "";
      return;
    }
    const mime = /\bxhtml\+xml\b/.test(type)
      ? "application/xhtml+xml"
      : /\bsvg\+xml\b/.test(type)
        ? "image/svg+xml"
        : /\bhtml\b/.test(type)
          ? "text/html"
          : null;
    if (mime) {
      detail.data = Promise.resolve(detail.data).then((data) =>
        typeof data === "string" ? strip(data, mime) : data,
      );
    }
  });
}

function turn(direction) {
  if (!noteNavigation()) return undefined;
  return direction > 0 ? view.goRight() : view.goLeft();
}

document.getElementById("reader-next").addEventListener("click", () => turn(1));
document
  .getElementById("reader-prev")
  .addEventListener("click", () => turn(-1));
// Any blank margin is a page turn: a click that lands on the stage or
// on the engine's own chrome (margins, gaps, header, footer — all of
// which retarget to the foliate-view host from its closed shadow root)
// goes through the same tap zones as the text, so the sides turn the
// page and the middle brings the bar back. Clicks inside the chapter
// itself are handled in that chapter's own document, where a link or a
// live selection can still be told apart from a tap.
// A tap on the margin, rather than on the text: the same rule, and the
// same reason for handling touch on pointerup. The engine snaps the
// page on every touchend, and a scroll between touchend and the click
// the browser would have synthesized cancels that click — a finger
// would tap and nothing would happen. When the click does arrive it is
// the same tap arriving twice, so a short window swallows it.
// A click on the margin while a passage is selected is putting the
// selection away, the same as a click on the text: the selection lives
// in the chapter, so the margin has to go and ask.
function bookHasSelection() {
  try {
    const contents =
      view && view.renderer && view.renderer.getContents
        ? view.renderer.getContents()
        : [];
    for (const content of contents) {
      const doc = content && content.doc;
      const sel = doc && doc.getSelection && doc.getSelection();
      if (sel && sel.rangeCount && !sel.isCollapsed) return true;
    }
  } catch (err) {
    /* the chapter went away mid-gesture */
  }
  return false;
}

let stageTouchAt = 0;
let stageGesture = newGesture();
const stageSurface = (target) =>
  target === stageArea || target === stage || target === view;
stageArea.addEventListener(
  "pointerdown",
  (e) => {
    if (stageGesture.id !== null && stageGesture.id !== e.pointerId) {
      stageGesture.spoiled = true;
      return;
    }
    stageGesture = {
      id: e.pointerId,
      x: e.clientX,
      y: e.clientY,
      spoiled: false,
      hadSelection: bookHasSelection(),
    };
  },
  { passive: true },
);
const spoilStage = (e) => {
  if (stageGesture.id === null || stageGesture.id === e.pointerId)
    stageGesture.spoiled = true;
};
stageArea.addEventListener("pointercancel", spoilStage, { passive: true });
stageArea.addEventListener("lostpointercapture", spoilStage, { passive: true });
stageArea.addEventListener("pointerup", (e) => {
  noteActivity();
  if (stageGesture.id !== null && stageGesture.id !== e.pointerId) {
    stageGesture.spoiled = true;
    return;
  }
  if (
    Math.abs(e.clientX - stageGesture.x) > TAP_SLOP_PX ||
    Math.abs(e.clientY - stageGesture.y) > TAP_SLOP_PX
  )
    stageGesture.spoiled = true; // a swipe: the engine's business
  const clean = !stageGesture.spoiled;
  stageGesture.id = null;
  if (!view) return;
  if (e.pointerType !== "touch" && e.pointerType !== "pen") return;
  stageTouchAt = Date.now();
  if (tocPanel && !tocPanel.hidden) {
    toggleTOC(false);
    return;
  }
  if (!clean) return;
  if (stageGesture.hadSelection || bookHasSelection()) return;
  if (!stageSurface(e.target)) return;
  tapAt(e.clientX, e.clientY);
});
stageArea.addEventListener("click", (e) => {
  if (!view) return;
  if (tocPanel && !tocPanel.hidden) return; // the click puts the drawer away
  if (!stageSurface(e.target)) return;
  if (Date.now() - stageTouchAt < TOUCH_CLICK_MS) return; // already handled
  if (stageGesture.hadSelection || bookHasSelection()) return;
  tapAt(e.clientX, e.clientY);
});
// The standard reading keys. Space is the one every reader agrees on
// (Shift reverses it, as everywhere else); h/l and j/k are for hands
// that live on vim; Home and End are the covers. The handler is shared
// between the page and every chapter document, because after a click
// in the text the frame owns the keyboard and a page-level listener
// alone goes deaf — the engine re-emits its load event per chapter, so
// each document gets wired as it arrives.
function handleKeys(e) {
  // Native buttons own Space/Enter; the reading shortcuts must not turn the
  // publication or invalidate the offer while its controls have keyboard focus.
  if (catchupPanel?.contains(e.target)) return;
  noteActivity();
  if (gotoDialog && gotoDialog.open) return;
  const helpDialog = document.getElementById("reader-help");
  // Arrow keys inside the settings panel adjust its controls, not the
  // book; Escape puts the panel away from the keyboard.
  if (settingsPanel && settingsPanel.contains(e.target)) {
    if (e.key === "Escape") settingsPanel.open = false;
    return;
  }
  if (e.key === "Escape" && settingsPanel && settingsPanel.open) {
    settingsPanel.open = false;
    return;
  }
  // While the contents drawer is up it owns the keyboard: Tab walks
  // it, Enter follows, Escape or t puts it away — and nothing leaks
  // through to turn a page underneath it.
  if (tocPanel && !tocPanel.hidden) {
    if (e.key === "Escape" || e.key === "t") {
      e.preventDefault();
      toggleTOC(false);
    }
    return;
  }
  // The catch-up offer never takes focus, so Escape typed in the
  // publication (a separate document) or on the page must still reach
  // it, the same as the drawer above.
  if (catchupPanel && !catchupPanel.hidden && e.key === "Escape") {
    e.preventDefault();
    catchup.dismiss();
    hideCatchup();
    return;
  }
  // "?" summons the help from anywhere, including from inside a
  // chapter document; the native dialog owns Escape and focus while
  // it is up.
  if (e.key === "?" && helpDialog && !e.ctrlKey && !e.metaKey && !e.altKey) {
    e.preventDefault();
    if (helpDialog.open) helpDialog.close();
    else helpDialog.showModal();
    return;
  }
  if (helpDialog && helpDialog.open) return;
  // Modified keys belong to the browser, and keys aimed at a form
  // field belong to the field.
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const tag = e.target && e.target.tagName;
  if (
    tag === "INPUT" ||
    tag === "TEXTAREA" ||
    tag === "SELECT" ||
    (e.target && e.target.isContentEditable)
  )
    return;
  switch (e.key) {
    case " ":
      e.preventDefault();
      turn(e.shiftKey ? -1 : 1);
      break;
    case "ArrowRight":
    case "PageDown":
    case "l":
    case "j":
      e.preventDefault();
      turn(1);
      break;
    case "ArrowLeft":
    case "PageUp":
    case "h":
    case "k":
      e.preventDefault();
      turn(-1);
      break;
    case "t":
      e.preventDefault();
      toggleTOC(true);
      break;
    case "g":
      e.preventDefault();
      revealChrome();
      openGoto("percent");
      break;
    case "f":
      e.preventDefault();
      toggleFullscreen();
      break;
    case "z":
      e.preventDefault();
      toggleChrome();
      break;
    case "Home":
      if (noteNavigation()) view.goToFraction(0);
      break;
    case "End":
      if (noteNavigation()) view.goToFraction(1);
      break;
  }
}
document.addEventListener("keydown", handleKeys);
window.addEventListener("beforeunload", () => {
  clearTimeout(pending);
  push();
  endSession();
});

// ------------------------------------------------------------ open

(async function start() {
  try {
    say("Fetching the book…");
    // Same-origin the cookie is enough and is what the UI download
    // route expects; detached there is no cookie, so the book comes
    // from the API with the bearer token like any other client.
    const resp = cfg.detached
      ? await api("v1/books/" + encodeURIComponent(cfg.bookID) + "/download")
      : await fetch(cfg.downloadURL, { credentials: "same-origin" });
    if (!resp.ok) throw new Error("this book could not be downloaded");
    const blob = await resp.blob();

    say("Opening…");
    view = document.createElement("foliate-view");
    stage.append(view);
    view.addEventListener("relocate", (e) => {
      paint(e.detail);
      if (finite(e.detail.fraction)) {
        clearFractionRetry();
        if (!restoring) {
          if (interactionPending) {
            interactionPending = false;
            readingDirty = true;
            catchup.moved();
            hideCatchup();
          }
          schedulePush();
          noteProgress();
        }
      } else {
        cancelScheduledPush();
        scheduleFractionRetry();
      }
    });
    // Each chapter document gets the reading keys: after a click in
    // the text, the frame owns the keyboard, and a listener on the
    // page alone would go deaf exactly when the reader looks focused.
    // The pointer is wired there for the same reason — a tap on the
    // text is how a phone turns a page once the arrows have faded.
    view.addEventListener("load", (e) => {
      e.detail.doc.addEventListener("keydown", handleKeys);
      wireChapterPointer(e.detail.doc);
    });
    view.addEventListener("link", (e) => {
      if (!noteNavigation()) e.preventDefault();
    });
    // Highlight drawing (ADR-0028): the engine asks how to draw each
    // annotation it anchors, and asks again for every chapter it
    // creates an overlay for. The color is resolved from the palette
    // table above — a token in, a CSS value out, nothing pass-through.
    view.addEventListener("draw-annotation", (e) => {
      const { draw, annotation } = e.detail;
      const color =
        ANNOTATION_COLORS[annotationDrawing.color(annotation.value)] ||
        ANNOTATION_COLORS.yellow;
      draw(Overlayer.highlight, { color });
    });
    view.addEventListener("create-overlay", () => {
      annotationDrawing.draw().catch(() => {});
    });
    await view.open(
      new File([blob], "book.epub", { type: "application/epub+zip" }),
    );
    stripScripts(view.book);
    // Counted before the first relocate paints a footer, so the very
    // first page the reader sees is already the app's number.
    positions = positionTable(view.book.sections);
    buildTOC(view.book.toc);
    // The renderer exists once the book is open; settings applied here
    // shape the very first page rather than repainting it.
    applySettings();

    const title = bookTitle();
    if (title) {
      titleText.textContent = title;
      document.title = title + " · liseur-sync";
    }
    // The detached page could not link the cover: it lives on the API
    // origin and a <link> fetch has no credential. Now that the token
    // is in hand, the icon is swapped for it; a book without a cover
    // gets the placeholder response, so the icon is still swapped for
    // it. Same-origin pages linked the cover route from the start, so
    // there is nothing to do.
    if (cfg.detached) {
      api("v1/books/" + encodeURIComponent(cfg.bookID) + "/cover?size=icon")
        .then(async (resp) => {
          if (!resp.ok) return;
          const cover = await resp.blob();
          if (!auth.responseCurrent(resp)) return;
          const nextFaviconObjectURL = URL.createObjectURL(cover);
          const link = document.querySelector("link[rel=icon]");
          if (!link) {
            URL.revokeObjectURL(nextFaviconObjectURL);
            return;
          }
          if (faviconObjectURL) URL.revokeObjectURL(faviconObjectURL);
          faviconObjectURL = nextFaviconObjectURL;
          link.href = faviconObjectURL;
        })
        .catch(() => {});
    }

    // Sync is best-effort: a book still opens on a server that has
    // lost its work mapping, it just opens at the beginning.
    let op = null;
    try {
      workID = await resolveWork();
      const identity = auth.identity();
      catchup.bind(identity?.account, workID, identity?.device);
      const result = await lastPosition();
      if (result.ok) op = result.op;
      catchup.baseline(op);
    } catch (err) {
      /* read on without sync */
    }

    // The candidates are tried in order because a pointer that
    // resolved on paper can still fail in the chapter: the CFI's spine
    // step is checked up front, but its path inside the document is
    // only walked once the chapter has loaded, and a CFI minted by
    // another engine or against another edition throws there. Each
    // rung falls to the next; a book with no usable pointer at all
    // opens at its text start rather than not at all.
    let opened = false;
    for (const target of startCandidates(op)) {
      try {
        await view.init({ lastLocation: target });
        opened = true;
        break;
      } catch (err) {
        /* stale pointer: descend to the coarser one */
      }
    }
    if (!opened) await view.init({ lastLocation: null });
    restoring = false;
    ready = !syncExpired;
    beginSession();
    // The annotations arrive after the book is on screen: they are
    // decoration on the text, never the reason the text waits.
    if (ready && !document.hidden) startLive();
    if (document.hidden) catchup.hide();
    if (!syncExpired) say("");
  } catch (err) {
    say((err && err.message) || "this book could not be opened", true);
  }
})();
