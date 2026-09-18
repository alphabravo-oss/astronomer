import { createFileRoute } from "@tanstack/react-router";
import { Boxes } from "lucide-react";
import { useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";

import { workloadColumns } from "@/components/resources/resource-list-columns";
import { DataTable, type Column } from "@/components/ui/data-table";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/components/ui/empty-state";
import { useNavigate } from "@tanstack/react-router";
import { useClusterNamespaces } from "@/lib/hooks/clusters";
import { useWorkloads } from "@/lib/hooks/workloads";
import { workloadDetailHref } from "@/components/resources/resource-table-primitives";
import type { Workload } from "@/types";
import { Select } from "@/components/ui/select";
import { pageRowCount } from "@/lib/api/pagination";
import type { WorkloadSort } from "@/lib/api/workloads";

const WORKLOAD_PAGE_SIZE = 50;

function WorkloadsPage() {
  const params = Route.useParams();
  const navigate = useNavigate();
  const clusterId = params.id;
  const [pageIndex, setPageIndex] = useState(0);
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("");
  const [namespace, setNamespace] = useState("");
  const [sort, setSort] = useState<WorkloadSort>("namespace_asc");
  const [debouncedSearch] = useDebouncedValue(search, { wait: 250 });

  const query = useWorkloads(clusterId, {
    page: pageIndex + 1,
    pageSize: WORKLOAD_PAGE_SIZE,
    search: debouncedSearch.trim() || undefined,
    kind: kind || undefined,
    namespace: namespace || undefined,
    sort,
  });
  const namespacesQuery = useClusterNamespaces(clusterId);
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
  const serverColumns = columns.map((column) => ({
    ...column,
    filter: undefined,
    sortable: false,
  }));
  const filtersActive = Boolean(search || kind || namespace);
  const clearFilters = () => {
    setSearch("");
    setKind("");
    setNamespace("");
    setPageIndex(0);
  };

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
      ) : workloads.length === 0 && !filtersActive ? (
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
          columns={serverColumns}
          keyExtractor={(workload) =>
            `${workload.kind}/${workload.namespace}/${workload.name}`
          }
          searchPlaceholder="Search workloads..."
          pageSize={WORKLOAD_PAGE_SIZE}
          filtersActive={filtersActive}
          onClearFilters={clearFilters}
          serverSide={{
            rowCount: pageRowCount(query.data),
            pagination: { pageIndex, pageSize: WORKLOAD_PAGE_SIZE },
            onPaginationChange: (next) => setPageIndex(next.pageIndex),
            search: {
              value: search,
              onChange: (value) => {
                setSearch(value);
                setPageIndex(0);
              },
            },
          }}
          toolbar={
            <div className="flex flex-wrap items-center gap-2">
              <Select
                aria-label="Filter workloads by kind"
                value={kind}
                onChange={(event) => {
                  setKind(event.target.value);
                  setPageIndex(0);
                }}
                containerClassName="w-auto"
              >
                <option value="">All kinds</option>
                {[
                  "Deployment",
                  "StatefulSet",
                  "DaemonSet",
                  "Job",
                  "CronJob",
                ].map((value) => (
                  <option key={value} value={value}>
                    {value}
                  </option>
                ))}
              </Select>
              <Select
                aria-label="Filter workloads by namespace"
                value={namespace}
                onChange={(event) => {
                  setNamespace(event.target.value);
                  setPageIndex(0);
                }}
                containerClassName="w-auto"
              >
                <option value="">All namespaces</option>
                {(namespacesQuery.data ?? []).map((item) => (
                  <option key={item.name} value={item.name}>
                    {item.name}
                  </option>
                ))}
              </Select>
              <Select
                aria-label="Sort workloads"
                value={sort}
                onChange={(event) => {
                  setSort(event.target.value as WorkloadSort);
                  setPageIndex(0);
                }}
                containerClassName="w-auto"
              >
                <option value="namespace_asc">Namespace A–Z</option>
                <option value="namespace_desc">Namespace Z–A</option>
                <option value="name_asc">Name A–Z</option>
                <option value="name_desc">Name Z–A</option>
                <option value="created_desc">Newest first</option>
                <option value="created_asc">Oldest first</option>
              </Select>
            </div>
          }
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
