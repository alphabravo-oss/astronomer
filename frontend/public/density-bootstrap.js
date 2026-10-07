// Apply the cached interface density before first paint. External,
// same-origin script so the application never needs CSP unsafe-inline.
(function () {
  try {
    if (localStorage.getItem("astronomer-density") === "compact") {
      document.documentElement.setAttribute("data-density", "compact");
    }
  } catch (_error) {
    // Default (comfortable) density applies.
  }
})();
