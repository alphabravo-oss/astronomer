import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";

import { SearchableClusterSwitcher } from "./cluster-scope-controls";

vi.stubGlobal(
  "ResizeObserver",
  class {
    observe() {
      return undefined;
    }
    unobserve() {
      return undefined;
    }
    disconnect() {
      return undefined;
    }
  },
);

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  useLocation: ({ select }: { select: (value: unknown) => unknown }) =>
    select({ pathname: "/dashboard/clusters/cluster-a", searchStr: "" }),
}));

vi.mock("@/lib/hooks/cluster-search", () => ({
  useClusterSearch: () => ({
    data: { pages: [{ data: [] }] },
    isLoading: false,
    isError: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    refetch: vi.fn(),
    fetchNextPage: vi.fn(),
  }),
}));

vi.mock("@/lib/hooks/projects", () => ({
  useProject: () => ({}),
  useProjectSearch: () => ({}),
}));

vi.mock("@/lib/cluster-scope", () => ({
  useClusterNamespaceScope: vi.fn(),
  useClusterScopeStore: (select: (state: unknown) => unknown) =>
    select({ namespacesByCluster: {}, projectByCluster: {} }),
  withClusterScopeSelection: (path: string) => path,
}));

describe("SearchableClusterSwitcher", () => {
  it("focuses search on open and restores the trigger on Escape", async () => {
    render(
      <SearchableClusterSwitcher
        clusterId="cluster-a"
        fallbackName="Current cluster"
      />,
    );

    const trigger = screen.getByRole("button", { name: /Current cluster/i });
    fireEvent.click(trigger);

    const search = screen.getByPlaceholderText("Find a cluster...");
    await waitFor(() => expect(search).toHaveFocus());
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});
