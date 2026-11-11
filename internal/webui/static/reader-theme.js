// What colour the reader's chrome is, and which palette it opens in.
//
// The reader's palette is a browser preference (localStorage), but the
// web UI's theme is a cookie the server also rendered, and an account
// set to dark should not open a white page at night. This file is the
// small, pure part of that decision so it can be tested without a book.

// The status bar colour for each palette: the stage colours from
// style.css, since the reader's bar is painted over the stage.
export const READER_THEME_COLORS = {
  original: "#ffffff",
  light: "#ffffff",
  sepia: "#f6ecd9",
  dark: "#18181a",
  "tokyo-night": "#16161e",
  "rose-pine": "#1f1d2e",
  black: "#000000",
};

// readerThemeFromCookie is what a reader with nothing saved opens in.
// A named app theme is the reader's; "system" is the OS's; anything
// else, including no cookie at all, is Light.
export function readerThemeFromCookie(cookie, prefersDark = false) {
  const value = String(cookie || "").split(";").map((part) => part.trim())
    .find((part) => part.startsWith("liseur_ui="))
    ?.slice("liseur_ui=".length).split(".")[0];
  if (value === "dark" || value === "tokyo-night" || value === "rose-pine") return value;
  if (value === "system" && prefersDark) return "dark";
  return "light";
}
