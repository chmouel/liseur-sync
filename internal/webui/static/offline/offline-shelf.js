import {
  activeAccount,
  accountContext,
  accountVersion,
  deploymentPrefix,
  listOfflineOutbox,
  listReadySnapshots,
  removeBookSnapshots,
  setActiveAccount,
  storagePartition,
  notifyOfflineChange,
} from "./assets/offline-storage.js";
import { offlineSync } from "./assets/offline-sync.js";
import { refreshRunner } from "./assets/pull-refresh.js";
import { armPull, pullIndicator } from "./assets/pull-refresh-dom.js";
import {
  authorFor,
  coverSource,
  formatLabel,
  initialFor,
  networkPresentation,
  progressClass,
  progressFor,
  progressLabel,
  removalGuard,
  savedLabel,
  sizeLabel,
  titleFor,
} from "./offline-shelf-model.js";

const status = document.getElementById("offline-status");
const notice = document.getElementById("offline-notice");
const books = document.getElementById("offline-books");
const brand = document.getElementById("offline-brand");
const libraryLink = document.getElementById("offline-open-library");
const networkNote = document.getElementById("offline-network-note");
const brandHref = brand?.getAttribute("href") || "";
let coordinators = [];
let guards = [];
let renderGeneration = 0;
let shelfRedrawGeneration = 0;

// Pulling the shelf down and pressing the button are the same errand:
// send what this device owes, then draw what is actually saved here.
// The runner is what keeps a second ask from starting a second send.
const indicator = pullIndicator(document.getElementById("pull-indicator"));
// What the last pass had to say about the queue. A coordinator reports
// an upload it could not make and then resolves anyway, and `render`
// runs afterwards and writes the book count over whatever it said, so
// without holding on to it here a failed send would leave no trace at
// all a minute later.
let trouble = "";
const runner = refreshRunner({
  async work() {
    trouble = "";
    await render({ syncFirst: true });
    // A coordinator declines outright when the browser says it is
    // offline, without a word through either channel. On this page of
    // all pages that is a likely way to press refresh, and a queue
    // nobody attempted must not read as a queue that emptied.
    if (!trouble && navigator.onLine === false && await queueWaiting()) {
      trouble = "Offline. Your reading syncs when this device is back online.";
    }
    if (!trouble) return "done";
    message(trouble, true);
    return "partial";
  },
  onState: (state) => {
    if (state === "refreshing") indicator.paint("refreshing");
    else indicator.settle(state);
  },
});
const retry = document.createElement("button");
retry.type = "button";
retry.className = "button secondary";
retry.textContent = "Retry sync";
retry.hidden = true;
retry.addEventListener("click", () => runner.ask());
status?.after(retry);
armPull({ scroller: document.scrollingElement, indicator, runner });

function message(text, error = false) {
  if (!status) return;
  status.textContent = text;
  status.classList.toggle("problem", error);
}

// A retry button with nothing to retry is an invitation to press it and
// learn nothing, so it exists only while this device owes something.
async function syncRetryVisibility() {
  let waiting = !!trouble;
  if (!waiting) waiting = await queueWaiting();
  retry.hidden = !waiting;
}

function announce(text) {
  if (notice) notice.textContent = text;
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// The library is a network away and an installed app has no back
// button, so while the network is gone the header stops offering it and
// says why in its place. offline-net.js has already put the browser's
// own answer on the document before the first paint; this only brings
// the rest of the header — and the keyboard — into line with it.
function applyNetwork() {
  const state = networkPresentation(navigator.onLine !== false);
  if (libraryLink) libraryLink.hidden = state.library === "hidden";
  if (networkNote) {
    networkNote.textContent = state.note;
    networkNote.hidden = !state.note;
  }
  if (!brand) return;
  if (state.brand === "link") {
    brand.setAttribute("href", brandHref);
    brand.removeAttribute("aria-disabled");
    brand.removeAttribute("tabindex");
  } else {
    brand.removeAttribute("href");
    brand.setAttribute("aria-disabled", "true");
    brand.setAttribute("tabindex", "-1");
  }
}

addEventListener("online", applyNetwork);
addEventListener("offline", applyNetwork);
applyNetwork();

function artFor(snapshot) {
  const art = element("span", "offline-book-art");
  art.setAttribute("aria-hidden", "true");
  const cover = coverSource(snapshot);
  if (cover) {
    const img = document.createElement("img");
    img.className = "offline-book-cover";
    img.src = cover;
    img.alt = "";
    img.decoding = "async";
    img.loading = "lazy";
    art.append(img);
    art.classList.add("has-cover");
  }
  art.append(element("span", "offline-book-initial", initialFor(snapshot)));
  return art;
}

function copyFor(snapshot) {
  const copy = element("div", "offline-book-copy");
  copy.append(element("span", "offline-book-eyebrow", savedLabel(snapshot)));
  const heading = element("h2", "offline-book-heading");
  heading.append(element("span", "offline-book-title", titleFor(snapshot)));
  copy.append(heading);
  const author = authorFor(snapshot);
  if (author) copy.append(element("span", "offline-book-author", author));
  const facts = element("span", "offline-book-facts");
  facts.append(element("span", "offline-book-badge", formatLabel(snapshot)));
  const size = sizeLabel(snapshot.bytes);
  if (size) facts.append(element("span", "offline-book-badge", size));
  copy.append(facts);
  const progress = progressFor(snapshot);
  if (progress !== null) {
    const label = element("span", "offline-book-progress-label", progressLabel(progress));
    label.setAttribute("aria-hidden", "true");
    copy.append(label);
    const rail = element("span", "offline-book-progress");
    rail.setAttribute("role", "progressbar");
    rail.setAttribute("aria-label", `${titleFor(snapshot)} reading progress`);
    rail.setAttribute("aria-valuemin", "0");
    rail.setAttribute("aria-valuemax", "100");
    rail.setAttribute("aria-valuenow", String(Math.round(progress * 100)));
    rail.setAttribute("aria-valuetext", progressLabel(progress));
    rail.append(element("span", progressClass(progress)));
    copy.append(rail);
  }
  return copy;
}

// actionsFor is the one part of a card that is a decision rather than a
// description. A saved copy can be hundreds of megabytes and getting it
// back needs the network that may be gone, so forgetting one takes two
// presses and the question is asked in the card, with its cost beside it.
function actionsFor(snapshot, { partition, account, context, generation, redraw }) {
  const actions = element("div", "offline-book-actions");
  const title = titleFor(snapshot);
  const size = sizeLabel(snapshot.bytes);

  const quiet = element("button", "button secondary offline-book-remove", "Remove");
  quiet.type = "button";
  quiet.setAttribute("aria-label", size
    ? `Remove “${title}” from this device, ${size} saved`
    : `Remove “${title}” from this device`);

  const question = element("div", "offline-book-confirm");
  question.hidden = true;
  question.append(element("p", "offline-book-ask", "Remove from this device?"));
  question.append(element("p", "offline-book-hint",
    size ? `${size} will be freed.` : "The saved copy will be deleted."));
  const choices = element("div", "offline-book-choices");
  const keep = element("button", "button secondary offline-book-keep", "Keep");
  keep.type = "button";
  const confirm = element("button", "button danger offline-book-confirmed", "Remove");
  confirm.type = "button";
  choices.append(keep, confirm);
  question.append(choices);

  const remove = async () => {
    quiet.disabled = true;
    confirm.disabled = true;
    try {
      await removeBookSnapshots({ partition, account, epoch: context.epoch, bookID: snapshot.bookID });
      notifyOfflineChange();
      announce(`“${title}” was removed from this device.`);
      await refreshBooks(partition, account, context, generation, redraw);
      status?.focus();
    } catch {
      quiet.disabled = false;
      confirm.disabled = false;
      // The question is still on the card, so the guard goes back to
      // armed with it: “Keep” answers it and “Remove” asks again. A
      // guard left unarmed here would leave both controls dead.
      guard.rearm();
      message("Couldn't remove that. Try again.", true);
      confirm.focus();
    }
  };

  // A question somebody is reading or pointing at is still being
  // answered, so its deadline waits for them to leave.
  let pointerOver = false;
  const guard = removalGuard({
    hold: () => pointerOver || question.contains(document.activeElement),
    arm() {
      quiet.hidden = true;
      question.hidden = false;
      announce(`Remove “${title}” from this device?${size ? ` ${size} will be freed.` : ""}`);
      keep.focus();
    },
    disarm() {
      question.hidden = true;
      quiet.hidden = false;
      announce(`“${title}” stays on this device.`);
      // The question went away on its own. Focus goes back to the
      // button that asked only if it was still in this card: a reader
      // who moved on to another book must not be pulled back.
      const focus = document.activeElement;
      if (focus && (question.contains(focus) || focus === quiet)) quiet.focus();
    },
    confirm: () => { void remove(); },
  });

  guards.push(guard);
  quiet.addEventListener("click", () => guard.press());
  confirm.addEventListener("click", () => guard.press());
  keep.addEventListener("click", () => guard.cancel());
  question.addEventListener("pointerenter", () => { pointerOver = true; });
  question.addEventListener("pointerleave", () => {
    pointerOver = false;
    guard.refresh();
  });
  question.addEventListener("focusout", (event) => {
    if (event.relatedTarget && question.contains(event.relatedTarget)) return;
    guard.refresh();
  });
  // Escape is the way out of a question without answering it.
  question.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    event.preventDefault();
    guard.cancel();
  });
  actions.append(quiet, question);
  return actions;
}

function drawBooks(snapshots, { partition, account, context, generation, redraw }) {
  if (!books || generation !== renderGeneration || redraw !== shelfRedrawGeneration) return;
  // The cards are about to go: whatever question one of them was still
  // asking has nobody left to answer it, and its timer would fire
  // against a button no longer on the page.
  for (const guard of guards) guard.dispose();
  guards = [];
  books.textContent = "";
  void syncRetryVisibility();
  if (!snapshots.length) {
    message("No books saved yet. Save a book from its page and it will be here offline.");
    return;
  }
  message(`${snapshots.length === 1 ? "One book is" : `${snapshots.length} books are`} saved for offline.`);
  for (const snapshot of snapshots) {
    const item = element("article", "offline-book");
    const body = element("a", "offline-book-main");
    body.href = `./read/?book=${encodeURIComponent(snapshot.bookID)}`;
    body.append(artFor(snapshot), copyFor(snapshot));
    item.append(body, actionsFor(snapshot, { partition, account, context, generation, redraw }));
    books.append(item);
  }
}

async function refreshBooks(partition, account, context, generation, redraw) {
  if (generation !== renderGeneration) return;
  const snapshots = await listReadySnapshots(partition, account);
  drawBooks(snapshots, { partition, account, context, generation, redraw });
}

// Whether this device is still holding reading nobody has taken.
async function queueWaiting() {
  try {
    const partition = storagePartition();
    const account = await activeAccount(partition);
    if (!account) return false;
    const outbox = await listOfflineOutbox({ partition, account, state: null });
    return outbox.length > 0;
  } catch {
    return false;
  }
}

async function refreshShelf() {
  const redraw = ++shelfRedrawGeneration;
  try {
    const partition = storagePartition();
    const account = await activeAccount(partition);
    if (!account || redraw !== shelfRedrawGeneration) return;
    const context = await accountContext(partition, account);
    if (redraw !== shelfRedrawGeneration) return;
    await refreshBooks(partition, account, context, renderGeneration, redraw);
  } catch {
    // A background write from another tab must not disturb the shelf.
  }
}

async function render({ syncFirst = false } = {}) {
  if (!books) return;
  books.textContent = "";
  message("Loading saved books…");
  const generation = ++renderGeneration;
  const redraw = ++shelfRedrawGeneration;
  coordinators.forEach(sync => sync.stop());
  coordinators = [];
  try {
    const partition = storagePartition();
    let account = await activeAccount(partition);
    if (!account) {
      try {
        const version = await accountVersion(partition);
        const response = await fetch("./account", {
          cache: "no-store",
          credentials: "same-origin",
        });
        if (response.ok && !response.redirected) {
          account = (await response.json()).account || null;
          if (account) await setActiveAccount(partition, account, version);
        }
      } catch {
        // An offline launch can only use the marker saved by an online page.
      }
    }
    if (!account) {
      message("Sign in online to manage downloaded books.");
      return;
    }
    const snapshots = await listReadySnapshots(partition, account);
    const context = await accountContext(partition, account);
    const outbox = await listOfflineOutbox({ partition, account, state: null });
    if (generation !== renderGeneration) return;
    const devices = new Set([...snapshots, ...outbox].map(row => row.deviceID).filter(Boolean));
    for (const deviceID of devices) {
      const sync = offlineSync({
        context: { ...context, deviceID }, base: deploymentPrefix(),
        onChange: async () => {
          if (generation !== renderGeneration) return;
          await refreshBooks(partition, account, context, generation, shelfRedrawGeneration);
        },
        onStatus: text => { if (text) { trouble = text; message(text, true); } },
        // A record already given up on is reported here rather than as
        // something waiting, so a shelf whose queue is entirely stuck
        // would otherwise read as a clean sync.
        onStuck: records => {
          if (records?.length) trouble = "Some offline changes could not be synced.";
        },
      });
      coordinators.push(sync);
    }
    if (syncFirst) await Promise.all(coordinators.map(sync => sync.trigger()));
    else coordinators.forEach(sync => sync.trigger());
    if (generation !== renderGeneration) return;
    const fresh = syncFirst
      ? await listReadySnapshots(partition, account)
      : snapshots;
    drawBooks(fresh, { partition, account, context, generation, redraw });
  } catch (error) {
    // The runner reads this: a shelf that could not be drawn is not a
    // refresh that succeeded, whatever the coordinators managed.
    trouble = error.message || "Offline storage is unavailable.";
    message(trouble, true);
    void syncRetryVisibility();
  }
}

function onOfflineNotice(detail) {
  if (!detail || detail.partition !== storagePartition()) return;
  const type = detail.type || "write";
  if (type === "logout" || type === "account") render();
  else if (type === "write") refreshShelf();
}

const channel = new BroadcastChannel("liseur-offline");
channel.onmessage = event => onOfflineNotice(event.data);
window.addEventListener("offline-change", event => onOfflineNotice(event.detail));
render();
