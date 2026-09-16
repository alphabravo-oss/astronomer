import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";

const shellApi = vi.hoisted(() => ({
  openShellSession: vi.fn(),
  closeShellSession: vi.fn(),
  listShellSessionCommands: vi.fn(),
}));
const authApi = vi.hoisted(() => ({ createStreamTicket: vi.fn() }));
const terminal = vi.hoisted(() => ({
  focus: vi.fn(),
  write: vi.fn(),
}));

vi.mock("@/lib/api/kubectl-shell", () => shellApi);
vi.mock("@/lib/api/auth", () => authApi);
vi.mock("@/lib/env", () => ({ wsBase: () => "ws://example.test" }));
vi.mock("@wterm/react", async () => {
  const React = await import("react");
  return {
    Terminal: ({ onReady }: { onReady?: (terminal: unknown) => void }) => {
      React.useEffect(() => {
        onReady?.({ element: document.createElement("div") });
      }, [onReady]);
      return React.createElement("div", { "data-testid": "terminal" });
    },
    useTerminal: () => ({
      ref: { current: null },
      write: terminal.write,
      resize: vi.fn(),
      focus: terminal.focus,
    }),
  };
});

import { ClusterShell } from "./cluster-shell";

const session = {
  id: "session-1",
  clusterId: "cluster-1",
  userId: "user-1",
  status: "active" as const,
  podName: "astronomer-shell-1",
  podNamespace: "kube-system",
  container: "shell",
  startedAt: "2026-09-10T00:00:00Z",
  lastInputAt: "2026-09-10T00:00:00Z",
  expiresAt: "2026-09-10T01:00:00Z",
  idleTimeoutSeconds: 1800,
};

describe("ClusterShell lifecycle", () => {
  const OriginalWebSocket = global.WebSocket;

  beforeEach(() => {
    vi.clearAllMocks();
    shellApi.openShellSession.mockResolvedValue(session);
    shellApi.closeShellSession.mockResolvedValue({ status: "closed" });
    shellApi.listShellSessionCommands.mockResolvedValue([]);
  });

  afterEach(() => {
    global.WebSocket = OriginalWebSocket;
  });

  it("cancels a pending stream ticket and tears down the session on tab close", async () => {
    let resolveTicket!: (ticket: { ticket: string; expiresAt: string }) => void;
    authApi.createStreamTicket.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveTicket = resolve;
        }),
    );
    const websocket = vi.fn();
    global.WebSocket = websocket as unknown as typeof WebSocket;

    const { unmount } = render(<ClusterShell clusterId="cluster-1" />);
    fireEvent.click(screen.getAllByRole("button", { name: "Connect" })[0]);
    await waitFor(() => expect(authApi.createStreamTicket).toHaveBeenCalled());

    unmount();
    await act(async () => {
      resolveTicket({
        ticket: "ticket-1",
        expiresAt: "2026-09-10T00:01:00Z",
      });
      await Promise.resolve();
    });

    expect(websocket).not.toHaveBeenCalled();
    expect(shellApi.closeShellSession).toHaveBeenCalledWith(
      "cluster-1",
      "session-1",
    );
  });

  it("reports drawer chip status and focuses only when visible", async () => {
    const onStatusChange = vi.fn();
    const { rerender } = render(
      <ClusterShell
        clusterId="cluster-1"
        visible={false}
        onStatusChange={onStatusChange}
      />,
    );

    expect(onStatusChange).toHaveBeenCalledWith("idle");
    expect(terminal.focus).not.toHaveBeenCalled();

    rerender(
      <ClusterShell
        clusterId="cluster-1"
        visible
        onStatusChange={onStatusChange}
      />,
    );
    await waitFor(() => expect(terminal.focus).toHaveBeenCalled());
  });
});
