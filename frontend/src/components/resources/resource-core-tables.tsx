import { useClusterEvents } from "@/lib/hooks/clusters";
import { ExplorerDataTable } from "@/components/resources/explorer-data-table";
import { eventColumns } from "@/components/resources/resource-list-columns";

export function EventsTable({ clusterId }: { clusterId: string }) {
  const query = useClusterEvents(clusterId, { limit: 200 });
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        Recent window: up to 200 cluster events. Filters apply only to this
        window; this is not complete event history.
      </p>
      <ExplorerDataTable
        clusterId={clusterId}
        resourceType="events"
        data={query.isError ? [] : query.data || []}
        columns={eventColumns}
        keyExtractor={(r) => r.id}
        searchPlaceholder="Filter recent events..."
        loading={query.isLoading}
        isError={query.isError}
        error={query.error}
        onRetry={() => query.refetch()}
        emptyState={{
          title: "No events found",
          description:
            "New observations will appear here as they are reported.",
        }}
        namespaceAccessor={(row) => row.involvedObject.namespace}
      />
    </div>
  );
}

export { NodesTable } from "./resource-nodes-table";
export { NamespacesTable } from "./resource-namespaces-table";
export { PodsTable } from "./resource-pods-table";
