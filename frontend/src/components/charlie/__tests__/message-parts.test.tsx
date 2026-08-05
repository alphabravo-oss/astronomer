import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import {
  boundedToolArguments,
  CharlieLifecycleNotice,
  CharlieMessageParts,
} from "../message-parts";
import { useAuthStore } from "@/lib/store";
import type { User } from "@/types";

describe("Charlie message safety and states", () => {
  afterEach(() =>
    useAuthStore.setState({ user: null, isAuthenticated: false }),
  );
  it("redacts sensitive tool arguments and bounds values", () => {
    expect(
      boundedToolArguments({
        token: "abc",
        name: "x".repeat(300),
        nested: { password: "nope" },
      }),
    ).toEqual({
      token: "[redacted]",
      name: "x".repeat(200),
      nested: { password: "[redacted]" },
    });
  });
  it.each([
    "reconnecting",
    "retrying",
    "partial",
    "central_unavailable",
    "agent_failover",
    "policy_denied",
    "expired",
  ])("renders %s lifecycle state", (state) => {
    const { unmount } = render(<CharlieLifecycleNotice state={state} />);
    expect(
      screen.getByText(
        /Reconnecting|Retrying|Partial response|central unavailable|Agent failover|Denied by product policy|Session expired/,
      ),
    ).toBeInTheDocument();
    unmount();
  });
  it("renders retrieval, sanitized citations, and complete tool metadata", () => {
    render(
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
              arguments: { secret: "x" },
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
  it("shows exact decision controls only for eligible approvers", () => {
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
          },
        }}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Approve exact Charlie action" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Deny exact Charlie action" }),
    ).toBeInTheDocument();
  });
});
