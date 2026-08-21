import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Radio } from "lucide-react";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  Detail,
  DetailGrid,
  ErrorMessage,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import { getClusterDeliveryInventory } from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { useParams } from "@/lib/navigation";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { LoadingState } from "@/components/ui/empty-state";

function ClusterDeliveryPage() {
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
    refetchInterval: liveFallback(10_000),
  });
  useLiveQueryInvalidation(
    "cluster_deployment.changed",
    projectId
      ? queryKeys.delivery.clusterInventory(projectId, clusterId)
      : queryKeys.delivery.all,
  );
  const inventory = query.data?.controllerInventory;
  return (
    <PageShell>
      <PageHeader
        title="Flux"
        description="Pinned controller compatibility for this cluster."
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
        {query.isLoading && <LoadingState title="Loading Flux inventory" />}
        {inventory && (
          <>
            <DetailGrid>
              <Detail
                label="Compatibility"
                value={
                  <DeliveryPhaseBadge value={inventory.compatibilityStatus} />
                }
              />
              <Detail
                label="Controllers ready"
                value={inventory.ready ? "Yes" : "No"}
              />
              <Detail label="Agent version" value={inventory.agentVersion} />
              <Detail label="Flux version" value={inventory.fluxVersion} />
              <Detail label="Kubernetes" value={inventory.kubernetesVersion} />
              <Detail
                label="Distribution digest"
                value={inventory.distributionDigest}
                mono
              />
              <Detail
                label="Observed"
                value={
                  inventory.observedAt
                    ? new Date(inventory.observedAt).toLocaleString()
                    : "Never"
                }
              />
              <Detail label="Error" value={inventory.errorCode || "None"} />
            </DetailGrid>
            <PageSection
              title="Controller set"
              description="Only the pinned source, Kustomize, and Helm controllers are part of the distribution."
            >
              <div className="grid gap-2 sm:grid-cols-3">
                {Object.entries(inventory.components).map(([name, version]) => (
                  <div
                    key={name}
                    className="flex items-center gap-2 rounded-md border border-border bg-card p-3"
                  >
                    <Radio className="h-4 w-4 text-primary" />
                    <div>
                      <p className="text-sm font-medium">{name}</p>
                      <p className="text-xs text-muted-foreground">{version}</p>
                    </div>
                  </div>
                ))}
              </div>
            </PageSection>
          </>
        )}
      </DeliveryProjectGate>
    </PageShell>
  );
}
export const Route = createFileRoute("/dashboard/clusters/$id/delivery/")({
  component: ClusterDeliveryPage,
});
