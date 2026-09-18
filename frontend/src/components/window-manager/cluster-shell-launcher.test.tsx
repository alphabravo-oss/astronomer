import { fireEvent, render, screen } from "@testing-library/react";

import { useWindowManagerStore } from "@/lib/window-manager-store";
import { ClusterShellLauncher } from "./cluster-shell-launcher";

function resetWindowManager() {
  useWindowManagerStore.setState({
    tabs: [],
    activeTabId: null,
    open: false,
    minimized: false,
    height: 420,
  });
}

describe("ClusterShellLauncher", () => {
  beforeEach(resetWindowManager);

  it("opens the active cluster shell from the topbar action", () => {
    render(
      <ClusterShellLauncher clusterId="cluster-1" clusterName="Production" />,
    );

    fireEvent.click(
      screen.getByRole("button", {
        name: /open cluster shell for production/i,
      }),
    );

    expect(useWindowManagerStore.getState().tabs).toEqual([
      expect.objectContaining({
        id: "shell:cluster-1",
        kind: "shell",
        clusterId: "cluster-1",
        clusterName: "Production",
      }),
    ]);
  });

  it("uses Ctrl+backtick to focus the same cluster shell tab", () => {
    render(
      <ClusterShellLauncher clusterId="cluster-1" clusterName="Production" />,
    );

    fireEvent.keyDown(window, { key: "`", ctrlKey: true });
    useWindowManagerStore.setState({ open: false, minimized: true });
    fireEvent.keyDown(window, { key: "`", ctrlKey: true });

    const state = useWindowManagerStore.getState();
    expect(state.tabs).toHaveLength(1);
    expect(state.activeTabId).toBe("shell:cluster-1");
    expect(state.open).toBe(true);
    expect(state.minimized).toBe(false);
  });

  it("does not register an active shortcut when shell access is disabled", () => {
    render(
      <ClusterShellLauncher
        clusterId="local"
        disabled
        disabledReason="Unavailable on the management plane"
      />,
    );

    expect(screen.getByRole("button")).toBeDisabled();
    fireEvent.keyDown(window, { key: "`", ctrlKey: true });
    expect(useWindowManagerStore.getState().tabs).toHaveLength(0);
  });

  it("renders no action without an active cluster", () => {
    const { container } = render(<ClusterShellLauncher />);
    expect(container).toBeEmptyDOMElement();
  });
});
