import { createFileRoute } from "@tanstack/react-router";
import { Boxes } from "lucide-react";

import { workloadColumns } from "@/components/resources/resource-list-columns";
import { DataTable, type Column } from "@/components/ui/data-table";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/components/ui/empty-state";
import { useNavigate } from "@tanstack/react-router";
import { useWorkloads } from "@/lib/hooks/workloads";
import { workloadDetailHref } from "@/components/resources/resource-table-primitives";
import type { Workload } from "@/types";

function WorkloadsPage() {
  const params = Route.useParams();
  const navigate = useNavigate();
  const clusterId = params.id;

  const query = useWorkloads(clusterId, { pageSize: 200 });
  const workloads = query.data?.data ?? [];
  const columns: Column<Workload>[] = [
    {
      ...workloadColumns[0],
      accessor: (workload) => (
        <span className="font-medium text-foreground font-mono text-xs">
          {workload.name}
        </span>
      ),
    },
    {
      key: "kind",
      header: "Kind",
      accessor: (workload) => workload.kind,
      sortAccessor: (workload) => workload.kind,
      filter: { label: "Kind" },
    },
    ...workloadColumns.slice(1),
  ];

  return (
    <div className="space-y-4">
      <div>
        <div className="flex items-center gap-2">
          <h1 className="text-2xl font-semibold">Workloads</h1>
        </div>
        <p className="text-sm text-muted-foreground">
          All Deployments, StatefulSets, DaemonSets, Jobs, and CronJobs in this
          cluster.
        </p>
      </div>

      {query.isError && workloads.length === 0 ? (
        <ErrorState
          title="Workloads unavailable"
          description="Astronomer could not list workloads from this cluster. Retry when the cluster connection is available."
        />
      ) : query.isLoading && workloads.length === 0 ? (
        <LoadingState title="Loading workloads" />
      ) : workloads.length === 0 ? (
        <EmptyState
          icon={Boxes}
          title="No workloads in this cluster"
          description="Install a chart from the catalog or use the kubectl shell to apply a manifest."
          actionLabel="Browse catalog"
          actionHref={`/dashboard/catalog?cluster_id=${clusterId}`}
        />
      ) : (
        <DataTable
          data={workloads}
          columns={columns}
          keyExtractor={(workload) =>
            `${workload.kind}/${workload.namespace}/${workload.name}`
          }
          searchPlaceholder="Search workloads..."
          onRowClick={(workload) =>
            void navigate({
              to: workloadDetailHref(
                clusterId,
                workload.kind,
                workload.namespace,
                workload.name,
              ),
            })
          }
          emptyState={{
            title: "No workloads in this cluster",
            description:
              "Install a chart from the catalog or use the kubectl shell to apply a manifest.",
          }}
        />
      )}
    </div>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/workloads/")({
  component: WorkloadsPage,
});
