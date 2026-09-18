import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  Boxes,
  Database,
  HardDrive,
  ShieldCheck,
} from "lucide-react";
import { useMemo } from "react";
import { DataTable } from "@/components/ui/data-table";
import { MetricCard } from "@/components/ui/metric-card";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import {
  DeliveryProjectGate,
  ErrorMessage,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import { getClusterDeliveryInventory } from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { useParams } from "@/lib/navigation";
import { liveFallback } from "@/lib/live/status-store";
import { formatRelativeTime } from "@/lib/utils";
import { systemComponentColumns } from "./-columns";

function SystemComponentsPage() {
  const { id: clusterId } = useParams<{ id: string }>();
  const { projectId, projects, projectQuery } = useDeliveryProjectScope({
    clusterId,
  });
  const { data: user } = useCurrentUser();
  const allowed = can(user, "delivery_inventory", "read", {
    type: "project",
    id: projectId,
  });
  const query = useQuery({
    queryKey: queryKeys.delivery.clusterInventory(projectId, clusterId),
    queryFn: () => getClusterDeliveryInventory(projectId, clusterId),
    enabled: Boolean(projectId && clusterId && allowed),
    refetchInterval: liveFallback(15_000),
  });
  const inventory = query.data?.controllerInventory;
  const components = useMemo(
    () => inventory?.systemComponents ?? [],
    [inventory?.systemComponents],
  );
  const summary = useMemo(
    () => ({
      healthy: components.filter((item) => item.health === "healthy").length,
      attention: components.filter((item) =>
        ["degraded", "unavailable"].includes(item.health),
      ).length,
      astronomer: components.filter((item) => item.owner === "astronomer")
        .length,
      flux: components.filter((item) => item.owner === "flux").length,
      cluster: components.filter((item) =>
        ["cluster", "external"].includes(item.owner),
      ).length,
    }),
    [components],
  );
  const observedAt = inventory?.observedAt
    ? new Date(inventory.observedAt)
    : undefined;
  const stale = observedAt
    ? Date.now() - observedAt.getTime() > 5 * 60 * 1000
    : true;

  const columns = systemComponentColumns(clusterId);

  return (
    <PageShell>
      <PageHeader
        title="System Components"
        description="Observed cluster infrastructure and Astronomer services, with explicit lifecycle ownership. Flux-managed applications and cluster-owned infrastructure are intentionally distinct."
      />
      <DeliveryProjectGate
        projectId={projectId}
        loading={projectQuery.isLoading}
        error={projectQuery.isError}
        projectsCount={projects.length}
        permission="delivery_inventory:read"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        {query.isError && <ErrorMessage error={query.error} />}
        {stale && !query.isLoading ? (
          <div className="flex items-start gap-3 rounded-md border border-warning/40 bg-warning/10 p-4 text-sm">
            <AlertTriangle className="mt-0.5 h-4 w-4 text-warning" />
            <div>
              <p className="font-medium">Inventory evidence is stale</p>
              <p className="text-muted-foreground">
                {observedAt
                  ? "Last observed " +
                    formatRelativeTime(observedAt.toISOString()) +
                    "."
                  : "This cluster has not reported system inventory yet."}
              </p>
            </div>
          </div>
        ) : null}
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <MetricCard
            title="Astronomer-owned"
            value={summary.astronomer}
            icon={<Boxes className="h-4 w-4" />}
          />
          <MetricCard
            title="Flux distribution"
            value={summary.flux}
            icon={<ShieldCheck className="h-4 w-4" />}
          />
          <MetricCard
            title="Cluster / external"
            value={summary.cluster}
            icon={<HardDrive className="h-4 w-4" />}
          />
          <MetricCard
            title="Healthy / attention"
            value={summary.healthy + " / " + summary.attention}
            icon={
              summary.attention ? (
                <AlertTriangle className="h-4 w-4" />
              ) : (
                <Database className="h-4 w-4" />
              )
            }
          />
        </div>
        <PageSection
          title="Component inventory"
          description={
            observedAt
              ? "Observed " +
                formatRelativeTime(observedAt.toISOString()) +
                ". Resource totals are aggregate requested and limited amounts for desired replicas."
              : "Waiting for the cluster agent to publish its first observation."
          }
        >
          <DataTable
            data={components}
            columns={columns}
            keyExtractor={(row) => row.id}
            searchable
            searchPlaceholder="Search components, owners, namespaces, or versions…"
            pageSize={25}
            loading={query.isLoading}
            emptyMessage="No system components have been observed."
            persistKey={"delivery-system-components:" + clusterId}
            resizable
          />
        </PageSection>
      </DeliveryProjectGate>
    </PageShell>
  );
}

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/system-components/",
)({
  component: SystemComponentsPage,
});
