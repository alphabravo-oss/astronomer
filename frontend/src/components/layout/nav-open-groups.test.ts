import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { Box } from "lucide-react";

import { useOpenNavGroups } from "@/components/layout/nav-open-groups";
import type { NavGroup } from "@/components/layout/sidebar-navigation";

const groups: NavGroup[] = [
  {
    label: "Home",
    items: [{ label: "Overview", href: "/dashboard", icon: Box, exact: true }],
  },
  {
    label: "Workloads",
    items: [{ label: "Pods", href: "/dashboard/pods", icon: Box }],
  },
  {
    label: "Services",
    items: [{ label: "Services", href: "/dashboard/services", icon: Box }],
  },
];

beforeEach(() => {
  window.localStorage.clear();
});

describe("useOpenNavGroups", () => {
  it("opens the active group on mount", () => {
    const { result } = renderHook(() =>
      useOpenNavGroups("global", groups, "/dashboard/pods"),
    );
    expect(result.current.openGroups.has("Workloads")).toBe(true);
  });

  it("closes the previous group when opening another or navigating", () => {
    const { result, rerender } = renderHook(
      ({ pathname }) => useOpenNavGroups("global", groups, pathname),
      { initialProps: { pathname: "/dashboard/pods" } },
    );
    act(() => result.current.toggleGroup("Services"));
    expect([...result.current.openGroups]).toEqual(["Services"]);

    rerender({ pathname: "/dashboard" });
    expect([...result.current.openGroups]).toEqual(["Home"]);
    rerender({ pathname: "/dashboard/services" });
    expect([...result.current.openGroups]).toEqual(["Services"]);
  });

  it("toggling a group closes it, and persists per scope", () => {
    const { result } = renderHook(() =>
      useOpenNavGroups("global", groups, "/outside"),
    );
    act(() => result.current.toggleGroup("Workloads"));
    expect(result.current.openGroups.has("Workloads")).toBe(true);
    act(() => result.current.toggleGroup("Workloads"));
    expect(result.current.openGroups.has("Workloads")).toBe(false);
    expect(
      JSON.parse(
        window.localStorage.getItem("astronomer.sidebar.openGroups.global") ??
          "[]",
      ),
    ).not.toContain("Workloads");
  });

  it("restores persisted groups for a scope on a fresh mount", () => {
    window.localStorage.setItem(
      "astronomer.sidebar.openGroups.cluster",
      JSON.stringify(["Services"]),
    );
    const { result } = renderHook(() =>
      useOpenNavGroups("cluster", groups, "/outside"),
    );
    expect(result.current.openGroups.has("Services")).toBe(true);
  });

  it("normalizes old multi-open preferences and ignores unavailable groups", () => {
    window.localStorage.setItem(
      "astronomer.sidebar.openGroups.global",
      JSON.stringify(["Workloads", "Services", "Removed section", 1]),
    );
    const { result } = renderHook(() =>
      useOpenNavGroups("global", groups, "/outside"),
    );
    expect([...result.current.openGroups]).toEqual(["Services"]);
    act(() => result.current.toggleGroup("Workloads"));
    expect(
      JSON.parse(
        window.localStorage.getItem("astronomer.sidebar.openGroups.global")!,
      ),
    ).toEqual(["Workloads"]);
  });

  it("prefers the active route over persisted groups on a deep link", () => {
    window.localStorage.setItem(
      "astronomer.sidebar.openGroups.global",
      JSON.stringify(["Home", "Services"]),
    );
    const { result } = renderHook(() =>
      useOpenNavGroups("global", groups, "/dashboard/pods"),
    );
    expect([...result.current.openGroups]).toEqual(["Workloads"]);
  });

  it("does not leak groups across global and cluster scopes", () => {
    window.localStorage.setItem(
      "astronomer.sidebar.openGroups.cluster",
      JSON.stringify(["Services"]),
    );
    const { result, rerender } = renderHook(
      ({ scope }: { scope: "global" | "cluster" }) =>
        useOpenNavGroups(scope, groups, "/outside"),
      { initialProps: { scope: "global" } },
    );
    act(() => result.current.toggleGroup("Workloads"));
    rerender({ scope: "cluster" });
    expect([...result.current.openGroups]).toEqual(["Services"]);
    act(() => result.current.toggleGroup("Tool UIs"));
    expect([...result.current.openGroups]).toEqual(["Tool UIs"]);
    rerender({ scope: "global" });
    expect([...result.current.openGroups]).toEqual(["Workloads"]);
  });

  it("preserves a manual collapse when navigation metadata refreshes", () => {
    const { result, rerender } = renderHook(
      ({ navGroups }) =>
        useOpenNavGroups("global", navGroups, "/dashboard/pods"),
      { initialProps: { navGroups: groups } },
    );
    act(() => result.current.toggleGroup("Workloads"));
    rerender({ navGroups: [...groups] });
    expect([...result.current.openGroups]).toEqual([]);
  });

  it("tolerates corrupt stored preferences", () => {
    window.localStorage.setItem("astronomer.sidebar.openGroups.global", "{");
    const { result } = renderHook(() =>
      useOpenNavGroups("global", groups, "/outside"),
    );
    expect([...result.current.openGroups]).toEqual([]);
  });
});
