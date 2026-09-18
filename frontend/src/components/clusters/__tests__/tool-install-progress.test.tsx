import { render, screen, within } from "@testing-library/react";
import { ToolInstallProgress } from "@/components/clusters/tool-install-progress";

const operation = vi.hoisted(() => ({
  status: "failed",
  operationType: "install",
  events: [
    {
      id: "1",
      stage: "release.started",
      level: "info",
      message: "base started",
      createdAt: "2026-09-10T12:00:00Z",
      detail: {
        releaseName: "istio-base",
        namespace: "istio-system",
        stepIndex: 0,
      },
    },
    {
      id: "2",
      stage: "release.completed",
      level: "info",
      message: "base ready",
      createdAt: "2026-09-10T12:00:01Z",
      detail: {
        releaseName: "istio-base",
        namespace: "istio-system",
        stepIndex: 0,
      },
    },
    {
      id: "3",
      stage: "release.failed",
      level: "error",
      message: "istiod failed",
      createdAt: "2026-09-10T12:00:02Z",
      detail: {
        releaseName: "istiod",
        namespace: "istio-system",
        stepIndex: 1,
      },
    },
  ],
}));
vi.mock("@/lib/hooks/tools", () => ({
  useToolOperation: () => ({ data: operation }),
}));

describe("ToolInstallProgress", () => {
  it("shows the last durable state of each release", () => {
    render(
      <ToolInstallProgress
        operationId="op"
        toolName="Istio"
        onClose={vi.fn()}
      />,
    );
    const releases = within(
      screen.getByRole("list", { name: "Release progress" }),
    ).getAllByRole("listitem");
    expect(releases).toHaveLength(2);
    expect(releases[0]).toHaveTextContent("1. istio-base · completed");
    expect(releases[1]).toHaveTextContent("2. istiod · failed");
  });
  it("labels reverse operations without claiming they deployed a tool", () => {
    operation.operationType = "rollback";
    operation.status = "completed";
    render(
      <ToolInstallProgress
        operationId="op"
        toolName="Istio"
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByText("Rolling back Istio")).toBeInTheDocument();
    expect(screen.getByText("Completed")).toBeInTheDocument();
    expect(screen.queryByText("Deployed")).not.toBeInTheDocument();
  });
});
