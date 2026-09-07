import { css, Resource } from "./vendor/readium/readium.js";

const virtualOrigin = "https://publication.invalid/";
const documentTypes = new Set(["application/xhtml+xml", "text/html", "image/svg+xml"]);

// Resolve only archive-local references. Never pass a publisher URL to the
// authenticated request function, including protocol-relative URLs.
export function publicationHref(reference, base) {
  const url = new URL(reference, new URL(base, virtualOrigin));
  if (url.origin !== new URL(virtualOrigin).origin || url.search) return null;
  return url.pathname.slice(1) + url.hash;
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
    this.raw = new Map();
    this.blobs = new Map();
    this.urls = new Set();
    this.controller = new AbortController();
    this.closed = false;
  }

  async bytes(href) {
    const key = href.split("#")[0];
    if (!this.entries.has(key) || this.closed) throw Error("Unavailable publication resource");
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

  async document(href, transform = true, ancestors = new Set()) {
    const type = this.entries.get(href)?.type || "application/xhtml+xml";
    const text = new TextDecoder().decode(await this.bytes(href));
    const doc = new DOMParser().parseFromString(text, type === "text/html" ? type : "application/xml");
    if (doc.querySelector("parsererror")) throw Error("The publication contains invalid markup.");
    stripPublicationCode(doc);
    doc.documentElement.setAttribute("data-reader-href", href);
    if (transform) await this.transform(doc, href, ancestors);
    return doc;
  }

  async stylesheet(text, base, ancestors) {
    const tree = css.parse(text, { parseCustomProperty: true });
    const replacements = [];
    css.walk(tree, node => {
      if (node.type === "Url") replacements.push({ node, value: node.value });
      if (node.type === "Atrule" && node.name.toLowerCase() === "import" && node.prelude) {
        const node2 = node.prelude.children.first;
        if (node2?.type === "String") replacements.push({ node: node2, value: node2.value });
      }
    });
    await Promise.all(replacements.map(async ({ node, value }) => { node.value = await this.assetURL(value, base, ancestors); }));
    return css.generate(tree);
  }

  async assetURL(reference, base, ancestors = new Set()) {
    if (reference.startsWith("#")) return reference;
    // Images and fonts may be embedded. Executable document containers are
    // removed, and SVG loaded as an image cannot execute script.
    if (/^data:(image\/|font\/|application\/(font|vnd\.ms-fontobject))/i.test(reference)) return reference;
    const resolved = publicationHref(reference, base);
    if (!resolved) return "data:,";
    const [key, fragment] = resolved.split("#");
    const entry = this.entries.get(key);
    if (!entry || ancestors.has(key)) return "data:,";
    if (!this.blobs.has(key)) {
      const next = new Set(ancestors).add(key);
      const pending = (async () => {
        let data = await this.bytes(key);
        if (/javascript|ecmascript/i.test(entry.type || "")) return "data:,";
        if (entry.type === "text/css") data = await this.stylesheet(new TextDecoder().decode(data), key, next);
        else if (documentTypes.has(entry.type)) data = new XMLSerializer().serializeToString(await this.document(key, true, next));
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

  async transform(doc, base, ancestors) {
    for (const el of [...doc.querySelectorAll("*")]) {
      const name = el.localName.toLowerCase();
      if (name === "style") el.textContent = await this.stylesheet(el.textContent, base, ancestors);
      if (el.hasAttribute("style")) {
        // Parse a declaration list inside a rule so the CSS parser handles
        // escaped URLs and nested functions in inline styles too.
        const wrapped = await this.stylesheet("x{" + el.getAttribute("style") + "}", base, ancestors);
        el.setAttribute("style", wrapped.slice(2, -1));
      }
      if (name === "a") {
        const href = el.getAttribute("href") || el.getAttributeNS("http://www.w3.org/1999/xlink", "href");
        if (href) {
          const target = publicationHref(href, base);
          el.setAttribute("href", target ? "#" + encodeURIComponent(target) : "#");
          if (target) el.setAttribute("data-reader-href", target);
        }
        continue;
      }
      if (name === "link" && !/^(stylesheet|icon)$/i.test(el.getAttribute("rel") || "")) { el.remove(); continue; }
      for (const attr of [...el.attributes]) {
        if (["src", "href", "poster", "background"].includes(attr.localName)) {
          el.setAttributeNS(attr.namespaceURI, attr.name, await this.assetURL(attr.value, base, ancestors));
        }
      }
      // A source set's descriptors are retained; candidates in an EPUB are
      // archive URLs (data URLs belong in src, where commas are unambiguous).
      if (el.hasAttribute("srcset")) {
        const candidates = el.getAttribute("srcset").split(",");
        el.setAttribute("srcset", (await Promise.all(candidates.map(async candidate => {
          const [url, ...descriptor] = candidate.trim().split(/\s+/);
          return [await this.assetURL(url, base, ancestors), ...descriptor].join(" ");
        }))).join(", "));
      }
    }
  }

  get(link) {
    const owner = this;
    return new class extends Resource {
      async link() { return link; }
      async length() { return (await owner.bytes(link.href)).length; }
      async read() {
        const doc = await owner.document(link.href);
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
    this.urls.clear(); this.raw.clear(); this.blobs.clear();
  }
}
