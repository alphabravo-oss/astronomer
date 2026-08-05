import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  abortCharlieSession,
  createCharlieSession,
  getCharlieOverview,
  getCharlieHistory,
  sendCharlieMessage,
  subscribeCharlieSessionEvents,
} from "@/lib/api/charlie";
import { CharlieShell } from "../charlie-shell";

vi.mock("@/lib/api/charlie", () => ({
  abortCharlieSession: vi.fn(),
  createCharlieSession: vi.fn(),
  getCharlieOverview: vi.fn(),
  getCharlieHistory: vi.fn(),
  searchCharlieContext: vi.fn().mockResolvedValue([
    { type: "alert", id: "active", requiredVerb: "read", label: "Alerts", summary: "Current authorized alert scope" },
  ]),
  sendCharlieMessage: vi.fn(),
  subscribeCharlieSessionEvents: vi.fn(),
}));

vi.mock("@/lib/navigation", () => ({
  usePathname: () => "/dashboard/alerting",
}));
vi.mock("@/lib/link", () => ({
  Link: ({
    href,
    children,
    ...props
  }: {
    href: string;
    children: React.ReactNode;
  }) => (
    <a href={href} {...props}>
      {children}
    </a>
  ),
}));

describe("Charlie global shell accessibility", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(getCharlieOverview).mockResolvedValue({
      sessions: [],
      mode: "read_only",
    });
    vi.mocked(getCharlieHistory).mockResolvedValue([]);
    vi.mocked(subscribeCharlieSessionEvents).mockReturnValue(() => undefined);
  });

  it("does not fetch Charlie overview or history while the drawer is closed", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <CharlieShell><main>Dashboard</main></CharlieShell>
      </QueryClientProvider>,
    );
    await Promise.resolve();
    expect(screen.queryByRole("dialog", { name: "Charlie" })).toBeNull();
    expect(getCharlieOverview).not.toHaveBeenCalled();
    expect(getCharlieHistory).not.toHaveBeenCalled();
  });

  it("opens with the non-command-palette shortcut, exposes route context, and closes with Escape", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <CharlieShell>
          <main>Dashboard</main>
        </CharlieShell>
      </QueryClientProvider>,
    );
    const launcher = screen.getByRole("button", {
      name: "Open Charlie assistant",
    });
    expect(launcher).toHaveAttribute("aria-expanded", "false");
    launcher.focus();
    fireEvent.keyDown(window, { key: ".", ctrlKey: true, shiftKey: true });
    expect(
      await screen.findByRole("dialog", { name: "Charlie" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Alerts")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Remove Alerts" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("listitem", { name: /Alerts: Current authorized/ })).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toHaveClass("max-sm:max-w-none");
    expect(launcher).toHaveAttribute("aria-expanded", "true");

    const composer = screen.getByLabelText("Message Charlie");
    composer.focus();
    fireEvent.keyDown(composer, { key: ".", ctrlKey: true, shiftKey: true });
    expect(screen.getByRole("dialog", { name: "Charlie" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Remove Alerts" }));
    expect(screen.queryByRole("button", { name: "Remove Alerts" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Add context" }));
    fireEvent.change(screen.getByLabelText("Search authorized context"), {
      target: { value: "alerts" },
    });
    fireEvent.click(await screen.findByRole("button", { name: /Alerts/ }));
    expect(screen.getByRole("button", { name: "Remove Alerts" })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(launcher).toHaveFocus();
    expect(abortCharlieSession).not.toHaveBeenCalled();
  });

  it("uses the new session intent as the first turn without sending it twice", async () => {
    vi.mocked(createCharlieSession).mockResolvedValue({ id: "session-1" } as never);
    vi.mocked(getCharlieHistory).mockResolvedValue([]);
    vi.mocked(sendCharlieMessage).mockResolvedValue({} as never);
    vi.mocked(subscribeCharlieSessionEvents).mockReturnValue(() => undefined);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <CharlieShell><main>Dashboard</main></CharlieShell>
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    const composer = await screen.findByLabelText("Message Charlie");
    const objective = "Inspect the control plane and correlate agent fleet heartbeats, connection history, authentication results, server ownership, tunnel health, and recent rollout changes";
    fireEvent.change(composer, { target: { value: objective } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(createCharlieSession).toHaveBeenCalledTimes(1));
    expect(createCharlieSession).toHaveBeenCalledWith(expect.objectContaining({ intent: objective }));
    expect(sendCharlieMessage).not.toHaveBeenCalled();

    fireEvent.change(composer, { target: { value: "Now check tunnel health" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(sendCharlieMessage).toHaveBeenCalledWith("session-1", "Now check tunnel health"));
  });

  it("omits context removed before the first network request", async () => {
    vi.mocked(createCharlieSession).mockResolvedValue({ id: "session-1" } as never);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <CharlieShell><main>Dashboard</main></CharlieShell>
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove Alerts" }));
    fireEvent.change(screen.getByLabelText("Message Charlie"), {
      target: { value: "Explain the current alert state" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(createCharlieSession).toHaveBeenCalledTimes(1));
    expect(createCharlieSession).toHaveBeenCalledWith(
      expect.objectContaining({ resources: [] }),
    );
  });

  it("restores the latest authorized private user session and displays its effective mode", async () => {
    vi.mocked(getCharlieOverview).mockResolvedValue({
      mode: "approval",
      sessions: [
        {
          id: "incident-event",
          visibility: "incident",
          source: "event",
          state: "active",
        },
        {
          id: "private-user",
          visibility: "private",
          source: "user",
          state: "active",
        },
      ] as never,
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <CharlieShell><main>Dashboard</main></CharlieShell>
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    expect(await screen.findByLabelText("Current Charlie mode: approval")).toBeInTheDocument();
    await waitFor(() => expect(getCharlieHistory).toHaveBeenCalledWith("private-user"));
    expect(subscribeCharlieSessionEvents).toHaveBeenCalledWith(
      "private-user",
      expect.any(Function),
      expect.any(Function),
    );
  });

  it("aborts only after explicit typed confirmation", async () => {
    vi.mocked(getCharlieOverview).mockResolvedValue({
      mode: "read_only",
      sessions: [{ id: "private-user", visibility: "private", source: "user", state: "active" }] as never,
    });
    vi.mocked(abortCharlieSession).mockResolvedValue(undefined);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <CharlieShell><main>Dashboard</main></CharlieShell>
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    fireEvent.click(await screen.findByRole("button", { name: "Abort turn" }));
    const confirm = screen.getByRole("button", { name: "Abort session" });
    expect(confirm).toBeDisabled();
    expect(abortCharlieSession).not.toHaveBeenCalled();
    fireEvent.change(screen.getByPlaceholderText("ABORT CHARLIE SESSION"), {
      target: { value: "ABORT CHARLIE SESSION" },
    });
    fireEvent.click(confirm);
    await waitFor(() => expect(abortCharlieSession).toHaveBeenCalledWith("private-user"));
  });
});
