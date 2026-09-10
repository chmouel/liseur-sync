import { reconcileReadingState } from "./reader-reconcile.js";

// Match Android's bounded fallback past malformed position records. A real
// zero is readable; missing, null and out-of-range fractions are not.
export function latestReadablePosition(ops, workID) {
  if (!Array.isArray(ops)) return null;
  const fraction = value => typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 1;
  return ops.find(op => op?.op_id && op.work_id === workID && fraction(op.progression)) || null;
}

export function positionAcknowledged(body, op) {
  const result = body?.results?.[0];
  return result?.op_id === op.op_id && ["applied", "duplicate"].includes(result.status);
}

// Coalescing is per topic. An event during a read owes one more read; failure
// keeps that obligation even if no later event arrives.
export function topicRefresh({
  refresh, setTimer = setTimeout, clearTimer = clearTimeout, delay = 1500,
}) {
  let active = false, running = false, timer = null, epoch = 0;
  let batchInFlight = [];
  const owed = new Set();
  const drain = async () => {
    timer = null;
    if (!active || running || !owed.size) return;
    running = true;
    const run = epoch;
    const batch = [...owed];
    batchInFlight = batch;
    batch.forEach((topic) => owed.delete(topic));
    try {
      for (const topic of batch) {
        if (!active || epoch !== run) break;
        try {
          if (await refresh(topic) === false && epoch === run) owed.add(topic);
        } catch {
          if (epoch === run) owed.add(topic);
        }
      }
    } finally {
      running = false;
      batchInFlight = [];
      schedule();
    }
  };
  const schedule = () => {
    if (active && !running && !timer && owed.size) timer = setTimer(drain, delay);
  };
  return {
    owe(topics) {
      for (const topic of topics)
        if (topic === "positions" || topic === "annotations") owed.add(topic);
      schedule();
    },
    start() { active = true; schedule(); },
    stop() {
      active = false; epoch++;
      batchInFlight.forEach((topic) => owed.add(topic));
      clearTimer(timer); timer = null;
    },
    reset() {
      epoch++; owed.clear(); batchInFlight = [];
      clearTimer(timer); timer = null;
    },
  };
}

export function candidateID(account, work, op) {
  if (!op?.op_id || !op.device_id) return null;
  return JSON.stringify([account, work, op.op_id, op.device_id]);
}

/**
 * catchupState decides what, if anything, to say about a position that
 * arrived from somewhere else.
 *
 * It holds the three sides of the comparison — the agreed baseline, this
 * device's own position, and the newest remote one — and asks
 * `reconcileReadingState` which of them moved. A remote move on its own
 * becomes an offer; a move on both sides becomes a conflict carrying
 * both positions, because there is no honest way to pick one.
 *
 * Baseline, local position and dirtiness are fed in from durable
 * storage, so a reload does not turn a disagreement back into a bare
 * offer.
 *
 * Presentation is deliberately not immediate. An offer waits for a
 * moment when the reader is not mid-page: opening the book, or coming
 * back to the tab. A conflict waits for the same moment rather than
 * interrupting, which is what the Android client does with a conflict
 * it has preserved.
 */
export function catchupState() {
  let account = null, work = null, device = null, generation = 0;
  let candidate = null, offer = null, hidden = false, resume = false;
  let baseline = null, remote = null, localOp = null, localDirty = false;
  const authored = new Map();
  const ignored = new Set();
  const identify = (op) => candidateID(account, work, op);
  const ours = (op) => authored.has(op.op_id) && authored.get(op.op_id) === op.device_id;
  const evaluate = () => {
    const id = remote && identify(remote);
    if (!id || ignored.has(id) || ours(remote)) {
      candidate = null;
      return;
    }
    const { decision } = reconcileReadingState({
      // Having authored nothing since the book opened is not having no
      // position: this device is where the baseline says it is. Without
      // that, every remote echo of the opening position looks like an
      // invitation to somewhere else.
      local: localOp || baseline, remote, baseline, localDirty,
    });
    if (decision !== "pull" && decision !== "conflict") {
      candidate = null;
      return;
    }
    candidate = {
      id, kind: decision, op: structuredClone(remote),
      local: structuredClone(localOp || baseline), generation,
    };
  };
  // The reader has answered about this remote position by staying where
  // they are. It becomes the agreed baseline — it has been seen and
  // answered — and its id is remembered so it is never raised again.
  const refuse = (op) => {
    const id = op && identify(op);
    if (id) {
      ignored.add(id);
      baseline = structuredClone(op);
    }
    if (ignored.size > 256) ignored.delete(ignored.values().next().value);
    offer = null; resume = false;
    evaluate();
  };
  // The reader is going there. It is now both the agreed baseline and
  // this device's own position, and this device owes nothing.
  const adopt = (op) => {
    if (!op) return null;
    baseline = structuredClone(op);
    localOp = structuredClone(op);
    localDirty = false;
    candidate = null; offer = null; resume = false;
    return structuredClone(op);
  };
  return {
    bind(nextAccount, nextWork, nextDevice) {
      const sameBook = account === nextAccount && work === nextWork;
      account = nextAccount; work = nextWork; device = nextDevice;
      generation++; candidate = null; offer = null;
      if (!sameBook) resume = false;
      if (!sameBook) {
        baseline = null; remote = null; localOp = null; localDirty = false;
        authored.clear(); ignored.clear();
      }
    },
    // The position this device and the server last agreed on.
    baseline(op) { baseline = op ? structuredClone(op) : null; evaluate(); },
    // This device's durable position, and whether the server has it.
    local(op, dirty = !!op) {
      localOp = op ? structuredClone(op) : null;
      localDirty = !!op && dirty;
      evaluate();
    },
    // settled folds an acknowledged local op into the baseline: it is
    // now what both sides know, so it is no longer local movement.
    settled(op) {
      if (!op) return;
      baseline = structuredClone(op);
      if (localOp?.op_id === op.op_id) localDirty = false;
      evaluate();
    },
    wrote(op) {
      authored.set(op.op_id, device);
      if (authored.size > 256) authored.delete(authored.keys().next().value);
    },
    // The position this device and the server last agreed on, which is
    // what tells this reader's own stale copy on the server apart from
    // another device having moved.
    agreed() {
      return baseline ? structuredClone(baseline) : null;
    },
    observe(op) {
      remote = op ? structuredClone(op) : null;
      evaluate();
    },
    hide() { hidden = true; resume = false; offer = null; },
    resume() {
      if (!hidden) return;
      hidden = false; resume = true;
    },
    // present marks a moment where an offer may be shown without
    // interrupting: the book opening, or a reload landing on a
    // disagreement that was already there.
    present() { if (!hidden) resume = true; },
    offer() {
      if (hidden || !resume || offer) return offer;
      resume = false;
      if (candidate?.generation === generation) offer = structuredClone(candidate);
      return offer;
    },
    shown: () => offer,
    // The reader turned a page. That does not settle the other device's
    // position — it disagrees with it. The offer comes down, the
    // knowledge stays, and the next quiet moment presents a conflict.
    moved() {
      offer = null; resume = false;
      if (!localDirty) {
        localDirty = true;
        evaluate();
      }
    },
    dismiss() { refuse(offer ? offer.op : null); },
    refuse,
    adopt,
    accept(shown) {
      if (!shown || hidden || offer !== shown || shown.generation !== generation ||
          shown.id !== identify(shown.op) || candidate?.id !== shown.id) {
        offer = null;
        return null;
      }
      return adopt(shown.op);
    },
  };
}
