import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  CharlieLifecycleNotice,
  CharlieMessageParts,
} from "../message-parts";
import { decideCharlieApproval } from "@/lib/api/charlie";
import { useAuthStore } from "@/lib/store";
import type { User } from "@/types";

vi.mock("@/lib/api/charlie", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/charlie")>()),
  decideCharlieApproval: vi.fn(),
}));

describe("Charlie message safety and states", () => {
  afterEach(() =>
    useAuthStore.setState({ user: null, isAuthenticated: false }),
  );
  it.each([
    "reconnecting",
    "retrying",
    "partial",
    "central_unavailable",
    "agent_failover",
    "policy_denied",
    "expired",
    "waiting_approval",
    "disabled",
    "read_only_finding",
    "approval_required",
    "mcp_denied",
    "auto_blocked",
    "destructive_denied",
    "verification_failed",
    "emergency_stopped",
  ])("renders %s lifecycle state", (state) => {
    const { unmount } = render(<CharlieLifecycleNotice state={state} />);
    expect(
      screen.getByText(
        /Reconnecting|Retrying|Partial response|central unavailable|Agent failover|Denied by product policy|Session expired|Waiting for exact approval|Charlie disabled|Read-only finding|Approval required|MCP request denied|Automatic action blocked|Destructive action denied|Verification failed|Emergency stop active/,
      ),
    ).toBeInTheDocument();
    unmount();
  });
  it("renders retrieval and server-produced tool summaries without argument values", () => {
    const view = render(
      <CharlieMessageParts
        message={{
          id: "m",
          role: "assistant",
          content: "ok",
          retrieval: { state: "complete", documentCount: 2 },
          citations: [
            {
              id: "c",
              title: "Docs",
              source: "Charlie docs",
              href: "javascript:x",
            },
          ],
          tools: [
            {
              id: "t",
              capability: "inspect",
              effect: "read",
              risk: "low",
              argumentSummary: ["namespace", "secret"],
              state: "complete",
              result: "done",
              auditCorrelationId: "audit-1",
            },
          ],
        }}
      />,
    );
    expect(screen.getByText("2 documents")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Docs" })).toBeNull();
    expect(screen.getByText("inspect")).toBeInTheDocument();
    expect(screen.getByText("namespace, secret")).toBeInTheDocument();
    expect(view.container.textContent).not.toContain("tool-secret-canary");
  });
  it("never renders approval controls without server eligibility", () => {
    useAuthStore.setState({
      user: { id: "u", isSuperuser: true } as unknown as User,
      isAuthenticated: true,
    });
    render(
      <CharlieMessageParts
        message={{
          id: "m",
          role: "assistant",
          content: "",
          approval: {
            id: "a",
            title: "Restart",
            state: "pending",
            eligible: false,
            capability: "restart",
            target: "cluster/a",
            risk: "medium",
          },
        }}
      />,
    );
    expect(screen.queryByRole("button", { name: /Approve exact/ })).toBeNull();
    expect(screen.getByText(/did not confirm/)).toBeInTheDocument();
  });
  it("requires charlie:approve even when the server marks the action eligible", () => {
    useAuthStore.setState({ user: { id: "u" } as User, isAuthenticated: true });
    render(
      <CharlieMessageParts
        message={{
          id: "m",
          role: "assistant",
          content: "",
          approval: {
            id: "a",
            title: "Restart",
            state: "pending",
            eligible: true,
            capability: "restart",
            target: "cluster/a",
            risk: "medium",
          },
        }}
      />,
    );
    expect(screen.queryByRole("button", { name: /Approve exact/ })).toBeNull();
    expect(screen.getByText(/Requires charlie:approve/)).toBeInTheDocument();
  });
  it("requires a separate confirmation and carries rationale", async () => {
    useAuthStore.setState({
      user: { id: "u", isSuperuser: true } as unknown as User,
      isAuthenticated: true,
    });
    render(
      <CharlieMessageParts
        message={{
          id: "m",
          role: "assistant",
          content: "",
          approval: {
            id: "a",
            title: "Restart",
            state: "pending",
            eligible: true,
            capability: "restart",
            target: "cluster/a",
            risk: "medium",
            effect: "restart one deployment",
            requiredPermission: "deployments:update",
          },
        }}
      />,
    );
    expect(screen.getByText("restart one deployment")).toBeInTheDocument();
    expect(screen.getByText("deployments:update")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Rationale for Restart"), {
      target: { value: "Health checks are failing" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Review approval" }));
    expect(screen.getByText("Approve exact Charlie action")).toBeInTheDocument();
    expect(decideCharlieApproval).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Approve exact action" }));
    await waitFor(() =>
      expect(decideCharlieApproval).toHaveBeenCalledWith(
        "a",
        "approve",
        "Health checks are failing",
      ),
    );
  });
});
