// The thin layer between real touch events and `pull-refresh.js`.
//
// Everything here is a browser fact rather than a decision: which
// scroller the finger is on, whether this browser will let the gesture
// be taken from it, and where the indicator sits. The decisions live
// next door, where they can be tested without a browser.
//
// The one browser fact worth writing down: Chrome on Android and Safari
// on iOS both reload the page on this very gesture, and they decide to
// before the first cancellable `touchmove` arrives, so `preventDefault`
// alone does not reliably take it back. `overscroll-behavior-y:
// contain` does. Where that property is not understood — older iOS —
// the gesture is not armed at all and the browser keeps its own
// refresh, which is a coarse version of the same wish. The button is
// the precise one, and it is always there.

import { pullGesture } from "./pull-refresh.js";

/** Whether this browser will hand over the gesture at all. */
export function pullSupported(win = window) {
  return !!win.CSS?.supports?.("overscroll-behavior-y", "contain") &&
    !!win.matchMedia?.("(pointer: coarse)")?.matches &&
    "ontouchstart" in win;
}

// A touch that began inside something else's gesture is not ours. A
// horizontal scroller (the insights heatmap) and an open menu both
// take drags of their own, and a form control's own dragging is its
// business.
function claimable(target) {
  for (let node = target; node instanceof Element; node = node.parentElement) {
    if (node.hasAttribute("data-no-pull")) return false;
    if (node.tagName === "DETAILS" && node.open) return false;
    if (node.tagName === "INPUT" || node.tagName === "TEXTAREA" || node.tagName === "SELECT")
      return false;
    if (node.scrollWidth > node.clientWidth + 1) {
      const overflow = getComputedStyle(node).overflowX;
      if (overflow === "auto" || overflow === "scroll") return false;
    }
  }
  return true;
}

/**
 * pullIndicator owns the little spinner: what it says and where it
 * sits. It is separate from the gesture because the button raises the
 * same spinner on a machine with no touchscreen at all.
 */
export function pullIndicator(element, win = window) {
  let clearing = null;
  const paint = (state, distance = 0, armed = false) => {
    if (!element) return;
    win.clearTimeout(clearing);
    element.dataset.state = state;
    element.hidden = state === "idle";
    element.classList.toggle("armed", armed);
    element.style.transform = distance > 0 ? `translateY(${Math.round(distance)}px)` : "";
    // "Refreshing" is not an outcome and must not be announced: the
    // spinner is visible, and a live region repeating it on every pull
    // is noise. Only what happened is worth saying.
    element.textContent = state === "failed"
      ? "Could not refresh."
      : state === "done" ? "Refreshed." : "";
  };
  return {
    paint,
    /** Say what happened, then go quiet. */
    settle(state) {
      paint(state);
      if (state !== "idle") clearing = win.setTimeout(() => paint("idle"), 2000);
    },
  };
}

/**
 * armPull wires a scroller, an indicator and a refresh runner together,
 * and returns the way to undo it — including when nothing was armed, so
 * a caller never has to ask which happened.
 */
export function armPull({ scroller, indicator, runner, win = window }) {
  if (!scroller || !runner || !pullSupported(win)) return () => {};

  const doc = scroller.ownerDocument || win.document;
  const target = scroller === doc.scrollingElement ? doc.documentElement : scroller;
  const previous = target.style.overscrollBehaviorY;
  target.style.overscrollBehaviorY = "contain";

  const atTop = () => (scroller.scrollTop || 0) <= 0;
  const pull = pullGesture({ atTop, enabled: () => !runner.busy });
  const paint = (state, distance, armed) => indicator?.paint(state, distance, armed);

  let claiming = false;
  const onStart = (event) => {
    claiming = claimable(event.target);
    if (claiming) pull.start(event.touches);
  };
  const onMove = (event) => {
    if (!claiming) return;
    const verdict = pull.move(event.touches);
    if (!verdict) return;
    if (verdict.kind === "cancel") {
      paint("idle");
      return;
    }
    // Only now, once the gesture is certainly ours, is the page held
    // still. Calling this on every move would take scrolling away.
    if (event.cancelable) event.preventDefault();
    paint("pulling", verdict.distance, verdict.armed);
  };
  const onEnd = () => {
    const verdict = pull.end();
    claiming = false;
    if (!verdict) return;
    if (!verdict.refresh) {
      paint("idle");
      return;
    }
    void runner.ask();
  };
  const onCancel = () => {
    claiming = false;
    if (pull.cancel()) paint("idle");
  };

  scroller.addEventListener("touchstart", onStart, { passive: true });
  scroller.addEventListener("touchmove", onMove, { passive: false });
  scroller.addEventListener("touchend", onEnd, { passive: true });
  scroller.addEventListener("touchcancel", onCancel, { passive: true });

  return () => {
    scroller.removeEventListener("touchstart", onStart);
    scroller.removeEventListener("touchmove", onMove);
    scroller.removeEventListener("touchend", onEnd);
    scroller.removeEventListener("touchcancel", onCancel);
    target.style.overscrollBehaviorY = previous;
  };
}
