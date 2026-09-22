/**
 * useSearchParam (P023.7): a free-text URL query param generalising
 * useTabParam. The setter must preserve unrelated params, replace (not
 * push) history, and debounce the URL write when asked without delaying
 * the value it returns.
 */
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSearchParam } from "@/lib/use-search-param";

const nav = vi.hoisted(() => ({
  search: "",
  navigate: vi.fn(),
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  useNavigate: () => nav.navigate,
  useLocation: <T,>({
    select,
  }: {
    select: (location: { pathname: string; searchStr: string }) => T;
  }) => select({ pathname: "/dashboard/clusters", searchStr: nav.search }),
}));

describe("useSearchParam", () => {
  beforeEach(() => {
    nav.search = "";
    nav.navigate.mockClear();
  });

  it("reads an empty value when the param is absent", () => {
    const { result } = renderHook(() => useSearchParam("q"));
    expect(result.current[0]).toBe("");
  });

  it("resolves the current value from the URL", () => {
    nav.search = "q=prod";
    const { result } = renderHook(() => useSearchParam("q"));
    expect(result.current[0]).toBe("prod");
  });

  it("commits immediately (replace, no scroll reset) and preserves other params", () => {
    nav.search = "status=Healthy";
    const { result } = renderHook(() => useSearchParam("q"));

    act(() => {
      result.current[1]("prod");
    });

    expect(result.current[0]).toBe("prod");
    expect(nav.navigate).toHaveBeenCalledWith({
      to: "/dashboard/clusters?status=Healthy&q=prod",
      replace: true,
      resetScroll: false,
    });
  });

  it("deletes the param on an empty value", () => {
    nav.search = "q=prod&status=Healthy";
    const { result } = renderHook(() => useSearchParam("q"));

    act(() => {
      result.current[1]("");
    });

    expect(nav.navigate).toHaveBeenCalledWith({
      to: "/dashboard/clusters?status=Healthy",
      replace: true,
      resetScroll: false,
    });
  });

  describe("debounced", () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it("updates the returned value immediately but delays the URL write", () => {
      const { result } = renderHook(() =>
        useSearchParam("q", { debounceMs: 250 }),
      );

      act(() => {
        result.current[1]("p");
      });
      expect(result.current[0]).toBe("p");
      expect(nav.navigate).not.toHaveBeenCalled();

      act(() => {
        vi.advanceTimersByTime(249);
      });
      expect(nav.navigate).not.toHaveBeenCalled();

      act(() => {
        vi.advanceTimersByTime(1);
      });
      expect(nav.navigate).toHaveBeenCalledTimes(1);
      expect(nav.navigate).toHaveBeenCalledWith({
        to: "/dashboard/clusters?q=p",
        replace: true,
        resetScroll: false,
      });
    });

    it("only fires once for rapid keystrokes within the debounce window", () => {
      const { result } = renderHook(() =>
        useSearchParam("q", { debounceMs: 250 }),
      );

      act(() => {
        result.current[1]("p");
        vi.advanceTimersByTime(100);
        result.current[1]("pr");
        vi.advanceTimersByTime(100);
        result.current[1]("pro");
        vi.advanceTimersByTime(250);
      });

      expect(nav.navigate).toHaveBeenCalledTimes(1);
      expect(nav.navigate).toHaveBeenCalledWith({
        to: "/dashboard/clusters?q=pro",
        replace: true,
        resetScroll: false,
      });
    });
  });
});
