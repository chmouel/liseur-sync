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

export function catchupState() {
  let account = null, work = null, device = null, generation = 0;
  let candidate = null, offer = null, hidden = false, resume = false, baseline = null;
  const authored = new Map();
  const ignored = new Set();
  const identify = (op) => candidateID(account, work, op);
  return {
    bind(nextAccount, nextWork, nextDevice) {
      const sameBook = account === nextAccount && work === nextWork;
      account = nextAccount; work = nextWork; device = nextDevice;
      generation++; candidate = null; offer = null;
      if (!sameBook) resume = false;
      if (!sameBook) {
        baseline = null;
        authored.clear(); ignored.clear();
      }
    },
    baseline(op) { baseline = identify(op); },
    wrote(op) {
      authored.set(op.op_id, device);
      if (authored.size > 256) authored.delete(authored.keys().next().value);
    },
    observe(op) {
      const id = identify(op);
      if (!id || id === baseline || ignored.has(id) ||
          (authored.has(op.op_id) && authored.get(op.op_id) === op.device_id)) {
        candidate = null;
        return;
      }
      candidate = { id, op: structuredClone(op), generation };
    },
    hide() { hidden = true; resume = false; offer = null; },
    resume() {
      if (!hidden) return;
      hidden = false; resume = true;
    },
    offer() {
      if (hidden || !resume || offer) return offer;
      resume = false;
      if (candidate?.generation === generation) offer = structuredClone(candidate);
      return offer;
    },
    shown: () => offer,
    moved() {
      baseline = candidate?.id || offer?.id || baseline;
      generation++; candidate = null; offer = null; resume = false;
    },
    dismiss() {
      if (offer) ignored.add(offer.id);
      if (ignored.size > 256) ignored.delete(ignored.values().next().value);
      offer = null; resume = false;
    },
    accept(shown) {
      if (!shown || hidden || offer !== shown || shown.generation !== generation ||
          shown.id !== identify(shown.op) || candidate?.id !== shown.id) {
        offer = null;
        return null;
      }
      baseline = shown.id;
      candidate = null; offer = null; resume = false;
      return structuredClone(shown.op);
    },
  };
}
