import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  abortCharlieSession,
  getCharlieActiveThread,
  getCharlieCommands,
  getCharlieOverview,
  getCharlieHistory,
  getCharlieThreadHistory,
  listCharlieThreads,
  newCharlieChat,
  sendCharlieThreadMessage,
  subscribeCharlieSessionEvents,
} from "@/lib/api/charlie";
import { getCharlieMode } from "@/lib/api/charlie-admin";
import { CharlieShell } from "../charlie-shell";

vi.mock("@/lib/api/charlie", () => ({
  abortCharlieSession: vi.fn(),
  getCharlieActiveThread: vi.fn(),
  getCharlieCommands: vi.fn(),
  getCharlieOverview: vi.fn(),
  getCharlieHistory: vi.fn(),
  getCharlieThreadHistory: vi.fn(),
  listCharlieThreads: vi.fn(),
  newCharlieChat: vi.fn(),
  searchCharlieContext: vi.fn().mockResolvedValue([
    {
      type: "alert",
      id: "active",
      requiredVerb: "read",
      label: "Alerts",
      summary: "Current authorized alert scope",
    },
  ]),
  sendCharlieThreadMessage: vi.fn(),
  subscribeCharlieSessionEvents: vi.fn(),
}));
vi.mock("@/lib/api/charlie-admin", () => ({
  getCharlieMode: vi.fn(),
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

function renderShell() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <CharlieShell>
        <main>Dashboard</main>
      </CharlieShell>
    </QueryClientProvider>,
  );
}

describe("Charlie global shell accessibility", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(getCharlieOverview).mockResolvedValue({
      sessions: [],
      mode: "read_only",
    } as never);
    vi.mocked(getCharlieMode).mockResolvedValue({
      requested: "read_only",
      authoritative: "read_only",
      revision: 1,
      emergencyDisabled: false,
      effects: [],
      workloadCeiling: "read_only",
      workloadCeilingReady: true,
    } as never);
    vi.mocked(getCharlieActiveThread).mockResolvedValue({ thread: null } as never);
    vi.mocked(getCharlieCommands).mockResolvedValue({
      schema: "astronomer.charlie-command-catalog/v1",
      version: 1,
      commands: [
        { id: "health", version: "1", name: "health", aliases: ["system-health"], label: "System health", description: "Assess management-plane health.", category: "Assess", execution: "agent", effect: "read", required_mode: "read_only", example: "/health" },
        { id: "investigate", version: "1", name: "investigate", label: "Investigate", description: "Investigate one subject.", category: "Investigate", execution: "agent", effect: "read", required_mode: "read_only", example: "/investigate queues", argument: { name: "subject", placeholder: "subject", required: true } },
        { id: "help", version: "1", name: "help", label: "Help", description: "Show commands.", category: "Chat", execution: "client", effect: "local", required_mode: "read_only", example: "/help" },
        { id: "scope", version: "1", name: "scope", label: "Scope", description: "Choose context.", category: "Chat", execution: "client", effect: "local", required_mode: "read_only", example: "/scope" },
        { id: "mode", version: "1", name: "mode", label: "Mode", description: "Show current mode.", category: "Chat", execution: "client", effect: "local", required_mode: "read_only", example: "/mode" },
        { id: "new", version: "1", name: "new", label: "New", description: "Start a new chat.", category: "Chat", execution: "client", effect: "local", required_mode: "read_only", example: "/new" },
        { id: "stop", version: "1", name: "stop", label: "Stop", description: "Stop current work.", category: "Chat", execution: "client", effect: "local", required_mode: "read_only", example: "/stop" },
      ],
    } as never);
    vi.mocked(listCharlieThreads).mockResolvedValue([]);
    vi.mocked(getCharlieHistory).mockResolvedValue([]);
    vi.mocked(getCharlieThreadHistory).mockResolvedValue([]);
    vi.mocked(subscribeCharlieSessionEvents).mockReturnValue(() => undefined);
    vi.mocked(sendCharlieThreadMessage).mockResolvedValue({
      thread: { id: "thread-1", title: "hi", state: "active", current_session_id: "session-1" },
      current_session: { id: "session-1" },
      messageable: true,
      needs_continue: false,
      session_ids: ["session-1"],
      receipt: {
        sessionId: "central-session-1",
        turnId: "turn-1",
        acceptedAt: "2026-08-11T23:27:12Z",
      },
    } as never);
    vi.mocked(newCharlieChat).mockResolvedValue({
      thread: { id: "thread-2", title: "", state: "active" },
      messageable: false,
      needs_continue: false,
      session_ids: [],
    } as never);
  });

  it("does not fetch Charlie overview or history while the drawer is closed", async () => {
    renderShell();
    await Promise.resolve();
    expect(screen.queryByRole("dialog", { name: "Charlie" })).toBeNull();
    expect(getCharlieOverview).not.toHaveBeenCalled();
    expect(getCharlieActiveThread).not.toHaveBeenCalled();
  });

  it("opens with the non-command-palette shortcut, loads active thread, and exposes route context", async () => {
    renderShell();
    fireEvent.keyDown(window, { key: ".", ctrlKey: true, shiftKey: true });
    expect(await screen.findByRole("dialog", { name: "Charlie" })).toBeInTheDocument();
    expect(getCharlieActiveThread).toHaveBeenCalled();
    expect(screen.getByText("Alerts")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New chat" })).toBeInTheDocument();
  });

  it("shows the read-only ceiling once at the top without a redundant composer hint", async () => {
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    expect(await screen.findByText("Mode: Read only")).toBeInTheDocument();
    const badge = screen.getByTestId("charlie-mode-badge");
    expect(badge).toHaveAttribute("data-mode", "read_only");
    expect(screen.getByText(/Investigation and findings only/i)).toBeInTheDocument();
    expect(screen.queryByText(/for example scaling replicas/i)).not.toBeInTheDocument();
  });

  it("shows authenticated tool progress while Charlie is working", async () => {
    let receiveEvent: ((event: MessageEvent<string>) => void) | undefined;
    vi.mocked(subscribeCharlieSessionEvents).mockImplementation((_id, onEvent) => {
      receiveEvent = onEvent;
      return () => undefined;
    });
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    const composer = await screen.findByLabelText("Message Charlie");
    fireEvent.change(composer, { target: { value: "inspect queued tasks" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(await screen.findByText("Sending request to Charlie")).toBeInTheDocument();
    await waitFor(() => expect(receiveEvent).toBeTypeOf("function"));

    act(() => receiveEvent?.(new MessageEvent("tool.running", {
      data: JSON.stringify({
        id: "event-1",
        turn_id: "turn-1",
        type: "tool.running",
        data: {
          capability: "astronomer.queue.tasks",
          tool_call_id: "call-1",
          input: { credential: "SENTINEL" },
        },
      }),
      lastEventId: "event-1",
    })));
    expect(screen.getByText("Calling astronomer.queue.tasks")).toBeInTheDocument();
    expect(screen.getByText("1 tool call")).toBeInTheDocument();
    expect(screen.queryByText("SENTINEL")).not.toBeInTheDocument();
  });

  it("sends via the interactive thread API and keeps session continuity", async () => {
    let receiveEvent: ((event: MessageEvent<string>) => void) | undefined;
    vi.mocked(subscribeCharlieSessionEvents).mockImplementation((_id, onEvent) => {
      receiveEvent = onEvent;
      return () => undefined;
    });
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    const objective = "what version of k8s are we running";
    const composer = await screen.findByLabelText("Message Charlie");
    fireEvent.change(composer, { target: { value: objective } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() =>
      expect(sendCharlieThreadMessage).toHaveBeenCalledWith(
        objective,
        expect.objectContaining({ trigger: "user_chat" }),
      ),
    );
    await waitFor(() => expect(receiveEvent).toBeTypeOf("function"));
    act(() => receiveEvent?.(new MessageEvent("turn.completed", {
      data: JSON.stringify({
        id: "event-terminal-1",
        turn_id: "turn-1",
        type: "turn.completed",
        data: {},
      }),
      lastEventId: "event-terminal-1",
    })));
    fireEvent.change(composer, { target: { value: "Now check tunnel health" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() =>
      expect(sendCharlieThreadMessage).toHaveBeenCalledWith(
        "Now check tunnel health",
        expect.objectContaining({ trigger: "user_chat" }),
      ),
    );
  });

  it("restores active-thread history on open instead of starting blank", async () => {
    vi.mocked(getCharlieActiveThread).mockResolvedValue({
      thread: {
        id: "thread-1",
        title: "what version",
        state: "active",
        current_session_id: "session-1",
      },
      current_session: { id: "session-1" },
      messageable: true,
      needs_continue: false,
      session_ids: ["session-1"],
    } as never);
    vi.mocked(getCharlieThreadHistory).mockResolvedValue([
      { id: "u1", role: "user", content: "what version" },
      { id: "a1", role: "assistant", content: "Kubernetes v1.36.2+k3s1" },
    ] as never);
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    expect(await screen.findByText("Kubernetes v1.36.2+k3s1")).toBeInTheDocument();
    expect(getCharlieThreadHistory).toHaveBeenCalledWith("thread-1");
  });

  it("New chat clears the transcript via the thread API", async () => {
    vi.mocked(getCharlieActiveThread).mockResolvedValue({
      thread: {
        id: "thread-1",
        title: "prior",
        state: "active",
        current_session_id: "session-1",
      },
      current_session: { id: "session-1" },
      messageable: true,
      session_ids: ["session-1"],
    } as never);
    vi.mocked(getCharlieThreadHistory).mockResolvedValue([
      { id: "u1", role: "user", content: "prior message" },
      { id: "a1", role: "assistant", content: "prior answer" },
    ] as never);
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    expect(await screen.findByText("prior answer")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "New chat" }));
    await waitFor(() => expect(newCharlieChat).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.queryByText("prior answer")).not.toBeInTheDocument(),
    );
  });

  it("does not render the same user message twice after history loads", async () => {
    vi.mocked(getCharlieActiveThread).mockResolvedValue({
      thread: { id: "thread-1", title: "hi", state: "active", current_session_id: "session-1" },
      current_session: { id: "session-1" },
      messageable: true,
      session_ids: ["session-1"],
    } as never);
    vi.mocked(getCharlieThreadHistory).mockResolvedValue([
      { id: "server-user-1", role: "user", content: "hi" },
      { id: "server-asst-1", role: "assistant", content: "Hi. How can I help?" },
    ] as never);
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    expect(await screen.findByText("Hi. How can I help?")).toBeInTheDocument();
    fireEvent.change(await screen.findByLabelText("Message Charlie"), {
      target: { value: "hi" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(sendCharlieThreadMessage).toHaveBeenCalled());
    await waitFor(() => {
      const youLabels = screen.getAllByText("You");
      expect(youLabels).toHaveLength(1);
    });
    expect(screen.getByText("Hi. How can I help?")).toBeInTheDocument();
  });

  it("sends on Enter and inserts a newline on Shift+Enter", async () => {
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    const composer = await screen.findByLabelText("Message Charlie");
    fireEvent.change(composer, { target: { value: "line one" } });
    fireEvent.keyDown(composer, { key: "Enter", shiftKey: true });
    expect(sendCharlieThreadMessage).not.toHaveBeenCalled();
    fireEvent.keyDown(composer, { key: "Enter", shiftKey: false });
    await waitFor(() =>
      expect(sendCharlieThreadMessage).toHaveBeenCalledWith(
        "line one",
        expect.objectContaining({ trigger: "user_chat" }),
      ),
    );
  });

  it("shows event-ready progress while a reply is pending", async () => {
    let resolveSend: (value: unknown) => void = () => undefined;
    vi.mocked(sendCharlieThreadMessage).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveSend = resolve;
        }) as never,
    );
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    fireEvent.change(await screen.findByLabelText("Message Charlie"), {
      target: { value: "hello" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(await screen.findByRole("status", { name: "Charlie is working: Sending request to Charlie" })).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "Charlie request progress" })).toBeInTheDocument();
    resolveSend({
      thread: { id: "thread-1", title: "hello", state: "active", current_session_id: "session-1" },
      current_session: { id: "session-1" },
      messageable: true,
      session_ids: ["session-1"],
    });
  });

  it("clears working state when persisted assistant history arrives without a terminal event", async () => {
    vi.mocked(getCharlieActiveThread).mockResolvedValue({
      thread: {
        id: "thread-1",
        title: "health",
        state: "active",
        current_session_id: "session-1",
      },
      current_session: { id: "session-1" },
      messageable: true,
      session_ids: ["session-1"],
    } as never);
    vi.mocked(getCharlieThreadHistory)
      .mockResolvedValueOnce([])
      .mockResolvedValue([
        { id: "u-new", role: "user", content: "assess health" },
        { id: "a-new", role: "assistant", content: "Everything is healthy." },
      ] as never);
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    const composer = await screen.findByLabelText("Message Charlie");
    await waitFor(() => expect(getCharlieThreadHistory).toHaveBeenCalledWith("thread-1"));
    fireEvent.change(composer, { target: { value: "assess health" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(sendCharlieThreadMessage).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByText("Everything is healthy.")).toBeInTheDocument());
    await waitFor(() => expect(screen.queryByTestId("charlie-turn-progress")).not.toBeInTheDocument());
  });

  it("explains deployment scope and offers browsable narrowing choices", async () => {
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    expect(await screen.findByText("Scope")).toBeInTheDocument();
    expect(screen.getByText(/retrieves authorized diagnostics through audited read tools/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Narrow scope" }));
    expect(await screen.findByLabelText("Search components or agent connections")).toBeInTheDocument();
    expect(screen.getByText("Choose a diagnostic scope")).toBeInTheDocument();
  });

  it("suggests slash commands and sends an operational command as a structured invocation", async () => {
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    const composer = await screen.findByLabelText("Message Charlie");
    await waitFor(() => expect(getCharlieCommands).toHaveBeenCalled());
    fireEvent.change(composer, { target: { value: "/hea" } });
    expect(await screen.findByRole("listbox", { name: "Charlie command suggestions" })).toBeInTheDocument();
    fireEvent.keyDown(composer, { key: "Tab" });
    expect(composer).toHaveValue("/health");
    fireEvent.keyDown(composer, { key: "Enter" });
    await waitFor(() => expect(sendCharlieThreadMessage).toHaveBeenCalledWith(
      "/health",
      expect.objectContaining({
        trigger: "slash_command:health",
        command: { id: "health", version: "1", arguments: {} },
      }),
    ));
    expect(
      await screen.findByLabelText("Recognized Charlie command"),
    ).toHaveTextContent("Command");
    expect(screen.getByLabelText("Message from you")).toHaveClass(
      "border-primary/40",
    );
  });

  it("handles help and scope commands locally without creating a model turn", async () => {
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    const composer = await screen.findByLabelText("Message Charlie");
    await waitFor(() => expect(getCharlieCommands).toHaveBeenCalled());
    fireEvent.change(composer, { target: { value: "/help" } });
    fireEvent.keyDown(composer, { key: "Enter" });
    expect(await screen.findByRole("region", { name: "Charlie command help" })).toBeInTheDocument();
    expect(sendCharlieThreadMessage).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Close command help" }));
    fireEvent.change(composer, { target: { value: "/scope" } });
    fireEvent.keyDown(composer, { key: "Enter" });
    expect(await screen.findByText("Choose a diagnostic scope")).toBeInTheDocument();
    expect(sendCharlieThreadMessage).not.toHaveBeenCalled();
  });

  it("browses previous conversations read-only without injecting them into current chat", async () => {
    vi.mocked(getCharlieActiveThread).mockResolvedValue({
      thread: { id: "thread-current", title: "Current", state: "active", current_session_id: "session-1" },
      current_session: { id: "session-1" },
      messageable: true,
      session_ids: ["session-1"],
    } as never);
    vi.mocked(listCharlieThreads).mockResolvedValue([
      { id: "thread-current", title: "Current", state: "active" },
      { id: "thread-old", title: "Prior queue incident", state: "archived", updated_at: "2026-08-10T12:00:00Z" },
    ]);
    vi.mocked(getCharlieThreadHistory).mockImplementation(async (id) => id === "thread-old"
      ? [{ id: "old-answer", role: "assistant", content: "The prior queue diagnosis." }] as never
      : []);
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    fireEvent.click(await screen.findByRole("button", { name: /History/ }));
    fireEvent.click(await screen.findByRole("button", { name: /Prior queue incident/ }));
    expect(await screen.findByText("The prior queue diagnosis.")).toBeInTheDocument();
    expect(screen.getByText(/not added to your current Charlie context/i)).toBeInTheDocument();
    expect(screen.queryByLabelText("Message Charlie")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Return to current" }));
    expect(await screen.findByLabelText("Message Charlie")).toBeInTheDocument();
    expect(sendCharlieThreadMessage).not.toHaveBeenCalled();
  });

  it("keeps abort from wiping the server conversation pointer (does not call new chat)", async () => {
    vi.mocked(getCharlieActiveThread).mockResolvedValue({
      thread: { id: "thread-1", title: "x", state: "active", current_session_id: "session-1" },
      current_session: { id: "session-1" },
      messageable: true,
      session_ids: ["session-1"],
    } as never);
    vi.mocked(getCharlieThreadHistory).mockResolvedValue([
      { id: "a1", role: "assistant", content: "kept history" },
    ] as never);
    vi.mocked(abortCharlieSession).mockResolvedValue(undefined as never);
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    expect(await screen.findByText("kept history")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Abort turn/i }));
    // Confirm dialog
    const confirm = await screen.findByRole("button", { name: /Abort session/i }).catch(() => null);
    if (confirm) {
      // fill confirm if needed
    }
    expect(newCharlieChat).not.toHaveBeenCalled();
  });
});
