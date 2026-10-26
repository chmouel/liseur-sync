// The sentence being heard, found in the chapter on screen.
//
// An audiobook does not say which words it is speaking. The audio-to-book
// mapping gives a fraction of the chapter for a playback time, and a live
// Whisper match every so often quotes the words that were actually spoken.
// This module turns either into a sentence of the rendered document: the
// fraction is a character offset in the chapter's visible text, the quote
// is found in that text, and between quotes a small clock advances at the
// pace the narrator has actually been reading.
//
// Offsets are indexes into the chapter's text with whitespace collapsed to
// single spaces, which is how the server quotes passages too.

import { selectorFor } from "./reader-anchor.js";

const SKIP = new Set(["script", "style", "noscript", "template", "head"]);
const BLOCKS = new Set([
  "body", "p", "li", "blockquote", "pre", "td", "th", "figcaption", "caption",
  "h1", "h2", "h3", "h4", "h5", "h6", "div", "dd", "dt", "section", "article",
  "aside", "header", "footer", "figure", "nav",
]);
const SPACE = /\s/;
const CONTEXT = 32;
const MIN_SENTENCE = 3;
const MAX_SENTENCE = 320;

const tag = (element) => ((element && element.localName) || "").toLowerCase();
const head = (text, count) => Array.from(text || "").slice(0, count).join("");
const tail = (text, count) => Array.from(text || "").slice(-count).join("");
const clamp = (value, min, max) => Math.max(min, Math.min(max, value));

// textRuns lists the visible text nodes of a chapter with the block each
// belongs to. It is the only part of this module that walks a DOM.
export function textRuns(doc) {
  const body = doc && doc.body;
  if (!body) return [];
  const runs = [];
  const walker = doc.createTreeWalker(body, 4 /* SHOW_TEXT */, {
    acceptNode(node) {
      for (let element = node.parentElement; element && element !== body; element = element.parentElement) {
        if (SKIP.has(tag(element))) return 2; // FILTER_REJECT
      }
      return 1; // FILTER_ACCEPT
    },
  });
  let node;
  while ((node = walker.nextNode())) {
    let block = node.parentElement;
    while (block && block !== body && !BLOCKS.has(tag(block))) block = block.parentElement;
    runs.push({ node, text: node.data, block: block || body });
  }
  return runs;
}

// narrationIndex collapses the runs into one string and remembers where
// every character of it came from, so an offset can become a DOM point.
export function narrationIndex(runs, lang) {
  let text = "";
  let space = true;
  const nodes = [];
  const glyphs = [];
  const blocks = [];
  for (const run of runs || []) {
    if (!blocks.length || blocks[blocks.length - 1].block !== run.block) {
      if (blocks.length) blocks[blocks.length - 1].end = text.length;
      blocks.push({ block: run.block, start: text.length, end: text.length });
    }
    const raw = [];
    let piece = "";
    const data = run.text || "";
    for (let i = 0; i < data.length; i++) {
      if (SPACE.test(data[i])) {
        if (space) continue;
        piece += " ";
        space = true;
      } else {
        const code = data.charCodeAt(i);
        // The server counts code points, so the second half of a
        // surrogate pair is not a glyph of its own.
        const prev = i > 0 ? data.charCodeAt(i - 1) : 0;
        if (!(code >= 0xdc00 && code <= 0xdfff && prev >= 0xd800 && prev <= 0xdbff)) {
          glyphs.push(text.length + piece.length);
        }
        piece += data[i];
        space = false;
      }
      raw.push(i);
    }
    if (raw.length) nodes.push({ node: run.node, start: text.length, raw });
    text += piece;
  }
  if (blocks.length) blocks[blocks.length - 1].end = text.length;
  const index = { text, nodes, glyphs, blocks: blocks.filter((b) => b.end > b.start) };
  index.sentences = sentences(index, lang);
  return index;
}

// offsetOfFraction turns a fraction of the chapter, as the audio mapping
// gives it, into an offset. Like the server, it counts only visible
// characters other than whitespace.
export function offsetOfFraction(index, fraction) {
  const { glyphs, text } = index;
  if (!glyphs.length || !Number.isFinite(fraction)) return 0;
  if (fraction >= 1) return text.length;
  return glyphs[clamp(Math.floor(fraction * glyphs.length), 0, glyphs.length - 1)];
}

function segmentRanges(text, lang) {
  const Segmenter = globalThis.Intl && globalThis.Intl.Segmenter;
  if (Segmenter) {
    let segmenter;
    try { segmenter = new Segmenter(lang || undefined, { granularity: "sentence" }); }
    catch { segmenter = new Segmenter(undefined, { granularity: "sentence" }); }
    return Array.from(segmenter.segment(text), (s) => [s.index, s.index + s.segment.length]);
  }
  const out = [];
  const pattern = /[^.!?…]*(?:[.!?…]+["'”’»)\]]*|$)\s*/gu;
  let match;
  while ((match = pattern.exec(text)) !== null && match[0].length) {
    out.push([match.index, match.index + match[0].length]);
  }
  return out;
}

// splitLong cuts a run-on sentence so the highlight stays readable,
// preferring a clause break and never cutting inside a word.
function splitLong(text, start, end) {
  const out = [];
  while (end - start > MAX_SENTENCE) {
    const window = text.slice(start, start + MAX_SENTENCE);
    let cut = -1;
    for (const mark of [/[;:—–]\s/g, /,\s/g, /\s/g]) {
      let m;
      mark.lastIndex = Math.floor(MAX_SENTENCE / 3);
      while ((m = mark.exec(window)) !== null) cut = m.index + m[0].length;
      if (cut > 0) break;
    }
    if (cut <= 0) cut = MAX_SENTENCE;
    out.push({ start, end: trimEnd(text, start, start + cut) });
    start = trimStart(text, start + cut, end);
  }
  if (end > start) out.push({ start, end });
  return out;
}

const trimStart = (text, start, end) => { while (start < end && text[start] === " ") start++; return start; };
const trimEnd = (text, start, end) => { while (end > start && text[end - 1] === " ") end--; return end; };
const wordy = (s) => /[\p{L}\p{N}]/u.test(s);
// A sentence segmenter stops after "Mr." or an initial; a narrator does not.
const ABBREVIATION = /(?:^|[\s(“"‘'])(?:\p{L}|Mr|Mrs|Ms|Dr|St|Jr|Sr|Prof|Mt|Capt|Col|Gen|Lt|Sgt|Rev|Hon|Mme|Mlle|Mgr|vs|etc|cf|No)\.$/u;

// sentences splits every block on its own, so a heading without a full
// stop is never glued to the paragraph after it.
export function sentences(index, lang) {
  const { text, blocks } = index;
  const out = [];
  for (const block of blocks) {
    const local = [];
    for (const [s, e] of segmentRanges(text.slice(block.start, block.end), lang)) {
      const start = trimStart(text, block.start + s, block.start + e);
      const end = trimEnd(text, start, block.start + e);
      if (end <= start) continue;
      const piece = text.slice(start, end);
      const prev = local[local.length - 1];
      // "Mr." or a lone closing quote is not something anyone says on
      // its own; it belongs with its neighbour.
      if (prev && (Array.from(piece).length < MIN_SENTENCE || !wordy(piece))) {
        prev.end = end;
        continue;
      }
      if (prev && (Array.from(text.slice(prev.start, prev.end)).length < MIN_SENTENCE
        || ABBREVIATION.test(text.slice(prev.start, prev.end)))) {
        prev.end = end;
        continue;
      }
      local.push({ start, end });
    }
    for (const s of local) out.push(...splitLong(text, s.start, s.end));
  }
  return out;
}

// sentenceAt is the last sentence starting at or before offset; an offset
// in the gap between blocks belongs to the sentence that follows it.
export function sentenceAt(list, offset) {
  if (!list || !list.length) return null;
  let lo = 0;
  let hi = list.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (list[mid].start <= offset) lo = mid;
    else hi = mid - 1;
  }
  const found = list[lo];
  if (offset >= found.end && lo + 1 < list.length) return list[lo + 1];
  return found;
}

// wordAt is the word around offset, kept inside the sentence.
export function wordAt(text, sentence, offset) {
  const at = clamp(Math.floor(offset), sentence.start, sentence.end - 1);
  let start = at;
  let end = at;
  while (start > sentence.start && text[start - 1] !== " ") start--;
  while (end < sentence.end && text[end] !== " ") end++;
  if (end <= start) end = Math.min(sentence.end, start + 1);
  return { start, end };
}

// position turns an offset into a DOM point; `end` asks for the point
// just after the character before offset, the end of a range.
export function position(index, offset, end = false) {
  const { nodes } = index;
  if (!nodes.length) return null;
  const target = end ? offset - 1 : offset;
  let lo = 0;
  let hi = nodes.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (nodes[mid].start <= target) lo = mid;
    else hi = mid - 1;
  }
  const entry = nodes[lo];
  const k = clamp(target - entry.start, 0, entry.raw.length - 1);
  return { node: entry.node, offset: entry.raw[k] + (end ? 1 : 0) };
}

export function blockAt(index, offset) {
  for (const block of index.blocks) {
    if (offset >= block.start && offset < block.end) return block.block;
  }
  return index.blocks.length ? index.blocks[index.blocks.length - 1].block : null;
}

// quoteOffset finds a quoted passage in the chapter and returns the
// offset just after its highlight: the server quotes the words that end
// at the playback time it was asked about. A quote that matches more than
// once takes the occurrence closest to `near`.
export function quoteOffset(index, quote, near) {
  if (!quote) return null;
  const norm = (s) => (s || "").replace(/\s+/g, " ");
  const highlight = norm(quote.highlight).trim();
  if (!highlight) return null;
  const before = norm(quote.before);
  const after = norm(quote.after);
  const attempts = [
    [before + highlight + after, before.length],
    [before.trimStart() + highlight, before.trimStart().length],
    [highlight + after.trimEnd(), 0],
    [highlight, 0],
  ];
  for (const [needle, lead] of attempts) {
    if (!needle) continue;
    const found = [];
    for (let at = index.text.indexOf(needle); at >= 0; at = index.text.indexOf(needle, at + 1)) {
      found.push(at + lead + highlight.length);
    }
    if (!found.length) continue;
    if (found.length === 1 || !Number.isFinite(near)) {
      if (found.length === 1) return found[0];
      continue;
    }
    return found.reduce((best, value) => (Math.abs(value - near) < Math.abs(best - near) ? value : best));
  }
  return null;
}

// narrationClock keeps the last exact sighting of the narrator, an
// (audio time, offset) pair, and the pace they have been reading at.
// `rate` arguments are the mapping's own characters per second for the
// chapter, which is what is used until two sightings say otherwise, and
// what bounds the learnt pace so one bad match cannot run away with it.
export function narrationClock() {
  let anchor = null;
  let pace = null;
  return {
    reset() { anchor = null; pace = null; },
    snap(href, t, offset, rate) {
      if (!Number.isFinite(t) || !Number.isFinite(offset)) return;
      if (anchor && anchor.href === href && t - anchor.t >= 8 && Number.isFinite(rate) && rate > 0) {
        const seen = (offset - anchor.offset) / (t - anchor.t);
        if (seen >= 0.3 * rate && seen <= 3 * rate) {
          pace = clamp(0.5 * (pace ?? rate) + 0.5 * seen, 0.6 * rate, 1.6 * rate);
        } else {
          pace = null;
        }
      } else if (!anchor || anchor.href !== href || t < anchor.t) {
        pace = null;
      }
      anchor = { href, t, offset };
    },
    offsetAt(href, t, estimate, rate) {
      if (!anchor || anchor.href !== href || !(t >= anchor.t) || !Number.isFinite(rate) || rate <= 0) {
        return estimate;
      }
      return anchor.offset + (t - anchor.t) * clamp(pace ?? rate, 0.6 * rate, 1.6 * rate);
    },
    get anchor() { return anchor; },
    get pace() { return pace; },
  };
}

// The rest needs a real document: ranges and locators of the rendered
// chapter, in the shape Readium's decorator and navigator resolve.

export function chapterIndex(doc, cache) {
  if (cache && cache.has(doc)) return cache.get(doc);
  const root = doc.documentElement;
  const lang = root && (root.getAttribute("lang") || root.getAttribute("xml:lang"));
  const index = narrationIndex(textRuns(doc), lang || undefined);
  if (cache) cache.set(doc, index);
  return index;
}

export function rangeOf(doc, index, start, end) {
  const from = position(index, start);
  const to = position(index, end, true);
  if (!from || !to) return null;
  const range = doc.createRange();
  range.setStart(from.node, from.offset);
  range.setEnd(to.node, to.offset);
  return range;
}

export function locatorOf(doc, index, href, start, end) {
  const range = rangeOf(doc, index, start, end);
  if (!range) return null;
  const block = blockAt(index, start) || doc.body;
  const before = doc.createRange();
  before.selectNodeContents(block);
  before.setEnd(range.startContainer, range.startOffset);
  const after = doc.createRange();
  after.selectNodeContents(block);
  after.setStart(range.endContainer, range.endOffset);
  return {
    href,
    type: "application/xhtml+xml",
    locations: {
      progression: index.text.length ? start / index.text.length : 0,
      cssSelector: selectorFor(block, doc),
    },
    text: {
      before: tail(before.toString(), CONTEXT),
      highlight: range.toString(),
      after: head(after.toString(), CONTEXT),
    },
  };
}
