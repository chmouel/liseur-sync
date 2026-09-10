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
});

test("an excerpt is counted in characters a reader would count", () => {
  const emoji = "🙂".repeat(EXCERPT_CHARS + 10);
  assert.equal(Array.from(excerptOf(op({ text: { highlight: emoji } }))).length, EXCERPT_CHARS);
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
