// The cached shell is shared, so apply only the local presentation preference.
// Missing or unrecognised choices keep the stylesheet's light default.
const THEME_COLORS = {
  light: "#ffffff",
  dark: "#18181a",
  "tokyo-night": "#16161e",
  "rose-pine": "#1f1d2e",
};

const THEMES = ["light", "dark", "system", "tokyo-night", "rose-pine"];

const appearance = document.cookie.split(";").map(value => value.trim())
  .find(value => value.startsWith("liseur_ui="))?.slice("liseur_ui=".length);
let theme = "";
for (const choice of (appearance || "").split(".")) {
  if (THEMES.includes(choice)) {
    theme = choice;
    document.documentElement.dataset.theme = choice;
  }
}

// The status bar has to match the shelf, and the shelf's palette is the
// cookie's, which the server never saw: the shell is cached while the
// cookie is per browser. A named theme settles on its own colour; only
// an explicit "system" is left to the media-queried pair in
// shell.html. A missing or unrecognised choice is the light shelf —
// the stylesheet's default — so it must resolve to light here too,
// rather than to a dark system bar over a light page.
if (theme !== "system") {
  const named = theme || "light";
  const meta = document.getElementById("offline-theme-color");
  if (meta) {
    meta.removeAttribute("media");
    meta.setAttribute("content", THEME_COLORS[named] || THEME_COLORS.light);
  }
  document.getElementById("offline-theme-color-dark")?.remove();
}

if (navigator.storage?.persist) {
  navigator.storage.persist().catch(() => {});
}

if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("./sw.js", { scope: "./" }).catch((error) => {
    console.warn("offline shell could not be installed", error);
  });
}
