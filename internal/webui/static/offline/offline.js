// The cached shell is shared, so apply only the local presentation preference.
// Missing or unrecognised choices keep the stylesheet's light default.
const appearance = document.cookie.split(";").map(value => value.trim())
  .find(value => value.startsWith("liseur_ui="))?.slice("liseur_ui=".length);
for (const theme of (appearance || "").split(".")) {
  if (["light", "dark", "system", "tokyo-night", "rose-pine"].includes(theme)) {
    document.documentElement.dataset.theme = theme;
  }
}

if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("./sw.js", { scope: "./" }).catch((error) => {
    console.warn("offline shell could not be installed", error);
  });
}
