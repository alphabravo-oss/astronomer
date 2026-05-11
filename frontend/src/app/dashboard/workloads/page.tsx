'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { useWorkloads, useClusters, useClusterNamespaces, useScaleWorkload, useRestartWorkload } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { useClusterStore } from '@/lib/store';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { Workload } from '@/types';
import { Boxes, RotateCcw, Scale, Loader2, X, Server } from 'lucide-react';

export default function WorkloadsPage() {
  const router = useRouter();
  const { selectedClusterId, selectedCluster } = useClusterStore();
  const [namespace, setNamespace] = useState<string>('');
  const [kindFilter, setKindFilter] = useState<string>('');
  const [scaleTarget, setScaleTarget] = useState<Workload | null>(null);
  const [scaleReplicas, setScaleReplicas] = useState(1);

  const { data: clustersData } = useClusters({ pageSize: 50 });
  const { data: namespacesData } = useClusterNamespaces(selectedClusterId || '');
  const { data: workloadsData, isLoading } = useWorkloads(selectedClusterId || '', {
    namespace: namespace || undefined,
    kind: kindFilter || undefined,
    pageSize: 200,
  });

  const scaleWorkload = useScaleWorkload();
  const restartWorkload = useRestartWorkload();

  const workloads = workloadsData?.data || [];
  const namespaces = namespacesData || [];

  // Refetch the workloads list whenever the selected cluster signals an
  // informer-level change. cluster.k8s_changed is the placeholder until the
  // agent grows per-kind events; cluster.connected/disconnected handles the
  // case where the agent reconnects with new state.
  useLiveQueryInvalidation(
    [
      'cluster.k8s_changed',
      'cluster.connected',
      'cluster.disconnected',
      'cluster.heartbeat',
    ],
    selectedClusterId
      ? [['workloads', selectedClusterId], ['clusters', selectedClusterId]]
      : [['workloads']],
  );

  const handleScale = async () => {
    if (!scaleTarget || !selectedClusterId) return;
    await scaleWorkload.mutateAsync({
      clusterId: selectedClusterId,
      kind: scaleTarget.kind,
      namespace: scaleTarget.namespace,
      name: scaleTarget.name,
      replicas: scaleReplicas,
    });
    setScaleTarget(null);
  };

  const handleRestart = async (workload: Workload) => {
    if (!selectedClusterId) return;
    await restartWorkload.mutateAsync({
      clusterId: selectedClusterId,
      kind: workload.kind,
      namespace: workload.namespace,
      name: workload.name,
    });
  };

  const columns: Column<Workload>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          <p className="text-xs text-muted-foreground">{row.clusterName}</p>
        </div>
      ),
      sortAccessor: (row) => row.name,
    },
    {
      key: 'kind',
      header: 'Kind',
      accessor: (row) => (
        <span className="px-2 py-0.5 rounded text-xs font-medium bg-muted text-muted-foreground">
          {row.kind}
        </span>
      ),
      sortAccessor: (row) => row.kind,
    },
    {
      key: 'namespace',
      header: 'Namespace',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">{row.namespace}</span>
      ),
    },
    {
      key: 'ready',
      header: 'Ready',
      accessor: (row) => {
        const [current, desired] = row.ready.split('/').map(Number);
        const isReady = current === desired && current > 0;
        return (
          <span className={cn('text-sm font-mono tabular-nums', isReady ? 'text-status-success' : 'text-status-warning')}>
            {row.ready}
          </span>
        );
      },
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'age',
      header: 'Age',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{row.age || formatRelativeTime(row.createdAt)}</span>
      ),
    },
    {
      key: 'actions',
      header: 'Actions',
      sortable: false,
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          {(row.kind === 'Deployment' || row.kind === 'StatefulSet' || row.kind === 'ReplicaSet') && (
            <button
              onClick={() => {
                setScaleTarget(row);
                setScaleReplicas(row.desiredReplicas);
              }}
              className="inline-flex items-center justify-center h-7 w-7 rounded text-muted-foreground
                hover:text-foreground hover:bg-accent transition-colors"
              title="Scale"
            >
              <Scale className="h-3.5 w-3.5" />
            </button>
          )}
          {(row.kind === 'Deployment' || row.kind === 'StatefulSet' || row.kind === 'DaemonSet') && (
            <button
              onClick={() => handleRestart(row)}
              className="inline-flex items-center justify-center h-7 w-7 rounded text-muted-foreground
                hover:text-foreground hover:bg-accent transition-colors"
              title="Restart"
            >
              <RotateCcw className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Workloads</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Manage deployments, statefulsets, and other workloads across your clusters
        </p>
      </div>

      {/* Cluster selection notice */}
      {!selectedClusterId && (
        <div className="flex items-center gap-3 p-4 rounded-lg border border-border bg-card">
          <Server className="h-5 w-5 text-muted-foreground flex-shrink-0" />
          <p className="text-sm text-muted-foreground">
            Select a cluster from the top bar to view its workloads.
          </p>
        </div>
      )}

      {selectedClusterId && (
        <DataTable
          data={workloads}
          columns={columns}
          keyExtractor={(row) => `${row.kind}/${row.namespace}/${row.name}`}
          onRowClick={(row) =>
            router.push(
              `/dashboard/workloads/${row.kind.toLowerCase()}/${row.namespace}/${row.name}`
            )
          }
          searchPlaceholder="Search workloads..."
          loading={isLoading}
          emptyMessage="No workloads found in this cluster"
          toolbar={
            <div className="flex items-center gap-2">
              <select
                value={namespace}
                onChange={(e) => setNamespace(e.target.value)}
                className="h-9 px-3 rounded-md border border-border bg-background text-sm
                  text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              >
                <option value="">All Namespaces</option>
                {namespaces.map((ns) => (
                  <option key={ns.name} value={ns.name}>
                    {ns.name}
                  </option>
                ))}
              </select>

              <select
                value={kindFilter}
                onChange={(e) => setKindFilter(e.target.value)}
                className="h-9 px-3 rounded-md border border-border bg-background text-sm
                  text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              >
                <option value="">All Kinds</option>
                <option value="Deployment">Deployment</option>
                <option value="StatefulSet">StatefulSet</option>
                <option value="DaemonSet">DaemonSet</option>
                <option value="Job">Job</option>
                <option value="CronJob">CronJob</option>
              </select>
            </div>
          }
        />
      )}

      {/* Scale Modal */}
      {scaleTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={() => setScaleTarget(null)} />
          <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl p-6 space-y-5">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-semibold text-foreground">Scale Workload</h3>
              <button
                onClick={() => setScaleTarget(null)}
                className="text-muted-foreground hover:text-foreground transition-colors"
              >
                <X className="h-5 w-5" />
              </button>
            </div>

            <div className="space-y-1">
              <p className="text-sm text-muted-foreground">
                Scaling <span className="text-foreground font-medium">{scaleTarget.name}</span> in{' '}
                <span className="font-mono text-xs">{scaleTarget.namespace}</span>
              </p>
              <p className="text-xs text-muted-foreground">
                Current replicas: {scaleTarget.desiredReplicas}
              </p>
            </div>

            <div className="space-y-3">
              <label className="text-sm font-medium text-foreground">Replicas</label>
              <div className="flex items-center gap-4">
                <input
                  type="range"
                  min="0"
                  max="20"
                  value={scaleReplicas}
                  onChange={(e) => setScaleReplicas(Number(e.target.value))}
                  className="flex-1 accent-primary"
                />
                <input
                  type="number"
                  min="0"
                  max="100"
                  value={scaleReplicas}
                  onChange={(e) => setScaleReplicas(Number(e.target.value))}
                  className="w-16 h-9 px-2 text-center rounded-md border border-border bg-background text-sm
                    tabular-nums focus:outline-none focus:ring-1 focus:ring-ring"
                />
              </div>
              {scaleReplicas === 0 && (
                <p className="text-xs text-status-warning">
                  Setting replicas to 0 will scale down all pods for this workload.
                </p>
              )}
            </div>

            <div className="flex justify-end gap-2 pt-2">
              <button
                onClick={() => setScaleTarget(null)}
                className="h-9 px-4 rounded-lg border border-border text-sm font-medium
                  text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={handleScale}
                disabled={scaleWorkload.isPending}
                className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                  text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
              >
                {scaleWorkload.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                Scale to {scaleReplicas}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
