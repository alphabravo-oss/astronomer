import { fireEvent, render, screen } from "@testing-library/react";

import { useWindowManagerStore } from "@/lib/window-manager-store";

vi.mock("@/components/clusters/cluster-shell", () => ({
  ClusterShell: ({
    clusterId,
    visible,
  }: {
    clusterId: string;
    visible: boolean;
  }) => (
    <div data-testid="cluster-shell" data-visible={String(visible)}>
      {clusterId}
    </div>
  ),
}));

vi.mock("./logs-tab", () => ({
  LogsTab: () => <div data-testid="logs-tab" />,
}));

vi.mock("./exec-tab", () => ({
  ExecTab: () => <div data-testid="exec-tab" />,
}));

import { WindowManager } from "./window-manager";

describe("WindowManager shell tabs", () => {
  beforeEach(() => {
    useWindowManagerStore.setState({
      tabs: [],
      activeTabId: null,
      open: false,
      minimized: false,
      height: 420,
    });
  });

  it("renders a cluster shell as its own tab without pod-shaped labels", async () => {
    useWindowManagerStore.getState().addTab({
      kind: "shell",
      clusterId: "cluster-1",
      clusterName: "Production",
    });

    render(<WindowManager />);

    expect(
      screen.getByRole("button", { name: "Shell · Production" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(await screen.findByTestId("cluster-shell")).toHaveTextContent(
      "cluster-1",
    );
    expect(screen.getByTestId("cluster-shell")).toHaveAttribute(
      "data-visible",
      "true",
    );
    expect(screen.queryByText("_/")).not.toBeInTheDocument();
  });

  it("keeps loaded console bodies mounted while switching tabs", async () => {
    useWindowManagerStore.getState().addTab({
      kind: "shell",
      clusterId: "cluster-1",
      clusterName: "Production",
    });
    useWindowManagerStore.getState().addTab({
      kind: "exec",
      clusterId: "cluster-1",
      namespace: "default",
      pod: "worker",
    });
    render(<WindowManager />);
    const shell = await screen.findByTestId("cluster-shell");
    const exec = await screen.findByTestId("exec-tab");
    expect(shell).toHaveAttribute("data-visible", "false");
    fireEvent.click(screen.getByRole("button", { name: "Shell · Production" }));
    expect(screen.getByTestId("cluster-shell")).toBe(shell);
    expect(screen.getByTestId("exec-tab")).toBe(exec);
    expect(shell).toHaveAttribute("data-visible", "true");
  });
});
