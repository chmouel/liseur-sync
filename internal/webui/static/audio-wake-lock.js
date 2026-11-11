// Screen Wake Lock while the reader follows the audiobook (#113).
//
// WebKit refuses a request from a hidden page and drops the sentinel
// when the page is hidden without asking again on return, so the owner
// re-syncs on visibilitychange. A browser without the API, a refused
// request and a battery saver are all normal: nothing here surfaces an
// error or any UI.

export function screenWakeLock({ nav = globalThis.navigator, doc = globalThis.document } = {}) {
  let sentinel = null;
  let requesting = false;
  let wanted = false;

  const release = async () => {
    if (!sentinel) return;
    const held = sentinel;
    sentinel = null;
    try { await held.release(); } catch {}
  };

  const acquire = async () => {
    if (sentinel || requesting || typeof nav?.wakeLock?.request !== "function") return;
    requesting = true;
    try {
      const lock = await nav.wakeLock.request("screen");
      lock?.addEventListener?.("release", () => {
        if (sentinel === lock) sentinel = null;
      });
      // The request may outlive the reason for it: a pause, a close or
      // the page going hidden while it was in flight.
      if (wanted && !doc?.hidden) sentinel = lock;
      else { try { await lock?.release?.(); } catch {} }
    } catch {
      // Refused (hidden page, battery saver, no permission): read on.
    } finally {
      requesting = false;
    }
  };

  return {
    held: () => sentinel !== null,
    async sync(want) {
      wanted = !!want && !doc?.hidden;
      if (!wanted) { await release(); return; }
      await acquire();
    },
    release,
  };
}
