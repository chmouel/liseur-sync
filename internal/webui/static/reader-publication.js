import { css, Resource } from "./vendor/readium/readium.js";

const virtualOrigin = "https://publication.invalid/";
const documentTypes = new Set(["application/xhtml+xml", "text/html", "image/svg+xml"]);
// A spine item of one of these types is not HTML: Readium's own frame
// builder would otherwise skip our fetcher and build an <img> pointed
// directly at the manifest's (fake) self link. reader-engine.js declares
// these as XHTML to Readium instead, and ReaderPublication.get() below
// recognizes the real type from its own entry map to serve a wrapper.
export const imageSpineTypes = new Set(["image/svg+xml", "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"]);

// Resolve only archive-local references. Never pass a publisher URL to the
// authenticated request function, including protocol-relative URLs.
export function publicationHref(reference, base) {
  const url = new URL(reference, new URL(base, virtualOrigin));
  if (url.origin !== new URL(virtualOrigin).origin || url.search) return null;
  return url.pathname.slice(1) + url.hash;
}

// Decodes archive bytes that may be UTF-16 (BOM or bare, as XML permits)
// or declare a non-UTF-8 encoding, falling back to UTF-8 for everything
// else. A wrong guess must never throw: fatal is always false, and an
// unsupported label falls back rather than reject the decode.
export function decodeText(bytes, { css = false } = {}) {
  const tryDecode = label => {
    try { return new TextDecoder(label, { fatal: false }).decode(bytes); }
    catch { return null; }
  };
  // TextDecoder strips a matching BOM itself (ignoreBOM defaults to
  // false, meaning "process it", not "ignore stripping it").
  if (bytes.length >= 3 && bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) return tryDecode("utf-8") ?? "";
  if (bytes.length >= 2 && bytes[0] === 0xff && bytes[1] === 0xfe) return tryDecode("utf-16le") ?? "";
  if (bytes.length >= 2 && bytes[0] === 0xfe && bytes[1] === 0xff) return tryDecode("utf-16be") ?? "";
  // No BOM: XML permits UTF-16 signaled only by the pattern of nulls in
  // "<?xml" (or, for a document with no declaration at all, "<").
  if (bytes.length >= 4 && bytes[0] === 0x3c && bytes[1] === 0x00 && bytes[2] === 0x3f && bytes[3] === 0x00) return tryDecode("utf-16le");
  if (bytes.length >= 4 && bytes[0] === 0x00 && bytes[1] === 0x3c && bytes[2] === 0x00 && bytes[3] === 0x3f) return tryDecode("utf-16be");
  // Sniff a declared encoding from the first kilobyte, read as latin1 so
  // every byte maps to one character regardless of the real encoding.
  const head = tryDecode("latin1")?.slice(0, 1024) ?? "";
  const match = css ? head.match(/@charset\s+"([^"]+)"/i) : head.match(/<\?xml[^>]*\bencoding\s*=\s*["']([^"']+)["']/i);
  const label = match?.[1]?.trim().toLowerCase();
  if (label && label !== "utf-8" && label !== "utf8") {
    const decoded = tryDecode(label);
    if (decoded != null) return decoded;
  }
  return tryDecode("utf-8") ?? "";
}

// A publication's own hrefs (readingOrder/resources, built server-side) and
// an in-content href (an <a> or <img> as the original author escaped it) can
// name the same archive entry with different, both-valid percent-encoding —
// e.g. a manifest href leaving a comma literal while a chapter's own anchor
// escapes it as %2c. Percent-decoding both to the same characters is how we
// tell they are the same key without assuming either side's escaping style.
function canonicalize(href) {
  try { return decodeURIComponent(href); }
  catch { return href; }
}

export function stripPublicationCode(doc) {
  for (const element of [...doc.querySelectorAll("*")]) {
    const name = element.localName.toLowerCase();
    if (["script", "iframe", "object", "embed", "base", "form"].includes(name) ||
        (name === "meta" && element.hasAttribute("http-equiv"))) {
      element.remove();
      continue;
    }
    for (const attr of [...element.attributes]) {
      if (/^on/i.test(attr.localName) || ["nonce", "srcdoc", "action", "formaction", "ping"].includes(attr.localName) ||
          (/^(href|src)$/i.test(attr.localName) && /^\s*javascript:/i.test(attr.value))) {
        element.removeAttributeNode(attr);
      }
    }
  }
}

// All URLs in a publication are transport-free in the in-memory model. The
// only code which knows the authenticated HTTP resource prefix lives here.
export class ReaderPublication {
  constructor({ request, current, prefix, manifest, packageHref }) {
    Object.assign(this, { request, current, prefix, packageHref });
    this.entries = new Map([...manifest.readingOrder, ...(manifest.resources || [])].map(link => [link.href, link]));
    this.entries.set(packageHref, { href: packageHref, type: "application/oebps-package+xml" });
    // Indexes entries a second time under their decoded form, so a
    // differently-escaped in-content reference to the same archive entry
    // still resolves (see resolveKey and canonicalize above).
    this.canonicalEntries = new Map([...this.entries.keys()].map(key => [canonicalize(key), key]));
    this.raw = new Map();
    this.blobs = new Map();
    this.urls = new Set();
    // Which spine document(s) a cached entry (raw bytes or a derived blob)
    // was reached from. retain() below uses this to release entries no
    // spine document inside the retained set still needs, instead of
    // holding every fetched byte and blob for the whole reading session.
    this.referrers = new Map();
    this.controller = new AbortController();
    this.closed = false;
  }

  addReferrer(key, rootHref) {
    if (!rootHref) return;
    let set = this.referrers.get(key);
    if (!set) this.referrers.set(key, set = new Set());
    set.add(rootHref);
  }

  async bytes(href, rootHref = href.split("#")[0]) {
    const key = href.split("#")[0];
    if (!this.entries.has(key) || this.closed) throw Error("Unavailable publication resource");
    this.addReferrer(key, rootHref);
    if (!this.raw.has(key)) {
      const pending = (async () => {
        const response = await this.request(this.prefix + key, { signal: this.controller.signal });
        if (!response.ok) throw Error(response.status === 409
          ? "The publication changed. Open the book again from your library."
          : "A publication resource could not be loaded.");
        const bytes = await response.arrayBuffer();
        if (this.closed || !this.current(response)) throw Error("The reader credential changed. Open the book again from your library.");
        return new Uint8Array(bytes);
      })();
      this.raw.set(key, pending);
      pending.catch(() => this.raw.delete(key));
    }
    return this.raw.get(key);
  }

  // Releases raw bytes and blobs for every cached entry whose referrers are
  // all outside keepRoots (the spine hrefs Readium's active frame window
  // still needs), coordinating our cache with that window instead of
  // holding the whole publication in memory for the reading session. The
  // package document is never evicted.
  retain(keepRoots) {
    for (const [key, referrers] of this.referrers) {
      if (key === this.packageHref) continue;
      if ([...referrers].some(root => keepRoots.has(root))) continue;
      this.raw.delete(key);
      const blob = this.blobs.get(key);
      if (blob) {
        this.blobs.delete(key);
        blob.then(url => { this.urls.delete(url); URL.revokeObjectURL(url); }, () => {});
      }
      this.referrers.delete(key);
    }
  }

  // Maps a publication-relative href (fragment-free) to the exact key this
  // publication's entries are stored under, tolerating a different but
  // equivalent percent-encoding. Returns null when the entry truly does not
  // exist.
  resolveKey(key) {
    if (this.entries.has(key)) return key;
    return this.canonicalEntries.get(canonicalize(key)) ?? null;
  }

  async document(href, transform = true, ancestors = new Set(), rootHref = href.split("#")[0]) {
    const type = this.entries.get(href)?.type || "application/xhtml+xml";
    const text = decodeText(await this.bytes(href, rootHref));
    const doc = new DOMParser().parseFromString(text, type === "text/html" ? type : "application/xml");
    if (doc.querySelector("parsererror")) throw Error("The publication contains invalid markup.");
    stripPublicationCode(doc);
    doc.documentElement.setAttribute("data-reader-href", href);
    if (transform) await this.transform(doc, href, ancestors, rootHref);
    return doc;
  }

  async stylesheet(text, base, ancestors, rootHref) {
    const tree = css.parse(text, { parseCustomProperty: true });
    const replacements = [];
    css.walk(tree, node => {
      if (node.type === "Url") replacements.push({ node, value: node.value });
      if (node.type === "Atrule" && node.name.toLowerCase() === "import" && node.prelude) {
        const node2 = node.prelude.children.first;
        if (node2?.type === "String") replacements.push({ node: node2, value: node2.value });
      }
    });
    await Promise.all(replacements.map(async ({ node, value }) => { node.value = await this.assetURL(value, base, ancestors, rootHref); }));
    return css.generate(tree);
  }

  async assetURL(reference, base, ancestors = new Set(), rootHref = base.split("#")[0]) {
    if (reference.startsWith("#")) return reference;
    // Images and fonts may be embedded. Executable document containers are
    // removed, and SVG loaded as an image cannot execute script.
    if (/^data:(image\/|font\/|application\/(font|vnd\.ms-fontobject))/i.test(reference)) return reference;
    const resolved = publicationHref(reference, base);
    if (!resolved) return "data:,";
    const [rawKey, fragment] = resolved.split("#");
    const key = this.resolveKey(rawKey) ?? rawKey;
    const entry = this.entries.get(key);
    if (!entry || ancestors.has(key)) return "data:,";
    this.addReferrer(key, rootHref);
    if (!this.blobs.has(key)) {
      const next = new Set(ancestors).add(key);
      const pending = (async () => {
        let data = await this.bytes(key, rootHref);
        if (/javascript|ecmascript/i.test(entry.type || "")) return "data:,";
        if (entry.type === "text/css") data = await this.stylesheet(decodeText(data, { css: true }), key, next, rootHref);
        else if (documentTypes.has(entry.type)) data = new XMLSerializer().serializeToString(await this.document(key, true, next, rootHref));
        if (this.closed) throw Error("Publication closed");
        const url = URL.createObjectURL(new Blob([data], { type: entry.type || "application/octet-stream" }));
        this.urls.add(url);
        return url;
      })();
      this.blobs.set(key, pending);
      pending.catch(() => this.blobs.delete(key));
    }
    return await this.blobs.get(key) + (fragment ? "#" + fragment : "");
  }

  async transform(doc, base, ancestors, rootHref) {
    for (const el of [...doc.querySelectorAll("*")]) {
      const name = el.localName.toLowerCase();
      if (name === "style") el.textContent = await this.stylesheet(el.textContent, base, ancestors, rootHref);
      if (el.hasAttribute("style")) {
        // Parse a declaration list inside a rule so the CSS parser handles
        // escaped URLs and nested functions in inline styles too.
        const wrapped = await this.stylesheet("x{" + el.getAttribute("style") + "}", base, ancestors, rootHref);
        el.setAttribute("style", wrapped.slice(2, -1));
      }
      if (name === "a") {
        const href = el.getAttribute("href") || el.getAttributeNS("http://www.w3.org/1999/xlink", "href");
        if (href) {
          const resolved = publicationHref(href, base);
          const [rawPath, fragment] = resolved ? resolved.split("#") : [null, null];
          const path = rawPath != null ? (this.resolveKey(rawPath) ?? rawPath) : null;
          const target = path != null ? path + (fragment ? "#" + fragment : "") : null;
          el.setAttribute("href", target ? "#" + encodeURIComponent(target) : "#");
          if (target) el.setAttribute("data-reader-href", target);
        }
        continue;
      }
      if (name === "link" && !/^(stylesheet|icon)$/i.test(el.getAttribute("rel") || "")) { el.remove(); continue; }
      for (const attr of [...el.attributes]) {
        if (["src", "href", "poster", "background"].includes(attr.localName)) {
          el.setAttributeNS(attr.namespaceURI, attr.name, await this.assetURL(attr.value, base, ancestors, rootHref));
        }
      }
      // A source set's descriptors are retained; candidates in an EPUB are
      // archive URLs (data URLs belong in src, where commas are unambiguous).
      if (el.hasAttribute("srcset")) {
        const candidates = el.getAttribute("srcset").split(",");
        el.setAttribute("srcset", (await Promise.all(candidates.map(async candidate => {
          const [url, ...descriptor] = candidate.trim().split(/\s+/);
          return [await this.assetURL(url, base, ancestors, rootHref), ...descriptor].join(" ");
        }))).join(", "));
      }
    }
  }

  // Builds a minimal XHTML frame for a spine item that is really an image:
  // a blob URL for the asset itself (through assetURL, so an SVG is still
  // stripped of scripts and a bitmap keeps its real mime type), sized from
  // the image's own intrinsic dimensions when they can be read so a fixed-
  // layout page still gets a usable viewport.
  async imageSpineDocument(href, type) {
    const src = await this.assetURL(href, "", new Set(), href);
    let width, height;
    if (type === "image/svg+xml") {
      const svg = (await this.document(href, false, new Set(), href)).documentElement;
      width = svg.getAttribute("width");
      height = svg.getAttribute("height");
      const viewBox = svg.getAttribute("viewBox")?.trim().split(/\s+/);
      if ((!width || !height) && viewBox?.length === 4) { width ||= viewBox[2]; height ||= viewBox[3]; }
    } else {
      try {
        const bitmap = await createImageBitmap(new Blob([await this.bytes(href, href)], { type }));
        width = bitmap.width; height = bitmap.height;
        bitmap.close();
      } catch { /* Dimensions stay unknown; the <img> still renders at its natural size. */ }
    }
    const viewport = width && height ? `<meta name="viewport" content="width=${Math.round(width)}, height=${Math.round(height)}"/>` : "";
    const doc = new DOMParser().parseFromString(
      `<html xmlns="http://www.w3.org/1999/xhtml"><head>${viewport}</head>` +
      `<body style="margin:0"><img src="${src}" alt="" style="width:100%;height:100%;object-fit:contain"/></body></html>`,
      "application/xhtml+xml");
    doc.documentElement.setAttribute("data-reader-href", href);
    return doc;
  }

  get(link) {
    const owner = this;
    return new class extends Resource {
      async link() { return link; }
      async length() { return (await owner.bytes(link.href)).length; }
      async read() {
        const entry = owner.entries.get(link.href);
        const doc = imageSpineTypes.has(entry?.type)
          ? await owner.imageSpineDocument(link.href, entry.type)
          : await owner.document(link.href);
        return new TextEncoder().encode(new XMLSerializer().serializeToString(doc));
      }
      close() {}
    }();
  }
  links() { return []; }
  close() {
    this.closed = true;
    this.controller.abort();
    for (const url of this.urls) URL.revokeObjectURL(url);
    this.urls.clear(); this.raw.clear(); this.blobs.clear(); this.referrers.clear();
  }
}
