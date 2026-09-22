import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { useDocumentTitle } from "@/lib/use-document-title";

describe("useDocumentTitle", () => {
  it("joins the given parts with the product name", () => {
    renderHook(() => useDocumentTitle(["Clusters", "Smoke East"]));
    expect(document.title).toBe("Clusters · Smoke East · Astronomer");
  });

  it("drops falsy parts", () => {
    renderHook(() => useDocumentTitle(["Overview", ""]));
    expect(document.title).toBe("Overview · Astronomer");
  });

  it("falls back to just the product name with no parts", () => {
    renderHook(() => useDocumentTitle([]));
    expect(document.title).toBe("Astronomer");
  });

  it("updates the title when parts change and restores it on unmount", () => {
    const original = document.title;
    const { rerender, unmount } = renderHook(
      ({ parts }) => useDocumentTitle(parts),
      { initialProps: { parts: ["Clusters"] } },
    );
    expect(document.title).toBe("Clusters · Astronomer");
    rerender({ parts: ["RBAC"] });
    expect(document.title).toBe("RBAC · Astronomer");
    unmount();
    expect(document.title).toBe(original);
  });
});
