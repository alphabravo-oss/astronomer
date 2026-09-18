/**
 * useTabParam (P2.4): the hook's per-page allowlist stays the real validator
 * on top of the routes' passthrough `validateSearch`, and its setter must
 * preserve every unrelated query param on a no-scroll replace.
 */
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useTabParam } from "@/lib/use-tab-param";

const nav = vi.hoisted(() => ({
  search: "",
  navigate: vi.fn(),
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  useNavigate: () => nav.navigate,
  useLocation: <T,>({ select }: { select: (location: { pathname: string; searchStr: string }) => T }) =>
    select({ pathname: "/dashboard/security", searchStr: nav.search }),
}));

const KEYS = ["cis", "templates", "policies"] as const;

describe("useTabParam", () => {
  beforeEach(() => {
    nav.search = "";
    nav.navigate.mockClear();
  });

  it("falls back when the param is absent", () => {
    const { result } = renderHook(() => useTabParam(KEYS, "cis"));
    expect(result.current[0]).toBe("cis");
  });

  it("falls back when the param is not in the allowlist", () => {
    nav.search = "tab=bogus";
    const { result } = renderHook(() => useTabParam(KEYS, "cis"));
    expect(result.current[0]).toBe("cis");
  });

  it("resolves an allowlisted param", () => {
    nav.search = "tab=policies";
    const { result } = renderHook(() => useTabParam(KEYS, "cis"));
    expect(result.current[0]).toBe("policies");
  });

  it("preserves unrelated query params on setTab and replaces without scroll", () => {
    nav.search = "cluster=c1&tab=cis";
    const { result } = renderHook(() => useTabParam(KEYS, "cis"));

    act(() => {
      result.current[1]("templates");
    });

    expect(nav.navigate).toHaveBeenCalledWith({
      to: "/dashboard/security?cluster=c1&tab=templates",
      replace: true,
      resetScroll: false,
    });
  });

  it("supports a custom param name without touching ?tab=", () => {
    nav.search = "tab=cis&sync=OutOfSync";
    const { result } = renderHook(() =>
      useTabParam(["", "Synced", "OutOfSync"] as const, "", "sync"),
    );
    expect(result.current[0]).toBe("OutOfSync");

    act(() => {
      result.current[1]("Synced");
    });

    expect(nav.navigate).toHaveBeenCalledWith({
      to: "/dashboard/security?tab=cis&sync=Synced",
      replace: true,
      resetScroll: false,
    });
  });
});
