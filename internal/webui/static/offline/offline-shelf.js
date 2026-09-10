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

// Pulling the shelf down and pressing the button are the same errand:
// send what this device owes, then draw what is actually saved here.
// The runner is what keeps a second ask from starting a second send.
const indicator = pullIndicator(document.getElementById("pull-indicator"));
const runner = refreshRunner({
  async work() {
    await Promise.all(coordinators.map(sync => sync.trigger()));
    await render();
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
  const author = typeof snapshot.author === "string" ? snapshot.author.trim() : "";
  return author ? `${title} — ${author}` : title;
}

async function render() {
  if (!books) return;
  const generation = ++renderGeneration;
  coordinators.forEach(sync => sync.stop());
  coordinators = [];
  books.textContent = "";
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
        onStatus: text => { if (text) message(text, true); },
      });
      coordinators.push(sync);
      sync.trigger();
    }
    if (!snapshots.length) {
      message("No books have been saved for offline reading yet.");
      return;
    }
    message(`${snapshots.length} book${snapshots.length === 1 ? "" : "s"} saved offline.`);
    for (const snapshot of snapshots) {
      const item = document.createElement("article");
      item.className = "offline-book";
      const heading = document.createElement("h2");
      const link = document.createElement("a");
      link.href = `./read/?book=${encodeURIComponent(snapshot.bookID)}`;
      link.textContent = titleFor(snapshot);
      heading.append(link);
      item.append(heading);
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
          await render();
        } catch (error) {
          remove.disabled = false;
          message(error.message || "The offline copy could not be removed.", true);
        }
      });
      item.append(remove);
      books.append(item);
    }
  } catch (error) {
    message(error.message || "Offline storage is unavailable.", true);
  }
}

const channel = new BroadcastChannel("liseur-offline");
channel.onmessage = event => {
  if (event.data.partition === storagePartition() && ["logout", "account"].includes(event.data.type))
    render();
};
render();
