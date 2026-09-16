import { test } from "node:test";
import assert from "node:assert/strict";
import { fillPrompt, PROMPT_PLACEHOLDERS } from "../static/reader-prompt.js";

const where = {
  title: "Middlemarch",
  author: "George Eliot",
  series: "",
  chapter: "Chapter XV",
  page: "212",
  pages: "904",
  percent: "23",
  text: "Character is not cut in marble",
};

test("every placeholder the list names is substituted", () => {
  const template = PROMPT_PLACEHOLDERS.map((name) => `{${name}}`).join("|");
  assert.equal(
    fillPrompt(template, where),
    PROMPT_PLACEHOLDERS.map((name) => where[name]).join("|"),
  );
});

test("a placeholder appearing twice is substituted twice", () => {
  assert.equal(
    fillPrompt("{percent}% in, so nothing past {percent}%.", where),
    "23% in, so nothing past 23%.",
  );
});

test("a name this module does not know is left as it was typed", () => {
  assert.equal(
    fillPrompt("{title} {} {not_a_name} {TITLE}", where),
    "Middlemarch {} {not_a_name} {TITLE}",
  );
});

test("a known name with nothing behind it becomes empty, never undefined", () => {
  assert.equal(fillPrompt("[{series}]", where), "[]");
  assert.equal(fillPrompt("[{series}]", {}), "[]");
  assert.equal(fillPrompt("[{author}]", { author: null }), "[]");
});

test("a numeric value is spelled rather than dropped", () => {
  assert.equal(fillPrompt("{page} of {pages}", { page: 1, pages: 0 }), "1 of 0");
});

test("an empty template yields nothing at all", () => {
  assert.equal(fillPrompt("", where), "");
  assert.equal(fillPrompt(undefined, where), "");
  assert.equal(fillPrompt(null, where), "");
});

test("the passage itself is inserted verbatim, braces and all", () => {
  const quoted = 'a line with {braces} and "quotes"';
  assert.equal(fillPrompt('"{text}"', { ...where, text: quoted }), `"${quoted}"`);
});
