import { beforeEach, describe, expect, it } from "vitest";
import { applyDensity, DENSITY_STORAGE_KEY } from "./density";
import { cn } from "./utils";

describe("cn with semantic type tokens", () => {
  it("keeps font-size tokens alongside text color utilities", () => {
    expect(cn("text-meta", "text-muted-foreground")).toBe(
      "text-meta text-muted-foreground",
    );
    expect(cn("text-body", "text-meta")).toBe("text-meta");
  });
});

describe("applyDensity", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute("data-density");
  });

  it("sets data-density and caches compact", () => {
    applyDensity("compact");
    expect(document.documentElement.dataset.density).toBe("compact");
    expect(localStorage.getItem(DENSITY_STORAGE_KEY)).toBe("compact");
  });

  it("clears the attribute and cache for comfortable or null", () => {
    applyDensity("compact");
    applyDensity("comfortable");
    expect(document.documentElement.hasAttribute("data-density")).toBe(false);
    expect(localStorage.getItem(DENSITY_STORAGE_KEY)).toBeNull();
    applyDensity("compact");
    applyDensity(null);
    expect(document.documentElement.hasAttribute("data-density")).toBe(false);
  });
});
