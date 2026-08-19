import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  clusterDeliveryPath,
  deliveryPageRowCount,
  deliveryProjectLabel,
  projectBoundToCluster,
  projectClusterId,
  useDeliveryPageIndex,
} from "./shared";

const nav = vi.hoisted(() => ({
  search: "",
  replace: vi.fn(),
}));

vi.mock("@/lib/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: nav.replace, back: vi.fn() }),
  usePathname: () => "/dashboard/delivery/rollouts/rollout-1",
  useSearchParams: () => new URLSearchParams(nav.search),
}));

vi.mock("@/lib/hooks", () => ({
  useProjects: vi.fn(),
}));

describe("useDeliveryPageIndex", () => {
  beforeEach(() => {
    nav.search = "";
    nav.replace.mockClear();
  });

  it.each(["page=-1", "page=1.5", "page=invalid"])(
    "falls back to the first page for %s",
    (search) => {
      nav.search = search;
      const { result } = renderHook(() => useDeliveryPageIndex());
      expect(result.current[0]).toBe(0);
    },
  );

  it("preserves project and filters while updating a named page", () => {
    nav.search = "project=project-1&state=progressing&cluster_page=2";
    const { result } = renderHook(() => useDeliveryPageIndex("cluster_page"));

    expect(result.current[0]).toBe(2);
    act(() => result.current[1](3));

    expect(nav.replace).toHaveBeenCalledWith(
      "/dashboard/delivery/rollouts/rollout-1?project=project-1&state=progressing&cluster_page=3",
    );
  });

  it("removes the page parameter when returning to the first page", () => {
    nav.search = "project=project-1&page=4";
    const { result } = renderHook(() => useDeliveryPageIndex());

    act(() => result.current[1](0));

    expect(nav.replace).toHaveBeenCalledWith(
      "/dashboard/delivery/rollouts/rollout-1?project=project-1",
    );
  });
});

describe("deliveryProjectLabel", () => {
  it("uses the cluster name when the project is bound to a known cluster", () => {
    expect(
      deliveryProjectLabel(
        {
          name: "astronomer-system",
          displayName: "Astronomer System",
          clusterId: "cluster-a",
        },
        new Map([["cluster-a", "fleet-a"]]),
      ),
    ).toBe("fleet-a");
  });

  it("falls back to the project display name when the cluster is unknown", () => {
    expect(
      deliveryProjectLabel(
        { name: "platform", displayName: "Platform", clusterId: "missing" },
        new Map(),
      ),
    ).toBe("Platform");
  });
});

describe("cluster delivery paths", () => {
  it("builds the Flux workspace URL for a cluster", () => {
    expect(clusterDeliveryPath("cluster-1")).toBe(
      "/dashboard/clusters/cluster-1/delivery",
    );
    expect(clusterDeliveryPath("cluster-1", "sources")).toBe(
      "/dashboard/clusters/cluster-1/delivery/sources",
    );
  });

  it("resolves the bound cluster from a project", () => {
    expect(projectClusterId({ clusterId: "a" })).toBe("a");
    expect(projectClusterId({ clusterIds: ["b"] })).toBe("b");
    expect(projectBoundToCluster({ clusterId: "a" }, "a")).toBe(true);
    expect(projectBoundToCluster({ clusterIds: ["b", "c"] }, "c")).toBe(true);
    expect(projectBoundToCluster({ clusterId: "a" }, "b")).toBe(false);
  });
});

describe("deliveryPageRowCount", () => {
  it("uses an authoritative total when the server knows it", () => {
    expect(
      deliveryPageRowCount(
        { data: [{ id: 1 }], count: 87, next: "/next", totalKnown: true },
        0,
        25,
      ),
    ).toBe(87);
  });

  it("enables exactly one next fetch for an unknown total", () => {
    expect(
      deliveryPageRowCount(
        {
          data: Array.from({ length: 25 }),
          count: 25,
          next: "/next",
          totalKnown: false,
        },
        2,
        25,
      ),
    ).toBe(76);
  });

  it("stops at the observed end of an unknown total", () => {
    expect(
      deliveryPageRowCount(
        { data: Array.from({ length: 7 }), count: 7, next: null, totalKnown: false },
        3,
        25,
      ),
    ).toBe(82);
  });
});
