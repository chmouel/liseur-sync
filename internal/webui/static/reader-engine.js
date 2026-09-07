import { EpubNavigator, Publication, Manifest, Locator, setScriptNonce } from "./vendor/readium/readium.js";
import * as CFI from "./vendor/foliate/epubcfi.js";
import { ReaderPublication, publicationHref, imageSpineTypes } from "./reader-publication.js";

const archiveProperty = "https://readium.org/webpub-manifest/properties#archive";
const colors = { yellow: "#ffd54f", green: "#81c784", blue: "#64b5f6", pink: "#f06292", purple: "#ba68c8", orange: "#ffb74d" };
const finite = Number.isFinite;
const relations = link => Array.isArray(link.rel) ? link.rel : [link.rel];
const tocItems = links => (links || []).map(link => ({ label: link.title, href: link.href, subitems: tocItems(link.children) }));

// This adapter keeps UI gestures and the native sync lifecycle independent of
// Readium's frame pool. All publication locations exposed here are archive URLs.
export class ReaderEngine extends HTMLElement {
  constructor() {
    super();
    this.renderer = document.createElement("div");
    // Readium sets an explicit pixel width on this element directly
    // (ReadiumCSS caps a single column's width to a readable line length
    // on a wide viewport). With inset:0 alone, an explicit width leaves
    // the box flush left: left and right are both already pinned to 0,
    // so the browser's over-constrained resolution drops "right" rather
    // than centering. margin:auto on left/right is what makes that same
    // resolution split the leftover space evenly instead.
    this.renderer.style.cssText = "position:absolute;inset:0;overflow:hidden;margin:0 auto";
    this.renderer.getContents = () => this.contents();
    this.renderer.goTo = target => this.goTo(target);
    this.renderer.render = () => { if (this.navigator) this.relocate(this.navigator.currentLocator); };
    this.annotations = new Map();
    this.settings = {};
    this.styleText = "";
    this.preferences = {};
    this.history = { pushState() {} };
  }

  connectedCallback() {
    this.style.cssText = "position:absolute;inset:0;display:block";
    this.append(this.renderer);
  }

  async open({ request, current, bookID }) {
    this.request = request;
    const response = await request("v1/books/" + encodeURIComponent(bookID) + "/publication/manifest.json");
    if (!response.ok) throw Error("This publication could not be opened.");
    const raw = await response.json();
    if (!current(response)) throw Error("The reader credential changed. Open the book again.");
    const positionLink = raw.links.find(link => relations(link).includes("http://readium.org/position-list"));
    const packageLink = raw.links.find(link => relations(link).includes("package"));
    if (!positionLink || !packageLink) throw Error("The publication has no positions or package document.");
    const prefix = packageLink.href.slice(0, packageLink.href.indexOf("/resources/") + 11);
    const packageHref = packageLink.href.slice(prefix.length);
    const positionResponse = await request(positionLink.href.replace(/^\//, ""));
    if (!positionResponse.ok) throw Error("Publication positions could not be loaded.");
    const list = await positionResponse.json();
    if (!current(positionResponse)) throw Error("The reader credential changed. Open the book again.");
    const normalize = value => {
      if (!value || typeof value !== "object") return;
      if (typeof value.href === "string" && value.href.startsWith(prefix)) value.href = value.href.slice(prefix.length);
      for (const child of Object.values(value)) normalize(child);
    };
    normalize(raw); normalize(list);
    this.positionList = (list.positions || []).map(value => Locator.deserialize(value));
    if (!this.positionList.length || this.positionList.some(value => !value)) throw Error("The publication has no usable positions.");
    this.resources = new ReaderPublication({ request, current, prefix: prefix.replace(/^\//, ""), manifest: raw, packageHref });
    // Readium's frame builder only routes a spine item through our fetcher
    // when its type says HTML; an SVG or bitmap spine item is otherwise
    // built into an <img> frame pointed straight at item.toURL(baseURL),
    // which resolves against our fake self link rather than a real
    // resource endpoint. Declaring these as XHTML to Readium sends them
    // through the normal document path instead, where ReaderPublication
    // recognizes the (still-true) original type from its own entry map
    // and serves a wrapper document embedding the asset as a blob image.
    const forReadium = { ...raw, readingOrder: raw.readingOrder.map(link =>
      imageSpineTypes.has(link.type) ? { ...link, type: "application/xhtml+xml" } : link) };
    const manifest = Manifest.deserialize(forReadium);
    if (!manifest) throw Error("Invalid publication manifest.");
    // A local virtual base makes Readium's link resolution deterministic. All
    // actual requests use ReaderPublication, and all rendered assets are blobs.
    manifest.setSelfLink(new URL("publication/manifest.json", location.href).href);
    this.publication = new Publication({ manifest, fetcher: this.resources });
    this.packageDocument = await this.resources.document(packageHref, false);
    this.packageHref = packageHref;
    this.book = {
      metadata: raw.metadata,
      toc: tocItems(raw.toc),
      sections: raw.readingOrder.map(link => ({ id: link.href, size: link.size || 0, compressedSize: link.properties?.[archiveProperty]?.entryLength, linear: "yes" })),
    };
    this.fixed = raw.metadata?.presentation?.layout === "fixed" || raw.metadata?.layout === "fixed";
    if (this.fixed) for (const section of this.book.sections) section.compressedSize = 1;
    this.sectionPositions = this.book.sections.map(section => this.positionList.filter(p => p.href === section.id));
    const nonce = document.querySelector('script[type="module"][nonce]')?.nonce;
    if (!nonce) throw Error("The reader's script policy is missing.");
    setScriptNonce(nonce);
  }

  contents() {
    return [...this.renderer.querySelectorAll("iframe")]
      .filter(frame => frame.style.visibility !== "hidden" && frame.contentDocument?.body)
      .map(frame => ({ doc: frame.contentDocument, index: this.book.sections.findIndex(s => s.id === frame.contentDocument.documentElement.dataset.readerHref) }));
  }

  frameLoaded(wnd) {
    const doc = wnd.document;
    const href = doc.documentElement.getAttribute("data-reader-href");
    this.applyDocumentStyle(doc);
    doc.addEventListener("click", event => {
      const link = event.target.closest?.("a[data-reader-href]");
      if (!link) return;
      event.preventDefault(); event.stopPropagation();
      const navigation = new CustomEvent("link", { cancelable: true });
      if (this.dispatchEvent(navigation)) this.goTo(link.dataset.readerHref).catch(() => {});
    }, true);
    this.dispatchEvent(new CustomEvent("load", { detail: { doc, index: this.book.sections.findIndex(s => s.id === href) } }));
    this.drawAnnotations().catch(() => {});
  }

  async init({ lastLocation }) {
    const target = lastLocation == null ? this.positionList[0] : await this.targetLocator(lastLocation);
    if (this.navigator) return this.navigate(target);
    this.navigator = new EpubNavigator(this.renderer, this.publication, {
      frameLoaded: wnd => this.frameLoaded(wnd),
      positionChanged: locator => this.relocate(locator),
      tap: () => true,
      click: () => true,
      handleLocator: () => false,
    }, this.positionList, target, { preferences: this.preferences, defaults: {} });
    try { await this.navigator.load(); }
    catch (error) { await this.navigator.destroy(); this.navigator = null; throw error; }
    this.relocate(this.navigator.currentLocator);
  }

  relocate(locator) {
    if (!locator) return;
    const index = this.book.sections.findIndex(s => s.id === locator.href);
    if (index < 0) return;
    const sectionFraction = locator.locations.progression || 0;
    // The server's per-position totalProgression already reflects each
    // section's real weight (bytes, not the discrete position count,
    // which a bounded fallback list may distribute unevenly across many
    // chapters); interpolate within the section's start/end bounds
    // rather than dividing by position counts.
    const bounds = this.getSectionFractions();
    const fraction = bounds[index] + sectionFraction * (bounds[index + 1] - bounds[index]);
    locator = locator.copyWithLocations({ totalProgression: fraction });
    const sizes = this.book.sections.map(s => s.size || 0);
    const remaining = (1 - sectionFraction) * sizes[index];
    const findTOC = items => {
      for (const item of items || []) {
        const child = findTOC(item.subitems);
        if (child) return child;
        if (item.href?.split("#")[0] === locator.href) return item;
      }
      return null;
    };
    this.lastLocation = {
      locator: locator.serialize(), fraction, sectionFraction,
      section: { current: index, total: this.book.sections.length },
      location: { current: (locator.locations.position || 1) - 1, total: this.positionList.length },
      time: { section: remaining / 1600, total: (remaining + sizes.slice(index + 1).reduce((a, b) => a + b, 0)) / 1600 },
      tocItem: findTOC(this.book.toc),
    };
    this.dispatchEvent(new CustomEvent("relocate", { detail: this.lastLocation }));
    this.drawAnnotations().catch(() => {});
    if (index !== this.lastWindowIndex) { this.lastWindowIndex = index; this.releaseOutOfWindow(index); }
  }

  // Coordinates our own resource cache (raw bytes, decoded blobs) with
  // Readium's active frame window instead of retaining every fetched byte
  // for the whole reading session. A chapter well outside the current
  // position also has its own built frame released from Readium's pool
  // (evict is a no-op while Readium still has it preloaded) so a stale
  // blob is not left referencing a resource we are about to release.
  releaseOutOfWindow(index) {
    if (!this.resources) return;
    const windowSize = 6;
    const keep = new Set();
    for (let i = Math.max(0, index - windowSize); i <= Math.min(this.book.sections.length - 1, index + windowSize); i++) {
      keep.add(this.book.sections[i].id);
    }
    if (this.navigator?.framePool?.evict) {
      for (const section of this.book.sections) if (!keep.has(section.id)) this.navigator.framePool.evict(section.id);
    }
    this.resources.retain(keep);
  }

  getSectionFractions() {
    return [...this.sectionPositions.map(positions => positions[0]?.locations.totalProgression || 0), 1];
  }

  cfiHref(value) {
    const parsed = CFI.parse(value);
    const parts = CFI.collapse(parsed);
    const itemref = CFI.toElement(this.packageDocument, parts[0]);
    const id = itemref?.getAttribute("idref");
    const item = [...this.packageDocument.querySelectorAll("manifest > item")].find(el => el.getAttribute("id") === id);
    const href = item && publicationHref(item.getAttribute("href"), this.packageHref);
    const resolvedHref = href && (this.resources.resolveKey(href.split("#")[0]) ?? href.split("#")[0]);
    if (!resolvedHref) throw Error("The saved CFI does not name a chapter.");
    return { href: resolvedHref, parsed };
  }

  resolveNavigation(target) {
    if (typeof target === "number") return { index: target, anchor: 0 };
    if (target && typeof target === "object") {
      if (finite(target.index)) return target;
      if (finite(target.fraction)) {
        const fraction = Math.max(0, Math.min(1, target.fraction));
        // Use the same byte-weighted section bounds relocate() reports
        // progress with, not the position list's raw index spacing: with
        // a bounded fallback list, a chapter's share of positions can be
        // far smaller than its share of the book's bytes, and indexing
        // straight into positionList would then favor small chapters
        // over the one the reader actually meant to reach.
        const bounds = this.getSectionFractions();
        let index = bounds.length - 2;
        while (index > 0 && bounds[index] > fraction) index--;
        const span = bounds[index + 1] - bounds[index];
        const anchor = span > 0 ? (fraction - bounds[index]) / span : 0;
        return { index, anchor };
      }
      if (target.href) return { index: this.book.sections.findIndex(s => s.id === target.href.split("#")[0]), locator: Locator.deserialize(target) };
    }
    if (typeof target === "string") {
      const href = target.startsWith("epubcfi(") ? this.cfiHref(target).href : target.split("#")[0];
      const index = this.book.sections.findIndex(s => s.id === href);
      if (index < 0) throw Error("The saved position does not name a chapter.");
      return { index, target };
    }
    throw Error("Invalid reading position.");
  }

  async cfiLocator(value) {
    const { href, parsed } = this.cfiHref(value);
    const doc = await this.resources.document(href, false);
    const local = Array.isArray(parsed) ? [parsed.at(-1)] : { ...parsed, parent: [parsed.parent.at(-1)] };
    const range = CFI.toRange(doc, local);
    const body = doc.querySelector("body");
    const before = doc.createRange();
    before.selectNodeContents(body); before.setEnd(range.startContainer, range.startOffset);
    const after = doc.createRange();
    after.selectNodeContents(body); after.setStart(range.endContainer, range.endOffset);
    let highlight = range.toString();
    let suffix = after.toString();
    if (!highlight) { highlight = suffix.slice(0, 48); suffix = suffix.slice(highlight.length); }
    return Locator.deserialize({ href, type: "application/xhtml+xml", locations: {}, text: {
      before: before.toString().slice(-64), highlight, after: suffix.slice(0, 64),
    } });
  }

  getCFIForRange(doc, range) {
    const href = doc?.documentElement?.dataset?.readerHref;
    const index = this.book.sections.findIndex(section => section.id === href);
    if (index < 0) throw Error("The selected text is not in a reading section.");
    // The package spine includes non-linear items omitted from reading order.
    const item = [...this.packageDocument.querySelectorAll("manifest > item")].find(el => {
      const path = publicationHref(el.getAttribute("href"), this.packageHref)?.split("#")[0];
      return path && (this.resources.resolveKey(path) ?? path) === href;
    });
    const itemref = item && [...this.packageDocument.querySelectorAll("spine > itemref")]
      .find(el => el.getAttribute("idref") === item.getAttribute("id"));
    if (!itemref) throw Error("The selected text has no package spine item.");
    return CFI.joinIndir(CFI.fromElements([itemref])[0], CFI.fromRange(range));
  }

  async targetLocator(target) {
    const resolved = this.resolveNavigation(target);
    if (resolved.locator) return resolved.locator;
    if (resolved.target?.startsWith("epubcfi(")) return this.cfiLocator(resolved.target);
    const section = this.book.sections[resolved.index];
    if (!section) throw Error("Unknown reading section.");
    const fragment = resolved.target?.split("#").slice(1).join("#");
    return Locator.deserialize({ href: section.id, type: "application/xhtml+xml", locations: {
      progression: finite(resolved.anchor) ? resolved.anchor : 0,
      fragments: fragment ? [fragment] : [],
    } });
  }

  navigate(locator) {
    return new Promise((resolve, reject) => this.navigator.go(locator, false, ok => ok ? resolve() : reject(Error("The saved position could not be reached."))));
  }
  async goTo(target) { return this.navigate(await this.targetLocator(target)); }
  goToFraction(fraction) { return this.goTo({ fraction }); }
  turn(method) { return new Promise(resolve => this.navigator?.[method](false, resolve)); }
  goRight() { return this.turn("goRight"); }
  goLeft() { return this.turn("goLeft"); }

  applySettings(settings, styleText, theme) {
    this.settings = settings; this.styleText = styleText;
    const margin = { none: 8, narrow: 16, normal: 32, wide: 48 }[settings.margin] || 32;
    // Readium's own auto column width targets a fixed character-count
    // range (its own defaults, unrelated to any setting here) and grows
    // the reading column to keep that count reachable, so widening only
    // pageGutter (the column's own inner padding) leaves the column
    // exactly as wide as before it — the padding increase is absorbed
    // by a matching growth of the column. Driving the target line
    // length itself is what actually makes "narrow" show more of the
    // viewport as text and "wide" show less, matching what a margin
    // control is expected to do. A null max (and min) lifts Readium's
    // own column-width cap entirely — "none" is the only choice where
    // the column grows to fill the available width instead of stopping
    // at a fixed character count.
    const lineLength = {
      none: { optimalLineLength: 90, minimalLineLength: null, maximalLineLength: null },
      narrow: { optimalLineLength: 90, minimalLineLength: 60, maximalLineLength: 110 },
      normal: { optimalLineLength: 65, minimalLineLength: 40, maximalLineLength: 80 },
      wide: { optimalLineLength: 48, minimalLineLength: 32, maximalLineLength: 60 },
    }[settings.margin] || { optimalLineLength: 65, minimalLineLength: 40, maximalLineLength: 80 };
    this.renderer.style.inset = `0 0 ${settings.flow === "scrolled" ? 0 : margin}px`;
    this.preferences = {
      scroll: settings.flow === "scrolled",
      columnCount: settings.columns === "auto" ? null : Number(settings.columns),
      pageGutter: margin,
      ...lineLength,
      fontSize: settings.size === 100 ? null : settings.size / 100,
      lineHeight: Number(settings.spacing) || null,
      hyphens: settings.hyphenate || null,
      textAlign: settings.justify ? "justify" : null,
      backgroundColor: theme?.bg || null,
      textColor: theme?.fg || null,
    };
    if (this.navigator) {
      // Readium may rewrite its injected stylesheet after the preference
      // promise resolves. Reapply the reader-owned sheet and inline scale
      // after that commit as well as before it.
      Promise.resolve(this.navigator.submitPreferences(this.preferences)).then(() => {
        const apply = () => {
          for (const { doc } of this.contents()) this.applyDocumentStyle(doc);
        };
        apply();
        // Preference commits can replace the visible frame on the next
        // layout turn; apply once after that replacement too.
        setTimeout(apply, 0);
      });
    }
    for (const { doc } of this.contents()) this.applyDocumentStyle(doc);
  }
  applyDocumentStyle(doc) {
    let style = doc.getElementById("liseur-reader-appearance");
    if (!style) { style = doc.createElementNS("http://www.w3.org/1999/xhtml", "style"); style.id = "liseur-reader-appearance"; doc.head.append(style); }
    style.textContent = this.styleText;
    // Readium's injected stylesheet is intentionally authoritative and can
    // be inserted after the user sheet. Keep the reader's scale as an
    // inline user preference so the setting remains effective for books
    // that pin their root size.
    const size = Number(this.settings.size);
    if (size && size !== 100) {
      doc.documentElement.style.setProperty("font-size", `${size}%`, "important");
      // Keep the measured body size stable even when the publication or
      // Readium stylesheet establishes a different root size.
      doc.body?.style.setProperty("font-size", `${16 * size / 100}px`, "important");
    } else {
      doc.documentElement.style.removeProperty("font-size");
      doc.body?.style.removeProperty("font-size");
    }
  }

  async addAnnotation({ value, color = "yellow", id = value, locator }) {
    this.annotations.set(id, { value, color, locator });
    await this.drawAnnotations();
  }
  async deleteAnnotation({ value }) { this.annotations.delete(value); await this.drawAnnotations(); }
  async drawAnnotations() {
    if (!this.navigator) return;
    if (this.drawing) {
      this.redrawPending = true;
      return;
    }
    this.drawing = true;
    try {
      const decorations = [];
      const shown = new Set(this.contents().map(({ doc }) => doc.documentElement.dataset.readerHref));
      for (const [id, entry] of this.annotations) {
        try {
          let locator = entry.locator && Locator.deserialize(entry.locator);
          if (!locator && entry.value) {
            const href = this.cfiHref(entry.value).href;
            if (!shown.has(href)) continue;
            locator = await this.cfiLocator(entry.value);
          }
          if (locator) decorations.push({ id, locator, style: { tint: colors[entry.color] || colors.yellow, enforceContrast: false } });
        } catch {
          // An unresolvable CFI (a cross-engine or otherwise malformed
          // annotation) must not take every later, valid annotation down
          // with it: skip only this one entry and keep drawing the rest.
        }
      }
      this.navigator.applyDecorations(decorations, "annotations");
    } finally {
      this.drawing = false;
      if (this.redrawPending) {
        this.redrawPending = false;
        queueMicrotask(() => this.drawAnnotations());
      }
    }
  }

  async destroy() { this.resources?.close(); await this.navigator?.destroy(); }
}
customElements.define("readium-view", ReaderEngine);
