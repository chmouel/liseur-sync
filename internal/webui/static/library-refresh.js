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
  if (!account) return true;
  const [storage, sync] = await Promise.all([
    import("./offline-storage.js"),
    import("./offline-sync.js"),
  ]);
  const partition = storage.storagePartition();
  const queued = await storage.listOfflineOutbox({ partition, account, state: null });
  const devices = new Set(queued.map(record => record.deviceID).filter(Boolean));
  if (!devices.size) return true;
  // A coordinator declines outright when the browser says it is
  // offline, before it has said anything through either channel. A
  // queue that was never even attempted is not a queue that emptied.
  if (navigator.onLine === false) return false;
  const context = await storage.accountContext(partition, account);
  const base = storage.deploymentPrefix();
  let delivered = true;
  // One coordinator per device, exactly as the installed shelf builds
  // them, and stopped again afterwards: this is a single errand, not a
  // background service. A device's failure is its own; the others still
  // get their turn.
  await Promise.all([...devices].map(async deviceID => {
    // A coordinator swallows the upload it could not make and says so
    // through `onStatus`, so it resolves whether or not the queue is
    // empty. Without a listener the drain would look like a success
    // every time. The last thing it says is how the pass ended.
    //
    // A record already given up on is reported down the other channel
    // and counts for nothing in `onStatus`, so a shelf holding only
    // stuck records would drain to a clean "Refreshed" while the
    // reading in them stayed where it was.
    let trouble = "";
    let stuck = false;
    const coordinator = sync.offlineSync({
      context: { ...context, deviceID }, base,
      onStatus: message => { trouble = message || ""; },
      onStuck: records => { if (records?.length) stuck = true; },
    });
    try {
      await coordinator.trigger();
    } catch {
      trouble = "undelivered";
    } finally {
      if (trouble || stuck) delivered = false;
      coordinator.stop();
    }
  }));
  return delivered;
}

// Revealing the button here is the whole of its feature detection: with
// no module running, a control that would do nothing is never shown.
function reveal() {
  document.querySelectorAll("[data-refresh][hidden]").forEach(button => {
    button.hidden = false;
  });
}

// The page is redrawn in place, so the scroll position and the focus
// ring stay where the reader left them.
//
// This deliberately does not go through htmx. htmx announces itself
// with `HX-Request`, and this route answers that header with a fragment
// of the card list rather than the page around it, which is right for
// the reveal sentinel and wrong for a refresh: a refresh wants the
// whole shelf, hero and counts included. So the page is fetched the way
// the browser fetched it, and the one region is swapped over.
async function redraw() {
  const target = document.querySelector("#content .page");
  if (!target) {
    window.location.reload();
    return;
  }
  // The button that asked for this lives inside the region about to be
  // replaced. Replacing it drops focus on the body, and a keyboard
  // reader's next Tab starts somewhere else entirely, so where the
  // focus was is noted now and given back to the same control in the
  // new markup: by id where there is one, and by the refresh marker
  // otherwise, which is the control this module owns.
  const focused = target.contains(document.activeElement) ? document.activeElement : null;
  const refocus = !focused ? null
    : focused.id ? `#${CSS.escape(focused.id)}`
    : focused.matches("[data-refresh]") ? "[data-refresh]"
    : null;
  const response = await fetch(window.location.href, {
    credentials: "same-origin",
    cache: "no-store",
    headers: { Accept: "text/html" },
  });
  // A session that expired mid-refresh answers with the sign-in page.
  // Let the browser go there rather than pasting it into the shelf.
  if (response.redirected) {
    window.location.assign(response.url);
    return;
  }
  if (!response.ok) throw new Error("The shelf could not be refreshed.");
  const fresh = new DOMParser()
    .parseFromString(await response.text(), "text/html")
    .querySelector("#content .page");
  if (!fresh) throw new Error("The shelf could not be refreshed.");
  target.replaceWith(document.adoptNode(fresh));
  // The new markup carries the reveal sentinel and the rest of the
  // page's own attributes; without this they are inert markup.
  window.htmx?.process?.(document.querySelector("#content"));
  if (refocus) document.querySelector(`#content ${refocus}`)?.focus?.({ preventScroll: true });
  reveal();
}

const runner = refreshRunner({
  async work() {
    // A queue nobody could empty is not a reason to refuse the shelf.
    const delivered = await drainQueue().catch(error => {
      console.warn("offline changes could not be delivered", error);
      return false;
    });
    await redraw();
    // The shelf is current either way. Saying "Refreshed" over a queue
    // that is still full would tell the reader their offline reading
    // had gone up when it has not, and this is the one moment they are
    // watching for an answer.
    return delivered ? "done" : "partial";
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

reveal();
// A reveal-paged list swapping more cards in brings its own copy of
// nothing, but a fragment can still replace the toolbar around it.
document.body.addEventListener("htmx:afterSwap", reveal);
