// Unit tests for what a reader is told about two places in a book.
//
// The distinction that matters here is exact versus near. A page
// derived from the resource another client named lands in the right
// chapter; one interpolated from a whole-book fraction does not, on
// exactly the books where it matters. Saying both the same way would be
// telling the reader something that is not true.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  EXCERPT_CHARS, excerptOf, placeHere, placeLabel, placeOf, placeSentence, relativeAge,
} from "../static/reader-place.js";
import { positionTable } from "../static/reader-positions.js";

// Three chapters of 1024, 4096 and 1024 stored bytes: 1, 4 and 1
// positions, six in all.
const sections = [
  { id: "one.xhtml", compressedSize: 1024 },
  { id: "two.xhtml", compressedSize: 4096 },
  { id: "three.xhtml", compressedSize: 1024 },
];
const table = positionTable(sections);
const view = { table, sections, resolveKey: () => null, editionSHA: "abc" };

const op = (locator, extra = {}) => ({
  op_id: "op-1", work_id: "w", progression: 0.5, locator, ...extra,
});

test("the book measures as Readium counts it", () => {
  assert.equal(table.total, 6);
});

test("a resource this book has gives an exact page", () => {
  const place = placeOf(op({
    href: "two.xhtml",
    locations: { progression: 0.5, totalProgression: 0.5 },
  }), view);
  assert.equal(place.exact, true);
  assert.equal(place.page, 4);
  assert.equal(placeLabel(place), "Page 4 of 6");
});

test("a bare fraction is interpolated, and says so", () => {
  const place = placeOf(op({ locations: { totalProgression: 0.5 } }), view);
  assert.equal(place.exact, false);
  assert.equal(place.page, 3);
  assert.equal(placeLabel(place), "Near page 3 of 6");
});

test("a resource named without an offset into it is near, not exact", () => {
  // Some clients write an href and a whole-book progression and nothing
  // else. The chapter is known, the page within it is not, and a page
  // presented as exact there is a number nobody measured.
  const place = placeOf(op({
    href: "two.xhtml",
    locations: { totalProgression: 0.9 },
  }), view);
  assert.equal(place.exact, false);
  assert.equal(place.page, 2, "the first page of the named chapter");
  assert.equal(placeLabel(place), "Near page 2 of 6");
});

test("the start of a resource is an exact page, not a missing one", () => {
  const place = placeOf(op({
    href: "two.xhtml",
    locations: { progression: 0, totalProgression: 0.2 },
  }), view);
  assert.equal(place.exact, true);
  assert.equal(place.page, 2);
});

test("a resource from another edition is not stood on", () => {
  const place = placeOf(op({
    href: "two.xhtml",
    locations: { progression: 0.9, totalProgression: 0.5 },
  }, { edition_sha: "other" }), view);
  assert.equal(place.exact, false, "another file's chapter offsets are not this file's");
  assert.equal(place.page, 3);
});

test("an op naming no edition is still trusted, as every old op did", () => {
  const place = placeOf(op({
    href: "two.xhtml", locations: { progression: 0.5, totalProgression: 0.5 },
  }), { ...view, editionSHA: "abc" });
  assert.equal(place.exact, true);
});

test("a resource this book does not have falls back to the fraction", () => {
  const place = placeOf(op({
    href: "elsewhere.xhtml", locations: { progression: 0.5, totalProgression: 0.25 },
  }), view);
  assert.equal(place.exact, false);
  assert.equal(place.page, 2);
});

test("an unmeasured book is described as a percentage", () => {
  const place = placeOf(op({ locations: { totalProgression: 0.42 } }), {});
  assert.equal(place.page, null);
  assert.equal(placeLabel(place), "42%");
});

test("the op's own progression stands in for a missing locator fraction", () => {
  const place = placeOf({ op_id: "o", progression: 0.75 }, {});
  assert.equal(placeLabel(place), "75%");
});

test("a place with nothing usable says nothing", () => {
  assert.equal(placeLabel(placeOf({ op_id: "o", progression: 7 }, {})), null);
  assert.equal(placeLabel(null), null);
  assert.equal(placeOf(null, view), null);
});

test("the page on screen is never interpolated", () => {
  const place = placeHere(
    { fraction: 0.5, section: { current: 1 }, sectionFraction: 0.99 }, view,
  );
  assert.equal(place.exact, true);
  assert.equal(place.page, 5, "the end of the second chapter, not the middle of the book");
  assert.equal(placeLabel(place), "Page 5 of 6");
});

test("the page on screen in an unmeasured book is still a percentage", () => {
  const place = placeHere({ fraction: 0.5, section: { current: 1 }, sectionFraction: 0.5 }, {});
  assert.equal(placeLabel(place), "50%");
  assert.equal(placeHere(null, view), null);
});

test("the excerpt is the passage, trimmed and capped", () => {
  assert.equal(excerptOf(op({ text: { highlight: "  a passage  " } })), "a passage");
  assert.equal(excerptOf(op({ text: { highlight: "" } })), null);
  assert.equal(excerptOf(op({})), null);
  assert.equal(excerptOf(null), null);
  const long = excerptOf(op({ text: { highlight: "x".repeat(1000) } }));
  assert.equal(long.length, EXCERPT_CHARS);
  assert.ok(long.endsWith("…"), "a passage the cap cut says so");
});

// The highlight of an anchor is one word by construction — the first
// word visible on the other device — so the context either side of it
// is the whole of what a reader can recognise their place by. A capture
// that filled its 32 code points stopped where it ran out of room,
// which is usually inside a word.
test("an anchor's context is read with its word", () => {
  assert.equal(
    excerptOf(op({
      text: {
        before: "é de ses mots. Il leva les yeux. ",
        highlight: "Alors",
        after: ", je me levai et partis vers la porte",
      },
    })),
    "…de ses mots. Il leva les yeux. Alors, je me levai et partis vers la…",
  );
});

test("context the capture did not have to cut is kept whole", () => {
  // `captureAnchor` cuts only when the block outruns the limit. A short
  // side stopped at the block's own edge, so its first and last words
  // are the writer's, and an ellipsis there would mark a cut nobody
  // made.
  assert.equal(
    excerptOf(op({ text: { before: "The ", highlight: "Carpet", after: "-Bag ends." } })),
    "The Carpet-Bag ends.",
  );
});

test("a script that does not space its words keeps its context", () => {
  // Japanese has no word boundary to drop back to, so dropping one
  // would throw the whole side away and leave the single word this
  // exists to escape.
  const before = "むかしむかし、あるところにおじいさんとおばあさんがすんでいましたと";
  const after = "。おじいさんは山へしばかりに、おばあさんは川へせんたくにいきました";
  const passage = excerptOf(op({ text: { before, highlight: "た", after } }));
  assert.ok(passage.includes("おじいさん"), passage);
  assert.equal(passage, "…" + before + "た" + after + "…");
});

test("the half of a word the capture cut is not shown", () => {
  assert.equal(
    excerptOf(op({
      text: {
        before: "orning he stood there and looked up ",
        highlight: "at",
        after: " the sky and then the night fell over everyth",
      },
    })),
    "…he stood there and looked up at the sky and then the night fell over…",
  );
});

test("a line in a document is not a pause in a sentence", () => {
  assert.equal(
    excerptOf(op({
      text: {
        before: "the\n  sky was\nbright and wide and very ",
        highlight: "very",
        after: " blue\nthat\tday and the next one after that xx",
      },
    })),
    "…sky was bright and wide and very very blue that day and the next one after that…",
  );
});

test("a passage with no context is not dressed up as a fragment", () => {
  // Some clients write a whole selection into `highlight` and no
  // context at all. Ellipses would claim something was cut away.
  assert.equal(
    excerptOf(op({ text: { highlight: "Call me Ishmael.", before: "", after: "" } })),
    "Call me Ishmael.",
  );
});

test("an excerpt is counted in characters a reader would count", () => {
  const emoji = "🙂".repeat(EXCERPT_CHARS + 10);
  assert.equal(Array.from(excerptOf(op({ text: { highlight: emoji } }))).length, EXCERPT_CHARS);
});

test("this device's side shows its own passage when one was taken", () => {
  const anchor = { before: "in the ", highlight: "beginning", after: " of it all" };
  const place = placeHere(
    { fraction: 0.5, section: { current: 1 }, sectionFraction: 0.5 }, { ...view, anchor },
  );
  assert.equal(place.excerpt, "in the beginning of it all");
  // A page whose first word is not unique in its block yields no
  // anchor, and a side with nothing to quote quotes nothing.
  assert.equal(
    placeHere({ fraction: 0.5, section: { current: 1 }, sectionFraction: 0.5 }, view).excerpt,
    null,
  );
});

test("how long ago, said the way a person would", () => {
  const now = Date.parse("2026-01-10T12:00:00Z");
  const ago = (iso) => relativeAge(iso, now);
  assert.equal(ago("2026-01-10T11:59:30Z"), "just now");
  assert.equal(ago("2026-01-10T11:45:00Z"), "15 minutes ago");
  assert.equal(ago("2026-01-10T11:00:00Z"), "an hour ago");
  assert.equal(ago("2026-01-10T07:00:00Z"), "5 hours ago");
  assert.equal(ago("2026-01-09T11:00:00Z"), "yesterday");
  assert.equal(ago("2026-01-05T12:00:00Z"), "5 days ago");
  assert.equal(ago("2025-12-05T12:00:00Z"), "a month ago");
  assert.equal(ago("2025-07-10T12:00:00Z"), "6 months ago");
  assert.equal(ago("2025-01-10T12:00:00Z"), "a year ago");
  assert.equal(ago("2023-01-10T12:00:00Z"), "3 years ago");
});

test("a clock from the future is not a sentence about the past", () => {
  const now = Date.parse("2026-01-10T12:00:00Z");
  assert.equal(relativeAge("2026-01-10T12:05:00Z", now), null);
  assert.equal(relativeAge("not a date", now), null);
  assert.equal(relativeAge(null, now), null);
});

test("the sentence carries the age when there is one", () => {
  const now = Date.parse("2026-01-10T12:00:00Z");
  const place = placeOf(op({
    href: "two.xhtml", locations: { progression: 0.5, totalProgression: 0.5 },
  }, { client_ts: "2026-01-10T11:00:00Z" }), view);
  assert.equal(placeSentence(place, now), "Page 4 of 6 · an hour ago");
  assert.equal(placeSentence(placeHere({ fraction: 0.5 }, {}), now), "50%");
  assert.equal(placeSentence(null, now), null);
});
