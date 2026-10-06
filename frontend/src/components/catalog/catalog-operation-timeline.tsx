import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { OperationEventTimeline } from "@/components/ui/operation-event-timeline";
import { QueryStates } from "@/components/ui/query-states";
import { ActionButton } from "@/components/ui/action-button";
import { getCatalogOperation, type CatalogOperation } from "@/lib/api/catalog";
import { queryKeys } from "@/lib/query-keys";

export function catalogOperationSettled(operation?: CatalogOperation) {
  if (!operation) return false;
  if (
    operation.deliveryObservationError ||
    operation.deliveryPhase === "unknown"
  )
    return false;
  return ["completed", "failed", "superseded"].includes(operation.status ?? "");
}
/** Poll cadence for a catalog operation: unconditional until it settles. */
export function catalogOperationPollInterval(
  operation?: CatalogOperation,
): number | false {
  return catalogOperationSettled(operation) ? false : 2_500;
}
export function CatalogOperationTimeline({
  operationId,
}: {
  operationId: string;
}) {
  const query = useQuery({
    queryKey: queryKeys.catalog.operation(operationId),
    queryFn: ({ signal }) => getCatalogOperation(operationId, signal),
    // No stream event is routed to `catalog.operation(id)` (`catalog_release.
    // changed` only refreshes the installed lists), so an in-flight operation
    // must keep polling even while the stream is open.
    refetchInterval: (current) =>
      catalogOperationPollInterval(current.state.data),
    throwOnError: false,
  });
  return (
    <QueryStates query={query} permission="catalog:read">
      {(operation) => (
        <section className="space-y-3">
          <p className="break-all text-sm">
            Operation {operation.id || operationId}
          </p>
          <p className="text-sm">
            Request: {operation.journalStatus || operation.status || "Unknown"}.
            Delivery: {operation.deliveryPhase || "Not yet observed"}.
          </p>
          {(operation.deliveryObservationError ||
            operation.deliveryPhase === "unknown") && (
            <p role="alert">
              Request processing has finished, but the workload outcome is
              unavailable. {operation.deliveryObservationError}
            </p>
          )}
          {operation.errorMessage && (
            <p role="alert" className="text-destructive">
              {operation.errorMessage}
            </p>
          )}
          {operation.deliveryObservedAt && (
            <p className="text-xs text-muted-foreground">
              Last observed: {operation.deliveryObservedAt}
            </p>
          )}
          <div className="flex flex-wrap gap-3">
            <ActionButton
              onClick={() => void query.refetch()}
              loading={query.isFetching}
            >
              Refresh operation
            </ActionButton>
            <Link to="/dashboard/delivery/rollouts">
              Inspect Delivery rollouts
            </Link>
          </div>
          <OperationEventTimeline
            header={`${operation.operationType ?? "Catalog"} operation`}
            headerMeta={`attempt ${operation.attemptCount ?? 1}`}
            events={operation.events ?? []}
            active={!catalogOperationSettled(operation)}
            activeLabel={
              operation.status === "pending"
                ? "Queued for the catalog reconciler"
                : "Waiting for the operation outcome…"
            }
            emptyLabel="Waiting for the catalog reconciler to record a stage…"
          />
        </section>
      )}
    </QueryStates>
  );
}
