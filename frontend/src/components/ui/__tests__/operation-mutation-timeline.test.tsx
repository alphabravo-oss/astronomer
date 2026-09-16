import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { OperationMutationTimeline } from "../operation-mutation-timeline";

describe("OperationMutationTimeline", () => {
  it("stays out of the layout before an operation starts", () => {
    const { container } = render(
      <OperationMutationTimeline
        label="Backup run"
        state={{ phase: "idle" }}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders one consistent lifecycle with durable operation identity", () => {
    render(
      <OperationMutationTimeline
        label="Backup run"
        state={{
          phase: "running",
          operation: { id: "op-123", status: "running" },
        }}
      />,
    );

    expect(screen.getByText("Backup run")).toBeInTheDocument();
    expect(screen.getByText("Submit request")).toBeInTheDocument();
    expect(screen.getByText("Accept durable operation")).toBeInTheDocument();
    expect(screen.getByText("Execute operation")).toBeInTheDocument();
    expect(screen.getByText("Operation op-123")).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
  });

  it("surfaces terminal failure evidence", () => {
    render(
      <OperationMutationTimeline
        label="Restore"
        state={{
          phase: "failed",
          operation: {
            id: "op-failed",
            status: "failed",
            errorMessage: "snapshot verification failed",
          },
        }}
      />,
    );

    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(
      screen.getByText("snapshot verification failed"),
    ).toBeInTheDocument();
  });
});
