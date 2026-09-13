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

const status = document.getElementById("offline-status");
const books = document.getElementById("offline-books");
let coordinators = [];
let renderGeneration = 0;
let shelfRedrawGeneration = 0;
const savedDateFormatter = new Intl.DateTimeFormat(undefined, {
  month: "short", day: "numeric", year: "numeric",
});

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
retry.addEventListener("click", () => runner.ask());
status?.after(retry);
armPull({ scroller: document.scrollingElement, indicator, runner });

function message(text, error = false) {
  if (!status) return;
  status.textContent = text;
  status.classList.toggle("problem", error);
}

function titleFor(snapshot) {
  const title = typeof snapshot.title === "string" && snapshot.title.trim()
    ? snapshot.title.trim()
    : "Untitled book";
  return title;
}

function authorFor(snapshot) {
  return typeof snapshot.author === "string" ? snapshot.author.trim() : "";
}

function initialFor(snapshot) {
  const first = titleFor(snapshot).match(/\p{L}|\p{N}/u)?.[0];
  return (first || "•").toUpperCase();
}

function positionsLabel(snapshot) {
  const count = Array.isArray(snapshot?.positions?.positions)
    ? snapshot.positions.positions.length
    : 0;
  return `${count} reading position${count === 1 ? "" : "s"} ready offline`;
}

function progressFor(snapshot) {
  const total = snapshot?.localPosition?.locator?.locations?.totalProgression;
  if (typeof total === "number" && Number.isFinite(total) && total >= 0 && total <= 1) return total;
  const own = snapshot?.localPosition?.progression;
  if (typeof own === "number" && Number.isFinite(own) && own >= 0 && own <= 1) return own;
  return null;
}

function progressPercent(progress) {
  return Math.max(0, Math.min(100, Math.round(progress * 20) * 5));
}

function progressClass(progress) {
  return `p${progressPercent(progress)}`;
}

function progressLabel(progress) {
  return progress >= 1 ? "Finished" : `${Math.round(progress * 100)}% read`;
}

function savedLabel(snapshot) {
  const stamp = Number(snapshot.readyAt || snapshot.createdAt || 0);
  if (!stamp) return "Saved on this device";
  return `Saved ${savedDateFormatter.format(new Date(stamp))}`;
}

function drawBooks(snapshots, { partition, account, context, generation, redraw }) {
  if (!books || generation !== renderGeneration || redraw !== shelfRedrawGeneration) return;
  books.textContent = "";
  if (!snapshots.length) {
    message("No books have been saved for offline reading yet.");
    return;
  }
  message(`${snapshots.length} book${snapshots.length === 1 ? "" : "s"} saved offline.`);
  for (const snapshot of snapshots) {
    const item = document.createElement("article");
    item.className = "offline-book";
    const body = document.createElement("a");
    body.className = "offline-book-main";
    body.href = `./read/?book=${encodeURIComponent(snapshot.bookID)}`;
    const art = document.createElement("span");
    art.className = "offline-book-art";
    art.setAttribute("aria-hidden", "true");
    const initial = document.createElement("span");
    initial.className = "offline-book-initial";
    initial.textContent = initialFor(snapshot);
    art.append(initial);
    body.append(art);
    const copy = document.createElement("div");
    copy.className = "offline-book-copy";
    const eyebrow = document.createElement("span");
    eyebrow.className = "offline-book-eyebrow";
    eyebrow.textContent = savedLabel(snapshot);
    copy.append(eyebrow);
    const heading = document.createElement("h2");
    heading.className = "offline-book-heading";
    const link = document.createElement("span");
    link.className = "offline-book-title";
    link.textContent = titleFor(snapshot);
    heading.append(link);
    copy.append(heading);
    const author = authorFor(snapshot);
    if (author) {
      const byline = document.createElement("span");
      byline.className = "offline-book-author";
      byline.textContent = author;
      copy.append(byline);
    }
    const meta = document.createElement("span");
    meta.className = "offline-book-meta";
    meta.textContent = positionsLabel(snapshot);
    copy.append(meta);
    const progress = progressFor(snapshot);
    if (progress !== null) {
      const progressText = document.createElement("span");
      progressText.className = "offline-book-progress-label";
      progressText.setAttribute("aria-hidden", "true");
      progressText.textContent = progressLabel(progress);
      copy.append(progressText);
      const rail = document.createElement("span");
      rail.className = "offline-book-progress";
      const percent = Math.round(progress * 100);
      rail.setAttribute("role", "progressbar");
      rail.setAttribute("aria-label", `${titleFor(snapshot)} reading progress`);
      rail.setAttribute("aria-valuemin", "0");
      rail.setAttribute("aria-valuemax", "100");
      rail.setAttribute("aria-valuenow", String(percent));
      rail.setAttribute("aria-valuetext", progressLabel(progress));
      const fill = document.createElement("span");
      fill.className = progressClass(progress);
      rail.append(fill);
      copy.append(rail);
    }
    body.append(copy);
    item.append(body);
    const actions = document.createElement("div");
    actions.className = "offline-book-actions";
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "button secondary";
    remove.textContent = "Remove";
    remove.addEventListener("click", async () => {
      remove.disabled = true;
      try {
        await removeBookSnapshots({
          partition, account, epoch: context.epoch, bookID: snapshot.bookID,
        });
        notifyOfflineChange();
        await refreshBooks(partition, account, context, generation, shelfRedrawGeneration);
      } catch (error) {
        remove.disabled = false;
        message(error.message || "The offline copy could not be removed.", true);
      }
    });
    actions.append(remove);
    item.append(actions);
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
        onChange: async () => refreshBooks(partition, account, context, generation, redraw),
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
