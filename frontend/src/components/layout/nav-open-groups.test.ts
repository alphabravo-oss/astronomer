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

  it("keeps a group open when navigating to a different group", () => {
    const { result, rerender } = renderHook(
      ({ pathname }) => useOpenNavGroups("global", groups, pathname),
      { initialProps: { pathname: "/dashboard/pods" } },
    );
    act(() => result.current.toggleGroup("Services"));
    expect(result.current.openGroups.has("Workloads")).toBe(true);
    expect(result.current.openGroups.has("Services")).toBe(true);

    rerender({ pathname: "/dashboard/services" });
    expect(result.current.openGroups.has("Workloads")).toBe(true);
    expect(result.current.openGroups.has("Services")).toBe(true);
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
});
