import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useStorageSnapshot } from "./use-storage-snapshot";
import {
  parsePersistedSizing,
  parsePersistedVisibility,
} from "@/components/ui/data-table-state";

afterEach(() => window.localStorage.clear());

describe("storage snapshots", () => {
  it("notifies mounted readers for writes in this tab and storage events from other tabs", () => {
    const first = renderHook(() => useStorageSnapshot("test:table"));
    const second = renderHook(() => useStorageSnapshot("test:table"));
    act(() => first.result.current[1]({ name: false }));
    expect(second.result.current[0]).toBe('{"name":false}');
    act(() => {
      window.localStorage.setItem("test:table", '{"name":true}');
      window.dispatchEvent(new StorageEvent("storage", { key: "test:table" }));
    });
    expect(first.result.current[0]).toBe('{"name":true}');
    expect(second.result.current[0]).toBe('{"name":true}');
  });

  it("switches storage scopes without exposing the old table's preferences", () => {
    window.localStorage.setItem("table:a", '{"name":false}');
    window.localStorage.setItem("table:b", '{"size":false}');
    const { result, rerender } = renderHook(
      ({ scope }) => useStorageSnapshot(scope),
      { initialProps: { scope: "table:a" } },
    );
    expect(result.current[0]).toBe('{"name":false}');
    rerender({ scope: "table:b" });
    expect(result.current[0]).toBe('{"size":false}');
  });

  it("rejects malformed and wrong-type persisted preferences", () => {
    expect(parsePersistedVisibility("[false]")).toEqual({});
    expect(parsePersistedVisibility('{"name":false,"size":"hidden"}')).toEqual({
      name: false,
    });
    expect(parsePersistedSizing('{"name":180,"size":-2,"bad":"100"}')).toEqual({
      name: 180,
    });
    expect(parsePersistedSizing("invalid json")).toEqual({});
  });
});
