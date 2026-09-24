import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";
import { ArrowRight, ExternalLink, Server } from "lucide-react";
import { useClusterEstateTable } from "@/lib/hooks/cluster-estate-table";
import { Link as RouterLink } from "@tanstack/react-router";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { PageHeader, PageShell } from "@/components/ui/page";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { formatPercentage } from "@/lib/utils";
import type { Cluster } from "@/types";
import { EmptyState, LoadingState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { getSharedGrafanaStatus } from "@/lib/api/monitoring-stack";
import { queryKeys } from "@/lib/query-keys";
import { fleetGrafanaOpenURL } from "@/components/monitoring/stack-spec";

function clusterMetricsPath(clusterId: string, range?: string | null): string {
  const base = `/dashboard/clusters/${clusterId}/metrics`;
  return range ? `${base}?range=${encodeURIComponent(range)}` : base;
}

function MonitoringFleetPage() {
  const navigate = useNavigate();
  const search = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const clusterId = search.get("cluster");
  const range = search.get("range");
  const { query: clustersQuery, serverSide } = useClusterEstateTable();
  const clusters = useMemo(
    () => clustersQuery.data?.data ?? [],
    [clustersQuery.data],
  );
  const grafanaQuery = useQuery({
    queryKey: queryKeys.monitoringStack.status("grafana"),
    queryFn: getSharedGrafanaStatus,
  });
  const grafanaOpenURL = fleetGrafanaOpenURL(grafanaQuery.data);

  useEffect(() => {
    if (!clusterId) return;
    void navigate({ to: clusterMetricsPath(clusterId, range), replace: true });
  }, [clusterId, range, navigate]);

  const columns: Column<Cluster>[] = [
    {
      key: "name",
      header: "Cluster",
      accessor: (row) => (
        <div className="min-w-0">
          <p className="font-medium text-foreground truncate">
            {row.displayName || row.name}
          </p>
          <p className="text-xs text-muted-foreground truncate">
            {row.environment || "—"} {row.region ? `· ${row.region}` : ""}
          </p>
        </div>
      ),
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: "cpu",
      header: "CPU",
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">
          {formatPercentage(row.cpuPercentage, 0)}
        </span>
      ),
      sortAccessor: (row) => row.cpuPercentage,
    },
    {
      key: "memory",
      header: "Memory",
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">
          {formatPercentage(row.memoryPercentage, 0)}
        </span>
      ),
      sortAccessor: (row) => row.memoryPercentage,
    },
    {
      key: "pods",
      header: "Pods",
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">
          {row.podCount}
        </span>
      ),
      sortAccessor: (row) => row.podCount,
      align: "center",
    },
    {
      key: "open",
      header: "",
      accessor: (row) => (
        <RouterLink
          to={clusterMetricsPath(row.id)}
          className="inline-flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground"
        >
          Metrics
          <ArrowRight className="h-3.5 w-3.5" />
        </RouterLink>
      ),
      sortable: false,
      align: "right",
    },
  ];

  if (clusterId) {
    return <LoadingState title="Opening cluster metrics" />;
  }

  return (
    <PageShell>
      <PageHeader
        title="Shared metrics"
        description="Open a cluster to see dashboards, node utilization, and the Prometheus stack for that environment."
        actions={
          grafanaOpenURL ? (
            <a
              href={grafanaOpenURL}
              className="inline-flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs font-medium text-foreground hover:bg-accent"
            >
              <ExternalLink className="h-3.5 w-3.5" />
              Open shared Grafana
            </a>
          ) : null
        }
      />
      <QueryStates
        query={clustersQuery}
        loadingTitle="Loading cluster metrics"
        permission="clusters:read"
        errorTitle="Failed to load cluster metrics"
        isEmpty={(result) =>
          result.data.length === 0 &&
          !serverSide.search.value &&
          serverSide.pagination.pageIndex === 0
        }
        empty={
          <EmptyState
            icon={Server}
            title="No clusters registered"
            description="Register an existing Kubernetes cluster to begin collecting and exploring metrics."
            actionLabel="Register cluster"
            actionHref="/dashboard/clusters/register"
          />
        }
      >
        <DataTable
          data={clusters}
          serverSide={serverSide}
          pageSize={serverSide.pagination.pageSize}
          columns={columns}
          keyExtractor={(row) => row.id}
          searchPlaceholder="Search clusters..."
          emptyState={{
            title: "No clusters available",
            description:
              "Resources will appear here when they are available in this scope.",
          }}
          onRowClick={(row) =>
            void navigate({ to: clusterMetricsPath(row.id) })
          }
        />
      </QueryStates>
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/monitoring/")({
  component: MonitoringFleetPage,
});
