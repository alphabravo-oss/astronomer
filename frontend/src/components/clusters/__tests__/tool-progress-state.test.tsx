import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ToolInstallProgress } from "../tool-install-progress";
const state = vi.hoisted(() => ({ query: {} as Record<string, unknown> }));
vi.mock("@/lib/hooks/tools", () => ({ useToolOperation: () => state.query }));
describe("tool progress query truth", () => {
  it("does not display cached completed operation after access is denied", () => {
    state.query = {
      isLoading: false,
      isError: true,
      error: { response: { status: 403 } },
      data: { status: "completed" },
      refetch: vi.fn(),
    };
    render(
      <ToolInstallProgress
        operationId="op"
        toolName="Demo"
        onClose={vi.fn()}
      />,
    );
    expect(screen.queryByText("Completed")).not.toBeInTheDocument();
    expect(
      screen.getByText("tools:read", { exact: false }),
    ).toBeInTheDocument();
  });
  it("does not invent Queued when the operation is missing", () => {
    state.query = { isLoading: false, isError: false, refetch: vi.fn() };
    render(
      <ToolInstallProgress
        operationId="op"
        toolName="Demo"
        onClose={vi.fn()}
      />,
    );
    expect(screen.queryByText("Queued")).not.toBeInTheDocument();
    expect(
      screen.getByText("The response did not contain data."),
    ).toBeInTheDocument();
  });
});
