// Which palette a reader opens in when nothing is saved for it (#112).
//
// The web UI's theme is a cookie, the reader's is localStorage, and a
// reader with nothing saved follows the former: dark app, dark book, no
// white flash at night.
import { test } from "node:test";
import assert from "node:assert/strict";
import { READER_THEME_COLORS, readerThemeFromCookie } from "../static/reader-theme.js";

test("a fresh reader with no app theme opens Light", () => {
  assert.equal(readerThemeFromCookie(""), "light");
  assert.equal(readerThemeFromCookie(undefined), "light");
  assert.equal(readerThemeFromCookie("session=abc; liseur_ui=light.grid.list"), "light");
});

test("a named app theme is the reader's theme", () => {
  for (const theme of ["dark", "tokyo-night", "rose-pine"]) {
    assert.equal(readerThemeFromCookie(`liseur_ui=${theme}.grid`), theme);
    assert.equal(readerThemeFromCookie(`liseur_ui=${theme}.grid`, true), theme);
  }
});

test("the system theme asks the OS", () => {
  assert.equal(readerThemeFromCookie("liseur_ui=system.grid", true), "dark");
  assert.equal(readerThemeFromCookie("liseur_ui=system.grid", false), "light");
});

test("an unrecognised cookie is not a theme", () => {
  for (const value of ["liseur_ui=", "liseur_ui=.grid", "liseur_ui=constructor.grid",
    "liseur_ui=__proto__.grid", "liseur_ui=nonsense.grid"]) {
    assert.equal(readerThemeFromCookie(value, true), "light", value);
  }
});

test("every palette has a status bar colour", () => {
  for (const theme of ["light", "sepia", "dark", "tokyo-night", "rose-pine", "black"]) {
    assert.match(READER_THEME_COLORS[theme], /^#[0-9a-f]{6}$/, theme);
  }
});
