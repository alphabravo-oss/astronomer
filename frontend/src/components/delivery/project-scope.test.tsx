import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useDeliveryProjectScope } from "./shared";
import { useClusterScopeStore } from "@/lib/cluster-scope";
const state = vi.hoisted(() => ({
  requested: "later" as string | null,
  selected: {} as Record<string, unknown>,
  page: {} as Record<string, unknown>,
  navigate: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: () => null,
  useNavigate: () => state.navigate,
  useLocation: (options?: { select?: (value: unknown) => unknown }) => {
    const value = {
      pathname: "/dashboard/delivery/bundles",
      searchStr: state.requested === null ? "" : `?project=${state.requested}`,
    };
    return options?.select ? options.select(value) : value;
  },
}));
vi.mock("@tanstack/react-query", () => ({ useQuery: () => state.page }));
vi.mock("@/lib/hooks/projects", () => ({ useProject: () => state.selected }));
describe("delivery project scope", () => {
  beforeEach(() => {
    state.navigate.mockClear();
    useClusterScopeStore.setState({
      projectByCluster: {},
      namespacesByCluster: {},
    });
    state.requested = "later";
    state.page = {
      data: {
        data: [{ id: "first", clusterId: "cluster" }],
        pagination: { has_more: true },
      },
      isError: false,
    };
    state.selected = {
      data: { id: "later", clusterId: "cluster" },
      isError: false,
    };
  });
  it("resolves a selected project independently of the first page", () => {
    expect(
      renderHook(() => useDeliveryProjectScope()).result.current.projectId,
    ).toBe("later");
  });
  it("does not switch a denied project to a different accessible project", () => {
    state.selected.isError = true;
    expect(
      renderHook(() => useDeliveryProjectScope()).result.current.projectId,
    ).toBe("");
    expect(state.navigate).not.toHaveBeenCalled();
  });
  it("rejects a selected project outside the cluster binding", () => {
    expect(
      renderHook(() => useDeliveryProjectScope({ clusterId: "other" })).result
        .current.projectId,
    ).toBe("");
  });
  it("does not treat a one-row partial page as the only project", () => {
    state.requested = "";
    state.selected = {};
    expect(
      renderHook(() => useDeliveryProjectScope()).result.current.projectId,
    ).toBe("");
    expect(state.navigate).not.toHaveBeenCalled();
  });
  it("uses the top-bar project remembered for a cluster route", () => {
    state.requested = null;
    useClusterScopeStore
      .getState()
      .setClusterScope("cluster", ["team-a"], "later");
    expect(
      renderHook(() => useDeliveryProjectScope({ clusterId: "cluster" })).result
        .current.projectId,
    ).toBe("later");
  });
});
