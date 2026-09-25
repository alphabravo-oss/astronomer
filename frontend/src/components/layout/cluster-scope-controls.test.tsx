import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useClusterNamespaceScope } from "@/lib/cluster-scope";
import { ClusterScopeControls } from "./cluster-scope-controls";

vi.mock("@/lib/cluster-scope", () => ({
  useClusterNamespaceScope: vi.fn(() => ({
    error: new Error("scope unavailable"),
    retry: vi.fn(),
  })),
}));

describe("ClusterScopeControls", () => {
  it("initializes cluster scope without showing controls on cluster-wide pages", () => {
    render(
      <ClusterScopeControls
        clusterId="cluster-1"
        applicability={{ project: false, namespaces: false }}
      />,
    );

    expect(useClusterNamespaceScope).toHaveBeenCalledWith("cluster-1");
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
