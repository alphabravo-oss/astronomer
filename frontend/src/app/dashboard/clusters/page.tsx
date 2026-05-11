'use client';

import { useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { useClusters, useDeleteCluster, queryKeys } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { RegisterClusterModal } from '@/components/clusters/register-cluster-modal';
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

export default function ClustersPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [showRegister, setShowRegister] = useState(searchParams.get('register') === 'true');
  const [statusFilter, setStatusFilter] = useState<string>('');
  const [providerFilter, setProviderFilter] = useState<string>('');
  const [envFilter, setEnvFilter] = useState<string>('');

  // Action menu state
  const [registerCluster, setRegisterCluster] = useState<Cluster | null>(null);
  const [editCluster, setEditCluster] = useState<Cluster | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Cluster | null>(null);
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
      await deleteMutation.mutateAsync(deleteTarget.id);
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
      accessor: (row) => <StatusBadge status={row.status} />,
      sortAccessor: (row) => row.status,
    },
    {
      key: 'provider',
      header: 'Provider',
      accessor: (row) => (
        <span className="text-muted-foreground">{providerDisplayName(row.provider)}</span>
      ),
      sortAccessor: (row) => row.provider,
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
              onClick: () => setRegisterCluster(row),
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
            Manage and monitor your registered Kubernetes clusters
          </p>
        </div>
        <button
          onClick={() => setShowRegister(true)}
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

      {/* Register Modal (new cluster) */}
      {showRegister && (
        <RegisterClusterModal onClose={() => setShowRegister(false)} />
      )}

      {/* Registration Command Modal (existing cluster) */}
      {registerCluster && (
        <RegisterClusterModal
          onClose={() => setRegisterCluster(null)}
          clusterId={registerCluster.id}
          clusterName={registerCluster.name}
        />
      )}

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
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        title="Delete Cluster"
        description={`This will remove the cluster "${deleteTarget?.displayName}" from Astronomer. The underlying Kubernetes cluster will not be destroyed.`}
        confirmText="Delete"
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  );
}
