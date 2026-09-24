// Replacement for FramePoolManager.update() in the pinned Readium 2.8.2
// build. build.mjs splices update's source text into the minified module in
// place of the original method, which puts it in that module's scope: `y`
// is FrameBlobBuilder, `P` is FrameManager, and `b` and `m` are the pool's
// prune and preload windows in positions (5 and 3). build.mjs checks those
// bindings before splicing, so a Readium upgrade fails the build instead of
// silently running this against a different module. Node only imports this
// file to read the source text; nothing here runs outside the bundle.
//
// Two things differ from upstream, and both are about turning into a new
// chapter:
//
// - The chapters on either side of the current one stay in the pool as
//   hidden, loaded frames for as long as the current one is shown. Upstream
//   measures its window in positions (about a kilobyte of text each), so
//   it did not build the next chapter's iframe until the reader stood on
//   the last screen or two of this one, and often not until the turn
//   itself. Script injection, the iframe load and column layout then all
//   happened while the reader waited.
// - Only the current frame is awaited before it is shown. Upstream waited
//   for every frame entering its window first, so a turn into a short
//   chapter also paid for building the one after it. The rest is now built
//   in the background: forward first, then behind, one at a time.
//
// Background builds overlap later updates, which upstream never had to
// handle. `building` makes one build per href at a time, so two updates
// never attach two frames for one chapter. `generation` discards a build
// started before a forced reload or a base URL change, `keep` discards one
// that finished after the reader moved away from it, and `ticket` leaves
// showing a frame to the newest update, so an older one that finishes last
// cannot hide what the reader turned to.

/* global y, P, b, m */
export async function update(pub, locator, modules, force = false) {
  const FrameBlobBuilder = y, FrameManager = P, prunePositions = b, preloadPositions = m;
  const index = this.positions.findIndex(p => p.locations.position === locator.locations.position);
  if (index < 0) throw Error(`Locator not found in position list: ${locator.locations.position}`);
  const current = this.positions[index].href;
  if (this.inprogress.has(current)) await this.inprogress.get(current).catch(() => {});
  this.building ??= new Map();
  this.generation ??= 0;
  const release = url => { this.injector?.releaseBlobUrl?.(url); URL.revokeObjectURL(url); };

  const ticket = this.ticket = {};
  const job = (async () => {
    const prune = new Set(), window = new Set();
    this.positions.forEach((position, i) => {
      if (i > index + prunePositions || i < index - prunePositions) prune.add(position.href);
      if (i < index + preloadPositions && i > index - preloadPositions) window.add(position.href);
    });
    const order = pub.readingOrder.findIndexWithHref(current);
    const keep = new Set(window);
    for (const neighbour of [pub.readingOrder.items[order + 1], order > 0 ? pub.readingOrder.items[order - 1] : null]) {
      if (neighbour?.href) keep.add(neighbour.href);
    }
    this.keep = keep;
    for (const href of prune) {
      if (keep.has(href) || !this.pool.has(href)) continue;
      const frame = this.pool.get(href);
      this.pool.delete(href);
      if (this.pendingUpdates.has(href)) this.pendingUpdates.set(href, { inPool: false });
      frame.destroy().catch(() => {});
    }
    if (force || (this.currentBaseURL !== undefined && pub.baseURL !== this.currentBaseURL)) {
      this.blobs.forEach(release);
      this.blobs.clear();
      if (force) this.pendingUpdates.clear();
      this.generation++;
    }
    this.currentBaseURL = pub.baseURL;

    // This update's own chapter is never dropped for leaving `keep`: only a
    // newer update changes `keep`, and that one decides what is shown.
    const stale = (generation, href) => this.closed || generation !== this.generation || (href !== current && !this.keep.has(href));
    const load = async href => {
      if (this.pendingUpdates.get(href)?.inPool === false) {
        const url = this.blobs.get(href);
        if (url) { release(url); this.blobs.delete(href); this.pendingUpdates.delete(href); }
      }
      if (this.pool.has(href)) {
        const frame = this.pool.get(href);
        if (this.blobs.has(href)) { await frame.load(modules); return; }
        this.pool.delete(href);
        this.pendingUpdates.delete(href);
        await frame.destroy();
      }
      const link = pub.readingOrder.findWithHref(href);
      if (!link) return;
      const generation = this.generation;
      if (!this.blobs.has(href)) {
        const url = await new FrameBlobBuilder(pub, this.currentBaseURL || "", link, { cssProperties: this.currentCssProperties, injector: this.injector }).build();
        if (this.closed || generation !== this.generation) { release(url); return; }
        this.blobs.set(href, url);
      }
      if (stale(generation, href)) return;
      const frame = new FrameManager(this.blobs.get(href), this.contentProtectionConfig, this.keyboardPeripheralsConfig, this.getFragmentIds(href));
      if (href !== current) await frame.hide();
      this.container.appendChild(frame.iframe);
      await frame.load(modules);
      if (stale(generation, href)) { await frame.destroy(); return; }
      this.pool.set(href, frame);
    };
    const ensure = async href => {
      while (this.building.has(href)) await this.building.get(href).catch(() => {});
      if (this.closed) return;
      const attempt = load(href);
      this.building.set(href, attempt);
      try { await attempt; }
      finally { if (this.building.get(href) === attempt) this.building.delete(href); }
    };

    let failure;
    try { await ensure(current); }
    catch (error) { failure = error; }
    const frame = this.pool.get(current);
    if (this.ticket === ticket && !this.closed && (frame?.source !== this._currentFrame?.source || force)) {
      await this._currentFrame?.hide();
      if (frame) {
        await frame.load(modules);
        await frame.show(locator.locations.progression);
      }
      this._currentFrame = frame;
      const active = frame && this.container.ownerDocument.activeElement;
      if (active && active.tagName === "IFRAME" && active !== frame.iframe) frame.iframe.focus({ preventScroll: true });
    }

    const at = href => pub.readingOrder.findIndexWithHref(href);
    const rest = [...keep].filter(href => href !== current);
    const ahead = rest.filter(href => at(href) > order).sort((x, z) => at(x) - at(z));
    const behind = rest.filter(href => at(href) <= order).sort((x, z) => at(z) - at(x));
    (async () => {
      for (const href of [...ahead, ...behind]) {
        if (this.closed || !this.keep.has(href)) continue;
        try { await ensure(href); } catch { /* A neighbour is a hint; the turn into it builds it again and reports its own failure. */ }
      }
    })();
    if (failure) throw failure;
  })();

  this.inprogress.set(current, job);
  try { await job; }
  finally { if (this.inprogress.get(current) === job) this.inprogress.delete(current); }
}
