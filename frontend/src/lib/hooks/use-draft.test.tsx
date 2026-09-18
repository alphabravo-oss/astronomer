import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useDraft } from "./use-draft";

describe("useDraft", () => {
  it("keeps edits for one revision and resets before rendering a new source", () => {
    const source = { name: "original" };
    const { result, rerender } = renderHook(({ value }) => useDraft(value), {
      initialProps: { value: source },
    });
    act(() => result.current[1]((value) => ({ ...value, name: "edited" })));
    expect(source.name).toBe("original");
    rerender({ value: source });
    expect(result.current[0].name).toBe("edited");
    rerender({ value: { name: "server revision" } });
    expect(result.current[0].name).toBe("server revision");
    rerender({ value: source });
    expect(result.current[0].name).toBe("original");
  });

  it("supports an explicit revision for pagination and stable dispatch", () => {
    const { result, rerender } = renderHook(
      ({ filter }) => useDraft(0, filter),
      { initialProps: { filter: "" } },
    );
    const dispatch = result.current[1];
    act(() => dispatch((page) => page + 1));
    expect(result.current[0]).toBe(1);
    expect(result.current[1]).toBe(dispatch);
    rerender({ filter: "new search" });
    expect(result.current[0]).toBe(0);
  });
});
