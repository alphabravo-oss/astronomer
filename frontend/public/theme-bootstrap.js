// Apply the stored theme before first paint. This remains an external,
// same-origin script so the application never needs CSP unsafe-inline.
(function () {
  try {
    // Keep the namespaced key: co-hosted applications may JSON-parse `theme`.
    var theme = localStorage.getItem("astronomer-theme");
    var dark =
      theme === "light"
        ? false
        : theme === "system"
          ? window.matchMedia("(prefers-color-scheme: dark)").matches
          : true;
    document.documentElement.classList.toggle("dark", dark);
  } catch (_error) {
    document.documentElement.classList.add("dark");
  }
})();
