// The passage on screen, written down so another reader can find it.
//
// A resource and a fraction of it reopen a book in the right chapter at
// roughly the right point. This is the rung above: the first visible
// word, its block, and enough text either side to find it again after a
// different font, a different column count and a different screen have
// moved everything else around.
//
// The shape is the Android app's, deliberately, down to the context
// lengths and the selector spelling — see ExactLocatorAnchor.kt in the
// liseur repository. Both clients must produce and read the same thing
// or neither can use the other's. `locations.liseurAnchor: 1` is what
// says an anchor was captured *this* way, on purpose; an engine's own
// idea of the current text is not the same claim and must not borrow
// the marker.
//
// One deliberate difference: a quote that appears more than once in its
// block is refused here rather than written down. Finding it again
// means taking the first match, and the first match is not necessarily
// the one the reader was looking at. A refusal costs the chapter rung,
// which is right; a wrong anchor costs the reader their place.

export const MARKER = "liseurAnchor";
export const MAX_BEFORE = 32;
export const MAX_HIGHLIGHT = 64;
export const MAX_AFTER = 32;
const MAX_SELECTOR = 2048;
const DEFAULT_MAX_LOCATOR_BYTES = 16 * 1024;

const BLOCKED = new Set(["script", "style", "noscript", "template"]);
const BLOCK_TAGS = new Set([
  "p", "li", "blockquote", "pre", "td", "th", "figcaption",
  "h1", "h2", "h3", "h4", "h5", "h6", "div",
]);

// Code points, not UTF-16 units: slicing a string in the middle of a
// surrogate pair produces a half character that will not match itself.
const head = (text, count) => Array.from(text || "").slice(0, count).join("");
const tail = (text, count) => Array.from(text || "").slice(-count).join("");

// An EPUB resource is served as XHTML, and an XML document reports
// tagName as it was written rather than upper-cased the way an HTML one
// does. localName is the name without the case question.
const tag = (element) => ((element && element.localName) || "").toLowerCase();

// occurrences counts complete before+highlight+after matches in a
// block's text. Anything but exactly one is unusable: none means the
// quote is not there, more than one means finding it again is a guess.
export function occurrences(blockText, quote) {
  if (!quote) return 0;
  let count = 0;
  let at = 0;
  for (;;) {
    const found = (blockText || "").indexOf(quote, at);
    if (found < 0) return count;
    count++;
    if (count > 1) return count;
    at = found + 1;
  }
}

export function quoteOf(anchor) {
  if (!anchor) return "";
  return (anchor.before || "") + (anchor.highlight || "") + (anchor.after || "");
}

// markLocator writes an anchor into a Readium locator, or returns the
// locator untouched when the result would not fit what the server
// accepts. Untouched is the honest answer: the chapter rung is still
// there, and a locator refused for its size is a page that never syncs.
export function markLocator(locator, anchor, maxBytes = DEFAULT_MAX_LOCATOR_BYTES) {
  if (!locator || !anchor) return locator;
  if (!anchor.cssSelector || anchor.cssSelector.length > MAX_SELECTOR) return locator;
  if (!anchor.highlight) return locator;
  const marked = {
    ...locator,
    locations: {
      ...(locator.locations || {}),
      [MARKER]: 1,
      cssSelector: anchor.cssSelector,
    },
    text: {
      before: tail(anchor.before, MAX_BEFORE),
      highlight: head(anchor.highlight, MAX_HIGHLIGHT),
      after: head(anchor.after, MAX_AFTER),
    },
  };
  const size = new TextEncoder().encode(JSON.stringify(marked)).length;
  return size < maxBytes ? marked : locator;
}

// selectorFor names a block the way the Android capture script names
// it: an id when it is unique in the document, otherwise a path of
// :nth-of-type steps down from body. The two must agree, because an
// anchor written on one device is looked up by selector on the other.
export function selectorFor(element, doc) {
  const escape = (value) =>
    doc?.defaultView?.CSS?.escape
      ? doc.defaultView.CSS.escape(value)
      : String(value).replace(/[^a-zA-Z0-9_-]/g, (ch) => "\\" + ch);
  if (element.id) {
    const byId = "#" + escape(element.id);
    if (doc.querySelectorAll(byId).length === 1) return byId;
  }
  const parts = [];
  let current = element;
  while (current && current !== doc.body) {
    const name = current.localName;
    if (!name) break;
    let index = 1;
    let sibling = current;
    while ((sibling = sibling.previousElementSibling)) {
      if (sibling.localName === current.localName) index++;
    }
    parts.unshift(name + ":nth-of-type(" + index + ")");
    current = current.parentElement;
  }
  return "body" + (parts.length ? " > " + parts.join(" > ") : "");
}

function words(text) {
  const Segmenter = globalThis.Intl && globalThis.Intl.Segmenter;
  if (Segmenter) {
    return Array.from(new Segmenter(undefined, { granularity: "word" }).segment(text))
      .filter((item) => item.isWordLike)
      .map((item) => ({ index: item.index, text: item.segment }));
  }
  let regex;
  try {
    regex = new RegExp("[\\p{L}\\p{N}\\p{M}]+(?:[’'][\\p{L}\\p{N}\\p{M}]+)*", "gu");
  } catch (_) {
    regex = /\S+/g;
  }
  const found = [];
  let match;
  while ((match = regex.exec(text)) !== null) found.push({ index: match.index, text: match[0] });
  return found;
}

// captureAnchor finds the first visible word in `doc` and describes it.
// Returns null when there is nothing usable, which is not a failure:
// the caller keeps the resource and its progression.
export function captureAnchor(doc, win) {
  if (!doc || !doc.body || !win) return null;
  const visible = (rect) =>
    rect.width > 0 && rect.height > 0 &&
    rect.right > 0 && rect.bottom > 0 &&
    rect.left < win.innerWidth && rect.top < win.innerHeight;
  const walker = doc.createTreeWalker(doc.body, 4 /* SHOW_TEXT */, {
    acceptNode: (node) => {
      const parent = node.parentElement;
      // 2 = FILTER_REJECT, 1 = FILTER_ACCEPT
      if (!parent || BLOCKED.has(tag(parent)) || !node.data.trim()) return 2;
      return 1;
    },
  });
  let node;
  while ((node = walker.nextNode())) {
    for (const word of words(node.data)) {
      const range = doc.createRange();
      range.setStart(node, word.index);
      range.setEnd(node, word.index + word.text.length);
      if (!Array.from(range.getClientRects()).some(visible)) continue;
      let block = node.parentElement;
      while (block && block !== doc.body && !BLOCK_TAGS.has(tag(block))) {
        block = block.parentElement;
      }
      block = block || doc.body;
      const beforeRange = doc.createRange();
      beforeRange.selectNodeContents(block);
      beforeRange.setEnd(node, word.index);
      const afterRange = doc.createRange();
      afterRange.selectNodeContents(block);
      afterRange.setStart(node, word.index + word.text.length);
      const anchor = {
        cssSelector: selectorFor(block, doc),
        before: tail(beforeRange.toString(), MAX_BEFORE),
        highlight: head(word.text, MAX_HIGHLIGHT),
        after: head(afterRange.toString(), MAX_AFTER),
      };
      // The reader is looking at exactly one place. If the words around
      // it do not say which one, say nothing rather than guess.
      if (occurrences(block.textContent || "", quoteOf(anchor)) !== 1) return null;
      return anchor;
    }
  }
  return null;
}
