// The offline shelf belongs in the rail only where a service worker can
// answer for it, which is why the entry ships hidden and this reveals
// it: a browser without one would be offered a page it can never keep.
import {
  activeAccount,
  listReadySnapshots,
  storagePartition,
} from "./offline-storage.js";

const link = document.querySelector("[data-rail-offline]");
if (link && "serviceWorker" in navigator) {
  link.hidden = false;
  let shown = "";

  const paint = async () => {
    try {
      const partition = storagePartition();
      const account = await activeAccount(partition);
      const count = account ? (await listReadySnapshots(partition, account)).length : 0;
      if (String(count) === shown) return;
      shown = String(count);
      link.querySelector(".rail-count")?.remove();
      if (!count) {
        link.removeAttribute("aria-label");
        return;
      }
      const badge = document.createElement("span");
      badge.className = "rail-count";
      badge.textContent = String(count);
      badge.setAttribute("aria-hidden", "true");
      link.append(badge);
      link.setAttribute("aria-label", `Offline, ${count} book${count === 1 ? "" : "s"} saved`);
    } catch {
      // A rail badge is not worth a broken page: the shelf still counts
      // what it holds when it opens.
    }
  };

  addEventListener("offline-change", paint);
  if (globalThis.BroadcastChannel) {
    const channel = new BroadcastChannel("liseur-offline");
    channel.onmessage = paint;
  }
  paint();
}
