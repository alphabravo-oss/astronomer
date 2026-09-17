import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { RemoteClusterPicker } from "./remote-cluster-picker";

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 48,
    scrollToIndex: vi.fn(),
    getVirtualItems: () =>
      Array.from({ length: count }, (_, index) => ({
        index,
        key: index,
        size: 48,
        start: index * 48,
      })),
  }),
}));

vi.mock("@/lib/hooks/clusters", () => ({
  useCluster: (id: string) => ({
    data: id
      ? { id, name: "selected", displayName: "Selected cluster" }
      : undefined,
  }),
}));

vi.mock("@/lib/hooks/cluster-search", () => ({
  useClusterSearch: () => ({
    data: {
      pages: [
        {
          data: [
            {
              id: "cluster-a",
              name: "alpha",
              displayName: "Alpha",
              environment: "production",
              region: "east",
            },
            {
              id: "cluster-b",
              name: "beta",
              displayName: "Beta",
              environment: "staging",
              region: "west",
            },
          ],
        },
      ],
    },
    hasNextPage: false,
    isLoading: false,
    isError: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
    refetch: vi.fn(),
  }),
}));

describe("RemoteClusterPicker", () => {
  it("resolves a selected value independently from the result page", () => {
    render(
      <RemoteClusterPicker
        value="selected-id"
        onChange={vi.fn()}
        ariaLabel="Target cluster"
      />,
    );

    expect(
      screen.getByRole("combobox", { name: "Target cluster" }),
    ).toHaveTextContent("Selected cluster");
  });

  it("permission-filters remote results and selects by keyboard", () => {
    const onChange = vi.fn();
    render(
      <RemoteClusterPicker
        value=""
        onChange={onChange}
        allowedClusterIds={["cluster-b"]}
        ariaLabel="Target cluster"
      />,
    );

    fireEvent.click(screen.getByRole("combobox", { name: "Target cluster" }));
    expect(screen.queryByText("Alpha")).not.toBeInTheDocument();
    expect(screen.getByText("Beta")).toBeInTheDocument();

    fireEvent.keyDown(
      screen.getByRole("searchbox", { name: "Search clusters" }),
      {
        key: "Enter",
      },
    );
    expect(onChange).toHaveBeenCalledWith("cluster-b");
  });
});
