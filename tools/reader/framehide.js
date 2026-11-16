export function coalesceFrameHide(FrameManager) {
  const hide = FrameManager.prototype.hide;
  const setCSSProperties = FrameManager.prototype.setCSSProperties;
  const load = FrameManager.prototype.load;
  const show = FrameManager.prototype.show;

  // One ordered queue per frame. Readium hides a frame by asking the
  // content document to acknowledge it, so hide, show, style and load
  // calls overlap whenever a reader turns pages quickly. Every call runs
  // after the one asked for before it, which is what keeps a hidden frame
  // from being painted by a late show.
  const queue = (frame) => {
    if (!frame.visibilityQueue) frame.visibilityQueue = { tail: null, last: null };
    return frame.visibilityQueue;
  };
  const enqueue = (frame, kind, run) => {
    const q = queue(frame);
    // Consecutive hides are one intent, and the first one's
    // acknowledgement serves both. A show in between is a different
    // intent, so a hide after it gets its own turn in the queue.
    if (kind === "hide" && q.last?.kind === "hide") return q.last.promise;
    let promise;
    if (q.tail) {
      promise = q.tail.then(run);
    } else {
      // Nothing is in flight: start now, so a reader's first hide or show
      // is not delayed by a turn of the event loop.
      try {
        promise = Promise.resolve(run());
      } catch (error) {
        promise = Promise.reject(error);
      }
    }
    const entry = { kind, promise };
    q.last = entry;
    const tail = promise.then(
      () => {},
      () => {},
    );
    q.tail = tail;
    tail.then(() => {
      if (q.tail === tail) q.tail = null;
      if (q.last === entry) q.last = null;
    });
    return promise;
  };

  FrameManager.prototype.hide = function () {
    return enqueue(this, "hide", () => hide.call(this));
  };
  FrameManager.prototype.show = function (...args) {
    return enqueue(this, "show", () => show.apply(this, args));
  };
  FrameManager.prototype.setCSSProperties = function (...args) {
    // A style write is not a visibility change: it is owed to whatever was
    // asked for before it, and its failure is not the reader's.
    enqueue(this, "style", () => setCSSProperties.apply(this, args)).catch(() => {});
  };
  FrameManager.prototype.load = function (...args) {
    return enqueue(this, "load", () => load.apply(this, args));
  };
}
