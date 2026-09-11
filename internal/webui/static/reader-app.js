// Reader controls and native sync over the Readium publication engine.

import "./reader-engine.js";
import { openSession } from "./reader-session.js";
import { uploadSessions } from "./reader-session-upload.js";
import { positionTable, pageAt, pageLocation } from "./reader-positions.js";
import { readerAuth } from "./reader-auth.js";
import { offlineSync, readingSync, outboxLock } from "./offline-sync.js";
import { liveStream } from "./reader-live.js";
import { catchupState, topicRefresh, latestReadablePosition, positionAcknowledged } from "./reader-sync.js";
import { reconcileReadingState } from "./reader-reconcile.js";
import { agreedEdition, startCandidates as restoreCandidates } from "./reader-restore.js";
import { placeOf, placeHere, placeLabel, placeSentence, relativeAge } from "./reader-place.js";
import { decideBookSync } from "./reader-sync-choice.js";
import { markLocator } from "./reader-anchor.js";
import { annotationCFI, annotationAnchor, annotationRenderer } from "./reader-annotations.js";
import { buildChapters, chapterForLocation, pagesLeftInChapter } from "./reader-chapters.js";
import {
  clearOfflineSessionCheckpoint,
  accountContext,
  accountVersion,
  setActiveAccount,
  claimOfflineReader,
  assertOfflineContext,
  notifyOfflineChange,
  finishOfflineSession,
  getOfflineSessionCheckpoint,
  getReadySnapshot,
  listOfflineAnnotations,
  listOfflineOutbox,
  removeOfflineOutbox,
  retryOfflineOutbox,
  discardOfflineOutbox,
  queueOfflineAnnotation,
  removeOfflineAnnotation,
  restoreOfflineAnnotation,
  saveReadingPosition,
  readingState,
  agreeReadingBaseline,
  saveOfflineSessionCheckpoint,
  deploymentPrefix,
  storagePartition,
  updateOfflineAnnotation,
} from "./offline-storage.js";
import * as CFI from "./vendor/foliate/epubcfi.js";

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
  offline: el.dataset.offline === "1",
  handed: null,
};
const annotationsEnabled = false;

if (cfg.offline) {
  cfg.bookID = new URL(location.href).searchParams.get("book") || "";
}

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
let catalogEditionSHA = "";
// What a locator may weigh. The server's own default; it names a
// smaller one when it refuses a batch for size, and that is the only
// way to learn it, so this starts conservative rather than unbounded.
let locatorLimitBytes = 16 * 1024;
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
const catchupDetail = document.getElementById("reader-catchup-detail");
const catchupExcerpt = document.getElementById("reader-catchup-excerpt");
const catchupAccept = document.getElementById("reader-catchup-accept");
const catchupDismiss = document.getElementById("reader-catchup-dismiss");
// The book's positions, counted once when it opens. Null for a book
// this recipe cannot measure, which leaves the engine's own locations
// to say what page it is.
let positions = null;
let chapters = null;
let offlineSnapshot = null;
let offlineAccount = null;
let offlineCSRF = "";
let offlineCheckpoint = null;
let offlineContext = null;
let offlineCoordinator = null;
let releaseOfflineReader = null;
// True when this page holds the book's reader claim, so it owns the
// single checkpoint slot for the sitting. A second tab reads on with
// its own fresh sitting rather than adopting this one's.
let sessionOwner = false;
let offlineInvalidated = false;
// An online reader on the deployment's own origin queues its reading
// state in the same IndexedDB the offline app uses, and drains it
// through the same lock. The separate reader origin (ADR-0007 phase 3)
// has no such storage of its own to share, so it keeps posting
// directly: that boundary is deliberate, not an oversight.
const durableSync = !cfg.offline && !cfg.detached;
const offlinePartition = cfg.detached ? null : storagePartition();
const offlineBase = cfg.offline ? deploymentPrefix() : "";
let readingCoordinator = null;
let sendTimer = null;

function say(message, isError) {
  status.textContent = message;
  status.classList.toggle("problem", !!isError);
  status.hidden = !message;
}

// ------------------------------------------- refused reading changes

const stuckPanel = document.getElementById("reader-stuck");
const stuckText = document.getElementById("reader-stuck-text");
const stuckRetry = document.getElementById("reader-stuck-retry");
const stuckDiscard = document.getElementById("reader-stuck-discard");
let stuck = [];

const stuckNames = {
  position: "reading position",
  session: "reading session",
  annotation: "annotation",
};

function stuckSentence(records) {
  if (records.length > 1) return `${records.length} reading changes could not be saved.`;
  const [only] = records;
  const name = stuckNames[only.kind] || "reading change";
  return only.error
    ? `A ${name} could not be saved: ${only.error}.`
    : `A ${name} could not be saved.`;
}

// showStuck names what the queue is holding and offers the two answers
// there are. An empty list takes the panel away, which is the whole
// point: every refusal must have an end.
function showStuck(records) {
  stuck = records;
  if (!stuckPanel) return;
  stuckPanel.hidden = !records.length;
  if (!records.length) return;
  stuckText.textContent = stuckSentence(records);
}

// Editing the queue takes the queue's own lock, so a drain already in
// flight finishes before a record is revived or thrown away underneath
// it.
async function answerStuck(act, discarding) {
  if (!offlineContext || !stuck.length) return;
  const records = stuck;
  showStuck([]);
  const owned = [];
  const work = async () => {
    for (const record of records) {
      const took = await act({ ...offlineContext, kind: record.kind, id: record.id });
      if (took) owned.push(took);
    }
  };
  let settled = false;
  try {
    if (navigator.locks) await navigator.locks.request(outboxLock(offlineContext), work);
    else await work();
    settled = true;
  } catch { /* the queue is best-effort; the next drain reports again */ }
  // Only a discard, and only for the annotations that discard actually
  // spoke for. *Try again* restores nothing: it leaves the reader's
  // payload queued, and the local copy is what that payload will
  // deliver, so replacing it with the server's version would send one
  // thing and show another. A record a newer mutation replaced owns
  // nothing, and one whose discard threw never entered this list — so a
  // later failure does not strand an earlier success unrestored.
  if (discarding && owned.length) await restoreDiscardedAnnotations(owned);
  if (!settled) console.warn("Not every stuck change could be answered.");
  (readingCoordinator || offlineCoordinator)?.trigger();
}

// Discarding an annotation leaves a question the local store cannot
// answer. A rejected *edit* is still the reader's rejected text, so the
// discard leaves it marked unsaved, and only the server can say what
// the annotation really holds. The book's annotations are re-read and
// both the store and the page are set to what comes back: restored
// where the server has a version, removed where it has none.
//
// A reader who cannot reach the server keeps the unsaved marking and
// the page follows the local store, which is honest about what is
// known. The next drain asks again, because a drain only runs when
// there is something to talk to.
const unrestored = new Set();

async function restoreDiscardedAnnotations(ids) {
  const owned = [...new Set(ids)];
  if (!owned.length) return;
  try {
    // A mutation queued between the transaction and this request owns
    // the annotation now, and its payload is what will be delivered.
    const queued = await listOfflineOutbox({
      ...offlineContext, kind: "annotation", state: null,
    }).catch(() => []);
    const touched = owned.filter(id =>
      !queued.some(record => record.annotationID === id));
    for (const id of owned) {
      if (!touched.includes(id)) unrestored.delete(id);
    }
    if (!touched.length) return;
    let server = null;
    if (workID) {
      const response = await api(
        "v1/works/" + encodeURIComponent(workID) + "/annotations",
      ).catch(() => null);
      const data = response?.ok ? await response.json().catch(() => null) : null;
      if (Array.isArray(data?.annotations)) server = data.annotations;
    }
    const stored = server ? [] : await listOfflineAnnotations({
      partition: storagePartition(), account: offlineAccount, bookID: cfg.bookID,
    });
    const resolved = new Map();
    for (const id of touched) {
      if (!server) {
        // Nothing authoritative to write, so the store is only read: the
        // page stops drawing a note the discard took and keeps the rest
        // marked unsaved.
        resolved.set(id, stored.find(value => value.id === id) || null);
        unrestored.add(id);
        continue;
      }
      const fresh = server.find(value => value.id === id);
      const annotation = fresh ? { ...fresh, pending: false } : null;
      // The write refuses if a mutation arrived while the request was in
      // the air, and only what was written may reach the page.
      if (await restoreOfflineAnnotation({
        ...offlineContext, bookID: cfg.bookID, id, annotation,
      })) {
        resolved.set(id, annotation);
        unrestored.delete(id);
      }
    }
    const drawn = annotationDrawing.annotations();
    const next = drawn
      .filter(value => !resolved.has(value.id) || resolved.get(value.id))
      .map(value => resolved.get(value.id) || value);
    // A discarded deletion is an annotation coming back, so it may not
    // be on the page at all.
    for (const [id, annotation] of resolved) {
      if (annotation && !drawn.some(value => value.id === id)) next.push(annotation);
    }
    await replaceAnnotations(next);
  } catch (error) {
    console.warn("The annotation list could not be refreshed:", error);
  }
}

// Two drains can overlap, and the button can fire during one, so the
// re-ask runs one at a time like the conflict resolver above it. A
// drain is also the right moment for it: it only runs when there is a
// server to ask.
let restoringAnnotations = null;
function retryUnrestoredAnnotations() {
  if (restoringAnnotations) return restoringAnnotations;
  if (!unrestored.size) return Promise.resolve();
  restoringAnnotations = restoreDiscardedAnnotations([...unrestored])
    .finally(() => { restoringAnnotations = null; });
  return restoringAnnotations;
}

stuckRetry?.addEventListener("click", () => { answerStuck(retryOfflineOutbox, false); });
stuckDiscard?.addEventListener("click", () => { answerStuck(discardOfflineOutbox, true); });

// ------------------------------------------------------------ auth

const auth = readerAuth({
  ...cfg,
  apiBase: cfg.offline ? offlineBase : cfg.apiBase,
  tokenURL: cfg.offline ? offlineBase + "ui/reader/token" : cfg.tokenURL,
  csrf: () => cfg.offline ? offlineCSRF : cfg.csrf,
  handed: cfg.handed,
  onChange(identity, previous) {
    if (!cfg.offline) offlineAccount = identity.account;
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
    if (cfg.offline) {
      syncExpired = true;
      say("Sign in again to sync offline changes.", true);
      return;
    }
    syncExpired = true;
    ready = false;
    lifecycle++;
    workID = null;
    catalogEditionSHA = "";
    session = null;
    unsent = [];
    retryOp = null;
    readingDirty = false;
    cancelScheduledPush();
    clearTimeout(sendTimer);
    sendTimer = null;
    readingCoordinator?.stop();
    readingCoordinator = null;
    releaseOfflineReader?.();
    releaseOfflineReader = null;
    sessionOwner = false;
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
  return { identity: auth.identity(), work: workID, view, lifecycle, offline: cfg.offline };
}
function current(stamp) {
  if (cfg.offline) {
    return !offlineInvalidated && !!stamp && stamp.offline && stamp.view === view &&
      stamp.lifecycle === lifecycle;
  }
  return stamp && auth.current(stamp.identity) && stamp.work === workID &&
    stamp.view === view && stamp.lifecycle === lifecycle;
}

function prepareOfflineSync() {
  if (!offlineContext) return;
  offlineCoordinator = offlineSync({
    context: offlineContext, base: offlineBase,
    onChange: async () => {
      const fresh = await getReadySnapshot({ ...offlineContext, bookID: cfg.bookID });
      if (fresh?.localPosition && !readingDirty) {
        catchup.observe(fresh.localPosition);
        showCatchup();
      }
      await resolveAnnotationConflicts();
      await retryUnrestoredAnnotations();
    },
    onStatus: message => say(message, !!message),
    onStuck: showStuck,
  });
  const retry = document.createElement("button");
  retry.type = "button";
  retry.className = "button secondary";
  retry.textContent = "Retry sync";
  retry.addEventListener("click", () => offlineCoordinator.trigger());
  status.after(retry);
  offlineCoordinator.trigger();
}

// prepareReadingSync claims the local queue for an online reader.
//
// The account marker is written from a credential this page actually
// holds — the token introspection said whose it is — so a reader opened
// straight from a bookmark queues durably without having to visit the
// library first. An unchanged account keeps its epoch, so downloaded
// books and anything already queued survive.
async function prepareReadingSync(identity) {
  if (!durableSync || !identity?.account || !offlinePartition) return;
  try {
    const version = await accountVersion(offlinePartition);
    const epoch = await setActiveAccount(offlinePartition, identity.account, version);
    offlineContext = {
      partition: offlinePartition, account: identity.account, epoch, deviceID: identity.device,
    };
    offlineAccount = identity.account;
  } catch (error) {
    // No local queue is a weaker reader, not a broken one: it posts
    // straight through the way it always did.
    console.warn("Reading changes cannot be queued locally:", error);
    offlineContext = null;
    return;
  }
  readingCoordinator = readingSync({
    context: offlineContext,
    request: (path, options) => api(path, options),
    bookID: cfg.bookID,
    onChange: async () => {
      await refreshLocalReadingState();
      await resolveAnnotationConflicts();
      await retryUnrestoredAnnotations();
    },
    onStatus: message => { if (!syncExpired) say(message, !!message); },
    onStuck: records => {
      showStuck(records);
      // The drain no longer speaks through the status line, so this is
      // what clears a transient failure the coordinator put there.
      if (!records.length && !syncExpired) say("");
    },
  });
}

// The durable state is the truth about what this device has said. It is
// re-read after every drain so an acknowledgement settles the baseline
// and a still-queued page keeps counting as local movement.
async function refreshLocalReadingState() {
  if (!offlineContext || !durableSync) return;
  const state = await readingState({ ...offlineContext, bookID: cfg.bookID }).catch(() => null);
  if (!state) return;
  const queued = await listOfflineOutbox({
    ...offlineContext, kind: "position", state: null,
  }).catch(() => []);
  const dirty = queued.some(record => record.deviceID === offlineContext.deviceID &&
    record.bookID === cfg.bookID);
  catchup.baseline(state.baseline);
  catchup.local(state.local, dirty);
  if (!dirty && !readingDirty) retryOp = null;
}

function scheduleSend() {
  if (!readingCoordinator) return;
  clearTimeout(sendTimer);
  sendTimer = setTimeout(() => {
    sendTimer = null;
    readingCoordinator?.trigger();
  }, 1500);
}

async function checkOfflineAccount() {
  if (!offlineContext || offlineInvalidated) return;
  try { await assertOfflineContext(offlineContext); }
  catch {
    offlineInvalidated = true;
    lifecycle++;
    session = null;
    offlineCheckpoint = null;
    readingDirty = false;
    workID = null;
    cancelScheduledPush();
    offlineCoordinator?.stop();
    await view?.destroy().catch(() => {});
    stage.textContent = "";
    view = null;
    offlineSnapshot = null;
    hideCatchup();
    for (const element of [titleText, chapterText, progressText, pageText]) {
      if (element) element.textContent = "";
    }
    await annotationDrawing.clear().catch(() => {});
    releaseOfflineReader?.();
    releaseOfflineReader = null;
    sessionOwner = false;
    say("Offline access ended. Close this reader and sign in again.", true);
  }
}
if (cfg.offline) {
  const channel = new BroadcastChannel("liseur-offline");
  channel.onmessage = () => checkOfflineAccount();
  window.addEventListener("offline-change", checkOfflineAccount);
  window.addEventListener("pageshow", checkOfflineAccount);
  document.addEventListener("visibilitychange", checkOfflineAccount);
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
  if (!auth.responseCurrent(resp)) return null;
  // The catalog's own digest for this book, kept so a position can name
  // the edition it was read in. Cross-checked against the digest the
  // engine actually opened rather than trusted on its own: the two can
  // differ if the file changed between the manifest request and this
  // one, and a locator filed under the wrong edition is worse than one
  // filed under none.
  catalogEditionSHA = (data.identifiers || [])
    .find(id => id && id.kind === "sha256")?.value || "";
  return data.work_id || null;
}

// editionSHA is the digest both the opened publication and the catalog
// agree on, or "" when they do not agree or either is unknown. An op
// carries it so the other client can tell whether this position is a
// place in the same bytes it has; an op without it is read as a
// position in an unnamed edition, which is what every op said before.
function editionSHA() {
  return agreedEdition((view && view.editionSHA) || "", catalogEditionSHA);
}

async function lastPosition() {
  if (!workID) return { ok: false };
  const work = workID;
  const stamp = snapshot();
  const resp = await api(
    "v1/works/" + encodeURIComponent(work) + "/positions?limit=20",
  );
  if (!resp.ok) return { ok: false };
  stamp.identity = auth.responseIdentity(resp);
  const ops = (await resp.json()).ops || [];
  if (work !== workID || !current(stamp)) return { ok: false };
  return { ok: true, op: latestReadablePosition(ops, work) };
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

const percent = (fraction) =>
  finite(fraction) ? `${Math.round(fraction * 100)}%` : null;

// The detail line carries what the question above it leaves out: the
// percentage behind a page number, and how long ago the other device
// was there. The percentage is repeated on purpose — it is the one
// number every client agrees on, and so the one to quote when two page
// counts disagree.
function placeDetail(place, name) {
  if (!place) return null;
  const said = [name, percent(place.fraction)].filter(Boolean).join(" ");
  if (!said) return null;
  const age = relativeAge(place.at);
  return age ? `${said}, ${age}` : said;
}

// A passage from a document, so it is set as a text node and nothing
// else — another device's most of all. It is capped by the presenter;
// the two-line clamp is in the CSS.
function showExcerpt(element, place) {
  if (!element) return;
  const excerpt = place?.excerpt;
  element.textContent = excerpt ? `“${excerpt}”` : "";
  element.hidden = !excerpt;
}

function showCatchup() {
  const offer = catchup.offer();
  if (!offer || !catchupPanel) return;
  const seen = placeView();
  const there = placeOf(offer.op, seen);
  // The near side is the page actually on screen, which is the one the
  // reader can check by looking down; the op is only what to fall back
  // on before the book has painted.
  const mine = placeHere(here, seen) || placeOf(offer.local, seen);
  const thereLabel = placeLabel(there);
  const mineLabel = placeLabel(mine);
  catchupPanel.classList.toggle("conflict", offer.kind === "conflict");
  if (offer.kind === "conflict") {
    // Both sides moved since they last agreed. Say what both of them
    // are; deciding which one the reader meant is not this program's
    // business.
    catchupText.textContent = mineLabel && thereLabel
      ? `This device is at ${mineLabel}; another device reached ${thereLabel}. Which one is where you are?`
      : "This device and another device are in different places in this book.";
    catchupAccept.textContent = thereLabel ? `Go to ${thereLabel}` : "Go to the other position";
    catchupDismiss.textContent = mineLabel ? `Stay at ${mineLabel}` : "Stay here";
    setDetail([placeDetail(mine, "Here"), placeDetail(there, "Another device")]);
  } else {
    catchupText.textContent = thereLabel
      ? `Continue from ${thereLabel} read on another device?`
      : "Continue from the position read on another device?";
    catchupAccept.textContent = "Continue there";
    catchupDismiss.textContent = "Stay here";
    setDetail([placeDetail(there, "")]);
  }
  showExcerpt(catchupExcerpt, there);
  catchupPanel.hidden = false;
}

function setDetail(parts) {
  if (!catchupDetail) return;
  const line = parts.filter(Boolean).join(" · ");
  catchupDetail.textContent = line;
  catchupDetail.hidden = !line;
}

const refreshes = topicRefresh({
  async refresh(topic) {
    if (!ready || document.hidden || !workID) return false;
    const run = lifecycle;
    const activity = activityGeneration;
    if (topic === "annotations") return true;
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
  refreshes.owe(["positions"]);
  live.start();
}

// Answering settles the disagreement for good: the position the reader
// did not take becomes the agreed baseline too, because it has been
// seen and answered. Without that, every reload asks again.
function rememberAnswer(op, settled) {
  if (!op || !offlineContext) return;
  agreeReadingBaseline({
    ...offlineContext, bookID: cfg.bookID, workID, baseline: op, settled,
  }).catch(() => {});
}

// Taking the other device's position withdraws this device's own
// undelivered ones. The queue drains oldest first, so a page turn still
// waiting there would land after the reader's answer and reinstate the
// position they just refused. Nothing is lost: an op the server never
// acknowledged is a claim about where the reader was, and they have
// just said otherwise. It runs under the queue's own lock so a send
// already in flight finishes before the queue is edited underneath it.
async function withdrawQueuedPositions() {
  if (!offlineContext) return;
  const withdraw = async () => {
    const queued = await listOfflineOutbox({
      ...offlineContext, kind: "position", state: null,
    });
    for (const record of queued) {
      if (record.bookID !== cfg.bookID || record.deviceID !== offlineContext.deviceID) continue;
      await removeOfflineOutbox({
        ...offlineContext, kind: "position", id: record.id, settle: false,
      });
    }
  };
  try {
    if (navigator.locks) await navigator.locks.request(outboxLock(offlineContext), withdraw);
    else await withdraw();
  } catch { /* the queue is best-effort; a stale op is not worth an error */ }
}

// Every way of saying no goes through here: an answered offer settles
// durably, or the next open raises the same disagreement again.
async function dismissCatchup() {
  const shown = catchup.shown();
  hideCatchup();
  if (await keepHere(shown ? shown.op : null) === "unsent") showCatchup();
}

// Staying put is an answer about the other device's position, not a
// refusal to answer: that position becomes the agreed baseline, and
// this device's own page is sent so the other side learns it too.
//
// The sending comes first, because the refusal is durable and the page
// it is answering with may not be. A baseline written on top of a page
// this device never managed to record would survive the reload that
// the page did not: the other position would count as answered and
// this one would be gone. So nothing is settled until the local page
// is safely in the queue — which, when it is already sitting there
// waiting to go up, it is.
//
// Sending takes as long as it takes, and the question stays usable
// while it does, so `still` is asked once more at the end: a reader who
// withdrew the question in the meantime gets no answer written on their
// behalf. Three outcomes — "kept", the answer stands; "unsent", the
// page would not go and nothing was settled, so put the question back;
// "withdrawn", the page went but the question is gone and whatever is
// on screen now belongs to somebody else.
async function keepHere(op, still) {
  if (op && view && here && finite(here.fraction)) {
    if (readingDirty) {
      // Through the single-flight, never straight at `pushPosition`: an
      // earlier page may still be uploading, and two positions in the
      // air at once can land in the wrong order and make the older one
      // this device's head — the server walked backwards.
      if (!await settleBeforeAnswer()) return "unsent";
    } else if (catchup.pending()) {
      // Already written to the durable queue, which is all this answer
      // needs; what it wants now is delivering, not writing again.
      // `pushPosition` drops the retry key as soon as it has saved, so
      // a push here would file the same spot under a second op id.
      readingCoordinator?.trigger();
    }
  }
  if (still && !still()) return "withdrawn";
  catchup.refuse(op);
  rememberAnswer(op, false);
  return "kept";
}

// An answer must not be given on top of a page this device has not
// managed to send. A failed flush leaves the local page dirty on
// purpose, so adopting the remote position now would lose it for good
// the moment retryOp and readingDirty are cleared.
async function settleBeforeAnswer() {
  cancelScheduledPush();
  await settlePosition();
  return !readingDirty;
}

// Going to the other device's position: the same journey whether the
// panel offered it or the reader asked. The answer itself has already
// been recorded by the caller; this is only the trip.
async function goThere(op, stamp, activity) {
  if (!op || !view) return;
  retryOp = null;
  readingDirty = false;
  interactionPending = false;
  // Withdraw before anything else can drain the queue: ending the
  // sitting triggers a send, and a page delivered after the answer
  // would reinstate the position the reader just refused.
  clearTimeout(sendTimer);
  sendTimer = null;
  await withdrawQueuedPositions();
  if (!current(stamp) || !view) return;
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
    rememberAnswer(op, true);
  }
}

catchupDismiss?.addEventListener("click", dismissCatchup);
catchupPanel?.addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    event.preventDefault();
    void dismissCatchup();
  }
});
catchupAccept?.addEventListener("click", async () => {
  const shown = catchup.shown();
  const activity = activityGeneration;
  hideCatchup();
  if (!shown || !view) return;
  if (!await settleBeforeAnswer()) return;
  const stamp = snapshot();
  await goThere(current(stamp) ? catchup.accept(shown) : null, stamp, activity);
});

// ------------------------------------------- syncing on request

// The passive panel above appears when this reader happens to notice a
// disagreement. This is the other half (ADR-0040): the reader asks, and
// gets an answer even when the answer is "you are in step" — that is
// worth knowing, and a button that silently does nothing is not.
const syncButton = document.getElementById("reader-sync");
const syncDialog = document.getElementById("reader-sync-dialog");
const syncSummary = document.getElementById("reader-sync-summary");
const syncHereText = document.getElementById("reader-sync-here");
const syncHereExcerpt = document.getElementById("reader-sync-here-excerpt");
const syncThereText = document.getElementById("reader-sync-there");
const syncThereSide = document.getElementById("reader-sync-there-side");
const syncExcerpt = document.getElementById("reader-sync-excerpt");
const syncTake = document.getElementById("reader-sync-take");
const syncKeep = document.getElementById("reader-sync-keep");
const syncCancel = document.getElementById("reader-sync-cancel");
// The position the open dialog is asking about. Cleared when it closes,
// so an answer can never be given about a question no longer on screen.
let syncOffered = null;
let syncAsking = false;
// Whether the catch-up panel was up when this dialog opened. Cancelling
// changes nothing, and a disagreement that was already on screen and
// still unanswered is part of the nothing that must not change.
let syncCovered = false;

// This device's side, as an op, so the decision is made on the same two
// shapes everywhere: the same fields, the same anchor comparison the
// reconciler uses. It is never a position of zero when there is none —
// that would invent a side.
function positionHere() {
  if (!view || !here || !finite(here.fraction)) return null;
  return { progression: here.fraction, locator: locatorFor(here) || {} };
}

// Two of the summaries below promise this page is on its way up. The
// reader asked for that now, which is the whole point of the button, so
// it is sent rather than left to a retry timer that may be half a
// minute away. The op carries its own retry key, so a page already
// queued is delivered rather than written a second time.
function sendMineNow() {
  if (!view || !here || !finite(here.fraction) || restoring) return;
  // A page already written to the queue wants delivering, not writing
  // again. The durable path drops the retry key the moment it has
  // saved, so a second push here would mint a second op id for the
  // same spot and file the reader's place twice for pressing twice.
  if (!readingDirty && catchup.pending()) {
    readingCoordinator?.trigger();
    return;
  }
  readingDirty = true;
  void push().catch(() => {}).then(() => { readingCoordinator?.trigger(); });
}

const SYNC_SUMMARIES = {
  "no-remote": "No other device has a position for this book yet. This page is being sent, and will be there when one asks.",
  "no-position": "Nothing has been read in this book yet, here or anywhere else. Your place will sync as soon as you have one.",
  "in-step": "Both devices are in the same place. Nothing to do.",
  "no-local": "Only your other device has read this book so far.",
  "owed": "The server has an older copy of this device's own position. Nothing else has read this book, so this page is simply on its way up.",
  "unreadable": "The position from your other device cannot be opened in this copy of the book. Keeping this page sends it instead.",
  "ahead": "Your other device has read further than this one.",
  "behind": "This device has read further than your other one.",
  "same-page": "The same page, but not the same spot. The passage below is what the other device had on screen.",
  "near-page": "Within a whisker of each other, but not the same spot. The passage below is what the other device had on screen.",
};

// `same-page` from the decision means only that the two progressions
// are within rounding of each other, and in a long book that is not
// enough to claim a page: a book with a thousand positions has several
// of them inside the same rounding. The numbers actually on screen are
// what settle it, and where there are none it says only what it knows.
function relationWording(decision, mine, there) {
  const key = decision.relation || decision.verdict;
  if (key !== "same-page") return key;
  const here = mine?.page, other = there?.page;
  if (!mine?.exact || !there?.exact) return "near-page";
  return here && other && here === other ? "same-page" : "near-page";
}

async function askBookSync() {
  if (!syncDialog || syncAsking) return;
  syncAsking = true;
  syncButton?.setAttribute("aria-busy", "true");
  try {
    syncCovered = !!catchupPanel && !catchupPanel.hidden;
    hideCatchup();
    // The offline reader has no server to ask: its positions are
    // queued on this device and go up when it is next online. Saying
    // so is the whole of the answer, and nudging the queue is the
    // whole of the action.
    if (cfg.offline) {
      offlineCoordinator?.trigger();
      presentBookSync(null, "This copy is offline. Your reading is saved here and syncs when this device is next online.");
      return;
    }
    const stamp = snapshot();
    // A server that cannot be reached at all rejects rather than
    // answering, and a button that turns an unreachable server into an
    // unhandled rejection and no dialog is the silence this exists to
    // end. Both failures mean the same thing to a reader.
    const result = ready && workID
      ? await lastPosition().catch(() => ({ ok: false }))
      : null;
    if (!current(stamp)) return;
    if (!result) {
      presentBookSync(null, "This book is not syncing on this device.");
      return;
    }
    if (!result.ok) {
      presentBookSync(null, "The server could not be reached. Your reading is safe here and will be sent when it can.");
      return;
    }
    catchup.observe(result.op);
    presentBookSync(result.op, null);
  } finally {
    syncAsking = false;
    syncButton?.removeAttribute("aria-busy");
  }
}

// Nothing has been applied by the time this runs: the server's answer
// has been read, and neither side has been changed.
function presentBookSync(remote, note) {
  const seen = placeView();
  const mine = placeHere(here, seen);
  const decision = note
    ? { verdict: "note" }
    : decideBookSync({
      local: positionHere(),
      remote,
      baseline: catchup.agreed(),
      // `readingDirty` goes false the moment the page is written to the
      // durable queue, which is before anyone has delivered it. Asking
      // the queue instead is what keeps a page still waiting there from
      // being reported as in step with a server that has never seen it.
      localDirty: readingDirty || catchup.pending(),
      resolvable: startCandidates(remote).length > 0,
    });
  // Taking the other side and keeping this one are two questions, and
  // only a real choice asks both. With no page here there is nothing to
  // keep: a Keep button would put the other device's position away
  // without publishing anything in its place, leaving the reader with
  // neither.
  const takeable = decision.verdict === "ask" || decision.verdict === "no-local";
  const keepable = decision.verdict === "ask" || decision.verdict === "unreadable";
  if (decision.verdict === "no-remote" || decision.verdict === "owed") sendMineNow();
  // A second side is shown when there is one to compare with. This
  // reader's own position sitting on the server is not another device,
  // and putting it under that heading would say something untrue.
  const other = takeable || keepable ? remote : null;
  const there = placeOf(other, seen);
  syncOffered = other;

  syncSummary.textContent = note ||
    SYNC_SUMMARIES[relationWording(decision, mine, there)] || "";
  syncHereText.textContent = placeLabel(mine) || "Not known yet";
  syncThereText.textContent = placeSentence(there) || "";
  syncThereSide.hidden = !other;
  showExcerpt(syncHereExcerpt, mine);
  showExcerpt(syncExcerpt, there);

  syncTake.hidden = !takeable;
  syncKeep.hidden = !keepable;
  // With nothing to choose between, the only button left is the way
  // out, and calling it "Cancel" would suggest something was pending.
  syncCancel.textContent = syncTake.hidden && syncKeep.hidden ? "Close" : "Cancel";
  if (!syncDialog.open) syncDialog.showModal();
}

syncButton?.addEventListener("click", () => void askBookSync());
syncCancel?.addEventListener("click", () => syncDialog.close());
// Cancelling is a real answer — "not this way" — and changes nothing at
// all, neither the page, nor what the two sides have agreed on, nor a
// question that was already waiting. An answer below clears the flag
// first, because answering the dialog answers the panel with it.
syncDialog?.addEventListener("close", () => {
  syncOffered = null;
  if (syncCovered) showCatchup();
  syncCovered = false;
});

syncTake?.addEventListener("click", async () => {
  const op = syncOffered;
  const activity = activityGeneration;
  if (!op || !view) {
    syncCovered = false;
    syncDialog.close();
    return;
  }
  // The page this device has not managed to send goes up before the
  // other one is adopted, or it is lost. If it will not go, the
  // question stays open and says so: closing on a failure would take
  // away the answer and the chance to give it again in one move.
  if (!await settleBeforeAnswer()) {
    syncSummary.textContent =
      "This page could not be sent, so the other position was not taken. Try again in a moment.";
    return;
  }
  // Sending can take a moment, and Cancel and Escape both keep working
  // while it does. A reader who took the question away in the meantime
  // has answered it: the page went up, which is never wrong, and
  // nothing else here happens.
  if (syncOffered !== op) return;
  syncCovered = false;
  syncDialog.close();
  // The panel may be up behind the dialog, asking about this very
  // position. Answering here answers it.
  hideCatchup();
  const stamp = snapshot();
  if (!current(stamp)) return;
  await goThere(catchup.adopt(op), stamp, activity);
});

syncKeep?.addEventListener("click", async () => {
  const op = syncOffered;
  // Same rule as Take: an answer is only given once the page it is
  // given from has been recorded, and only if the question is still
  // being asked by the time it has been.
  const answer = await keepHere(op, () => syncOffered === op);
  if (answer === "withdrawn" || syncOffered !== op) return;
  if (answer === "unsent") {
    syncSummary.textContent =
      "This page could not be sent, so nothing was settled. Try again in a moment.";
    return;
  }
  syncCovered = false;
  syncDialog.close();
  hideCatchup();
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
const ANNOTATION_MAX_EXCERPT_BYTES = 1 << 10;
const ANNOTATION_MAX_BODY_BYTES = 16 << 10;

const annotationDrawing = annotationRenderer({
  getView: () => view,
  current,
  changed: buildAnnotationList,
});
const annotationActions = document.getElementById("reader-annotation-actions");
const annotationColor = document.getElementById("reader-annotation-color");
const addNoteButton = document.getElementById("reader-add-note");
let selectedText = null;

function annotationStamp() {
  return snapshot();
}

async function replaceAnnotations(next) {
  return annotationDrawing.replace(next, annotationStamp());
}

function annotationPayload(annotation, baseRev = annotation.rev || 0) {
  const payload = {
    id: annotation.id,
    base_rev: baseRev,
    work_id: annotation.work_id,
    kind: annotation.kind,
    progression: annotation.progression,
    excerpt: annotation.excerpt || "",
    color: annotation.color || "",
    body: annotation.body || "",
    client_ts: annotation.client_ts || new Date().toISOString(),
  };
  if (annotation.locator) payload.locator = annotation.locator;
  return payload;
}

function annotationValidationError(annotation) {
  const bytes = value => new TextEncoder().encode(value || "").byteLength;
  if (annotation.kind === "note" && !annotation.body?.trim())
    return "A note requires a body.";
  if (bytes(annotation.excerpt) > ANNOTATION_MAX_EXCERPT_BYTES)
    return "That selection is too long to save as an annotation.";
  if (bytes(annotation.body) > ANNOTATION_MAX_BODY_BYTES)
    return "That note is too long to save.";
  if (annotation.kind === "bookmark" && annotation.body)
    return "A bookmark cannot have a note.";
  return "";
}

async function applyAnnotationResult(annotation, result) {
  if (result.status === "conflict" && result.server) {
    await handleAnnotationConflict(annotation, result.server);
    return false;
  }
  if (!["applied", "duplicate"].includes(result.status)) {
    say(result.reason || "The annotation could not be saved.", true);
    return false;
  }
  annotation.rev = result.rev || annotation.rev || 0;
  annotation.seq = result.seq || annotation.seq || 0;
  annotation.pending = false;
  await replaceAnnotations(annotationDrawing.annotations());
  return true;
}

async function writeAnnotation(annotation, baseRev = annotation.rev || 0) {
  const validationError = annotationValidationError(annotation);
  if (validationError) {
    say(validationError, true);
    return false;
  }
  const payload = annotationPayload(annotation, baseRev);
  if (cfg.offline) {
    annotation.pending = true;
    await queueOfflineAnnotation({
      ...offlineContext,
      partition: offlinePartition,
      account: offlineAccount,
      bookID: cfg.bookID,
      annotation,
      payload: { method: "write", annotation: payload },
    });
    notifyOfflineChange();
    await replaceAnnotations(await listOfflineAnnotations({ ...offlineContext, bookID: cfg.bookID }));
    return true;
  }
  const response = await api("v1/annotations", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ annotations: [payload] }),
  });
  if (!response.ok) {
    say("The annotation could not be saved.", true);
    return false;
  }
  const body = await response.json().catch(() => null);
  const result = body?.results?.[0];
  if (!result) {
    say("The annotation response was incomplete.", true);
    return false;
  }
  return applyAnnotationResult(annotation, result);
}

async function deleteAnnotation(annotation) {
  if (!window.confirm("Delete this annotation?")) return;
  if (cfg.offline) {
    const tombstone = { ...annotation, deleted: true, pending: true };
    await queueOfflineAnnotation({
      ...offlineContext,
      partition: offlinePartition,
      account: offlineAccount,
      bookID: cfg.bookID,
      annotation: tombstone,
      payload: { method: "delete", id: annotation.id, rev: annotation.rev || 0 },
    });
    notifyOfflineChange();
    await replaceAnnotations(await listOfflineAnnotations({ ...offlineContext, bookID: cfg.bookID }));
    return;
  }
  const response = await api(
    "v1/annotations/" + encodeURIComponent(annotation.id) +
      "?rev=" + encodeURIComponent(annotation.rev || 0),
    { method: "DELETE" },
  );
  if (response.status === 409) {
    const body = await response.json().catch(() => null);
    if (body?.server) await handleAnnotationConflict(
      { ...annotation, deleted: true, pending: true },
      body.server,
    );
    return;
  }
  if (!response.ok) {
    say("The annotation could not be deleted.", true);
    return;
  }
  await replaceAnnotations(annotationDrawing.annotations().map(value =>
    value.id === annotation.id ? { ...value, deleted: true } : value));
}

async function handleAnnotationConflict(local, server, record = null) {
  const keepLocal = window.confirm(
    "This annotation changed elsewhere. Choose OK to keep your local version, or Cancel to use the server version.",
  );
  if (keepLocal) {
    local.rev = server.rev;
    local.pending = true;
    if (record) {
      await queueOfflineAnnotation({
        ...offlineContext,
        partition: storagePartition(),
        account: offlineAccount,
        bookID: cfg.bookID,
        annotation: local,
        payload: local.deleted
          ? { method: "delete", id: local.id, rev: server.rev }
          : { method: "write", annotation: annotationPayload(local, server.rev) },
      });
      notifyOfflineChange();
    } else if (local.deleted) {
      const response = await api(
        "v1/annotations/" + encodeURIComponent(local.id) +
          "?rev=" + encodeURIComponent(server.rev),
        { method: "DELETE" },
      );
      if (!response.ok) {
        say("The annotation could not be deleted.", true);
        return;
      }
    } else {
      await writeAnnotation(local, server.rev);
    }
    await replaceAnnotations([
      ...annotationDrawing.annotations().filter(value => value.id !== local.id),
      local,
    ]);
    return;
  }
  if (record) {
    await removeOfflineOutbox({
      ...offlineContext,
      partition: storagePartition(),
      account: offlineAccount,
      kind: record.kind,
      id: record.id,
    });
    if (server.deleted) {
      await removeOfflineAnnotation({
        ...offlineContext,
        partition: storagePartition(),
        account: offlineAccount,
        bookID: cfg.bookID,
        id: server.id,
        removeAnnotation: false,
      });
    } else {
      await updateOfflineAnnotation({
        ...offlineContext,
        partition: storagePartition(),
        account: offlineAccount,
        bookID: cfg.bookID,
        annotation: server,
      });
    }
  }
  await replaceAnnotations(annotationDrawing.annotations().map(value =>
    value.id === local.id ? server : value));
}

// A conflicted annotation is a disagreement about the reader's own
// words, and the page holding the book is where it is settled. That is
// true of an online reader too: it drains the same queue and raises the
// same conflicts, and leaving them for the installed app would strand
// them on a device that may never open this book again.
//
// It is the only answer an annotation gets. The stuck panel deliberately
// does not offer one, because discarding an annotation's outbox row
// would leave the annotation itself marked unsaved with nothing left to
// save it; both answers here settle the annotation store as well.
async function resolveStoredAnnotationConflicts() {
  if (!cfg.offline && !offlineContext) return;
  if (!offlineAccount) return;
  const conflicts = await listOfflineOutbox({
    partition: storagePartition(),
    account: offlineAccount,
    kind: "annotation",
    state: "conflict",
  });
  if (!conflicts.length) return;
  const local = await listOfflineAnnotations({
    partition: storagePartition(),
    account: offlineAccount,
    bookID: cfg.bookID,
  });
  for (const record of conflicts) {
    if (record.bookID !== cfg.bookID || !record.details) continue;
    const annotation = local.find(value => value.id === record.annotationID);
    if (annotation) await handleAnnotationConflict(annotation, record.details, record);
  }
}

// Every drain ends by asking whether it raised an annotation conflict,
// and two drains can overlap. One at a time, then: `window.confirm` is
// modal, and a second run would queue a dialog about an annotation the
// first run is already settling.
let resolvingConflicts = null;
function resolveAnnotationConflicts() {
  if (resolvingConflicts) return resolvingConflicts;
  resolvingConflicts = resolveStoredAnnotationConflicts()
    .catch(error => { console.warn("An annotation conflict is unresolved:", error); })
    .finally(() => { resolvingConflicts = null; });
  return resolvingConflicts;
}

function selectionFromDocument(doc) {
  const selection = doc.getSelection?.();
  if (!selection || !selection.rangeCount || selection.isCollapsed) return null;
  const range = selection.getRangeAt(0);
  const text = selection.toString().trim();
  if (!text || !view?.getCFIForRange) return null;
  return {
    cfi: view.getCFIForRange(doc, range),
    href: doc.documentElement.dataset.readerHref,
    text: text.slice(0, 4096),
    progression: finite(here?.fraction) ? here.fraction : null,
  };
}

function showAnnotationActions() {
  if (annotationActions) annotationActions.hidden = !selectedText;
}

function clearSelectedText() {
  selectedText = null;
  showAnnotationActions();
}

function annotationFromSelection(kind, body = "", color = "yellow") {
  const selection = selectedText;
  if (!selection || !workID) return null;
  const attachedKind = kind === "note" ? "highlight" : kind;
  return {
    id: opID(),
    rev: 0,
    work_id: workID,
    kind: attachedKind,
    locator: {
      href: selection.href,
      type: "application/xhtml+xml",
      locations: { fragments: [selection.cfi] },
      text: { highlight: selection.text },
    },
    progression: selection.progression,
    excerpt: selection.text,
    color: attachedKind === "highlight" && ANNOTATION_COLORS[color] ? color : "",
    body,
    client_ts: new Date().toISOString(),
  };
}

async function createSelectionAnnotation(action) {
  if (!selectedText) return;
  let body = "";
  if (action === "note") {
    body = window.prompt("Note for this passage:", "");
    if (body === null || !body.trim()) return;
  }
  const annotation = annotationFromSelection(
    action,
    body.trim(),
    annotationColor?.value || "yellow",
  );
  if (!annotation) return;
  const validationError = annotationValidationError(annotation);
  if (validationError) {
    say(validationError, true);
    return;
  }
  clearSelectedText();
  await replaceAnnotations([...annotationDrawing.annotations(), annotation]);
  await writeAnnotation(annotation);
}

async function createStandaloneNote() {
  if (!workID) return;
  const body = window.prompt("Note about this place in the book:", "");
  if (body === null || !body.trim()) return;
  const annotation = {
    id: opID(),
    rev: 0,
    work_id: workID,
    kind: "note",
    progression: finite(here?.fraction) ? here.fraction : null,
    body: body.trim(),
    client_ts: new Date().toISOString(),
  };
  const validationError = annotationValidationError(annotation);
  if (validationError) {
    say(validationError, true);
    return;
  }
  await replaceAnnotations([...annotationDrawing.annotations(), annotation]);
  await writeAnnotation(annotation);
}

async function editAnnotation(annotation) {
  const nextBody = window.prompt(
    annotation.kind === "highlight" ? "Edit attached note:" : "Edit note:",
    annotation.body || "",
  );
  if (nextBody === null) return;
  let color = annotation.color;
  if (annotation.kind === "highlight") {
    color = window.prompt(
      "Highlight color (yellow, green, blue, pink, purple or orange):",
      annotation.color || "yellow",
    );
    if (color === null) return;
    color = color.trim().toLowerCase();
    if (!ANNOTATION_COLORS[color]) {
      say("Choose one of the available highlight colors.", true);
      return;
    }
  }
  const next = { ...annotation, body: nextBody, color, client_ts: new Date().toISOString() };
  const validationError = annotationValidationError(next);
  if (validationError) {
    say(validationError, true);
    return;
  }
  await replaceAnnotations(annotationDrawing.annotations().map(value =>
    value.id === annotation.id ? next : value));
  await writeAnnotation(next, annotation.rev || 0);
}

function wireSelection(doc) {
  const update = () => {
    const next = selectionFromDocument(doc);
    if (next) {
      selectedText = next;
      showAnnotationActions();
    } else if (!annotationActions?.matches(":hover")) {
      clearSelectedText();
    }
  };
  doc.addEventListener("selectionchange", update);
  doc.addEventListener("mouseup", () => setTimeout(update, 0));
  doc.addEventListener("touchend", () => setTimeout(update, 0), { passive: true });
}

annotationActions?.addEventListener("click", event => {
  const action = event.target?.dataset?.annotationAction;
  if (action === "cancel") {
    clearSelectedText();
    return;
  }
  if (["highlight", "note", "bookmark"].includes(action))
    createSelectionAnnotation(action).catch(error =>
      say(error.message || "The annotation could not be created.", true));
});
addNoteButton?.addEventListener("click", () => {
  createStandaloneNote().catch(error =>
    say(error.message || "The note could not be created.", true));
});

// buildAnnotationList fills the sidebar with the entries that do not
// draw over the text. Excerpts and bodies are set as text nodes only.
function buildAnnotationList() {
  if (!annPanel || !annList) return;
  annList.textContent = "";
  const listed = annotationDrawing.annotations().filter(
    (a) => !a.deleted && (cfg.offline ||
      a.kind !== "highlight" || !annotationAnchor(a) || annotationDrawing.failed(a.id)),
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
    kind.textContent = a.kind + (a.pending ? " · pending" : "");
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
    const actions = document.createElement("span");
    actions.className = "reader-ann-actions";
    if (a.kind !== "bookmark") {
      const edit = document.createElement("button");
      edit.type = "button";
      edit.textContent = "Edit";
      edit.addEventListener("click", () => editAnnotation(a));
      actions.append(edit);
    }
    const remove = document.createElement("button");
    remove.type = "button";
    remove.textContent = "Delete";
    remove.addEventListener("click", () => deleteAnnotation(a));
    actions.append(remove);
    li.append(actions);
    ul.append(li);
  }
  annList.append(ul);
}

// loadAnnotations is best-effort like every other sync call: a reader
// on a server without the routes, or offline, still reads the book.
async function loadAnnotations() {
  if (!workID || !view) return false;
  if (cfg.offline) {
    await assertOfflineContext(offlineContext);
    const local = await listOfflineAnnotations({
      partition: offlinePartition,
      account: offlineAccount,
      bookID: cfg.bookID,
    });
    await replaceAnnotations(local);
    await resolveStoredAnnotationConflicts();
    return true;
  }
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
  await resolveStoredAnnotationConflicts();
  return current(stamp);
}

// bookTitle is read from the package document rather than from the
// catalog, so the page says what the file says even when the two have
// drifted. Readium keeps a title either as a string or as a
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
// `location` here is what the Readium adapter hands the relocate event: a total
// fraction, the section index, and a CFI. The engine estimates the
// fraction from section sizes it already knows, so there is no separate
// "generate locations" pass and no moment when progress is unknown.
//
// A non-finite fraction yields null: there is no position to push, and
// the caller asks the engine to remeasure rather than record a wrong one.
function locatorFor(location) {
  if (location.locator && finite(location.fraction)) return withAnchor(location.locator);
  if (!finite(location.fraction)) return null;
  const section = location.section || {};
  const index = typeof section.current === "number" ? section.current : 0;
  const sections = (view.book && view.book.sections) || [];
  return withAnchor({
    href: (sections[index] && sections[index].id) || "",
    type: "application/xhtml+xml",
    title: bookTitle(),
    locations: {
      fragments: location.cfi ? [location.cfi] : [],
      progression: sectionProgression(location),
      totalProgression: location.fraction,
      position: index + 1,
    },
  });
}

// withAnchor describes the passage on screen well enough for another
// client to find it again, when it can. A CFI says the same thing but
// only to a reader that speaks CFI; the app on a phone does not, and a
// percentage lands it in the wrong paragraph. When there is no anchor
// to be had — nothing visible yet, a quote that appears twice in its
// block, a locator that would grow past what the server accepts — the
// locator goes as it was, and the resource and its progression are
// still a good place to reopen at.
function withAnchor(locator) {
  try {
    return markLocator(locator, view.visibleAnchor(), locatorLimitBytes);
  } catch (err) {
    return locator;
  }
}

// sectionProgression recovers the fraction within the current section
// from the total fraction and the section boundaries, because Readium's
// `progression` is within-resource and the Readium adapter reports the total.
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

// push records where we are. Transient failures retain the operation for
// the next foreground retry, reconnect or page turn.
//
// The op log is append-only and idempotent by op id, so a retry of the
// same position must replay the same op — the whole op, byte for byte,
// because a fresh client_ts under an old id is a different payload and
// the server rightly calls that a conflict. The op is therefore built
// once and kept until the server confirms it holds it; a different
// position is a different op and gets a new id. The server answers 200
// with a status per op: "applied" and "duplicate" both mean the log
// has it. A refusal is never an acknowledgement.
let retryOp = null;
let positionInFlight = null;
let positionAgain = false;
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
  if (positionInFlight) {
    positionAgain = true;
    return positionInFlight;
  }
  const activity = activityGeneration;
  positionInFlight = pushPosition().finally(() => {
    positionInFlight = null;
    const again = positionAgain;
    positionAgain = false;
    if (again && readingDirty) push();
    else if (activity !== activityGeneration && readingDirty) schedulePush();
  });
  return positionInFlight;
}

// recoverRefusal answers the one refusal a position can recover from
// by itself.
//
// A batch refused for the size of its locator stored nothing, so the
// op id is still free — and `docs/integrating.md` names resending the
// same op without its locator as the recovery. That is the protocol's
// deliberate exception to replaying the same bytes: everything else
// about the op, its id included, stays as it was, so the reading is
// recorded once and under the identity it was first given. The
// progression alone still says where the reader is.
async function recoverRefusal(resp, op) {
  const body = await resp.json().catch(() => null);
  if (body?.code !== "locator_too_large") return;
  if (Number.isFinite(body.limit) && body.limit > 0) locatorLimitBytes = body.limit;
  if (!op.locator || retryOp?.op !== op) return;
  const { locator: _dropped, ...bare } = op;
  // Left in retryOp under the same key, so the next push — a page turn,
  // a foreground retry, the flush on the way out — sends this reduced
  // copy rather than building the refused one again. A reader who has
  // since moved on gets a different key and a new op, which is right:
  // the newer page is the one worth recording.
  retryOp = { key: retryOp.key, op: bare };
}

async function pushPosition() {
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
  const edition = editionSHA();
  // The retry key is what makes a repeat of the same position replay the
  // same bytes rather than mint a new op. The edition belongs in it: the
  // same chapter fraction in a re-uploaded file is a different place,
  // and reusing the old op for it would file the new reading under the
  // old bytes.
  const key =
    (locator.locations.fragments[0] || "") +
    "@" +
    locator.locations.totalProgression +
    "@" +
    edition;
  const op =
    retryOp && retryOp.key === key
      ? retryOp.op
      : {
          op_id: opID(),
          work_id: workID,
          client_ts: new Date().toISOString(),
          progression: locator.locations.totalProgression,
          locator: locator,
          // Named only when it is known and agreed. An absent field is
          // how every op read before this, and the other client treats
          // it as an edition nobody vouched for.
          ...(edition ? { edition_sha: edition } : {}),
        };
  retryOp = { key, op };
  catchup.wrote(op);
  if (cfg.offline || (durableSync && offlineContext)) {
    try {
      await saveReadingPosition({
        ...offlineContext,
        bookID: cfg.bookID,
        workID,
        op,
        requireSnapshot: cfg.offline,
      });
      catchup.local(op);
      if (cfg.offline) notifyOfflineChange();
      else if (leaving) {
        // The queue is the guarantee, but an unload is the last moment
        // this page can speak and another device is probably waiting.
        // The queued copy carries the same bytes under the same op id,
        // so a later delivery is a duplicate rather than a second page.
        api("v1/ops", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          keepalive: true,
          body: JSON.stringify({ ops: [op] }),
        }).catch(() => {});
      } else scheduleSend();
      if (retryOp?.op === op) {
        retryOp = null;
        if (locatorFor(here)?.locations.fragments[0] === locator.locations.fragments[0] &&
            here.fraction === locator.locations.totalProgression) readingDirty = false;
      }
    } catch (err) {
      // An expired local account cannot be queued into. The reader
      // keeps reading; the credential layer is what says so.
      if (!cfg.offline && err?.code === "auth") {
        offlineContext = null;
        readingCoordinator?.stop();
        readingCoordinator = null;
        releaseOfflineReader?.();
        releaseOfflineReader = null;
        sessionOwner = false;
        return;
      }
      say(err.message || "This reading position could not be saved on this device.", true);
    }
    return;
  }
  const request = api("v1/ops", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    // keepalive lets the final flush outlive the page: without it a
    // position pushed from beforeunload is cancelled mid-flight.
    keepalive: true,
    body: JSON.stringify({ ops: [op] }),
  });
  try {
    const resp = await request;
    stamp.identity = auth.responseIdentity(resp) || stamp.identity;
    if (!resp.ok) {
      if (current(stamp) && auth.responseCurrent(resp)) await recoverRefusal(resp, op);
      return;
    }
    if (!current(stamp) || !auth.responseCurrent(resp)) return;
    const out = await resp.json().catch(() => null);
    if (!current(stamp) || !auth.responseCurrent(resp)) return;
    const result = out?.results?.[0];
    if (result?.op_id === op.op_id && result.status === "conflict") {
      if (retryOp?.op === op) retryOp = null;
      say("This position changed while it was syncing. Retrying with a new operation.", true);
      return;
    }
    if (!positionAcknowledged(out, op)) return;
    catchup.settled(op);
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
  // Local durability need not wait for the network debounce.
  pending = setTimeout(push, cfg.offline || offlineContext ? 0 : 1500);
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
  if (session || !workID || document.hidden || offlineInvalidated) return;
  if (!here || !finite(here.fraction)) return;
  session = openSession({
    id: opID(),
    workID: workID,
    startedAt: new Date(),
    now: performance.now(),
    fraction: here.fraction,
    supportsActiveMs: cfg.offline
      ? offlineSnapshot?.supportsActiveMs === true
      : auth.identity()?.supportsActiveMs === true,
    checkpoint: cfg.offline || sessionOwner ? offlineCheckpoint : null,
  });
  offlineCheckpoint = null;
  checkpointSession();
}

// A sitting is checkpointed as it goes, so a browser that is killed
// mid-page still leaves the reading behind: the next open finds the
// checkpoint and carries on inside the same sitting instead of
// discarding the minutes before the crash. Only the page holding the
// book's reader claim writes one, because a checkpoint names one
// sitting and two pages finalizing under that name is a refused
// session, not a longer one.
function checkpointSession() {
  if (!session || !offlineContext || !(cfg.offline || sessionOwner)) return;
  const checkpoint = session.checkpoint();
  if (!checkpoint) return;
  saveOfflineSessionCheckpoint({
    ...offlineContext,
    bookID: cfg.bookID,
    checkpoint,
  }).catch(error => say(
    error.message || "This reading progress could not be saved on this device.",
    true,
  ));
}

function noteActivity() {
  activityGeneration++;
  if (document.hidden || restoring) return;
  interactionPending = true;
  if (session) {
    session.activity(performance.now(), here && here.fraction);
    checkpointSession();
  } else {
    beginSession();
  }
  if (unsent.length) pushSession();
}

function noteNavigation() {
  if (!view || restoring || document.hidden || offlineInvalidated) return false;
  noteActivity();
  catchup.moved();
  hideCatchup();
  return true;
}

// noteProgress follows a relocate. It is not activity: the engine
// relocates on a resize or a font change too, and only a key, a tap or
// a scroll says somebody was there.
function noteProgress() {
  if (session) {
    session.progress(here && here.fraction);
    checkpointSession();
  }
}

// True once the page is going away. Only then does a finished sitting go
// out directly as well as durably: an unload is the last moment another
// device could hear about this sitting promptly. On any other path the
// queue is both the guarantee and prompt enough, and posting the same
// sitting twice at once races itself at the server.
let leaving = false;

async function endSession() {
  if (!session) return;
  const sessionID = session.checkpoint()?.id;
  const payload = session.close(
    performance.now(),
    new Date(),
    here && here.fraction,
  );
  session = null;
  if (cfg.offline || offlineContext) {
    try {
      if (payload) {
        await finishOfflineSession({
          ...offlineContext,
          bookID: cfg.bookID,
          payload,
        });
        // Only now may the page speak directly. The durable write is
        // what spends the session id: it removes the checkpoint, so no
        // later page can resume this sitting and close it again with a
        // different ending. Sending first and queueing afterwards lost
        // that race on an unload — the request went, the IndexedDB
        // write did not finish, and the next open finalized the same
        // id with different bytes, which the server refuses forever as
        // a reused id.
        //
        // An unload that kills the page before this point sent nothing
        // either, so the checkpoint that survives is still true.
        if (!cfg.offline && leaving) {
          unsent = [payload];
          pushSession(true);
        }
      } else {
        await clearOfflineSessionCheckpoint({
          ...offlineContext,
          sessionID,
          bookID: cfg.bookID,
        });
      }
      if (cfg.offline) notifyOfflineChange();
      else readingCoordinator?.trigger();
    } catch (error) {
      say(error.message || "This reading session could not be saved on this device.", true);
    }
    return;
  }
  if (payload) unsent.push(payload);
  pushSession(true);
}

let sessionsInFlight = 0;

// pushSession sends every unconfirmed sitting in one batch. A retry
// prodded by activity waits for the request already out; a close does
// not, because the page may not be here when that one comes back, and
// the server holds the same payload twice as once.
async function pushSession(closing) {
  if (!unsent.length || (sessionsInFlight && !closing)) return;
  const batch = unsent.slice();
  const stamp = snapshot();
  sessionsInFlight++;
  try {
    await uploadSessions(batch, {
      canSend: () => current(stamp),
      send: async (body) => {
        const resp = await api("v1/sessions", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          keepalive: true,
          body,
        });
        stamp.identity = auth.responseIdentity(resp) || stamp.identity;
        return resp;
      },
      responseCurrent: (resp) => current(stamp) && auth.responseCurrent(resp),
      accepted: async (sent) => {
        unsent = unsent.filter((p) => !sent.includes(p));
        // The queued copy is what a later drain would replay. Settling
        // it here keeps the same sitting from going out twice.
        if (offlineContext) {
          for (const item of sent) {
            await removeOfflineOutbox({
              ...offlineContext, kind: "session", id: item.session_id,
            }).catch(() => {});
          }
        }
      },
      deferred: (status, code) => console.warn("Reading sessions are waiting to sync:", status, code),
      refused: (item, code) => {
        unsent = unsent.filter((p) => p !== item);
        console.warn("Reading session refused:", code);
        say("A reading session could not be saved; other reading will still sync.", true);
      },
    });
  } catch (err) {
    // The next activity or close retries the unchanged pending sittings.
    if (!err.terminal) console.warn("Reading sessions are waiting to sync:", err);
  } finally {
    sessionsInFlight--;
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
  push();
  pushSession();
  readingCoordinator?.trigger();
  if (ready) startLive();
  beginSession();
  if (here && !finite(here.fraction)) {
    fractionRetryAttempt = 0;
    scheduleFractionRetry();
  }
});
window.addEventListener("pageshow", () => { leaving = false; });
window.addEventListener("pagehide", () => {
  lifecycle++;
  leaving = true;
  live.stop();
  refreshes.stop();
  catchup.hide();
  hideCatchup();
  cancelScheduledPush();
  push();
  endSession();
});
window.addEventListener("online", () => {
  if (document.hidden) return;
  push();
  pushSession();
  readingCoordinator?.trigger();
  if (ready) startLive();
});
setInterval(() => {
  if (document.hidden || navigator.onLine === false) return;
  push();
  pushSession();
  readingCoordinator?.trigger();
}, 30000);

// The ladder itself is in reader-restore.js, which knows nothing about
// this page. All that is decided here is what the open publication can
// answer: which edition it is, what its spine is called, and whether a
// CFI names a chapter in it.
function restoreView() {
  return {
    editionSHA: editionSHA(),
    sections: (view.book && view.book.sections) || [],
    resolveKey: (key) => view.resources?.resolveKey(key) ?? null,
    cfiResolves: (cfi) => {
      try {
        const resolved = view.resolveNavigation(cfi);
        return (
          resolved != null &&
          typeof resolved.index === "number" &&
          !!view.book.sections[resolved.index]
        );
      } catch (err) {
        return false;
      }
    },
  };
}

function startCandidates(op) {
  return op ? restoreCandidates(op, restoreView()) : [];
}

// What the presenter needs to turn an op into a page a reader can
// check: the same facts the restore ladder stands on, plus the position
// table and this page's own anchor. Before the book has opened there is
// only the table, which is enough for a percentage and honest about the
// rest.
function placeView() {
  if (!view) return { table: positions };
  return { ...restoreView(), table: positions, anchor: view.visibleAnchor() };
}

// ------------------------------------------------------ appearance

// Reader appearance, Komga-style: theme, font, size, spacing, layout.
// All of it is a browser preference — stored in localStorage, applied
// through the engine's user stylesheet and layout attributes, never
// sent to the server. Typography defaults to "what the publisher said":
// a fresh reader sets the book in the face it was shipped in, and every
// override below exists only once the user asks for it.
//
// Colour is the exception, and deliberately so. The publisher's own
// palette is whatever that EPUB's stylesheet happens to declare, which
// across a shelf means every book opening a different colour — and a
// book that declares nothing inherits the page, which is not a decision
// anybody made. So a fresh reader opens Light, matching the web UI's
// own default, and Publisher stays one radio away for the books whose
// design is the point.
const SETTINGS_KEY = "liseur.reader.settings";
// On a phone the browser chrome already consumes part of the viewport, and
// leaving our own bar visible takes another useful slice from the page. This
// is only the default: a saved choice still wins, and the setting remains
// available for readers who want the bar pinned.
const COMPACT_READER = /Android/i.test(navigator.userAgent) ||
  window.matchMedia("(max-width: 700px)").matches;
const SETTINGS_DEFAULTS = Object.freeze({
  theme: "light",
  font: "publisher",
  size: 100,
  spacing: "0",
  justify: false,
  hyphenate: false,
  flow: COMPACT_READER ? "scrolled" : "paginated",
  columns: "auto",
  margin: COMPACT_READER ? "narrow" : "normal",
  autohide: COMPACT_READER,
  footer: "chapter",
});
// What the footer's middle slot shows; a click on the footer walks
// this ring, the way a tap does in the app. "positions-chapter" shows
// how many Readium positions remain in the current chapter.
const FOOTER_MODES = ["chapter", "positions-chapter", "time-chapter", "time-book", "empty"];
const THEMES = {
  light: {
    bg: "#ffffff", fg: "#1b1b1f", link: "#1a63c4", scheme: "light",
    selectionBg: "#cfe3ff", selectionFg: "#1b1b1f",
  },
  sepia: {
    bg: "#f6ecd9", fg: "#5b4636", link: "#8a5a2b", scheme: "light",
    selectionBg: "#d8c2a0", selectionFg: "#4a392c",
  },
  dark: {
    bg: "#202124", fg: "#cfcfd4", link: "#8ab4f8", scheme: "dark",
    selectionBg: "#4f6fbe", selectionFg: "#f5f7ff",
  },
  "tokyo-night": {
    bg: "#1a1b26",
    fg: "#c0caf5",
    link: "#7aa2f7",
    scheme: "dark",
    selectionBg: "#445c9b",
    selectionFg: "#eef2ff",
  },
  "rose-pine": {
    bg: "#191724",
    fg: "#e0def4",
    link: "#c4a7e7",
    scheme: "dark",
    selectionBg: "#5c4a88",
    selectionFg: "#f7f4ff",
  },
  black: {
    bg: "#000000", fg: "#ababae", link: "#7aa2d8", scheme: "dark",
    selectionBg: "#375f9d", selectionFg: "#f5f7ff",
  },
};
const FONTS = {
  serif: 'Georgia, "Times New Roman", "Liberation Serif", serif',
  sans: 'system-ui, -apple-system, "Segoe UI", Roboto, "Liberation Sans", sans-serif',
};
const MARGINS = { none: "8px", narrow: "16px", normal: "48px", wide: "72px" };
// The column gap follows the margin choice: "narrow" should mean the
// text gets the window, not just that the outer edge moved.
const GAPS = { none: "1%", narrow: "3%", normal: "7%", wide: "11%" };

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
      `html { color-scheme: ${theme.scheme}; --liseur-selection-bg: ${theme.selectionBg}; --liseur-selection-fg: ${theme.selectionFg}; }`,
      `html, body { background: ${theme.bg} !important; color: ${theme.fg} !important; }`,
      `body * { background-color: transparent !important; color: ${theme.fg} !important; }`,
      `a:any-link { color: ${theme.link} !important; }`,
      "body::selection, body *::selection { background-color: var(--liseur-selection-bg) !important; color: var(--liseur-selection-fg) !important; }",
      "body::-moz-selection, body *::-moz-selection { background-color: var(--liseur-selection-bg) !important; color: var(--liseur-selection-fg) !important; }",
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
  document.body.dataset.readerAutohide = String(!!settings.autohide);
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
  view.applySettings(settings, chapterCSS(settings), THEMES[settings.theme]);
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
  doc.addEventListener("dblclick", cancelDeferred, true);
  // A real second click starts with pointerdown, before the browser emits
  // the second click/dblclick pair. Cancel here as well so a platform with
  // a late or missing dblclick event cannot let the first deferred tap win.
  doc.addEventListener("pointerdown", () => {
    if (deferred) cancelDeferred();
  }, true);
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
    case "positions-chapter": {
      // Show how many Readium positions remain in the current chapter.
      if (!chapters || !positions || chapters.chapters.length === 0) return "";
      const section = location.section || {};
      const currentPage = pageAt(positions, section.current, location.sectionFraction);
      if (!currentPage) return "";
      const chapter = chapterForLocation(chapters.chapters, chapters.chapterIndexByResource, section.current, currentPage);
      const pagesLeft = pagesLeftInChapter(chapter, currentPage);
      if (pagesLeft === null) return "";
      if (pagesLeft === 0) return "Last page in chapter";
      return pagesLeft + (pagesLeft === 1 ? " page" : " pages") + " left in chapter";
    }
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
// which retarget to the readium-view host)
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
    void dismissCatchup();
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
  // A modal dialog owns the keyboard while it is up: the platform
  // handles Escape and focus, and a page turn behind it would move the
  // book out from under the question being asked.
  if (syncDialog && syncDialog.open) return;
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
    case "s":
      e.preventDefault();
      revealChrome();
      void askBookSync();
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
  leaving = true;
  push();
  endSession();
  view?.destroy().catch(() => {});
});

// ------------------------------------------------------------ open

(async function start() {
  try {
    say("Opening the publication…");
    view = document.createElement("readium-view");
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
      if (annotationsEnabled) wireSelection(e.detail.doc);
    });
    view.addEventListener("link", (e) => {
      if (!noteNavigation()) e.preventDefault();
    });
    const local = cfg.offline
      ? await getReadySnapshot({ partition: storagePartition(), bookID: cfg.bookID })
      : null;
    if (cfg.offline && !local) throw Error("This book is not available offline.");
    if (cfg.offline) {
      offlineSnapshot = local;
      // Offline the catalog cannot be asked, but it already was: a
      // snapshot is only stored ready once its publication URLs were
      // checked against the digest the catalog named at download time.
      // That is the same agreement resolveWork() reaches online, made
      // earlier, so a position read on a plane still names its edition.
      catalogEditionSHA = local.digest || "";
      offlineAccount = local.account;
      offlineContext = {
        ...await accountContext(offlinePartition, offlineAccount), deviceID: local.deviceID,
      };
      if (local.epoch !== offlineContext.epoch)
        throw new Error("This offline copy belongs to an expired sign-in.");
      releaseOfflineReader = await claimOfflineReader(offlineContext, cfg.bookID);
      await assertOfflineContext(offlineContext);
      workID = local.workID || local.localPosition?.work_id || null;
      offlineCheckpoint = await getOfflineSessionCheckpoint({
        partition: offlinePartition,
        account: offlineAccount,
        bookID: cfg.bookID,
      });
    }
    await view.open({
      request: cfg.offline ? local.request : api,
      current: cfg.offline ? () => true : resp => auth.responseCurrent(resp),
      bookID: cfg.bookID,
    });
    // Counted before the first relocate paints a footer, so the very
    // first page the reader sees is already the app's number.
    positions = positionTable(view.book.sections);
    chapters = buildChapters(view.book.toc, view.book.sections, positions, view.book.packageHref);
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
    if (!cfg.offline && cfg.detached) {
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
    let op = cfg.offline ? offlineSnapshot.localPosition || null : null;
    if (cfg.offline) {
      catchup.bind(offlineAccount, workID, offlineSnapshot.deviceID);
      // A locally queued wire op omits device_id; the credential supplies it
      // on upload. Its echo after a reload is still our opening baseline.
      const stamped = op && { ...op, device_id: op.device_id || offlineSnapshot.deviceID };
      catchup.baseline(stamped);
      catchup.local(stamped, false);
    }
    if (!cfg.offline) try {
      workID = await resolveWork();
      const identity = auth.identity();
      catchup.bind(identity?.account, workID, identity?.device);
      await prepareReadingSync(identity);
      // A checkpoint names one sitting, so only the page that can claim
      // this book keeps one. A second tab is refused the claim and reads
      // on with a fresh sitting, which is what it did before.
      if (offlineContext) {
        releaseOfflineReader = await claimOfflineReader(offlineContext, cfg.bookID)
          .catch(() => null);
        if (releaseOfflineReader) {
          sessionOwner = true;
          offlineCheckpoint = await getOfflineSessionCheckpoint({
            partition: offlinePartition, account: offlineAccount, bookID: cfg.bookID,
          }).catch(() => null);
        }
      }
      // What this browser last said, read back from its own disk. A page
      // turn the server never acknowledged is still where the reader is,
      // and it is still owed.
      const stored = offlineContext
        ? await readingState({ ...offlineContext, bookID: cfg.bookID }).catch(() => null)
        : null;
      const queued = offlineContext
        ? await listOfflineOutbox({
          ...offlineContext, kind: "position", state: null,
        }).catch(() => [])
        : [];
      const dirty = queued.some(record => record.deviceID === offlineContext?.deviceID &&
        record.bookID === cfg.bookID);
      const result = await lastPosition();
      const remote = result.ok ? result.op : null;
      const baseline = stored ? stored.baseline : remote;
      catchup.baseline(baseline);
      catchup.local(stored?.local || null, dirty);
      const { decision } = reconcileReadingState({
        local: stored?.local || null, remote, baseline, localDirty: dirty,
      });
      // A conflict opens where this reader was, never where the other
      // device went: the trip is offered, not taken for them.
      op = decision === "push" || decision === "conflict"
        ? stored?.local || remote : remote || stored?.local || null;
      if (!stored && remote && offlineContext) {
        await agreeReadingBaseline({
          ...offlineContext, bookID: cfg.bookID, workID, baseline: remote,
        }).catch(() => {});
      }
      catchup.observe(remote);
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
    ready = !cfg.offline && !syncExpired;
    beginSession();
    if (cfg.offline) prepareOfflineSync();
    if (!cfg.offline && ready && !document.hidden) startLive();
    if (document.hidden) catchup.hide();
    if (!syncExpired) say("");
    // Opening the book is a moment where a disagreement can be raised
    // without taking the page out from under anybody.
    if (!document.hidden) {
      catchup.present();
      showCatchup();
    }
    if (readingCoordinator) readingCoordinator.trigger();
  } catch (err) {
    releaseOfflineReader?.();
    say((err && err.message) || "this book could not be opened", true);
  }
})();
