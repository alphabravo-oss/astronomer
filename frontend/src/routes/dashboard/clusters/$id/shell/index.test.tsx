import { fireEvent, render, screen } from "@testing-library/react";

import { useWindowManagerStore } from "@/lib/window-manager-store";

vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  useParams: () => ({ id: "cluster-1" }),
}));

const mockUseCluster = vi.hoisted(() => vi.fn());
const mockUsePermissionDecision = vi.hoisted(() => vi.fn());
vi.mock("@/lib/hooks/clusters", () => ({
  useCluster: () => mockUseCluster(),
}));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: () => mockUsePermissionDecision(),
}));

import { ClusterShellDeepLink } from "./-page";

describe("ClusterShellDeepLink", () => {
  beforeEach(() => {
    mockUsePermissionDecision.mockReturnValue({
      allowed: true,
      reason: "Allowed",
    });
    useWindowManagerStore.setState({
      tabs: [],
      activeTabId: null,
      open: false,
      minimized: false,
      height: 420,
    });
  });

  it("opens the global shell drawer instead of mounting a second terminal", () => {
    mockUseCluster.mockReturnValue({
      data: {
        id: "cluster-1",
        name: "prod-east",
        displayName: "Production East",
        isLocal: false,
      },
      isLoading: false,
    });

    render(<ClusterShellDeepLink />);

    expect(screen.getByText("Cluster shell opened")).toBeInTheDocument();
    expect(screen.queryByText("No active session")).not.toBeInTheDocument();
    expect(useWindowManagerStore.getState().tabs).toEqual([
      expect.objectContaining({
        kind: "shell",
        clusterId: "cluster-1",
        clusterName: "Production East",
      }),
    ]);

    useWindowManagerStore.setState({ open: false, minimized: true });
    fireEvent.click(screen.getByRole("button", { name: "Focus shell" }));
    expect(useWindowManagerStore.getState().open).toBe(true);
    expect(useWindowManagerStore.getState().minimized).toBe(false);
  });

  it("does not open a remote shell for the management plane cluster", () => {
    mockUseCluster.mockReturnValue({
      data: {
        id: "cluster-1",
        name: "local",
        displayName: "Management plane",
        isLocal: true,
      },
      isLoading: false,
    });

    render(<ClusterShellDeepLink />);

    expect(
      screen.getByText(/isn.t available on the management plane/i),
    ).toBeInTheDocument();
    expect(useWindowManagerStore.getState().tabs).toHaveLength(0);
  });

  it("shows a permission state without opening the drawer", () => {
    mockUseCluster.mockReturnValue({
      data: {
        id: "cluster-1",
        name: "prod-east",
        displayName: "Production East",
        isLocal: false,
      },
      isLoading: false,
    });
    mockUsePermissionDecision.mockReturnValue({
      allowed: false,
      reason: "Missing shell:exec",
      disabledReason: "Request shell access for this cluster",
    });

    render(<ClusterShellDeepLink />);

    expect(screen.getByText("Shell access required")).toBeInTheDocument();
    expect(
      screen.getByText("Request shell access for this cluster"),
    ).toBeInTheDocument();
    expect(useWindowManagerStore.getState().tabs).toHaveLength(0);
  });
});
