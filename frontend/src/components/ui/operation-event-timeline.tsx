import type { ReactNode } from "react";

import {
  OperationTimeline,
  type OperationTimelineStep,
} from "@/components/ui/operation-timeline";

/** The persisted stage-event shape shared by durable operations. */
export interface OperationStageEvent {
  id?: string;
  level?: string;
  stage?: string;
  message?: string;
  createdAt?: string;
  detail?: Record<string, unknown>;
}

export function operationStageSteps(
  events: OperationStageEvent[],
): OperationTimelineStep[] {
  return events.map((event, index) => {
    const failed = ["error", "fatal"].includes(event.level?.toLowerCase() ?? "");
    const completed = event.stage === "complete" && !failed;
    return {
      id: event.id ?? `${event.stage ?? "event"}-${index}`,
      label: event.message ?? event.stage ?? "Operation event",
      status: failed ? "failed" : completed ? "success" : "success",
      detail: [
        event.stage,
        event.createdAt && new Date(event.createdAt).toLocaleString(),
      ].filter(Boolean).join(" · ") || undefined,
      error: failed ? event.message : undefined,
    };
  });
}

export function OperationEventTimeline({
  header,
  headerMeta,
  events,
  active,
  activeLabel = "Operation in progress…",
  emptyLabel,
  footer,
  className,
}: {
  header: ReactNode;
  headerMeta?: ReactNode;
  events: OperationStageEvent[];
  active: boolean;
  activeLabel?: ReactNode;
  emptyLabel?: ReactNode;
  footer?: ReactNode;
  className?: string;
}) {
  const steps = operationStageSteps(events);
  if (active) {
    steps.push({ id: "in-flight", label: activeLabel, status: "running" });
  }
  return (
    <OperationTimeline
      header={header}
      headerMeta={headerMeta}
      steps={steps}
      emptyLabel={emptyLabel}
      footer={footer}
      className={className}
    />
  );
}
