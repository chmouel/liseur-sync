// What to say about a place in a book, so that two devices can be
// compared by a reader rather than by a program.
//
// A percentage is not something anyone can check. "Page 212 of 480",
// with the sentence that was on screen under it, is: the reader looks
// at the page in their hand and knows at once which side is theirs.
// This is the web's version of what `BookSyncDialog.Side` shows on the
// phone, computed the same way, from the same fields.
//
// The page is a Readium position, counted by `reader-positions.js`, so
// it is the same number the phone shows for the same spot. Two ways of
// arriving at it are not equally good and are not presented as such:
//
// - The op named a resource this copy of the book also has, and how far
//   into it the reader was. That lands in the right chapter whoever
//   wrote it, so the page is *exact*.
// - Only a fraction of the whole book survived. Clients do not compute
//   that fraction the same way — this reader interpolates the server's
//   position list by byte weight, the phone interpolates Readium's
//   synthetic positions — so on a book with a few large chapters the
//   two disagree. A page derived from it is *near*, and says so.
//
// Nothing here touches the DOM, the network or a clock it was not
// given.

import { pageAt } from "./reader-positions.js";
import { sameEdition, sectionHrefIn } from "./reader-restore.js";

/** As much of another device's excerpt as is worth reading to place oneself. */
export const EXCERPT_CHARS = 200;

const finite = (value) => typeof value === "number" && Number.isFinite(value);

const fractionOf = (value) =>
  finite(value) && value >= 0 && value <= 1 ? value : null;

const squash = (value) =>
  typeof value === "string" ? value.replace(/\s+/g, " ") : "";

const ELLIPSIS = "…";

// Both context fields are cut at a fixed number of code points, so both
// ends usually land inside a word. Half a word is not worth reading and
// is not the writer's text either, so it goes; a side with no space at
// all is one long partial word and goes whole.
const dropPartialHead = (text) => text.replace(/^\S*\s*/, "");
const dropPartialTail = (text) => text.replace(/\s*\S*$/, "");

const cap = (text) => {
  const points = Array.from(text);
  if (points.length <= EXCERPT_CHARS) return text;
  return points.slice(0, EXCERPT_CHARS - 1).join("") + ELLIPSIS;
};

/**
 * passageOf reads an anchor's three text fields as one passage.
 *
 * `before`, `highlight` and `after` are a single continuous run of text
 * from one block — the words leading up to the first visible word, that
 * word, and the words following it — so they are joined with nothing
 * between them. Any whitespace inside them is a line in a document
 * rather than a pause in a sentence, so it collapses.
 *
 * An anchor's context is the whole point of this: `highlight` alone is
 * one word by construction (`reader-anchor.js` pins the first visible
 * word, exactly as Android's `ExactLocatorAnchor` does), and one word
 * places nobody. Capturing *more* text instead is not the lever it
 * looks like: the phone compares the three fields verbatim to decide
 * that two devices are on the same spot, so a client that captured a
 * longer `after` would never agree with one that did not. The fields
 * are a wire contract; how much of them a reader is shown is not.
 *
 * A locator carrying only `highlight` is left as it is. Some clients
 * write a whole selection there and no context at all, and dressing
 * that up as an elided fragment would say something untrue about it.
 */
export function passageOf(text) {
  const highlight = squash(text?.highlight).trim();
  if (!highlight) return null;
  const before = dropPartialHead(squash(text?.before));
  const after = dropPartialTail(squash(text?.after));
  if (!before && !after) return cap(highlight);
  return cap(
    (before ? ELLIPSIS + before : "") + highlight + (after ? after + ELLIPSIS : ""),
  );
}

/**
 * excerptOf is the passage the writing client had on screen.
 *
 * It is another device's text, and it is returned as text: whatever
 * shows it must set it as text too. The cap is here rather than at the
 * point of display so that every caller gets it.
 */
export function excerptOf(op) {
  return passageOf(op?.locator?.text);
}

/**
 * placeOf describes where an op is, in this copy of this book.
 *
 * `view` supplies what the open publication knows: the position table,
 * the reading order, the equivalent-spelling lookup and the digest of
 * the bytes on screen. A caller with none of that still gets a
 * fraction, which is the one thing every client writes.
 */
export function placeOf(op, view = {}) {
  if (!op) return null;
  const locations = op.locator?.locations || {};
  const fraction = fractionOf(locations.totalProgression) ?? fractionOf(op.progression);
  const table = view.table || null;
  const place = {
    fraction, page: null, total: table?.total || null, exact: false,
    excerpt: excerptOf(op), at: op.client_ts || null,
  };
  if (!table) return place;

  const named = typeof op.edition_sha === "string" ? op.edition_sha : "";
  if (sameEdition(named, view.editionSHA || "")) {
    const href = sectionHrefIn(op.locator?.href, view.sections, view.resolveKey);
    const index = (view.sections || []).findIndex((section) => section && section.id === href);
    if (href && index >= 0) {
      // A locator may name a resource without saying how far into it the
      // reader was — a whole-book progression and an href is all some
      // clients write. Its first page is then the honest answer: right
      // about the chapter, a guess about the page, and *near* rather
      // than exact. Claiming exactness there would put a made-up page
      // number under a button the reader is choosing with.
      const within = fractionOf(locations.progression);
      const page = pageAt(table, index, within ?? 0);
      if (page) return { ...place, page, exact: within !== null };
    }
  }
  // No resource to stand on: interpolate, and admit that is what it is.
  if (fraction === null) return place;
  const page = Math.min(Math.max(1, Math.round(fraction * table.total)), table.total);
  return { ...place, page, exact: false };
}

/**
 * placeHere describes the page actually on screen.
 *
 * It is the side the reader can check by looking down, so it is never
 * interpolated: the engine says which section and how far into it, and
 * that is the same arithmetic the footer does.
 *
 * `view.anchor` is this page's own first visible word and its context,
 * captured the way a position is written. Two sides a page apart are
 * told apart by their numbers; two sides on the same page and not the
 * same spot are told apart by nothing else at all. Nothing is read from
 * the document here — the anchor arrives already taken.
 */
export function placeHere(location, view = {}) {
  if (!location) return null;
  const table = view.table || null;
  const place = {
    fraction: fractionOf(location.fraction), page: null,
    total: table?.total || null, exact: false,
    excerpt: passageOf(view.anchor), at: null,
  };
  if (!table) return place;
  const page = pageAt(table, location.section?.current, location.sectionFraction);
  return page ? { ...place, page, exact: true } : place;
}

/**
 * placeLabel is the place in one phrase.
 *
 * A page number is what a reader can place themselves by, but it is not
 * known until the book has been measured; a percentage is the fallback,
 * not the preference. A place with neither says nothing rather than
 * inventing something.
 */
export function placeLabel(place) {
  if (!place) return null;
  if (place.page && place.total) {
    return `${place.exact ? "Page" : "Near page"} ${place.page} of ${place.total}`;
  }
  if (place.fraction === null || place.fraction === undefined) return null;
  return `${Math.round(place.fraction * 100)}%`;
}

/**
 * relativeAge is how long ago, said the way a person would.
 *
 * A clock is not evidence about which position is right — that is
 * decided by movement away from an agreed baseline, and never here —
 * but it is a fact about the other side that helps a reader recognise
 * their own evening. A timestamp from the future is another device's
 * clock being wrong, and is worth no sentence at all.
 */
export function relativeAge(at, now = Date.now()) {
  if (!at) return null;
  const then = Date.parse(at);
  if (!Number.isFinite(then)) return null;
  const seconds = Math.round((now - then) / 1000);
  if (seconds < 0) return null;
  if (seconds < 90) return "just now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} minutes ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return hours === 1 ? "an hour ago" : `${hours} hours ago`;
  const days = Math.round(hours / 24);
  if (days === 1) return "yesterday";
  if (days < 30) return `${days} days ago`;
  const months = Math.round(days / 30);
  if (months < 12) return months === 1 ? "a month ago" : `${months} months ago`;
  const years = Math.round(months / 12);
  return years === 1 ? "a year ago" : `${years} years ago`;
}

/** The place and how long ago it was, in one line. */
export function placeSentence(place, now = Date.now()) {
  const label = placeLabel(place);
  if (!label) return null;
  const age = relativeAge(place?.at, now);
  return age ? `${label} · ${age}` : label;
}
