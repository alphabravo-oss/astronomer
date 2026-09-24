import { clusterColumns } from "./-columns";
import { Input } from "@/components/ui/input";
import { useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import { useNavigate } from "@tanstack/react-router";
import { useClusters, useDeleteCluster } from "@/lib/hooks/clusters";
import { queryKeys } from "@/lib/query-keys";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { useSearchParam } from "@/lib/use-search-param";
import { DataTable } from "@/components/ui/data-table";
import { ActionButton } from "@/components/ui/action-button";
import { Select } from "@/components/ui/select";
import { PageHeader, PageShell } from "@/components/ui/page";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { EditClusterModal } from "@/components/clusters/edit-cluster-modal";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import type { Cluster } from "@/types";
import { Plus, SearchX, Server } from "lucide-react";
import { pageTableCount } from "@/lib/api/pagination";

const CLUSTERS_PAGE_SIZE = 50;

export function ClustersPage() {
  const navigate = useNavigate();
  const [statusFilter, setStatusFilter] = useSearchParam("status");
  const [providerFilter, setProviderFilter] = useState<string>("");
  const [envFilter, setEnvFilter] = useState<string>("");
  const [pageIndex, setPageIndex] = useState(0);
  const [search, setSearch] = useSearchParam("q", { debounceMs: 250 });
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

  const columns = clusterColumns(navigate, setEditCluster, setDeleteTarget);
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
          isError={clustersQuery.isError}
          error={clustersQuery.error}
          errorMessage="Failed to load clusters."
          permission="clusters:read"
          onRetry={() => void clustersQuery.refetch()}
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
            ...pageTableCount(clustersQuery.data),
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
