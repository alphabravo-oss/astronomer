import type { OperationMutationState } from "@/lib/hooks/operation-mutation";
import {
  OperationTimeline,
  type OperationTimelineStep,
} from "@/components/ui/operation-timeline";

interface OperationMutationTimelineProps {
  label: string;
  state: OperationMutationState;
  className?: string;
}

/**
 * Presents the common submit -> accept -> execute lifecycle used by durable
 * mutation hooks. Keeping the adapter here gives every operation the same
 * status language and prevents routes from inventing one-off progress UIs.
 */
export function OperationMutationTimeline({
  label,
  state,
  className,
}: OperationMutationTimelineProps) {
  if (state.phase === "idle") return null;

  const steps: OperationTimelineStep[] = [
    {
      id: "submit",
      label: "Submit request",
      status: state.phase === "submitting" ? "running" : "success",
    },
    {
      id: "accept",
      label: "Accept durable operation",
      status:
        state.phase === "submitting"
          ? "pending"
          : state.phase === "queued"
            ? "running"
            : "success",
      detail: state.operation?.id
        ? `Operation ${state.operation.id}`
        : undefined,
    },
    {
      id: "execute",
      label: "Execute operation",
      status: executionStatus(state.phase),
      detail: state.operation?.status,
      error:
        state.phase === "failed" || state.phase === "partial"
          ? state.operation?.errorMessage
          : undefined,
    },
  ];

  return (
    <OperationTimeline
      header={<span className="text-sm font-medium">{label}</span>}
      headerMeta={phaseLabel(state.phase)}
      steps={steps}
      className={className}
    />
  );
}

function executionStatus(
  phase: OperationMutationState["phase"],
): OperationTimelineStep["status"] {
  switch (phase) {
    case "running":
      return "running";
    case "succeeded":
      return "success";
    case "partial":
    case "failed":
      return "failed";
    default:
      return "pending";
  }
}

function phaseLabel(phase: OperationMutationState["phase"]): string {
  switch (phase) {
    case "submitting":
      return "Submitting";
    case "queued":
      return "Queued";
    case "running":
      return "Running";
    case "partial":
      return "Needs attention";
    case "failed":
      return "Failed";
    case "succeeded":
      return "Complete";
    default:
      return "Not started";
  }
}
