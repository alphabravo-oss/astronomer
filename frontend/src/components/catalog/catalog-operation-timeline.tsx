import { useQuery } from "@tanstack/react-query";

import { OperationEventTimeline } from "@/components/ui/operation-event-timeline";
import { QueryStates } from "@/components/ui/query-states";
import { getCatalogOperation } from "@/lib/api/catalog";
import { queryKeys } from "@/lib/query-keys";
import { liveFallback } from "@/lib/live/status-store";

const terminalStatuses = new Set(["completed", "failed", "superseded"]);

export function CatalogOperationTimeline({ operationId }: { operationId: string }) {
  const query = useQuery({
    queryKey: queryKeys.catalog.operation(operationId),
    queryFn: () => getCatalogOperation(operationId),
    refetchInterval: (current) =>
      terminalStatuses.has(current.state.data?.status ?? "")
        ? false
        : liveFallback(2_500)(),
  });

  return (
    <QueryStates query={query} permission="catalog:read">
      {(operation) => (
        <OperationEventTimeline
          header={`${operation.operationType ?? "Catalog"} operation`}
          headerMeta={`attempt ${operation.attemptCount ?? 1}`}
          events={operation.events ?? []}
          active={!terminalStatuses.has(operation.status ?? "")}
          activeLabel={
            operation.status === "pending"
              ? "Queued for the catalog reconciler"
              : "Catalog reconciler working…"
          }
          emptyLabel="Waiting for the catalog reconciler to record a stage…"
        />
      )}
    </QueryStates>
  );
}
