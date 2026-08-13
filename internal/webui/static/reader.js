// The web reader (ADR-0007).
//
// Nothing here is vendored and nothing is fetched from a CDN: the whole
// renderer is this file, which is why the repository still has no build
// step. It is small because it does the least a reader can do — the
// point of the locator envelope at the bottom is that a better renderer
// can replace it without the protocol noticing.
//
// The security rule this file exists to keep: publication bytes are
// unpacked here, in the page, and handed to a sandboxed iframe as one
// self-contained document. No publication resource is ever fetched from
// a URL, so there is no URL a browser could be talked into navigating
// to on the authenticated origin.

'use strict';

// ---------------------------------------------------------------- zip

// Zip reads the central directory of a ZIP archive held in memory.
// EPUB is a ZIP, and this is the only part of that format the reader
// needs: names in, bytes out.
class Zip {
  constructor(buf) {
    this.view = new DataView(buf);
    this.bytes = new Uint8Array(buf);
    this.entries = new Map();
    this.#readCentralDirectory();
  }

  #readCentralDirectory() {
    const eocd = this.#findEOCD();
    let n = this.view.getUint16(eocd + 10, true);
    let off = this.view.getUint32(eocd + 16, true);
    // Zip64: the 32-bit fields saturate and the real ones live in a
    // separate record. Rare for a book, but a silent misparse would be
    // worse than a clear failure.
    if (off === 0xffffffff || n === 0xffff) {
      const loc = this.#find(0x07064b50, eocd - 20, eocd);
      if (loc < 0) throw new Error('zip64 archive without a locator');
      const z64 = Number(this.view.getBigUint64(loc + 8, true));
      n = Number(this.view.getBigUint64(z64 + 32, true));
      off = Number(this.view.getBigUint64(z64 + 48, true));
    }
    for (let i = 0; i < n; i++) {
      if (this.view.getUint32(off, true) !== 0x02014b50) {
        throw new Error('corrupt central directory');
      }
      const nameLen = this.view.getUint16(off + 28, true);
      const extraLen = this.view.getUint16(off + 30, true);
      const commentLen = this.view.getUint16(off + 32, true);
      const name = new TextDecoder().decode(
        this.bytes.subarray(off + 46, off + 46 + nameLen));
      this.entries.set(name, {
        method: this.view.getUint16(off + 10, true),
        compressedSize: this.view.getUint32(off + 20, true),
        localHeader: this.view.getUint32(off + 42, true),
      });
      off += 46 + nameLen + extraLen + commentLen;
    }
  }

  #findEOCD() {
    // The end-of-central-directory record sits at the end, behind a
    // comment of up to 64 KiB, so it is found by scanning backwards.
    const from = Math.max(0, this.bytes.length - 66000);
    const at = this.#find(0x06054b50, from, this.bytes.length - 22, true);
    if (at < 0) throw new Error('not a zip archive');
    return at;
  }

  #find(signature, from, to, last = false) {
    let found = -1;
    for (let i = from; i <= to; i++) {
      if (this.view.getUint32(i, true) === signature) {
        if (!last) return i;
        found = i;
      }
    }
    return found;
  }

  has(name) { return this.entries.has(name); }

  // bytes returns one entry, inflated if it needs to be. Deflate comes
  // from the browser's own DecompressionStream rather than a bundled
  // inflater, which is most of the reason this file is short.
  async read(name) {
    const e = this.entries.get(name);
    if (!e) throw new Error('missing from archive: ' + name);
    const h = e.localHeader;
    if (this.view.getUint32(h, true) !== 0x04034b50) {
      throw new Error('corrupt entry: ' + name);
    }
    const start = h + 30 + this.view.getUint16(h + 26, true) +
      this.view.getUint16(h + 28, true);
    const raw = this.bytes.subarray(start, start + e.compressedSize);
    if (e.method === 0) return raw;
    if (e.method !== 8) throw new Error('unsupported compression in ' + name);
    const stream = new Blob([raw]).stream()
      .pipeThrough(new DecompressionStream('deflate-raw'));
    return new Uint8Array(await new Response(stream).arrayBuffer());
  }

  async text(name) {
    return new TextDecoder('utf-8').decode(await this.read(name));
  }
}

// --------------------------------------------------------------- epub

const XHTML = 'application/xhtml+xml';

// resolvePath joins an href against the directory of the file it was
// found in, and normalises away the ".." segments EPUBs are full of.
function resolvePath(base, href) {
  const parts = base.split('/').slice(0, -1);
  for (const seg of decodeURIComponent(href.split('#')[0]).split('/')) {
    if (seg === '.' || seg === '') continue;
    if (seg === '..') parts.pop();
    else parts.push(seg);
  }
  return parts.join('/');
}

function parseXML(text, type = 'application/xml') {
  const doc = new DOMParser().parseFromString(text, type);
  if (doc.querySelector('parsererror')) throw new Error('malformed XML');
  return doc;
}

// Epub is the publication: where its parts are, and in what order they
// are meant to be read.
class Epub {
  static async open(buf) {
    const zip = new Zip(buf);
    const container = parseXML(await zip.text('META-INF/container.xml'));
    const rootfile = container.querySelector('rootfile');
    if (!rootfile) throw new Error('no rootfile in container.xml');
    const opfPath = rootfile.getAttribute('full-path');
    const opf = parseXML(await zip.text(opfPath));

    const manifest = new Map();
    for (const item of opf.querySelectorAll('manifest > item')) {
      manifest.set(item.getAttribute('id'), {
        path: resolvePath(opfPath, item.getAttribute('href')),
        type: item.getAttribute('media-type') || '',
      });
    }
    const spine = [];
    for (const ref of opf.querySelectorAll('spine > itemref')) {
      const item = manifest.get(ref.getAttribute('idref'));
      // linear="no" is the publisher saying "not part of the reading
      // order" — usually cover art or ads.
      if (item && ref.getAttribute('linear') !== 'no') spine.push(item);
    }
    if (spine.length === 0) throw new Error('publication has no readable spine');

    const titleEl = opf.querySelector('metadata > title') ||
      opf.getElementsByTagNameNS('*', 'title')[0];
    return new Epub(zip, spine, titleEl ? titleEl.textContent.trim() : '');
  }

  constructor(zip, spine, title) {
    this.zip = zip;
    this.spine = spine;
    this.title = title;
  }

  // document builds one chapter as a single self-contained HTML string:
  // stylesheets folded in, images and fonts turned into data: URLs,
  // publisher scripts dropped.
  //
  // Self-contained is the whole point. A sandboxed iframe has an opaque
  // origin, so it could not fetch a blob: URL minted out here even if we
  // wanted it to — and because it cannot, no route needs to serve
  // publication resources, and none does.
  async document(index, nonce) {
    const item = this.spine[index];
    const doc = parseXML(await this.zip.text(item.path), XHTML);

    for (const script of [...doc.querySelectorAll('script')]) script.remove();

    for (const link of [...doc.querySelectorAll('link[rel~="stylesheet"]')]) {
      const href = link.getAttribute('href');
      const style = doc.createElement('style');
      style.textContent = await this.#css(resolvePath(item.path, href));
      link.replaceWith(style);
    }
    for (const el of doc.querySelectorAll('img[src]')) {
      el.setAttribute('src', await this.#dataURL(resolvePath(item.path, el.getAttribute('src'))));
    }
    for (const el of doc.querySelectorAll('image')) {
      const href = el.getAttribute('xlink:href') || el.getAttribute('href');
      if (!href) continue;
      const url = await this.#dataURL(resolvePath(item.path, href));
      el.setAttribute('xlink:href', url);
      el.setAttribute('href', url);
    }

    // The publication's own CSS is collected rather than left where it
    // was found, because only the body survives the wrap: a stylesheet
    // in the head would otherwise be dropped, and a book would lose its
    // typography without anything reporting an error.
    const sheets = [];
    for (const style of [...doc.querySelectorAll('style')]) {
      sheets.push(await this.#inlineURLs(style.textContent || '', item.path));
      style.remove();
    }

    const body = doc.querySelector('body');
    return wrapChapter(body ? body.innerHTML : '', sheets.join('\n'), nonce);
  }

  async #css(path) {
    try {
      return await this.#inlineURLs(await this.zip.text(path), path);
    } catch {
      return '';
    }
  }

  // #inlineURLs rewrites url(...) references — fonts and background
  // images — the same way, so a stylesheet cannot be the one thing that
  // still reaches for the network.
  async #inlineURLs(css, base) {
    const refs = [...css.matchAll(/url\(\s*['"]?([^'")]+)['"]?\s*\)/g)];
    for (const [match, href] of refs) {
      if (/^(data|https?):/i.test(href)) continue;
      const url = await this.#dataURL(resolvePath(base, href));
      if (url) css = css.split(match).join('url("' + url + '")');
    }
    return css;
  }

  async #dataURL(path) {
    try {
      const bytes = await this.zip.read(path);
      let binary = '';
      for (let i = 0; i < bytes.length; i += 0x8000) {
        binary += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
      }
      return 'data:' + mediaType(path) + ';base64,' + btoa(binary);
    } catch {
      return '';
    }
  }
}

function mediaType(path) {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase();
  return {
    jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif',
    svg: 'image/svg+xml', webp: 'image/webp',
    otf: 'font/otf', ttf: 'font/ttf', woff: 'font/woff', woff2: 'font/woff2',
  }[ext] || 'application/octet-stream';
}

// wrapChapter puts the publisher's markup inside a document that cannot
// do anything with it. Two defences, and they are independent:
//
//   - the iframe is sandboxed without allow-same-origin, so this
//     document has an opaque origin with no cookies and no access to
//     the page that framed it;
// The publication's stylesheet goes second so a book still looks like
// itself; the reader's own CSS is a floor, not a house style.
//
//   - this CSP allows exactly the inlined data: resources and one
//     nonced script (ours). There is no connect-src, so a book cannot
//     call home; there is no script-src for the publisher, so a book
//     that shipped scripts does not run them.
function wrapChapter(bodyHTML, publicationCSS, nonce) {
  const csp = "default-src 'none'; img-src data:; media-src data:; " +
    "font-src data:; style-src 'unsafe-inline'; script-src 'nonce-" + nonce + "'";
  return '<!DOCTYPE html><html><head><meta charset="utf-8">' +
    '<meta http-equiv="Content-Security-Policy" content="' + csp + '">' +
    '<style>' + READER_CSS + '</style>' +
    (publicationCSS ? '<style>' + publicationCSS + '</style>' : '') +
    '</head><body>' + bodyHTML +
    '<script nonce="' + nonce + '">' + CHAPTER_SCRIPT + '</scr' + 'ipt>' +
    '</body></html>';
}

const READER_CSS = `
  html,body{margin:0;padding:0}
  body{padding:1.5rem 1.25rem 4rem;font:1rem/1.65 Georgia,serif;color:#111;
       background:#fff;overflow-wrap:break-word}
  img,svg,image{max-width:100%;height:auto}
  a{color:#0645ad}
  @media (prefers-color-scheme:dark){body{background:#16181d;color:#d8dae0}
    a{color:#8ab4f8}}
`;

// CHAPTER_SCRIPT runs inside the sandbox. It reports how far down the
// chapter the reader has scrolled and takes instructions to scroll
// somewhere — the only two things the outer page needs, and the only
// two it can have, since it cannot touch this document directly.
const CHAPTER_SCRIPT = `
  (function(){
    function fraction(){
      var h = document.documentElement.scrollHeight - window.innerHeight;
      return h > 0 ? Math.min(1, Math.max(0, window.scrollY / h)) : 0;
    }
    function report(){ parent.postMessage({type:'progress', fraction:fraction()}, '*'); }
    window.addEventListener('scroll', function(){
      clearTimeout(window.__t); window.__t = setTimeout(report, 120);
    }, {passive:true});
    window.addEventListener('message', function(e){
      var d = e.data || {};
      if (d.type === 'seek') {
        var h = document.documentElement.scrollHeight - window.innerHeight;
        window.scrollTo(0, h * d.fraction);
        report();
      }
      if (d.type === 'page') {
        window.scrollBy({top: d.direction * (window.innerHeight - 40), behavior:'smooth'});
      }
    });
    parent.postMessage({type:'ready'}, '*');
    report();
  })();
`;

// ------------------------------------------------------------ locator

// locatorFor builds the Readium Locator the sync protocol carries
// (ADR-0007). The server stores it verbatim and never reads it, so the
// shape is a promise to the other clients rather than to the server:
// progression is what everyone can act on, and href identifies the
// resource for anything that understands this publication's layout.
function locatorFor(epub, index, fraction) {
  const total = epub.spine.length;
  return {
    href: epub.spine[index].path,
    type: epub.spine[index].type || XHTML,
    locations: {
      progression: fraction,
      totalProgression: (index + fraction) / total,
      position: index + 1,
    },
    title: epub.title,
  };
}

// placeFromProgression is the other direction, and it is why a book
// started on a phone opens in the right place here: given only the
// fraction every client agrees on, say which chapter that is and how
// far into it.
function placeFromProgression(spineLength, totalProgression) {
  const clamped = Math.min(0.999999, Math.max(0, totalProgression || 0));
  const exact = clamped * spineLength;
  const index = Math.min(spineLength - 1, Math.floor(exact));
  return { index: index, fraction: exact - index };
}

// placeFromLocator prefers what the writing client actually said, and
// falls back to the fraction when this is a locator from a reader that
// lays the book out differently.
function placeFromLocator(epub, locator, progression) {
  if (locator && locator.href) {
    const index = epub.spine.findIndex((s) => s.path === locator.href);
    if (index >= 0) {
      const within = locator.locations && typeof locator.locations.progression === 'number'
        ? locator.locations.progression : 0;
      return { index: index, fraction: within };
    }
  }
  const total = locator && locator.locations &&
    typeof locator.locations.totalProgression === 'number'
    ? locator.locations.totalProgression : progression;
  return placeFromProgression(epub.spine.length, total);
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = {
    resolvePath, placeFromProgression, placeFromLocator, locatorFor, mediaType, Zip,
    wrapChapter,
  };
}
