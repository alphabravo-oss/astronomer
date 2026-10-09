import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { RegistrationTimeline } from "./registration-timeline";
import {
  getRegistrationStatus,
  retryRegistrationStep,
} from "@/lib/api/cluster-registration";
vi.mock("@/lib/api/cluster-registration", () => ({
  getRegistrationStatus: vi.fn(),
  retryRegistrationStep: vi.fn(),
}));
vi.mock("@/lib/live/hooks", () => ({
  useLiveEvents: () => ({ subscribe: () => () => {} }),
}));
vi.mock("@/lib/live/status-store", () => ({ useLiveStatus: () => "open" }));
describe("registration categorical actions", () => {
  it.each(["unsupported", "failed"] as const)(
    "does not treat %s as ready or silently permit retry",
    async (phase) => {
      const onReady = vi.fn();
      vi.mocked(getRegistrationStatus).mockResolvedValue({
        clusterId: "cluster-1",
        phase,
        steps: [
          {
            id: "step",
            stepName: "cluster_created",
            label: "Create record",
            status: "failed",
            progressPct: 0,
            stepOrder: 0,
            createdAt: "2026-10-09T00:00:00Z",
          },
        ],
      });
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      });
      const view = render(
        <QueryClientProvider client={client}>
          <RegistrationTimeline clusterId="cluster-1" onReady={onReady} />
        </QueryClientProvider>,
      );
      expect(await screen.findByText("Phase: " + phase)).toBeInTheDocument();
      if (phase === "unsupported")
        expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
      else expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
      expect(onReady).not.toHaveBeenCalled();
      expect(retryRegistrationStep).not.toHaveBeenCalled();
      view.unmount();
      client.clear();
    },
  );
});
