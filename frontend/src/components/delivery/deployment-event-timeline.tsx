import {
  OperationTimeline,
  type OperationTimelineStep,
} from "@/components/ui/operation-timeline";
import type { ClusterDeploymentEvent } from "@/lib/api/delivery-deployments";

export function deploymentEventStatus(
  event: ClusterDeploymentEvent,
): OperationTimelineStep["status"] {
  const value = `${event.eventType} ${event.toPhase}`.toLowerCase();
  if (value.includes("fail") || value.includes("error")) return "failed";
  if (value.includes("progress") || value.includes("reconcil"))
    return "running";
  return "success";
}

/**
 * A normalized view of persisted delivery observations, not inferred progress.
 * The raw event table remains available for operators who need every field.
 */
export function DeploymentEventTimeline({
  events,
}: {
  events: ClusterDeploymentEvent[];
}) {
  return (
    <OperationTimeline
      header={<span className="text-sm font-medium">Deployment timeline</span>}
      headerMeta="Observed transitions"
      emptyLabel="No deployment transitions have been observed."
      steps={events.map((event) => ({
        id: event.id,
        label: event.eventType.replaceAll("_", " "),
        status: deploymentEventStatus(event),
        detail: `${event.fromPhase || "—"} → ${event.toPhase || "—"} · ${new Date(event.observedAt).toLocaleString()}`,
        error:
          deploymentEventStatus(event) === "failed"
            ? event.message || event.reasonCode
            : undefined,
      }))}
    />
  );
}
