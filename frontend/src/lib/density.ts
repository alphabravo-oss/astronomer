export type InterfaceDensity = "compact" | "comfortable";

// Namespaced like the theme key. Caches the last known server preference so
// public/density-bootstrap.js can set data-density before first paint without
// an inline script (CSP).
export const DENSITY_STORAGE_KEY = "astronomer-density";

/** Comfortable is the default look, so it is represented by no attribute. */
export function applyDensity(density: InterfaceDensity | null) {
  const root = document.documentElement;
  if (density === "compact") root.setAttribute("data-density", "compact");
  else root.removeAttribute("data-density");
  try {
    if (density === "compact")
      localStorage.setItem(DENSITY_STORAGE_KEY, density);
    else localStorage.removeItem(DENSITY_STORAGE_KEY);
  } catch {
    // Storage unavailable: the attribute still applies in-session.
  }
}
