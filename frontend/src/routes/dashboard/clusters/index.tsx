import { Input } from "@/components/ui/input";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import { useNavigate } from "@tanstack/react-router";
import { registrationSearch } from "@/components/clusters/registration-flow";
import { useClusters, useDeleteCluster } from "@/lib/hooks/clusters";
import { queryKeys } from "@/lib/query-keys";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionButton } from "@/components/ui/action-button";
import { Select } from "@/components/ui/select";
import { PageHeader, PageShell } from "@/components/ui/page";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { EditClusterModal } from "@/components/clusters/edit-cluster-modal";
import { ActionMenu } from "@/components/ui/action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  formatRelativeTime,
  formatPercentage,
  providerDisplayName,
  distributionDisplayName,
} from "@/lib/utils";
import type { Cluster } from "@/types";
import { Plus, SearchX, Server, Terminal, Pencil, Trash2 } from "lucide-react";
import { pageRowCount } from "@/lib/api/pagination";

const CLUSTERS_PAGE_SIZE = 50;

function ClustersPage() {
  const navigate = useNavigate();
  const routeSearch = Route.useSearch();
  const [statusFilter, setStatusFilter] = useState<string>(
    typeof routeSearch.status === "string" ? routeSearch.status : "",
  );
  const [providerFilter, setProviderFilter] = useState<string>("");
  const [envFilter, setEnvFilter] = useState<string>("");
  const [pageIndex, setPageIndex] = useState(0);
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, { wait: 250 });

  // Action menu state
  const [editCluster, setEditCluster] = useState<Cluster | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Cluster | null>(null);
  const [forceDelete, setForceDelete] = useState(false);
  const deleteMutation = useDeleteCluster();

  const clustersQuery = useClusters({
    status: statusFilter || undefined,
    provider: providerFilter || undefined,
    environment: envFilter || undefined,
    search: debouncedSearch.trim() || undefined,
    page: pageIndex + 1,
    pageSize: CLUSTERS_PAGE_SIZE,
  });

  // Live updates: shape-changing events trigger a list refetch; per-row
  // metric ticks are merged in place by the layout's metrics merger.
  useLiveQueryInvalidation(
    [
      "cluster.connected",
      "cluster.disconnected",
      "cluster.created",
      "cluster.updated",
      "cluster.deleted",
      "cluster.status_changed",
      "cluster.heartbeat",
      "agent.reconnecting",
      "agent.failed",
    ],
    [queryKeys.clusters.all],
  );

  const clusters = clustersQuery.data?.data || [];
  const hasServerFilters = Boolean(
    statusFilter || providerFilter || envFilter || search,
  );

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteMutation.mutateAsync({
        id: deleteTarget.id,
        force: forceDelete,
      });
      setDeleteTarget(null);
    } catch {
      // Error handled by mutation
    }
  };

  const columns: Column<Cluster>[] = [
    {
      key: "name",
      header: "Name",
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.displayName}</p>
          <p className="text-xs text-muted-foreground">{row.name}</p>
        </div>
      ),
      sortAccessor: (row) => row.displayName,
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) =>
        row.decommissioning ? (
          <StatusBadge status="decommissioning" label="Decommissioning" pulse />
        ) : (
          <StatusBadge status={row.status} />
        ),
      sortAccessor: (row) =>
        row.decommissioning ? "decommissioning" : row.status,
    },
    {
      key: "provider",
      header: "Provider",
      accessor: (row) => (
        <span className="text-muted-foreground">
          {providerDisplayName(row.provider)}
        </span>
      ),
      sortAccessor: (row) => row.provider,
    },
    {
      key: "distribution",
      header: "Distribution",
      accessor: (row) => (
        <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground">
          {distributionDisplayName(row.distribution)}
        </span>
      ),
      sortAccessor: (row) => row.distribution,
    },
    {
      key: "version",
      header: "K8s Version",
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.kubernetesVersion}
        </span>
      ),
    },
    {
      key: "nodes",
      header: "Nodes",
      accessor: (row) => <span className="tabular-nums">{row.nodeCount}</span>,
      sortAccessor: (row) => row.nodeCount,
      align: "center",
    },
    {
      key: "pods",
      header: "Pods",
      accessor: (row) => <span className="tabular-nums">{row.podCount}</span>,
      sortAccessor: (row) => row.podCount,
      align: "center",
    },
    {
      key: "cpu",
      header: "CPU%",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={`gauge-bar-fill ${
                row.cpuPercentage >= 90
                  ? "bg-status-error"
                  : row.cpuPercentage >= 75
                    ? "bg-status-warning"
                    : "bg-status-success"
              }`}
              style={{ width: `${Math.min(row.cpuPercentage, 100)}%` }}
            />
          </div>
          <span className="text-xs tabular-nums text-muted-foreground w-10">
            {formatPercentage(
              row.cpuPercentage,
              row.cpuPercentage < 10 ? 1 : 0,
            )}
          </span>
        </div>
      ),
      sortAccessor: (row) => row.cpuPercentage,
    },
    {
      key: "mem",
      header: "Mem%",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={`gauge-bar-fill ${
                row.memoryPercentage >= 90
                  ? "bg-status-error"
                  : row.memoryPercentage >= 75
                    ? "bg-status-warning"
                    : "bg-status-success"
              }`}
              style={{ width: `${Math.min(row.memoryPercentage, 100)}%` }}
            />
          </div>
          <span className="text-xs tabular-nums text-muted-foreground w-10">
            {formatPercentage(
              row.memoryPercentage,
              row.memoryPercentage < 10 ? 1 : 0,
            )}
          </span>
        </div>
      ),
      sortAccessor: (row) => row.memoryPercentage,
    },
    {
      key: "heartbeat",
      header: "Last Heartbeat",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastHeartbeat ? formatRelativeTime(row.lastHeartbeat) : "Never"}
        </span>
      ),
      sortAccessor: (row) => row.lastHeartbeat ?? "",
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: "Registration Command",
              icon: <Terminal className="h-3.5 w-3.5" />,
              onClick: () =>
                void navigate({
                  to: "/dashboard/clusters/register",
                  search: registrationSearch(row.id),
                }),
            },
            {
              label: "Edit",
              icon: <Pencil className="h-3.5 w-3.5" />,
              onClick: () => setEditCluster(row),
            },
            {
              label: "Delete",
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteTarget(row),
              variant: "destructive",
              separator: true,
            },
          ]}
        />
      ),
      align: "center",
    },
  ];
  // The API owns the stable estate-wide order (created_at DESC, id DESC).
  // Page-local sorting would present a misleading partial order, so columns
  // remain unsortable until a sort parameter is supported end to end.
  const serverColumns = columns.map((column) => ({
    ...column,
    sortable: false,
  }));

  return (
    <PageShell>
      <PageHeader
        title="Clusters"
        description="Monitor and manage the existing Kubernetes clusters you've registered with Astronomer"
        actions={
          <ActionButton
            intent="primary"
            icon={<Plus className="h-4 w-4" />}
            onClick={() =>
              void navigate({ to: "/dashboard/clusters/register" })
            }
          >
            Register Cluster
          </ActionButton>
        }
      />

      <QueryStates
        query={clustersQuery}
        loadingTitle="Loading clusters"
        permission="clusters:read"
        errorTitle="Failed to load clusters"
        isEmpty={(response) => response.data.length === 0}
        empty={
          <EmptyState
            icon={hasServerFilters ? SearchX : Server}
            title={
              hasServerFilters
                ? "No clusters match these filters"
                : "No clusters registered"
            }
            description={
              hasServerFilters
                ? "Clear the status, provider, and environment filters to see all registered clusters."
                : "Register an existing Kubernetes cluster to start monitoring and operating it."
            }
            actionLabel={
              hasServerFilters ? "Clear filters" : "Register cluster"
            }
            actionIcon={hasServerFilters ? undefined : Plus}
            onAction={
              hasServerFilters
                ? () => {
                    setStatusFilter("");
                    setProviderFilter("");
                    setEnvFilter("");
                    setSearch("");
                    setPageIndex(0);
                  }
                : () => void navigate({ to: "/dashboard/clusters/register" })
            }
          />
        }
      >
        {/* Filters */}
        <DataTable
          data={clusters}
          columns={serverColumns}
          keyExtractor={(row) => row.id}
          persistKey="clusters"
          onRowClick={(row) =>
            void navigate({ to: `/dashboard/clusters/${row.id}` })
          }
          searchPlaceholder="Search clusters..."
          pageSize={CLUSTERS_PAGE_SIZE}
          filtersActive={hasServerFilters}
          onClearFilters={() => {
            setStatusFilter("");
            setProviderFilter("");
            setEnvFilter("");
            setSearch("");
            setPageIndex(0);
          }}
          serverSide={{
            rowCount: pageRowCount(clustersQuery.data),
            pagination: { pageIndex, pageSize: CLUSTERS_PAGE_SIZE },
            onPaginationChange: (next) => setPageIndex(next.pageIndex),
            search: {
              value: search,
              onChange: (value) => {
                setSearch(value);
                setPageIndex(0);
              },
            },
          }}
          emptyState={{
            title: "No clusters registered",
            description:
              "Register an existing Kubernetes cluster to start monitoring and operating it.",
            action: {
              label: "Register cluster",
              href: "/dashboard/clusters/register",
            },
          }}
          toolbar={
            <div className="flex items-center gap-2">
              <Select
                aria-label="Filter clusters by status"
                value={statusFilter}
                onChange={(e) => {
                  setStatusFilter(e.target.value);
                  setPageIndex(0);
                }}
                containerClassName="w-auto"
              >
                <option value="">All Statuses</option>
                <option value="active">Active</option>
                <option value="warning">Warning</option>
                <option value="error">Error</option>
                <option value="disconnected">Disconnected</option>
                <option value="connecting">Connecting</option>
              </Select>

              <Select
                aria-label="Filter clusters by provider"
                value={providerFilter}
                onChange={(e) => {
                  setProviderFilter(e.target.value);
                  setPageIndex(0);
                }}
                containerClassName="w-auto"
              >
                <option value="">All Providers</option>
                <option value="aws">AWS</option>
                <option value="gcp">GCP</option>
                <option value="azure">Azure</option>
                <option value="on-prem">On-Premise</option>
                <option value="digitalocean">DigitalOcean</option>
              </Select>

              <Select
                aria-label="Filter clusters by environment"
                value={envFilter}
                onChange={(e) => {
                  setEnvFilter(e.target.value);
                  setPageIndex(0);
                }}
                containerClassName="w-auto"
              >
                <option value="">All Environments</option>
                <option value="production">Production</option>
                <option value="staging">Staging</option>
                <option value="development">Development</option>
                <option value="testing">Testing</option>
              </Select>
            </div>
          }
        />
      </QueryStates>

      {/* "Re-show install command" → navigate to wizard step 2 for the
          existing cluster. The wizard's status endpoint handles
          already-`ready` clusters by short-circuiting to the cluster
          detail page rather than re-running registration. */}

      {/* Edit Modal */}
      {editCluster && (
        <EditClusterModal
          cluster={editCluster}
          onClose={() => setEditCluster(null)}
        />
      )}

      {/* Delete Confirmation */}
      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => {
          setDeleteTarget(null);
          setForceDelete(false);
        }}
        onConfirm={handleDelete}
        title="Delete Cluster"
        description={`This will remove the cluster "${deleteTarget?.displayName}" from Astronomer. The underlying Kubernetes cluster will not be destroyed.`}
        confirmText="Delete"
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deleteMutation.isPending}
      >
        <label className="flex items-start gap-2 text-xs text-muted-foreground cursor-pointer">
          <Input
            type="checkbox"
            checked={forceDelete}
            onChange={(e) => setForceDelete(e.target.checked)}
            className="mt-0.5 h-3.5 w-3.5 rounded-sm border-border"
          />
          <span>
            <span className="font-medium text-foreground">Force delete</span> —
            remove immediately instead of waiting for the agent to clean up. Use
            when the cluster is already gone; in-cluster Astronomer resources
            won&apos;t be uninstalled if the agent is unreachable.
          </span>
        </label>
      </ConfirmDialog>
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/clusters/")({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as { register?: string; status?: string } & Record<string, unknown>,
  component: ClustersPage,
});
