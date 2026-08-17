import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Radio } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  Detail,
  DetailGrid,
  inputClass,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import {
  getClusterDeliveryInventory,
  type ClusterDeployment,
} from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { useParams, useRouter } from "@/lib/navigation";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { formatRelativeTime } from "@/lib/utils";

function ClusterDeliveryPage() {
  const { id: clusterId } = useParams<{ id: string }>();
  const { projectId, projects, projectQuery, setProjectId } =
    useDeliveryProjectScope();
  const { data: user } = useCurrentUser();
  const allowed = can(user, "delivery_inventory", "read", {
    type: "project",
    id: projectId,
  });
  const router = useRouter();
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
  const columns: Column<ClusterDeployment>[] = [
    {
      key: "target",
      header: "Target",
      accessor: (row) => <code className="text-xs">{row.targetId}</code>,
    },
    {
      key: "phase",
      header: "Phase",
      accessor: (row) => <DeliveryPhaseBadge value={row.phase} />,
    },
    {
      key: "revision",
      header: "Observed revision",
      accessor: (row) => (
        <span className="font-mono text-xs">
          {row.observedRevision || "Not observed"}
        </span>
      ),
    },
    {
      key: "generation",
      header: "Generation",
      accessor: (row) => `${row.observedGeneration}/${row.desiredGeneration}`,
    },
    {
      key: "observed",
      header: "Last observed",
      accessor: (row) =>
        row.lastObservedAt ? formatRelativeTime(row.lastObservedAt) : "Never",
    },
  ];
  return (
    <PageShell>
      <PageHeader
        eyebrow="Cluster"
        title="Continuous Delivery"
        description="Pinned controller compatibility and every Astronomer-managed deployment on this cluster."
        actions={
          <label className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Project</span>
            <select
              aria-label="Delivery project"
              value={projectId}
              onChange={(e) => setProjectId(e.target.value)}
              className={inputClass}
            >
              <option value="">Select project</option>
              {projects.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.displayName}
                </option>
              ))}
            </select>
          </label>
        }
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
        <PageSection
          title="Cluster deployments"
          description={`${query.data?.deploymentCount ?? 0} delivery targets currently have state for this cluster.`}
        >
          <DataTable
            data={query.data?.deployments ?? []}
            columns={columns}
            keyExtractor={(row) => row.id}
            loading={query.isLoading}
            isError={query.isError}
            onRetry={() => void query.refetch()}
            searchable={false}
            emptyMessage="No delivery deployments on this cluster"
            onRowClick={(row) =>
              router.push(
                `/dashboard/delivery/deployments/${row.id}?project=${encodeURIComponent(projectId)}`,
              )
            }
          />
        </PageSection>
      </DeliveryProjectGate>
    </PageShell>
  );
}
export const Route = createFileRoute("/dashboard/clusters/$id/delivery/")({
  component: ClusterDeliveryPage,
});
