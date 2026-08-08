import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  abortCharlieSession,
  getCharlieActiveThread,
  getCharlieOverview,
  getCharlieHistory,
  getCharlieThreadHistory,
  newCharlieChat,
  sendCharlieThreadMessage,
  subscribeCharlieSessionEvents,
} from "@/lib/api/charlie";
import { getCharlieMode } from "@/lib/api/charlie-admin";
import { CharlieShell } from "../charlie-shell";

vi.mock("@/lib/api/charlie", () => ({
  abortCharlieSession: vi.fn(),
  getCharlieActiveThread: vi.fn(),
  getCharlieOverview: vi.fn(),
  getCharlieHistory: vi.fn(),
  getCharlieThreadHistory: vi.fn(),
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
    vi.mocked(getCharlieHistory).mockResolvedValue([]);
    vi.mocked(getCharlieThreadHistory).mockResolvedValue([]);
    vi.mocked(subscribeCharlieSessionEvents).mockReturnValue(() => undefined);
    vi.mocked(sendCharlieThreadMessage).mockResolvedValue({
      thread: { id: "thread-1", title: "hi", state: "active", current_session_id: "session-1" },
      current_session: { id: "session-1" },
      messageable: true,
      needs_continue: false,
      session_ids: ["session-1"],
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

  it("shows a clear read-only mode badge and composer hint", async () => {
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open Charlie assistant" }));
    expect(await screen.findByText("Mode: Read only")).toBeInTheDocument();
    const badge = screen.getByTestId("charlie-mode-badge");
    expect(badge).toHaveAttribute("data-mode", "read_only");
    expect(screen.getByTestId("charlie-mode-composer-hint")).toHaveTextContent(
      /cannot apply changes/i,
    );
  });

  it("sends via the interactive thread API and keeps session continuity", async () => {
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

  it("shows animated thinking indicator while a reply is pending", async () => {
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
    expect(await screen.findByRole("status", { name: "Charlie is thinking" })).toBeInTheDocument();
    resolveSend({
      thread: { id: "thread-1", title: "hello", state: "active", current_session_id: "session-1" },
      current_session: { id: "session-1" },
      messageable: true,
      session_ids: ["session-1"],
    });
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
