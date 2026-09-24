import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useDeliveryProjectScope } from "./shared";
const state = vi.hoisted(() => ({
  requested: "later",
  selected: {} as Record<string, unknown>,
  page: {} as Record<string, unknown>,
  navigate: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: () => null,
  useNavigate: () => state.navigate,
  useLocation: ({ select }: { select: (value: unknown) => unknown }) =>
    select({
      pathname: "/dashboard/delivery/bundles",
      searchStr: `?project=${state.requested}`,
    }),
}));
vi.mock("@tanstack/react-query", () => ({ useQuery: () => state.page }));
vi.mock("@/lib/hooks/projects", () => ({ useProject: () => state.selected }));
describe("delivery project scope", () => {
  beforeEach(() => {
    state.navigate.mockClear();
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
});
