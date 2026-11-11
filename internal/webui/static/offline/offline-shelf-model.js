// What the offline shelf says about a saved book, and what it takes to
// forget one. Kept apart from the page so the renderer stays a renderer
// and these rules can be exercised without a DOM.

const savedDate = new Intl.DateTimeFormat(undefined, {
  month: "short", day: "numeric", year: "numeric",
});

export const OFFLINE_NOTE = "You're offline, your library will be back when you reconnect.";

/** Whether the header may offer the online library at all. */
export function networkPresentation(online) {
  const reachable = online !== false;
  return {
    library: reachable ? "shown" : "hidden",
    brand: reachable ? "link" : "static",
    note: reachable ? "" : OFFLINE_NOTE,
  };
}

export function titleFor(snapshot) {
  const title = typeof snapshot?.title === "string" && snapshot.title.trim()
    ? snapshot.title.trim()
    : "Untitled book";
  return title;
}

export function authorFor(snapshot) {
  return typeof snapshot?.author === "string" ? snapshot.author.trim() : "";
}

export function initialFor(snapshot) {
  const first = titleFor(snapshot).match(/\p{L}|\p{N}/u)?.[0];
  return (first || "•").toUpperCase();
}

/** The cover the download kept, or nothing when the book has none. */
export function coverSource(snapshot) {
  const cover = snapshot?.cover;
  return typeof cover === "string" && cover.startsWith("data:image/") ? cover : "";
}

export function formatLabel(snapshot) {
  const type = typeof snapshot?.mediaType === "string" ? snapshot.mediaType : "";
  return type.startsWith("audio/") ? "Audiobook" : "Ebook";
}

/** Zero is "unknown", not "zero bytes": older copies recorded no size. */
export function sizeLabel(bytes) {
  const value = Number(bytes);
  if (!Number.isFinite(value) || value <= 0) return "";
  const units = ["B", "kB", "MB", "GB"];
  let size = value;
  let unit = 0;
  while (size >= 1000 && unit < units.length - 1) {
    size /= 1000;
    unit++;
  }
  const digits = unit === 0 || size >= 100 ? 0 : 1;
  return `${size.toFixed(digits)} ${units[unit]}`;
}

export function savedLabel(snapshot) {
  const stamp = Number(snapshot?.readyAt || snapshot?.createdAt || 0);
  if (!stamp) return "Saved on this device";
  return `Saved ${savedDate.format(new Date(stamp))}`;
}

export function progressFor(snapshot) {
  const total = snapshot?.localPosition?.locator?.locations?.totalProgression;
  if (typeof total === "number" && Number.isFinite(total) && total >= 0 && total <= 1) return total;
  const own = snapshot?.localPosition?.progression;
  if (typeof own === "number" && Number.isFinite(own) && own >= 0 && own <= 1) return own;
  return null;
}

export function progressPercent(progress) {
  return Math.max(0, Math.min(100, Math.round(progress * 20) * 5));
}

export function progressClass(progress) {
  return `p${progressPercent(progress)}`;
}

export function progressLabel(progress) {
  return progress >= 1 ? "Finished" : `${Math.round(progress * 100)}% read`;
}

/**
 * removalGuard makes forgetting a copy a decision rather than a slip.
 *
 * The first press only arms: the reader is asked, in place, whether to
 * remove the copy, and told how much of the device it costs. The second
 * press removes. Nothing is deleted on the way in, so a mistap costs a
 * tap to keep, and an armed button nobody answers goes quiet again.
 *
 * The timers are injected so the arming window is a rule with a test,
 * not three setTimeout calls scattered through a render pass.
 */
export function removalGuard({
  arm,
  disarm = () => {},
  confirm,
  timeout = 8000,
  setTimer = (fn, ms) => setTimeout(fn, ms),
  clearTimer = id => clearTimeout(id),
} = {}) {
  let armed = false;
  let timer = null;
  const reset = () => {
    if (timer !== null) clearTimer(timer);
    timer = null;
    armed = false;
  };
  return {
    armed: () => armed,
    // A card that is redrawn has no question left to answer, and a
    // timer that outlives it would disarm a button nobody is looking
    // at any more.
    dispose() {
      reset();
    },
    /** Returns "armed" while the question is up and "remove" when answered. */
    press() {
      if (armed) {
        reset();
        confirm();
        return "remove";
      }
      reset();
      armed = true;
      arm();
      timer = setTimer(() => {
        timer = null;
        armed = false;
        disarm();
      }, timeout);
      return "armed";
    },
    cancel() {
      if (!armed) return false;
      reset();
      disarm();
      return true;
    },
  };
}
