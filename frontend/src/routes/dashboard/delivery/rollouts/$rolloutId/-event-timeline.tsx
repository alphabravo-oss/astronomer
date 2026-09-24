import { useQuery } from "@tanstack/react-query";
import {
  OperationTimeline,
  type OperationTimelineStepStatus,
} from "@/components/ui/operation-timeline";
import {
  OffsetPagination,
  useOffsetPagination,
} from "@/components/ui/offset-pagination";
import { QueryStates } from "@/components/ui/query-states";
import {
  listDeliveryRolloutEvents,
  type DeliveryRolloutEvent,
} from "@/lib/api/delivery-rollouts";
import { pageCountLabel } from "@/lib/api/pagination";
import { queryKeys } from "@/lib/query-keys";
import { liveFallback } from "@/lib/live/status-store";

export function RolloutEventTimeline({
  projectId,
  rolloutId,
  allowed,
}: {
  projectId: string;
  rolloutId: string;
  allowed: boolean;
}) {
  const control = useOffsetPagination(`${projectId}:${rolloutId}`);
  const query = useQuery({
    queryKey: queryKeys.delivery.rolloutEvents(
      projectId,
      rolloutId,
      control.params,
    ),
    queryFn: ({ signal }) =>
      listDeliveryRolloutEvents(projectId, rolloutId, control.params, signal),
    enabled: !!projectId && !!rolloutId && allowed,
    throwOnError: false,
    refetchInterval: (query) =>
      query.state.status === "error" ? false : liveFallback(10_000)(),
  });
  if (!allowed) return null;
  return (
    <div className="space-y-3">
      <QueryStates query={query} permission="delivery_rollouts:read">
        <OperationTimeline
          header="Rollout events"
          headerMeta={`${pageCountLabel(query.isError ? undefined : query.data)} events`}
          steps={(query.isError ? [] : (query.data?.data ?? [])).map(eventStep)}
        />
      </QueryStates>
      <OffsetPagination
        control={control}
        query={query}
        label="rollout events"
      />
    </div>
  );
}
function eventStep(event: DeliveryRolloutEvent) {
  const failed =
    event.toState?.includes("failed") || event.eventType.includes("failed");
  const status: OperationTimelineStepStatus = failed
    ? "failed"
    : event.toState === "succeeded" || event.toState === "ready"
      ? "success"
      : "running";
  return {
    id: event.id,
    label: event.eventType.replaceAll("_", " "),
    status,
    detail: `${event.fromState || "—"} → ${event.toState || "—"} · ${new Date(event.occurredAt).toLocaleString()}`,
    error: failed ? event.reasonCode : undefined,
  };
}
