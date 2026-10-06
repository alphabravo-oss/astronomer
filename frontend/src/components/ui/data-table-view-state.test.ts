import { describe, expect, it } from "vitest";
import {
  decodeViewState,
  encodeViewState,
  isEmptyViewState,
  sanitizeViewState,
  viewTableKey,
} from "@/components/ui/data-table-view-state";

const keys = new Set(["name", "status", "age", "actions"]);

describe("view state", () => {
  it("round-trips through the URL encoding", () => {
    const state = sanitizeViewState(
      {
        search: "crash",
        filters: { status: ["Failed", "Pending"] },
        sort: [{ id: "age", desc: true }],
        hidden: ["age"],
        order: ["status", "name"],
        pinning: { start: ["name"], end: ["actions"] },
      },
      keys,
    );
    const encoded = encodeViewState(state);
    expect(decodeViewState(encoded, keys)).toEqual(state);
    // And survives an actual query-string round trip.
    const qs = new URLSearchParams({ "tv-pods": encoded }).toString();
    expect(
      decodeViewState(new URLSearchParams(qs).get("tv-pods"), keys),
    ).toEqual(state);
  });

  it("drops unknown columns and malformed fields", () => {
    const state = sanitizeViewState(
      {
        filters: { ghost: ["x"], status: "Failed" },
        sort: [{ id: "ghost", desc: false }, { id: "name" }],
        hidden: ["ghost", "age"],
        order: [1, 2],
        pinning: { start: ["ghost", "name"], end: ["name", "actions"] },
      },
      keys,
    );
    expect(state.filters).toBeUndefined();
    expect(state.sort).toBeUndefined();
    expect(state.hidden).toEqual(["age"]);
    expect(state.order).toBeUndefined();
    expect(state.pinning).toEqual({ start: ["name"], end: ["actions"] });
  });

  it("treats garbage and empty state as no state", () => {
    expect(decodeViewState("not json", keys)).toBeNull();
    expect(decodeViewState("[]", keys)).toBeNull();
    expect(decodeViewState("", keys)).toBeNull();
    expect(encodeViewState({})).toBe("");
    expect(isEmptyViewState(sanitizeViewState({ search: "" }, keys))).toBe(
      true,
    );
  });

  it("maps persistKey to a valid server table key", () => {
    expect(viewTableKey("Explorer:Pods")).toBe("explorer:pods");
    expect(viewTableKey("a b/c")).toBe("a-b/c");
    expect(viewTableKey("---")).toBe("table");
    expect(viewTableKey("x".repeat(200))).toHaveLength(128);
    expect(viewTableKey("clusters")).toMatch(/^[a-z0-9][a-z0-9:._/-]*$/);
  });
});
