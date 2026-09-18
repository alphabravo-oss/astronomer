import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  OperationEventTimeline,
  operationStageSteps,
} from "@/components/ui/operation-event-timeline";

describe("operation stage timeline adapter", () => {
  it("maps persisted event levels without inventing stages", () => {
    const steps = operationStageSteps([
      { id: "queued", level: "info", stage: "queue", message: "Queued" },
      { id: "failed", level: "error", stage: "install", message: "Helm failed" },
    ]);
    expect(steps.map((step) => step.status)).toEqual(["success", "failed"]);
    expect(steps[1].error).toBe("Helm failed");
  });

  it("adds an explicit in-flight row only while the operation is active", () => {
    render(
      <OperationEventTimeline
        header="Catalog operation"
        events={[{ id: "install", level: "info", stage: "install", message: "Installing" }]}
        active
        activeLabel="Catalog reconciler working…"
      />,
    );
    expect(screen.getByText("Installing")).toBeInTheDocument();
    expect(screen.getByText("Catalog reconciler working…")).toBeInTheDocument();
  });
});
