// Runs while the head is parsed, before the first paint, so the shelf
// never shows a link the device cannot follow: the installed app has no
// back button, and the browser's error page is the end of the road.
// Nothing else here depends on it.
(function () {
  const root = document.documentElement;
  const draw = () => {
    root.dataset.connection = navigator.onLine === false ? "offline" : "online";
  };
  draw();
  addEventListener("online", draw);
  addEventListener("offline", draw);
})();
