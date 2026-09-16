import { render, screen } from "@testing-library/react";

import {
  DeploymentEventTimeline,
  deploymentEventStatus,
} from "@/components/delivery/deployment-event-timeline";
import type { ClusterDeploymentEvent } from "@/lib/api/delivery-deployments";

const event = (overrides: Partial<ClusterDeploymentEvent> = {}) =>
  ({
    id: "event-1",
    deploymentId: "deployment-1",
    eventType: "phase_changed",
    fromPhase: "pending",
    toPhase: "ready",
    generation: 2,
    specDigest: "sha256:abc",
    reasonCode: "reconciled",
    message: "Applied successfully",
    observedAt: "2026-09-10T12:00:00Z",
    createdAt: "2026-09-10T12:00:00Z",
    rolloutId: null,
    ...overrides,
  }) as ClusterDeploymentEvent;

describe("DeploymentEventTimeline", () => {
  it("maps only persisted failed transitions to failures", () => {
    expect(deploymentEventStatus(event())).toBe("success");
    expect(
      deploymentEventStatus(event({ eventType: "reconcile_failed" })),
    ).toBe("failed");
  });

  it("renders recorded event details without inventing progress", () => {
    render(<DeploymentEventTimeline events={[event()]} />);

    expect(screen.getByText("phase changed")).toBeInTheDocument();
    expect(screen.getByText(/pending → ready/)).toBeInTheDocument();
    expect(
      screen.queryByText("Operation in progress…"),
    ).not.toBeInTheDocument();
  });
});
