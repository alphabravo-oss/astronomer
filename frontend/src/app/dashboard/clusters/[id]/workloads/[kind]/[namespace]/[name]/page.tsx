'use client';

import { useState } from 'react';
import { useParams, useRouter } from '@/lib/navigation';
import { detailHref } from '@/lib/k8s-paths';
import { useWorkload, useWorkloadPods, useWorkloadMetrics } from '@/lib/hooks';
import { StatusBadge } from '@/components/ui/status-badge';
import { DataTable, type Column } from '@/components/ui/data-table';
import { PodLogsViewer } from '@/components/workloads/pod-logs-viewer';
import { MetricsChart } from '@/components/monitoring/metrics-chart';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { Pod } from '@/types';
import { ArrowLeft, Box, Loader2 } from 'lucide-react';
import { Link } from '@/lib/link';

type TabKey = 'pods' | 'logs' | 'metrics';

export default function WorkloadDetailPage() {
  const params = useParams();
  const router = useRouter();
  const clusterId = params.id as string;
  const kind = params.kind as string;
  const namespace = params.namespace as string;
  const name = params.name as string;

  const [activeTab, setActiveTab] = useState<TabKey>('pods');
  const [selectedPod, setSelectedPod] = useState<string>('');
  const [metricsRange, setMetricsRange] = useState('1h');

  const { data: workload, isLoading: workloadLoading } = useWorkload(clusterId, kind, namespace, name);
  const { data: pods, isLoading: podsLoading } = useWorkloadPods(clusterId, kind, namespace, name);
  const { data: metrics } = useWorkloadMetrics(clusterId, kind, namespace, name, metricsRange);

  const tabs: { key: TabKey; label: string }[] = [
    { key: 'pods', label: `Pods (${pods?.length || 0})` },
    { key: 'logs', label: 'Logs' },
    { key: 'metrics', label: 'Metrics' },
  ];

  const podColumns: Column<Pod>[] = [
    {
      key: 'name',
      header: 'Name',
      // Name links into the generic pod detail (open-in-new-tab friendly);
      // stopPropagation so it doesn't double-fire the row click.
      accessor: (row) => (
        <Link
          href={detailHref(clusterId, 'pods', row.namespace ?? namespace, row.name)}
          onClick={(e) => e.stopPropagation()}
          className="font-mono text-xs text-foreground hover:underline"
        >
          {row.name}
        </Link>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={row.phase} />,
    },
    {
      key: 'ready',
      header: 'Ready',
      accessor: (row) => <span className="font-mono text-xs tabular-nums">{row.ready}</span>,
    },
    {
      key: 'restarts',
      header: 'Restarts',
      accessor: (row) => (
        <span className={cn('tabular-nums text-xs', row.restarts > 0 ? 'text-status-warning' : 'text-muted-foreground')}>
          {row.restarts}
        </span>
      ),
      sortAccessor: (row) => row.restarts,
      align: 'center',
    },
    {
      key: 'node',
      header: 'Node',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">{row.node}</span>
      ),
    },
    {
      key: 'ip',
      header: 'IP',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">{row.ip}</span>
      ),
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
      header: '',
      sortable: false,
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            setSelectedPod(row.name);
            setActiveTab('logs');
          }}
          className="text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          View Logs
        </button>
      ),
    },
  ];

  if (workloadLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!workload) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Box className="h-8 w-8 mb-3" />
        <p>Workload not found</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <Link
          href={`/dashboard/clusters/${clusterId}/${kind.toLowerCase()}s`}
          className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-colors mb-4"
        >
          <ArrowLeft className="h-4 w-4" />
          {kind}s
        </Link>
        <div className="flex items-start justify-between">
          <div className="space-y-1">
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-semibold text-foreground tracking-tight">{workload.name}</h1>
              <StatusBadge status={workload.status} size="lg" />
            </div>
            <div className="flex items-center gap-3 text-sm text-muted-foreground">
              <span className="px-2 py-0.5 rounded bg-muted text-xs font-medium">{workload.kind}</span>
              <span>Namespace: <span className="font-mono">{workload.namespace}</span></span>
              <span className="text-border">|</span>
              <span>Cluster: {workload.clusterName}</span>
            </div>
          </div>
          <div className="flex items-center gap-3 text-sm">
            <span className="text-muted-foreground">
              Ready: <span className={cn('font-mono font-medium', workload.status === 'Running' ? 'text-status-success' : 'text-status-warning')}>{workload.ready}</span>
            </span>
          </div>
        </div>
      </div>

      {/* Images */}
      {workload.images && workload.images.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {workload.images.map((image) => (
            <span
              key={image}
              className="px-2.5 py-1 rounded-md bg-muted text-xs font-mono text-muted-foreground"
            >
              {image}
            </span>
          ))}
        </div>
      )}

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex gap-6">
          {tabs.map((tab) => (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={cn(
                'pb-3 text-sm font-medium border-b-2 transition-colors',
                activeTab === tab.key
                  ? 'border-foreground text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              )}
            >
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {/* Tab Content */}
      {activeTab === 'pods' && (
        <div className="animate-fade-in">
          <DataTable
            data={pods || []}
            columns={podColumns}
            keyExtractor={(row) => row.name}
            searchPlaceholder="Search pods..."
            loading={podsLoading}
            emptyMessage="No pods found"
            onRowClick={(row) =>
              router.push(detailHref(clusterId, 'pods', row.namespace ?? namespace, row.name))
            }
          />
        </div>
      )}

      {activeTab === 'logs' && (
        <div className="animate-fade-in">
          <PodLogsViewer
            clusterId={clusterId}
            namespace={namespace}
            pods={pods || []}
            selectedPod={selectedPod}
            onPodChange={setSelectedPod}
          />
        </div>
      )}

      {activeTab === 'metrics' && (
        <div className="space-y-6 animate-fade-in">
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Time Range:</span>
            {['1h', '6h', '24h', '7d'].map((range) => (
              <button
                key={range}
                onClick={() => setMetricsRange(range)}
                className={cn(
                  'px-2.5 py-1 rounded-md text-xs font-medium transition-colors',
                  metricsRange === range
                    ? 'bg-primary text-primary-foreground'
                    : 'text-muted-foreground hover:text-foreground hover:bg-accent'
                )}
              >
                {range}
              </button>
            ))}
          </div>

          {metrics ? (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
              <MetricsChart
                title="CPU Usage"
                series={[metrics.cpuUsage]}
                unit="millicores"
              />
              <MetricsChart
                title="Memory Usage"
                series={[metrics.memoryUsage]}
                unit="bytes"
              />
            </div>
          ) : (
            <div className="flex items-center justify-center h-48 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin mr-2" />
              Loading metrics...
            </div>
          )}
        </div>
      )}
    </div>
  );
}
