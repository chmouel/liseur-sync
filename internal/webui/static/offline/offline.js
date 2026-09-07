if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("./sw.js", { scope: "./" }).catch((error) => {
    console.warn("offline shell could not be installed", error);
  });
}
