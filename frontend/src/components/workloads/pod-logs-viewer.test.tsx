import { fireEvent, render, screen } from "@testing-library/react";
import { vi } from "vitest";

import { PodLogsViewer } from "./pod-logs-viewer";
import type { Pod } from "@/types";

vi.mock("@/lib/hooks/workloads", () => ({
  usePodLogs: () => ({
    data: [
      {
        timestamp: "2026-09-17T12:00:00Z",
        message: "server ready",
        level: "info",
      },
    ],
    isLoading: false,
  }),
}));

describe("PodLogsViewer", () => {
  it("names log controls and exposes toggle state without announcing every line", () => {
    render(
      <PodLogsViewer
        clusterId="cluster-a"
        namespace="default"
        pods={
          [
            {
              name: "api-0",
              namespace: "default",
              containers: [{ name: "api" }],
            },
          ] as Pod[]
        }
        selectedPod="api-0"
        onPodChange={vi.fn()}
      />,
    );

    expect(screen.getByRole("combobox", { name: "Pod" })).toBeInTheDocument();
    expect(
      screen.getByRole("combobox", { name: "Log tail lines" }),
    ).toBeInTheDocument();
    const follow = screen.getByRole("button", {
      name: "Follow new log lines",
    });
    expect(follow).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(follow);
    expect(follow).toHaveAttribute("aria-pressed", "false");

    const log = screen.getByRole("log", { name: "Logs for default/api-0" });
    expect(log).toHaveAttribute("aria-live", "off");
    expect(log).toHaveTextContent("server ready");
  });
});
