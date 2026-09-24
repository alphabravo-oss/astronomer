import { act, renderHook } from "@testing-library/react";
import { pageRowCount } from "@/lib/api/pagination";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  clusterDeliveryPath,
  deliveryEntityPath,
  deliveryProjectLabel,
  projectBoundToCluster,
  projectClusterId,
  useDeliveryPageIndex,
} from "./shared";

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
  }) =>
    select({
      pathname: "/dashboard/delivery/rollouts/rollout-1",
      searchStr: nav.search,
    }),
}));

vi.mock("@/lib/hooks/projects", () => ({
  useProjects: vi.fn(),
}));

describe("useDeliveryPageIndex", () => {
  beforeEach(() => {
    nav.search = "";
    nav.navigate.mockClear();
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

    expect(nav.navigate).toHaveBeenCalledWith({
      to: "/dashboard/delivery/rollouts/rollout-1?project=project-1&state=progressing&cluster_page=3",
      replace: true,
    });
  });

  it("removes the page parameter when returning to the first page", () => {
    nav.search = "project=project-1&page=4";
    const { result } = renderHook(() => useDeliveryPageIndex());

    act(() => result.current[1](0));

    expect(nav.navigate).toHaveBeenCalledWith({
      to: "/dashboard/delivery/rollouts/rollout-1?project=project-1",
      replace: true,
    });
  });
});

describe("deliveryProjectLabel", () => {
  it("uses the project display name", () => {
    expect(
      deliveryProjectLabel({
        name: "astronomer-system",
        displayName: "Astronomer System",
      }),
    ).toBe("Astronomer System");
  });

  it("falls back to the project name", () => {
    expect(deliveryProjectLabel({ name: "platform", displayName: "" })).toBe(
      "platform",
    );
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

  it("keeps entity URLs inside the cluster workspace", () => {
    expect(
      deliveryEntityPath("deployments", "dep-1", {
        clusterId: "cluster-1",
        projectId: "project-1",
      }),
    ).toBe(
      "/dashboard/clusters/cluster-1/delivery/deployments/dep-1?project=project-1",
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

describe("pageRowCount", () => {
  it("uses an authoritative total when the server knows it", () => {
    expect(
      pageRowCount({
        data: [{ id: 1 }],
        pagination: {
          total: 87,
          limit: 25,
          offset: 0,
          has_more: true,
          next_offset: 25,
        },
      }),
    ).toBe(87);
  });

  it("enables exactly one next fetch for an unknown total", () => {
    expect(
      pageRowCount({
        data: Array.from({ length: 25 }),
        pagination: {
          limit: 25,
          offset: 50,
          has_more: true,
          next_offset: 75,
        },
      }),
    ).toBe(76);
  });

  it("stops at the observed end of an unknown total", () => {
    expect(
      pageRowCount({
        data: Array.from({ length: 7 }),
        pagination: {
          limit: 25,
          offset: 75,
          has_more: false,
          next_offset: null,
        },
      }),
    ).toBe(82);
  });
});
