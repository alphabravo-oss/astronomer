import { describe, expect, it } from "vitest";
import { extColumnLayouts, extFieldKind } from "./ext-table-layout";

describe("ext table layout", () => {
  it("maps formats to kinds", () => {
    expect(extFieldKind("number")).toBe("count");
    expect(extFieldKind("bytes")).toBe("bytes");
    expect(extFieldKind("datetime")).toBe("date");
    expect(extFieldKind("badge")).toBe("badge");
    expect(extFieldKind(undefined)).toBe("text");
  });

  it("grows exactly the first text column", () => {
    const layouts = extColumnLayouts([
      { format: "badge" },
      { format: "text" },
      { format: "text" },
    ]);
    expect(layouts.map((l) => l.grow)).toEqual([false, true, false]);
  });

  it("falls back to the first column when no text field exists", () => {
    const layouts = extColumnLayouts([
      { format: "number" },
      { format: "bytes" },
    ]);
    expect(layouts.map((l) => l.grow)).toEqual([true, false]);
  });
});
