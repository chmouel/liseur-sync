// "Look again" on the library page.
//
// This is the web half of the Android client's `LibraryRefresh`, minus
// the folder scan: the server watches its own folders, and the button
// beside this one is the way to ask it to walk them. What a reader
// wants from a pull here is the other two thirds — deliver whatever
// this browser read offline, then show the shelf the server has now.
//
// The order matters. Draining first means a position read on the train
// is on the server before the shelf is drawn, so the card that comes
// back says where the reader actually is rather than where they were
// before the tunnel. It is best-effort in both directions: a drain that
// fails still lets the page refresh, because a stale queue is no reason
// to withhold new books.
//
// Every part of this is an enhancement. The page it acts on is already
// complete when it arrives, which is why the button is served hidden
// and revealed here rather than the other way round.

import { refreshRunner } from "./pull-refresh.js";
import { armPull, pullIndicator } from "./pull-refresh-dom.js";

const indicator = pullIndicator(document.getElementById("pull-indicator"));
const account = document.body?.dataset.offlineAccount || "";

// The offline machinery is a large module that opens a database. A
// reader whose deployment has no offline shelf should not pay for it to
// refresh a page, so it is fetched only when there is an account whose
// queue could hold anything.
async function drainQueue() {
  if (!account) return;
  const [storage, sync] = await Promise.all([
    import("./offline-storage.js"),
    import("./offline-sync.js"),
  ]);
  const partition = storage.storagePartition();
  const queued = await storage.listOfflineOutbox({ partition, account, state: null });
  const devices = new Set(queued.map(record => record.deviceID).filter(Boolean));
  if (!devices.size) return;
  const context = await storage.accountContext(partition, account);
  const base = storage.deploymentPrefix();
  // One coordinator per device, exactly as the installed shelf builds
  // them, and stopped again afterwards: this is a single errand, not a
  // background service. A device's failure is its own — the others
  // still get their turn.
  await Promise.all([...devices].map(async deviceID => {
    const coordinator = sync.offlineSync({ context: { ...context, deviceID }, base });
    try {
      await coordinator.trigger();
    } finally {
      coordinator.stop();
    }
  }));
}

// htmx swaps the page's content in place, which keeps the scroll
// position and the focus ring where the reader left them. A browser
// without it reloads, which is the same wish said less gracefully.
async function redraw() {
  const htmx = window.htmx;
  if (!htmx?.ajax) {
    window.location.reload();
    return;
  }
  await htmx.ajax("GET", window.location.href, {
    source: document.body,
    target: "#content .page",
    select: "#content .page",
    swap: "outerHTML",
  });
}

const runner = refreshRunner({
  async work() {
    // A queue nobody could empty is not a reason to refuse the shelf.
    await drainQueue().catch(error => {
      console.warn("offline changes could not be delivered", error);
    });
    await redraw();
  },
  onState: (state) => {
    if (state === "refreshing") indicator.paint("refreshing");
    else indicator.settle(state);
  },
});

armPull({ scroller: document.scrollingElement, indicator, runner });

// The button is delegated rather than bound: the toolbar holding it is
// inside the region a refresh replaces, so a bound listener would work
// exactly once.
document.addEventListener("click", (event) => {
  const button = event.target.closest?.("[data-refresh]");
  if (!button) return;
  event.preventDefault();
  void runner.ask();
});

// Revealing it here is the whole of its feature detection: with no
// module running, a control that does nothing is never shown.
const reveal = () => {
  document.querySelectorAll("[data-refresh][hidden]").forEach(button => {
    button.hidden = false;
  });
};
reveal();
document.body.addEventListener("htmx:afterSwap", reveal);
