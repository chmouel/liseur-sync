const STORAGE_KEY = "liseur.reader.audio-panel.v1";
const EDGES = ["top", "right", "bottom", "left", "bottom-right", "free"];
const clamp = (n, min, max) => Math.max(min, Math.min(max, n));
const defaults = () => ({ size: "mini", edge: "bottom-right", x: 1, y: 1 });

export function panelPreferences(value) {
  if (!value || !["mini", "expanded"].includes(value.size) ||
      !EDGES.includes(value.edge) || !Number.isFinite(value.x) || !Number.isFinite(value.y)) {
    return defaults();
  }
  return { size: value.size, edge: value.edge, x: clamp(value.x, 0, 1), y: clamp(value.y, 0, 1) };
}

export function panelPosition(prefs, bounds) {
  const { left, top, right, bottom } = bounds;
  let x = left + prefs.x * (right - left);
  let y = top + prefs.y * (bottom - top);
  if (["right", "bottom-right"].includes(prefs.edge)) x = right;
  if (prefs.edge === "left") x = left;
  if (["bottom", "bottom-right"].includes(prefs.edge)) y = bottom;
  if (prefs.edge === "top") y = top;
  return { x: clamp(x, left, right), y: clamp(y, top, bottom) };
}

export function panelPlacement(position, bounds, snap = true) {
  const { left, top, right, bottom } = bounds;
  const x = clamp(position.x, left, right);
  const y = clamp(position.y, top, bottom);
  const distances = [["left", x - left], ["right", right - x], ["top", y - top], ["bottom", bottom - y]];
  distances.sort((a, b) => a[1] - b[1]);
  return {
    edge: snap && distances[0][1] <= 24 ? distances[0][0] : "free",
    x: right > left ? (x - left) / (right - left) : 0,
    y: bottom > top ? (y - top) / (bottom - top) : 0,
  };
}

export function audioPanel(panel, { toolbar, footer, stage, toggle, win = window } = {}) {
  if (!panel) return { setOpen() {}, destroy() {} };
  const doc = panel.ownerDocument;
  const handle = panel.querySelector("[data-audio-handle]");
  const sizeButton = panel.querySelector("[data-audio-size]");
  const details = panel.querySelector("[data-audio-details]");
  const transport = panel.querySelector(".reader-audio-transport");
  const header = panel.querySelector(".reader-audio-header");
  const announcement = panel.querySelector("[data-audio-announcement]");
  let prefs;
  try { prefs = panelPreferences(JSON.parse(win.localStorage.getItem(STORAGE_KEY))); }
  catch { prefs = defaults(); }
  let drag = null;
  let shield = null;
  let bounds;
  let docked = false;
  let frame = 0;
  let destroyed = false;
  const listeners = [];
  function on(target, event, fn, options) {
    if (!target) return;
    target.addEventListener(event, fn, options);
    listeners.push(() => target.removeEventListener(event, fn, options));
  }
  function save() {
    try { win.localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs)); } catch {}
  }
  function announce() {
    announcement.textContent = prefs.edge === "free" ? "Player moved" : "Player docked " + prefs.edge.replace("-", " ");
  }
  function place(position) {
    panel.style.left = `${position.x}px`;
    panel.style.top = `${position.y}px`;
  }
  function layout() {
    frame = 0;
    if (destroyed || panel.hidden) {
      stage?.style.removeProperty("--reader-audio-inset");
      return;
    }
    const vp = win.visualViewport;
    const width = vp?.width || win.innerWidth;
    const height = vp?.height || win.innerHeight;
    const left = vp?.offsetLeft || 0;
    const top = vp?.offsetTop || 0;
    const style = win.getComputedStyle(panel);
    const safe = side => parseFloat(style.getPropertyValue(`--audio-safe-${side}`)) || 0;
    const footerHeight = footer?.getBoundingClientRect().height || 0;
    const minX = left + Math.max(12, safe("left"));
    const minY = Math.max(top + Math.max(12, safe("top")), (toolbar?.getBoundingClientRect().bottom || 0) + 12);
    const endX = left + width - Math.max(12, safe("right"));
    const endY = top + height - Math.max(12, safe("bottom"), footerHeight + 12);
    panel.style.maxWidth = `${Math.max(0, endX - minX)}px`;
    const boxHeight = ["padding-top", "padding-bottom", "border-top-width", "border-bottom-width"]
      .reduce((total, property) => total + (parseFloat(style.getPropertyValue(property)) || 0), 0);
    const headerGap = parseFloat(win.getComputedStyle(header).getPropertyValue("margin-bottom")) || 0;
    const compactHeight = Math.ceil(header.getBoundingClientRect().height + transport.getBoundingClientRect().height + boxHeight + headerGap);
    docked = width <= 700 || endY - minY < compactHeight + 24;
    panel.dataset.audioLayout = docked ? "dock" : "floating";
    handle.hidden = docked;
    panel.style.maxHeight = `${Math.max(0, docked ? Math.min(320, height * 0.45, endY - minY) : endY - minY)}px`;
    const rect = panel.getBoundingClientRect();
    bounds = { left: minX, top: minY, right: Math.max(minX, endX - rect.width), bottom: Math.max(minY, endY - rect.height) };
    place(docked ? { x: left + (width - rect.width) / 2, y: bounds.bottom } : panelPosition(prefs, bounds));
    if (docked) stage?.style.setProperty("--reader-audio-inset", `${compactHeight + footerHeight + 12}px`);
    else stage?.style.removeProperty("--reader-audio-inset");
  }
  function schedule() {
    if (!destroyed && !frame) frame = win.requestAnimationFrame(layout);
  }
  function finishDrag(cancel = false) {
    if (!drag) return;
    const previous = drag;
    drag = null;
    shield?.remove();
    shield = null;
    panel.removeAttribute("data-audio-dragging");
    if (handle.hasPointerCapture?.(previous.id)) handle.releasePointerCapture(previous.id);
    if (cancel) prefs = previous.prefs;
    else if (previous.moved) {
      prefs = { ...prefs, ...panelPlacement(previous.position, bounds) };
      save();
      announce();
    }
    layout();
  }
  function renderSize() {
    panel.dataset.audioSize = prefs.size;
    details.hidden = prefs.size === "mini";
    sizeButton.setAttribute("aria-expanded", String(!details.hidden));
    sizeButton.setAttribute("aria-label", details.hidden ? "Expand audiobook player" : "Minimize audiobook player");
    sizeButton.title = details.hidden ? "Expand player" : "Minimize player";
    sizeButton.textContent = details.hidden ? "⤢" : "−";
  }
  on(sizeButton, "click", () => {
    finishDrag(true);
    if (prefs.size === "expanded" && details.contains(doc.activeElement)) sizeButton.focus();
    prefs = { ...prefs, size: prefs.size === "mini" ? "expanded" : "mini" };
    renderSize();
    save();
    layout();
  });
  for (const button of panel.querySelectorAll("[data-audio-dock]")) {
    on(button, "click", () => {
      finishDrag(true);
      prefs = { ...prefs, edge: button.dataset.audioDock, x: 1, y: 1 };
      save();
      layout();
      announce();
    });
  }
  on(handle, "pointerdown", event => {
    if (docked || drag || event.button !== 0 || !event.isPrimary || !bounds) return;
    event.preventDefault();
    handle.focus();
    drag = { id: event.pointerId, startX: event.clientX, startY: event.clientY,
      origin: panelPosition(prefs, bounds), prefs: { ...prefs }, moved: false };
    drag.position = drag.origin;
    handle.setPointerCapture(event.pointerId);
  });
  on(handle, "pointermove", event => {
    if (!drag || event.pointerId !== drag.id) return;
    const dx = event.clientX - drag.startX, dy = event.clientY - drag.startY;
    if (!drag.moved && Math.hypot(dx, dy) < 6) return;
    if (!drag.moved) {
      drag.moved = true;
      shield = doc.createElement("div");
      shield.className = "reader-audio-drag-shield";
      doc.body.append(shield);
      panel.dataset.audioDragging = "true";
    }
    drag.position = { x: clamp(drag.origin.x + dx, bounds.left, bounds.right), y: clamp(drag.origin.y + dy, bounds.top, bounds.bottom) };
    place(drag.position);
  });
  on(handle, "pointerup", event => { if (event.pointerId === drag?.id) finishDrag(); });
  on(handle, "pointercancel", () => finishDrag(true));
  on(handle, "lostpointercapture", () => finishDrag(true));
  on(doc, "keydown", event => {
    if (event.key === "Escape" && drag) { event.preventDefault(); finishDrag(true); }
  });
  on(handle, "keydown", event => {
    if (docked || !bounds) return;
    const delta = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[event.key];
    if (!delta) return;
    event.preventDefault();
    event.stopPropagation();
    const pos = panelPosition(prefs, bounds), step = event.shiftKey ? 40 : 10;
    prefs = { ...prefs, ...panelPlacement({ x: pos.x + delta[0] * step, y: pos.y + delta[1] * step }, bounds, false) };
    save();
    layout();
    announce();
  });
  on(win, "resize", () => { finishDrag(true); schedule(); });
  on(win.visualViewport, "resize", schedule);
  on(win.visualViewport, "scroll", schedule);
  const observer = new win.ResizeObserver(schedule);
  for (const element of [panel, toolbar, footer]) if (element) observer.observe(element);
  renderSize();
  return {
    setOpen(open) {
      finishDrag(true);
      if (open) layout();
      else {
        stage?.style.removeProperty("--reader-audio-inset");
        toggle?.focus();
      }
    },
    destroy() {
      finishDrag(true);
      destroyed = true;
      win.cancelAnimationFrame(frame);
      observer.disconnect();
      listeners.forEach(remove => remove());
      stage?.style.removeProperty("--reader-audio-inset");
    },
  };
}
