import { test } from "node:test";
import assert from "node:assert/strict";
import {
  narrationIndex, offsetOfFraction, sentenceAt, characterAt, position, quoteOffset, narrationClock,
} from "../static/reader-narration.js";

const index = (...blocks) => narrationIndex(
  blocks.flatMap((texts, b) => [].concat(texts).map((text, n) => ({ node: `${b}.${n}`, text, block: `p${b}` }))),
  "en",
);
const said = (ix) => ix.sentences.map(s => ix.text.slice(s.start, s.end));

test("the chapter text is collapsed the way the server quotes it", () => {
  const ix = index(["  Call me\n\tIshmael.", " Some years ago "], ["— never mind ", "how long."]);
  assert.equal(ix.text, "Call me Ishmael. Some years ago — never mind how long.");
  const at = position(ix, ix.text.indexOf("Ishmael"));
  assert.deepEqual(at, { node: "0.0", offset: 11 });
  const end = position(ix, ix.text.indexOf("Some") + 4, true);
  assert.deepEqual(end, { node: "0.1", offset: 5 });
});

test("a mapping fraction counts visible characters other than whitespace, by code point", () => {
  // Eight glyphs: a, b, the emoji (two UTF-16 units, one code point), c, d, e, f, g.
  const ix = index(["ab 😀c   de"], [" fg"]);
  assert.equal(ix.glyphs.length, 8);
  assert.equal(offsetOfFraction(ix, 0), 0);
  assert.equal(ix.text.slice(offsetOfFraction(ix, 3 / 8)), "c de fg");
  assert.equal(ix.text.slice(offsetOfFraction(ix, 6 / 8)), "fg");
  assert.equal(offsetOfFraction(ix, 1), ix.text.length);
});

test("sentences break where a narrator pauses, not after titles or initials", () => {
  const ix = index("Chapter One", "Mr. Smith met J. R. Hartley. He left! “Did he?” Yes.");
  assert.deepEqual(said(ix), [
    "Chapter One",
    "Mr. Smith met J. R. Hartley.",
    "He left!",
    "“Did he?”",
    "Yes.",
  ]);
});

test("sentences fall back to punctuation without Intl.Segmenter", () => {
  const saved = Intl.Segmenter;
  Intl.Segmenter = undefined;
  try {
    assert.deepEqual(said(index("One. Two? “Three!” Four")), ["One.", "Two?", "“Three!”", "Four"]);
  } finally {
    Intl.Segmenter = saved;
  }
});

test("a run-on sentence is cut at a clause, never inside a word", () => {
  const clause = "and the river went on past the town and the fields, ";
  const ix = index(clause.repeat(12) + "until the sea.");
  const pieces = said(ix);
  assert.ok(pieces.length > 1);
  for (const piece of pieces) {
    assert.ok(piece.length <= 320, piece.length);
    assert.match(piece, /^\S.*\S$/);
  }
  assert.equal(pieces.join(" "), ix.text);
  assert.ok(pieces.slice(0, -1).every(piece => piece.endsWith(",")));
});

test("an offset belongs to its sentence, or to the next one between them", () => {
  const ix = index("First one here. Second one here.");
  const second = ix.text.indexOf("Second");
  assert.equal(sentenceAt(ix.sentences, 0).start, 0);
  assert.equal(sentenceAt(ix.sentences, second - 1).start, second);
  assert.equal(sentenceAt(ix.sentences, 1e9).start, second);
});

test("following stays at the narrated character in text without spaces", () => {
  for (const text of ["这是一个没有空格的句子。", "これは空白のない文です。", "anunbrokenword."]) {
    const ix = index(text);
    const character = characterAt(ix.text, ix.sentences[0], 5.9);
    assert.deepEqual(character, { start: 5, end: 6 });
    assert.equal(ix.text.slice(character.start, character.end), text[5]);
  }
});

test("the narrated character is clamped inside its sentence without splitting a code point", () => {
  const text = "Before. 😀中𠮷";
  const sentence = { start: 8, end: text.length };
  for (const offset of [-1, 8, 9]) {
    const character = characterAt(text, sentence, offset);
    assert.equal(text.slice(character.start, character.end), "😀");
  }
  for (const offset of [11, 12, text.length, 1e9]) {
    const character = characterAt(text, sentence, offset);
    assert.equal(text.slice(character.start, character.end), "𠮷");
    assert.equal(character.end, sentence.end);
  }
});

test("a quote is found after its highlight, and a repeated one nearest the estimate", () => {
  const ix = index("He said no. She said no. He said no.");
  const quote = { before: "She said ", highlight: "no.", after: " He said" };
  assert.equal(quoteOffset(ix, quote, 0), ix.text.indexOf("She said no.") + 12);
  const bare = { highlight: "said  no." };
  const last = ix.text.lastIndexOf("said no.") + 8;
  assert.equal(quoteOffset(ix, bare, ix.text.length), last);
  assert.equal(quoteOffset(ix, bare, 0), 11);
  assert.equal(quoteOffset(ix, { highlight: "nowhere" }, 0), null);
  assert.equal(quoteOffset(ix, { before: "unknown context ", highlight: "She said" }, 0), ix.text.indexOf("She said") + 8);
});

test("the narration clock runs at the pace it has seen, within bounds", () => {
  const clock = narrationClock();
  assert.equal(clock.offsetAt("a", 10, 123, 10), 123, "no sighting yet: the estimate");
  clock.snap("a", 10, 100, 10);
  assert.equal(clock.offsetAt("a", 20, 0, 10), 200, "one sighting: the mapping's pace");
  assert.equal(clock.offsetAt("b", 20, 7, 10), 7, "another chapter: the estimate");
  assert.equal(clock.offsetAt("a", 5, 7, 10), 7, "before the sighting: the estimate");
  clock.snap("a", 20, 250, 10);
  assert.equal(clock.pace, 12.5);
  assert.equal(clock.offsetAt("a", 30, 0, 10), 375);
  clock.snap("a", 40, 2000, 10);
  assert.equal(clock.pace, null, "an implausible pace is not learnt");
  clock.snap("a", 60, 2300, 10);
  clock.snap("a", 80, 2800, 10);
  assert.ok(clock.pace <= 16, "the pace is held to 1.6 times the mapping's");
  clock.reset();
  assert.equal(clock.offsetAt("a", 90, 5, 10), 5);
});
