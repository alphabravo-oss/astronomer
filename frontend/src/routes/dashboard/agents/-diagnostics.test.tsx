import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentDiagnosticsDrawer } from "./-diagnostics";

describe("agent diagnostics unavailable state", () => {
  it("hides cached denied diagnostics and disables self-test", () => {
    render(
      <AgentDiagnosticsDrawer
        diagnosticsQuery={{
          isLoading: false,
          isError: true,
          error: { response: { status: 403 } },
          data: { agent: { clusterName: "cached-secret-cluster" } } as never,
          refetch: vi.fn(),
        }}
        operationsQuery={{
          isLoading: false,
          isError: true,
          error: { response: { status: 403 } },
          refetch: vi.fn(),
        }}
        canManage={false}
        upgradePlan={null}
        onClose={vi.fn()}
        onPlan={vi.fn()}
        onSelfTest={vi.fn()}
        onQueue={vi.fn()}
        onDownload={vi.fn()}
      />,
    );
    expect(screen.queryByText("cached-secret-cluster")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Self-test" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Bundle" })).toBeDisabled();
    expect(
      screen.getByText("cluster_agents:read", { exact: false }),
    ).toBeInTheDocument();
  });
  it("shows missing data as unavailable, not an endless loading state", () => {
    const missing = { isLoading: false, isError: false, refetch: vi.fn() };
    render(
      <AgentDiagnosticsDrawer
        diagnosticsQuery={missing}
        operationsQuery={missing}
        canManage
        upgradePlan={null}
        onClose={vi.fn()}
        onPlan={vi.fn()}
        onSelfTest={vi.fn()}
        onQueue={vi.fn()}
        onDownload={vi.fn()}
      />,
    );
    expect(screen.getByText("Diagnostics unavailable")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Self-test" })).toBeDisabled();
  });
});
