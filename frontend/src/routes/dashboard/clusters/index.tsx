import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { useRouter, useSearchParams } from '@/lib/navigation';
import { useClusters, useDeleteCluster, queryKeys } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
// RegisterClusterModal removed in sprint 22 — replaced by the
// /dashboard/clusters/register/* wizard. The "Re-show install command"
// row action now navigates to the wizard's step 2 for the existing
// cluster, which is the moral equivalent.
import { EditClusterModal } from '@/components/clusters/edit-cluster-modal';
import { ActionMenu } from '@/components/ui/action-menu';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import {
  formatRelativeTime,
  formatPercentage,
  providerDisplayName,
  distributionDisplayName,
} from '@/lib/utils';
import type { Cluster } from '@/types';
import { Plus, Terminal, Pencil, Trash2 } from 'lucide-react';

function ClustersPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  // Legacy ?register=true query param redirects to the new wizard
  // entry route. We do the redirect inside useEffect so deep-linked
  // bookmarks keep working without flashing the cluster list.
  const legacyRegisterParam = searchParams.get('register') === 'true';
  useEffect(() => {
    if (legacyRegisterParam) {
      router.replace('/dashboard/clusters/register');
    }
  }, [legacyRegisterParam, router]);
  const [statusFilter, setStatusFilter] = useState<string>('');
  const [providerFilter, setProviderFilter] = useState<string>('');
  const [envFilter, setEnvFilter] = useState<string>('');

  // Action menu state
  // Sprint 22 removed the legacy register-cluster modal; the
  // "Registration Command" action now navigates to the wizard's
  // step-2 install page for the cluster. Setter retained as a no-op
  // ref to keep the column callback site untouched while the
  // navigation handler does the heavy lift.
  const [editCluster, setEditCluster] = useState<Cluster | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Cluster | null>(null);
  const [forceDelete, setForceDelete] = useState(false);
  const deleteMutation = useDeleteCluster();

  const { data: clustersData, isLoading } = useClusters({
    status: statusFilter || undefined,
    provider: providerFilter || undefined,
    environment: envFilter || undefined,
    pageSize: 100,
  });

  // Live updates: shape-changing events trigger a list refetch; per-row
  // metric ticks are merged in place by the layout's metrics merger.
  useLiveQueryInvalidation(
    [
      'cluster.connected',
      'cluster.disconnected',
      'cluster.created',
      'cluster.updated',
      'cluster.deleted',
      'cluster.status_changed',
      'cluster.heartbeat',
      'agent.reconnecting',
      'agent.failed',
    ],
    [queryKeys.clusters.all],
  );

  const clusters = clustersData?.data || [];

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteMutation.mutateAsync({ id: deleteTarget.id, force: forceDelete });
      setDeleteTarget(null);
    } catch {
      // Error handled by mutation
    }
  };

  const columns: Column<Cluster>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.displayName}</p>
          <p className="text-xs text-muted-foreground">{row.name}</p>
        </div>
      ),
      sortAccessor: (row) => row.displayName,
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) =>
        row.decommissioning ? (
          <StatusBadge status="decommissioning" label="Decommissioning" pulse />
        ) : (
          <StatusBadge status={row.status} />
        ),
      sortAccessor: (row) => (row.decommissioning ? 'decommissioning' : row.status),
    },
    {
      key: 'provider',
      header: 'Provider',
      accessor: (row) => (
        <span className="text-muted-foreground">{providerDisplayName(row.provider)}</span>
      ),
      sortAccessor: (row) => row.provider,
      filter: { label: 'Provider' },
    },
    {
      key: 'distribution',
      header: 'Distribution',
      accessor: (row) => (
        <span className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">
          {distributionDisplayName(row.distribution)}
        </span>
      ),
      sortAccessor: (row) => row.distribution,
    },
    {
      key: 'version',
      header: 'K8s Version',
      accessor: (row) => <span className="font-mono text-xs text-muted-foreground">{row.kubernetesVersion}</span>,
    },
    {
      key: 'nodes',
      header: 'Nodes',
      accessor: (row) => <span className="tabular-nums">{row.nodeCount}</span>,
      sortAccessor: (row) => row.nodeCount,
      align: 'center',
    },
    {
      key: 'pods',
      header: 'Pods',
      accessor: (row) => <span className="tabular-nums">{row.podCount}</span>,
      sortAccessor: (row) => row.podCount,
      align: 'center',
    },
    {
      key: 'cpu',
      header: 'CPU%',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={`gauge-bar-fill ${
                row.cpuPercentage >= 90
                  ? 'bg-status-error'
                  : row.cpuPercentage >= 75
                    ? 'bg-status-warning'
                    : 'bg-status-success'
              }`}
              style={{ width: `${Math.min(row.cpuPercentage, 100)}%` }}
            />
          </div>
          <span className="text-xs tabular-nums text-muted-foreground w-10">
            {formatPercentage(row.cpuPercentage, row.cpuPercentage < 10 ? 1 : 0)}
          </span>
        </div>
      ),
      sortAccessor: (row) => row.cpuPercentage,
    },
    {
      key: 'mem',
      header: 'Mem%',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={`gauge-bar-fill ${
                row.memoryPercentage >= 90
                  ? 'bg-status-error'
                  : row.memoryPercentage >= 75
                    ? 'bg-status-warning'
                    : 'bg-status-success'
              }`}
              style={{ width: `${Math.min(row.memoryPercentage, 100)}%` }}
            />
          </div>
          <span className="text-xs tabular-nums text-muted-foreground w-10">
            {formatPercentage(row.memoryPercentage, row.memoryPercentage < 10 ? 1 : 0)}
          </span>
        </div>
      ),
      sortAccessor: (row) => row.memoryPercentage,
    },
    {
      key: 'heartbeat',
      header: 'Last Heartbeat',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.lastHeartbeat)}</span>
      ),
      sortAccessor: (row) => row.lastHeartbeat,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: 'Registration Command',
              icon: <Terminal className="h-3.5 w-3.5" />,
              onClick: () => router.push(`/dashboard/clusters/register/${row.id}/connect`),
            },
            {
              label: 'Edit',
              icon: <Pencil className="h-3.5 w-3.5" />,
              onClick: () => setEditCluster(row),
            },
            {
              label: 'Delete',
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteTarget(row),
              variant: 'destructive',
              separator: true,
            },
          ]}
        />
      ),
      align: 'center',
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Clusters</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Monitor and manage the existing Kubernetes clusters you&apos;ve registered with Astronomer
          </p>
        </div>
        <button
          onClick={() => router.push('/dashboard/clusters/register')}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          Register Cluster
        </button>
      </div>

      {/* Filters */}
      <DataTable
        data={clusters}
        columns={columns}
        keyExtractor={(row) => row.id}
        persistKey="clusters"
        onRowClick={(row) => router.push(`/dashboard/clusters/${row.id}`)}
        searchPlaceholder="Search clusters..."
        loading={isLoading}
        emptyMessage="No clusters found. Register your first cluster to get started."
        toolbar={
          <div className="flex items-center gap-2">
            <select
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value)}
              className="h-9 px-3 rounded-md border border-border bg-background text-sm
                text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">All Statuses</option>
              <option value="active">Active</option>
              <option value="warning">Warning</option>
              <option value="error">Error</option>
              <option value="disconnected">Disconnected</option>
              <option value="connecting">Connecting</option>
            </select>

            <select
              value={providerFilter}
              onChange={(e) => setProviderFilter(e.target.value)}
              className="h-9 px-3 rounded-md border border-border bg-background text-sm
                text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">All Providers</option>
              <option value="aws">AWS</option>
              <option value="gcp">GCP</option>
              <option value="azure">Azure</option>
              <option value="on-prem">On-Premise</option>
              <option value="digitalocean">DigitalOcean</option>
            </select>

            <select
              value={envFilter}
              onChange={(e) => setEnvFilter(e.target.value)}
              className="h-9 px-3 rounded-md border border-border bg-background text-sm
                text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">All Environments</option>
              <option value="production">Production</option>
              <option value="staging">Staging</option>
              <option value="development">Development</option>
              <option value="testing">Testing</option>
            </select>
          </div>
        }
      />

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
          <input
            type="checkbox"
            checked={forceDelete}
            onChange={(e) => setForceDelete(e.target.checked)}
            className="mt-0.5 h-3.5 w-3.5 rounded border-border"
          />
          <span>
            <span className="font-medium text-foreground">Force delete</span> — remove immediately
            instead of waiting for the agent to clean up. Use when the cluster is already gone;
            in-cluster Astronomer resources won&apos;t be uninstalled if the agent is unreachable.
          </span>
        </label>
      </ConfirmDialog>
    </div>
  );
}

export const Route = createFileRoute('/dashboard/clusters/')({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as { register?: string } & Record<string, unknown>,
  component: ClustersPage,
});
