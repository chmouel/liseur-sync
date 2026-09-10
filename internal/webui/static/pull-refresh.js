// The gesture that means "look again", and the runner behind it.
//
// This is the web half of the Android client's `LibraryRefresh`: the
// same distinction between a quiet refresh and one a reader asked for,
// and the same rule that a second ask joins the run already under way
// rather than starting a second one over the same work.
//
// Nothing here touches the DOM. A pull is a sequence of points and a
// verdict about them, which is the part worth testing without a
// browser; `pull-refresh-dom.js` is the thin layer that turns real
// touch events into these calls and moves an indicator.

/** How far the finger must travel before the release refreshes. */
export const PULL_THRESHOLD = 64;

/**
 * How far past the threshold the indicator is allowed to go. Beyond
 * this the finger keeps moving and the indicator does not, which is
 * what tells the reader the gesture has been understood.
 */
export const PULL_MAX = 96;

/**
 * Travel before the gesture is claimed. Below this a drag is still a
 * scroll, or a tap that wandered, and the page must keep it.
 */
export const PULL_SLOP = 12;

/**
 * How much straighter than sideways a drag must be to count as a pull.
 * A diagonal belongs to whatever scrolls horizontally underneath.
 */
export const PULL_STRAIGHTNESS = 1.5;

/**
 * distance is the indicator's travel for a finger that has moved `dy`.
 *
 * It is damped, so the last part of the pull costs more than the first
 * and the indicator never runs away past `PULL_MAX`. The reader should
 * feel resistance building rather than reach a hard stop.
 */
export function distance(dy) {
  if (!(dy > 0)) return 0;
  if (dy <= PULL_THRESHOLD) return dy;
  const past = dy - PULL_THRESHOLD;
  const room = PULL_MAX - PULL_THRESHOLD;
  return PULL_THRESHOLD + room * (1 - Math.exp(-past / room));
}

/**
 * pullGesture tracks one finger and says what the page should do.
 *
 * Every call returns one of:
 * - `null`: nothing to do, the browser keeps the event.
 * - `{ kind: "move", distance, armed }`: the gesture is ours; hold the
 *   page still and put the indicator here.
 * - `{ kind: "release", refresh }`: the finger left; run the refresh
 *   or spring back.
 * - `{ kind: "cancel" }`: abandoned — a second finger, a scroll that
 *   turned out to be sideways, or the browser taking the touch back.
 *
 * `atTop` is asked at the moment the finger lands, not remembered from
 * before: a list that was scrolled a moment ago is not a list at the
 * top, and pulling one down should scroll it, not refresh it.
 */
export function pullGesture({ atTop, enabled = () => true } = {}) {
  let tracking = false, claimed = false, startX = 0, startY = 0, travelled = 0;
  const idle = () => {
    tracking = false; claimed = false; travelled = 0;
  };
  return {
    get active() { return claimed; },
    start(touches) {
      idle();
      if (touches.length !== 1 || !enabled() || !atTop()) return null;
      tracking = true;
      startX = touches[0].clientX;
      startY = touches[0].clientY;
      return null;
    },
    move(touches) {
      if (!tracking) return null;
      // A second finger is a pinch or a two-finger scroll. Either way
      // it is not this gesture, and a gesture that keeps half of a
      // pinch is worse than one that lets go of all of it.
      if (touches.length !== 1) {
        const was = claimed;
        idle();
        return was ? { kind: "cancel" } : null;
      }
      const dy = touches[0].clientY - startY;
      const dx = Math.abs(touches[0].clientX - startX);
      if (!claimed) {
        if (dy <= PULL_SLOP && dx <= PULL_SLOP) return null;
        // Upward, or more sideways than down: this drag was never ours.
        if (dy <= 0 || dy < dx * PULL_STRAIGHTNESS) {
          idle();
          return null;
        }
        claimed = true;
      }
      // Dragged back above where it started: `distance` gives nothing,
      // so letting go there does nothing at all. The reader changed
      // their mind mid-pull, which is an answer.
      travelled = distance(dy);
      return { kind: "move", distance: travelled, armed: dy >= PULL_THRESHOLD };
    },
    end() {
      if (!claimed) {
        idle();
        return null;
      }
      const refresh = travelled >= PULL_THRESHOLD;
      idle();
      return { kind: "release", refresh };
    },
    cancel() {
      const was = claimed;
      idle();
      return was ? { kind: "cancel" } : null;
    },
  };
}

/**
 * refreshRunner is what a pull, a button and a shortcut all call.
 *
 * One run at a time. An ask that lands while a run is in flight joins
 * it and is answered by the same result, because two identical
 * refreshes over the same shelf are one refresh the reader asked for
 * twice.
 *
 * A *quiet* refresh — one nobody asked for — never raises the spinner,
 * which is the distinction `LibraryRefresh` draws between `scanQuietly`
 * and `all`. It is also debounced: coming back to a page that was
 * refreshed a moment ago should not walk the whole catalog again.
 *
 * `work` may reject. It is reported once, through `onState`, and never
 * thrown at the caller: a refresh that failed leaves what was already
 * on screen, which is more than an error page would.
 */
export function refreshRunner({
  work, onState = () => {}, now = () => Date.now(), quietInterval = 60_000,
}) {
  let running = null, visible = false, lastQuiet = 0;
  const announce = (state, error) => {
    onState(state, error);
  };
  const run = (loud) => {
    if (running) {
      // A quiet refresh arriving over a loud one must not lower the
      // spinner when it finishes; a loud one over a quiet one raises it.
      if (loud && !visible) {
        visible = true;
        announce("refreshing");
      }
      return running;
    }
    visible = loud;
    if (loud) announce("refreshing");
    running = (async () => {
      try {
        // Work that finished may still name what it finished as: a
        // refresh where one half of the errand failed is neither a
        // failure nor a plain success, and only the caller knows.
        const outcome = await work();
        if (visible) announce(typeof outcome === "string" ? outcome : "done");
        return true;
      } catch (error) {
        if (visible) announce("failed", error);
        return false;
      } finally {
        running = null;
        visible = false;
      }
    })();
    return running;
  };
  return {
    /** The reader asked. Spin. */
    ask: () => run(true),
    /** Nobody asked. Refresh at most every `quietInterval`. */
    quietly() {
      const at = now();
      if (at - lastQuiet < quietInterval) return Promise.resolve(false);
      lastQuiet = at;
      return run(false);
    },
    get busy() { return running !== null; },
  };
}
