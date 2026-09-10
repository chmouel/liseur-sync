// Unit tests for the passage anchor this reader writes for another one.
//
// The shape is the Android app's, and the two must agree exactly or
// neither can use the other's anchors: the marker, the context lengths,
// the selector spelling. The DOM half (captureAnchor) needs a real
// document and is exercised in the browser harness; what is here is the
// part that decides whether an anchor is worth writing down at all.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  MARKER, MAX_AFTER, MAX_BEFORE, MAX_HIGHLIGHT,
  markLocator, occurrences, quoteOf,
} from "../static/reader-anchor.js";

const locator = () => ({
  href: "OEBPS/ch1.xhtml",
  type: "application/xhtml+xml",
  locations: { progression: 0.5, totalProgression: 0.2, position: 3 },
});

const anchor = (over = {}) => ({
  cssSelector: "#p42",
  before: "and then ",
  highlight: "Ishmael",
  after: " said nothing",
  ...over,
});

test("the marker and the context lengths are the app's", () => {
  // These numbers are a contract with ExactLocatorAnchor.kt. Changing
  // one here without changing it there strands every anchor.
  assert.equal(MARKER, "liseurAnchor");
  assert.equal(MAX_BEFORE, 32);
  assert.equal(MAX_HIGHLIGHT, 64);
  assert.equal(MAX_AFTER, 32);
});

test("a marked locator says so, and keeps what it already said", () => {
  const marked = markLocator(locator(), anchor());
  assert.equal(marked.locations[MARKER], 1);
  assert.equal(marked.locations.cssSelector, "#p42");
  assert.deepEqual(marked.text, {
    before: "and then ", highlight: "Ishmael", after: " said nothing",
  });
  // The chapter rung underneath must survive being marked: if the
  // quote will not resolve on the other device, that is what is left.
  assert.equal(marked.locations.progression, 0.5);
  assert.equal(marked.locations.totalProgression, 0.2);
  assert.equal(marked.href, "OEBPS/ch1.xhtml");
});

test("context is cut to the app's bounds, from the right end", () => {
  const marked = markLocator(locator(), anchor({
    before: "x".repeat(100) + "END",
    highlight: "y".repeat(100),
    after: "START" + "z".repeat(100),
  }));
  // `before` is what comes *immediately* before, so it keeps its tail.
  assert.equal(marked.text.before.length, MAX_BEFORE);
  assert.ok(marked.text.before.endsWith("END"));
  assert.equal(marked.text.highlight.length, MAX_HIGHLIGHT);
  assert.equal(marked.text.after.length, MAX_AFTER);
  assert.ok(marked.text.after.startsWith("START"));
});

test("a character is not cut in half", () => {
  // Slicing UTF-16 units would leave half a surrogate pair, which no
  // longer matches itself and so anchors nothing.
  const marked = markLocator(locator(), anchor({ highlight: "😀".repeat(80) }));
  assert.equal([...marked.text.highlight].length, MAX_HIGHLIGHT);
  assert.equal(marked.text.highlight, "😀".repeat(MAX_HIGHLIGHT));
});

test("an anchor that would not fit is not written at all", () => {
  // Refused, not truncated into something that anchors the wrong
  // passage. The unmarked locator still names the chapter.
  const marked = markLocator(locator(), anchor({ cssSelector: "#" + "a".repeat(300) }), 200);
  assert.deepEqual(marked, locator());
  assert.equal(marked.locations[MARKER], undefined);
});

test("nothing worth saying leaves the locator alone", () => {
  assert.deepEqual(markLocator(locator(), null), locator());
  assert.deepEqual(markLocator(locator(), anchor({ highlight: "" })), locator());
  assert.deepEqual(markLocator(locator(), anchor({ cssSelector: "" })), locator());
  // A selector past the app's own cap is not one the app will read.
  assert.deepEqual(markLocator(locator(), anchor({ cssSelector: "x".repeat(3000) })), locator());
  assert.equal(markLocator(null, anchor()), null);
});

test("a quote that appears twice in its block is not unique", () => {
  const block = "the sea. the whale. the sea. the end.";
  assert.equal(occurrences(block, "the whale."), 1);
  assert.equal(occurrences(block, "the sea."), 2);
  assert.equal(occurrences(block, "nowhere"), 0);
  assert.equal(occurrences(block, ""), 0);
  assert.equal(occurrences("", "x"), 0);
});

test("the quote is the three parts joined, in order", () => {
  assert.equal(quoteOf(anchor()), "and then Ishmael said nothing");
  assert.equal(quoteOf({ highlight: "only" }), "only");
  assert.equal(quoteOf(null), "");
});

test("the source locator is never modified", () => {
  const original = locator();
  const before = JSON.stringify(original);
  markLocator(original, anchor());
  assert.equal(JSON.stringify(original), before);
});
