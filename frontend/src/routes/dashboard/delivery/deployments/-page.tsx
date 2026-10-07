import { pageTableCount } from "@/lib/api/pagination";
import { Select } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { useQuery } from "@tanstack/react-query";
import { Layers } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import {
  DeliveryProjectGate,
  inputClass,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  listClusterDeployments,
  type ClusterDeployment,
  type DeploymentPhase,
} from "@/lib/api/delivery-deployments";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { StackedCell } from "@/components/ui/stacked-cell";
import { useClock } from "@/lib/hooks/use-clock";
import {
  deploymentFreshness,
  deploymentDrift,
} from "@/lib/deployment-freshness";
import { DeploymentStatus, DeploymentObservationTime } from "./-freshness";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";

const phases: DeploymentPhase[] = [
  "pending",
  "blocked",
  "applying",
  "ready",
  "degraded",
  "failed",
  "suspended",
  "deleting",
  "removed",
  "unknown",
];

export function DeploymentsPage() {
  const now = useClock();
  const {
    projectId,
    projects,
    projectQuery,
    clusterId: workspaceClusterId,
    listHref,
    entityHref,
  } = useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const allowed = can(user, "delivery_deployments", "list", {
    type: "project",
    id: projectId,
  });
  const search = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const navigate = useNavigate();
  const phaseValue = search.get("phase") ?? "";
  const phase = phases.includes(phaseValue as DeploymentPhase)
    ? (phaseValue as DeploymentPhase)
    : undefined;
  const clusterId = workspaceClusterId ?? search.get("cluster") ?? undefined;
  const pageIndex = Math.max(0, Number(search.get("page") ?? 0) || 0);
  const pageSize = 50;
  const params = {
    limit: pageSize,
    offset: pageIndex * pageSize,
    ...(phase ? { phase } : {}),
    ...(clusterId ? { cluster_id: clusterId } : {}),
  };
  const updateSearch = (updates: {
    phase?: string;
    cluster?: string;
    page?: number;
  }) => {
    const next = new URLSearchParams(search);
    for (const [key, value] of Object.entries(updates)) {
      if (value) next.set(key, String(value));
      else next.delete(key);
    }
    if (workspaceClusterId) next.delete("cluster");
    void navigate({
      to: `${listHref("deployments")}?${next.toString()}`,
      replace: true,
    });
  };
  const query = useQuery({
    queryKey: queryKeys.delivery.deployments(projectId, params),
    queryFn: ({ signal }) => {
      signal.throwIfAborted();
      return listClusterDeployments(projectId, params, signal);
    },
    enabled: Boolean(projectId && allowed),
    refetchInterval: liveFallback(10_000),
  });
  useLiveQueryInvalidation(
    "cluster_deployment.changed",
    projectId
      ? queryKeys.delivery.deploymentsAll(projectId)
      : queryKeys.delivery.all,
  );
  const columns: Column<ClusterDeployment>[] = [
    {
      key: "deployment",
      header: "Deployment",
      kind: "name",
      minSize: 280,
      accessor: (row) => (
        <div className="flex min-w-0 items-center gap-2">
          <Layers className="h-4 w-4 shrink-0 text-muted-foreground" />
          <StackedCell
            primary={row.id}
            primaryClassName="font-mono text-xs font-normal"
            secondary={`target ${row.targetId.slice(0, 8)}`}
          />
        </div>
      ),
      sortAccessor: (row) => row.id,
    },
    {
      key: "cluster",
      header: "Cluster",
      kind: "id",
      minSize: 160,
      accessor: (row) => row.clusterId,
      sortAccessor: (row) => row.clusterId,
    },
    {
      key: "phase",
      header: "Status",
      kind: "status",
      accessor: (row) => <DeploymentStatus deployment={row} now={now} />,
    },
    {
      key: "revision",
      header: "Revision",
      size: 200,
      minSize: 180,
      maxSize: 320,
      accessor: (row) => (
        <StackedCell
          primary={
            row.observedRevision || row.desiredRevision || "Not observed"
          }
          primaryClassName="font-mono text-xs font-normal"
          secondary={`gen ${row.observedGeneration}/${row.desiredGeneration}`}
        />
      ),
      sortAccessor: (row) => row.observedRevision || row.desiredRevision || "",
    },
    {
      key: "drift",
      header: "Drift",
      kind: "status",
      size: 150,
      maxSize: 200,
      accessor: (row) =>
        deploymentDrift(deploymentFreshness(row, now), row.conditions),
    },
    {
      key: "observed",
      header: "Observed",
      ariaLabel: "Source observation time",
      kind: "age",
      size: 180,
      accessor: (row) => (
        <DeploymentObservationTime deployment={row} now={now} />
      ),
    },
  ];
  return (
    <DeliveryProjectGate
      projectId={projectId}
      loading={projectQuery.isLoading}
      error={projectQuery.isError}
      projectsCount={projects.length}
      permission="delivery_deployments:list"
      allowed={allowed}
      onRetry={() => void projectQuery.refetch()}
    >
      <PageShell>
        <PageHeader
          title="Deployments"
          description={
            workspaceClusterId
              ? "Desired and observed state for delivery targets on this cluster."
              : "Current desired and normalized observed state for every target and cluster pair."
          }
        />
        <p className="text-sm text-muted-foreground">
          Filters and totals use reported phase. Status reflects source
          freshness.
        </p>
        <DataTable
          data={query.data?.data ?? []}
          columns={columns}
          keyExtractor={(row) => row.id}
          loading={query.isLoading}
          isError={query.isError}
          error={query.error}
          permission="delivery_deployments:list"
          onRetry={() => void query.refetch()}
          searchable={false}
          filtersActive={!!phase || (!workspaceClusterId && !!clusterId)}
          onClearFilters={() =>
            updateSearch({ phase: "", cluster: "", page: 0 })
          }
          emptyState={{
            title: "No cluster deployments yet",
            description:
              "Deployments appear as rollouts assign component bundles to your clusters.",
          }}
          toolbar={
            <div className="flex flex-wrap gap-2">
              <Select
                aria-label="Reported phase"
                value={phase ?? ""}
                onChange={(e) =>
                  updateSearch({ phase: e.target.value, page: 0 })
                }
                className={inputClass}
              >
                <option value="">All reported phases</option>
                {phases.map((value) => (
                  <option key={value} value={value}>
                    {value}
                  </option>
                ))}
              </Select>
              {workspaceClusterId ? null : (
                <Input
                  aria-label="Cluster ID filter"
                  value={clusterId ?? ""}
                  onChange={(e) =>
                    updateSearch({ cluster: e.target.value, page: 0 })
                  }
                  className={inputClass}
                  placeholder="Cluster ID"
                />
              )}
            </div>
          }
          onRowClick={(row) =>
            void navigate({ to: entityHref("deployments", row.id) })
          }
          serverSide={{
            ...pageTableCount(query.data),
            pagination: { pageIndex, pageSize },
            onPaginationChange: (next) =>
              updateSearch({ page: next.pageIndex }),
          }}
        />
      </PageShell>
    </DeliveryProjectGate>
  );
}
