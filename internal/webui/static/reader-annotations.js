export function annotationCFI(a) {
  const fragments = a?.locator?.locations?.fragments;
  if (!Array.isArray(fragments)) return null;
  return fragments.find((s) => typeof s === "string" && s.startsWith("epubcfi(")) || null;
}

// foliate's add/delete calls yield while resolving a CFI. One queue owns all
// overlay mutations so an old add cannot land after a new set removed it.
export function annotationRenderer({ getView, current, changed }) {
  let annotations = [], colors = new Map(), failed = new Set();
  let epoch = 0, identity = null, chain = Promise.resolve();
  const drawn = new Map();
  const valid = (run, stamp) => run === epoch && current(stamp);
  const queue = (fn) => {
    chain = chain.catch(() => {}).then(fn);
    return chain;
  };
  const removeAll = async () => {
    for (const [view, cfis] of drawn) {
      for (const cfi of cfis) {
        try { await view.deleteAnnotation({ value: cfi }); } catch { /* stale CFI */ }
      }
    }
    drawn.clear();
  };
  const draw = async (run, stamp) => {
    if (!valid(run, stamp)) return;
    const view = getView();
    if (!view) return;
    for (const a of annotations) {
      if (!valid(run, stamp) || view !== getView()) return;
      const cfi = annotationCFI(a);
      if (a.kind !== "highlight" || !cfi || failed.has(a.id)) continue;
      colors.set(cfi, a.color);
      if (!drawn.has(view)) drawn.set(view, new Set());
      drawn.get(view).add(cfi);
      try { await view.addAnnotation({ value: cfi }); }
      catch { if (valid(run, stamp)) failed.add(a.id); }
    }
    if (valid(run, stamp)) changed();
  };
  return {
    annotations: () => annotations,
    failed: (id) => failed.has(id),
    color: (cfi) => colors.get(cfi),
    replace(next, stamp) {
      const run = ++epoch;
      identity = stamp;
      return queue(async () => {
        await removeAll();
        colors.clear(); failed.clear();
        if (!valid(run, stamp)) return;
        annotations = next;
        changed();
        await draw(run, stamp);
      });
    },
    clear() {
      ++epoch;
      annotations = []; colors.clear(); failed.clear();
      changed();
      return queue(removeAll);
    },
    draw() {
      const run = epoch, stamp = identity;
      return queue(() => draw(run, stamp));
    },
  };
}
