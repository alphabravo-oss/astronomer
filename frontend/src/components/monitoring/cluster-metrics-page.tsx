import { useCluster, useClusterMetrics, useClusterMetricsSummary, useClusterNodes, useClusterNamespaces } from '@/lib/hooks';
import { useTabParam } from '@/lib/use-tab-param';
import { useRollingMetrics } from '@/lib/use-rolling-metrics';
import { Link } from '@/lib/link';
import { MetricCard } from '@/components/ui/metric-card';
import { PageHeader, PageShell } from '@/components/ui/page';
import { MetricsChart } from '@/components/monitoring/metrics-chart';
import { DataTable, type Column } from '@/components/ui/data-table';
import { formatBytes, formatCPU, formatPercentage, cn } from '@/lib/utils';
import type { ClusterNode, Namespace } from '@/types';
import { LineChart, ArrowRight } from 'lucide-react';
import {
  Cpu,
  MemoryStick,
  Network,
  HardDrive,
  Box,
  Loader2,
} from 'lucide-react';

const timeRanges = [
  { value: '1h', label: '1H' },
  { value: '6h', label: '6H' },
  { value: '24h', label: '24H' },
  { value: '7d', label: '7D' },
] as const;

export function ClusterMetricsPage({ clusterId }: { clusterId: string }) {
  const [timeRange, setTimeRange] = useTabParam(
    timeRanges.map((r) => r.value),
    '1h',
    'range',
  );

  const { data: cluster } = useCluster(clusterId);
  const { data: summary } = useClusterMetricsSummary(clusterId);
  const { data: metrics, isLoading: metricsLoading } = useClusterMetrics(clusterId, timeRange);
  const { data: nodes, isLoading: nodesLoading, isError: nodesError, refetch: refetchNodes } = useClusterNodes(clusterId);
  const { data: namespaces, isLoading: namespacesLoading, isError: namespacesError, refetch: refetchNamespaces } = useClusterNamespaces(clusterId);

  const hasProm = metrics?.available === true;
  const rolling = useRollingMetrics(clusterId, summary);

  const nodeColumns: Column<ClusterNode>[] = [
    {
      key: 'name',
      header: 'Node',
      accessor: (row) => <span className="font-mono text-xs text-foreground">{row.name}</span>,
    },
    {
      key: 'cpu',
      header: 'CPU',
      accessor: (row) => {
        const pct = row.cpuCapacity > 0 ? (row.cpuUsage / row.cpuCapacity) * 100 : 0;
        return (
          <div className="flex items-center gap-2">
            <div className="w-20 gauge-bar">
              <div
                className={cn('gauge-bar-fill', pct >= 90 ? 'bg-status-error' : pct >= 75 ? 'bg-status-warning' : 'bg-status-success')}
                style={{ width: `${Math.min(pct, 100)}%` }}
              />
            </div>
            <span className="text-xs text-muted-foreground tabular-nums w-10">{formatPercentage(pct, 0)}</span>
          </div>
        );
      },
      sortAccessor: (row) => row.cpuUsage / Math.max(row.cpuCapacity, 1),
    },
    {
      key: 'memory',
      header: 'Memory',
      accessor: (row) => {
        const pct = row.memoryCapacity > 0 ? (row.memoryUsage / row.memoryCapacity) * 100 : 0;
        return (
          <div className="flex items-center gap-2">
            <div className="w-20 gauge-bar">
              <div
                className={cn('gauge-bar-fill', pct >= 90 ? 'bg-status-error' : pct >= 75 ? 'bg-status-warning' : 'bg-status-success')}
                style={{ width: `${Math.min(pct, 100)}%` }}
              />
            </div>
            <span className="text-xs text-muted-foreground tabular-nums w-10">{formatPercentage(pct, 0)}</span>
          </div>
        );
      },
      sortAccessor: (row) => row.memoryUsage / Math.max(row.memoryCapacity, 1),
    },
    {
      key: 'pods',
      header: 'Pods',
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">{row.podCount}/{row.podCapacity}</span>
      ),
      sortAccessor: (row) => row.podCount,
      align: 'center',
    },
  ];

  const nsColumns: Column<Namespace>[] = [
    {
      key: 'name',
      header: 'Namespace',
      accessor: (row) => <span className="font-mono text-xs text-foreground">{row.name}</span>,
    },
    {
      key: 'pods',
      header: 'Pods',
      accessor: (row) => <span className="tabular-nums text-xs">{row.podCount}</span>,
      sortAccessor: (row) => row.podCount,
      align: 'center',
    },
    {
      key: 'cpu',
      header: 'CPU Usage',
      accessor: (row) => <span className="text-xs tabular-nums text-muted-foreground">{formatCPU(row.cpuUsage)}</span>,
      sortAccessor: (row) => row.cpuUsage,
    },
    {
      key: 'memory',
      header: 'Memory Usage',
      accessor: (row) => <span className="text-xs tabular-nums text-muted-foreground">{formatBytes(row.memoryUsage)}</span>,
      sortAccessor: (row) => row.memoryUsage,
    },
  ];

  return (
    <PageShell>
      <PageHeader
        title="Metrics"
        description={`Resource utilization for ${cluster?.displayName || cluster?.name || 'this cluster'}`}
        actions={
          <div className="flex items-center gap-2">
            <div className="flex items-center gap-1 rounded-lg border border-border p-0.5">
              {timeRanges.map((range) => (
                <button
                  key={range.value}
                  onClick={() => setTimeRange(range.value)}
                  className={cn(
                    'px-3 py-1.5 rounded-md text-xs font-medium transition-colors',
                    timeRange === range.value
                      ? 'bg-primary text-primary-foreground'
                      : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {range.label}
                </button>
              ))}
            </div>
          </div>
        }
      />

      <div className={cn('grid grid-cols-1 sm:grid-cols-2 gap-4', hasProm ? 'lg:grid-cols-5' : 'lg:grid-cols-3')}>
        <MetricCard
          title="CPU Usage"
          value={summary ? formatPercentage(summary.cpuPercentage) : '--'}
          percentage={summary?.cpuPercentage}
          subtitle={summary ? `${formatCPU(summary.cpuUsage)} / ${formatCPU(summary.cpuCapacity)}` : undefined}
          icon={<Cpu className="h-4 w-4" />}
        />
        <MetricCard
          title="Memory Usage"
          value={summary ? formatPercentage(summary.memoryPercentage) : '--'}
          percentage={summary?.memoryPercentage}
          subtitle={summary ? `${formatBytes(summary.memoryUsage)} / ${formatBytes(summary.memoryCapacity)}` : undefined}
          icon={<MemoryStick className="h-4 w-4" />}
        />
        {hasProm && (
          <MetricCard
            title="Network RX"
            value={summary ? formatBytes(summary.networkReceive) : '--'}
            unit="/s"
            icon={<Network className="h-4 w-4" />}
          />
        )}
        {hasProm && (
          <MetricCard
            title="Disk Usage"
            value={summary ? formatBytes(summary.diskUsage) : '--'}
            subtitle={summary ? `of ${formatBytes(summary.diskCapacity)}` : undefined}
            icon={<HardDrive className="h-4 w-4" />}
          />
        )}
        <MetricCard
          title="Pod Count"
          value={summary ? summary.podCount : '--'}
          subtitle={summary ? `of ${summary.podCapacity} capacity` : undefined}
          icon={<Box className="h-4 w-4" />}
        />
      </div>

      {metricsLoading && !hasProm && rolling.count === 0 ? (
        <div className="flex items-center justify-center h-48">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground mr-2" />
          <span className="text-sm text-muted-foreground">Loading metrics...</span>
        </div>
      ) : hasProm && metrics ? (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <MetricsChart
            title="CPU Usage"
            series={[metrics.cpuUsage, metrics.cpuCapacity]}
            unit="millicores"
          />
          <MetricsChart
            title="Memory Usage"
            series={[metrics.memoryUsage, metrics.memoryCapacity]}
            unit="bytes"
          />
          <MetricsChart
            title="Network I/O"
            series={[metrics.networkReceive, metrics.networkTransmit]}
            unit="bytes/s"
          />
          <MetricsChart
            title="Pod Count"
            series={[metrics.podCount]}
            unit=""
          />
        </div>
      ) : (
        <div className="space-y-4">
          <div className="flex flex-col gap-3 rounded-lg border border-border bg-muted/40 p-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-start gap-2 text-xs text-muted-foreground">
              <LineChart className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
              <p>
                Live rolling window from metrics-server (CPU, memory, pods; this session only).
                Install the per-cluster Prometheus stack to add <strong>disk & network</strong> and
                keep <strong>long-term history</strong>.
              </p>
            </div>
            <Link
              href={`/dashboard/clusters/${clusterId}/monitoring-stack`}
              className="inline-flex flex-shrink-0 items-center gap-1.5 h-8 px-3 rounded-md border border-border text-xs font-medium hover:bg-accent transition-colors"
            >
              Set up monitoring stack
              <ArrowRight className="h-3.5 w-3.5" />
            </Link>
          </div>
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            <MetricsChart title="CPU Usage (live)" series={[rolling.cpu]} unit="%" />
            <MetricsChart title="Memory Usage (live)" series={[rolling.mem]} unit="%" />
            <MetricsChart title="Pod Count (live)" series={[rolling.pods]} unit="" />
          </div>
        </div>
      )}

      <div className="space-y-3">
        <h2 className="text-lg font-medium text-foreground">Node Utilization</h2>
        <DataTable
          data={nodes || []}
          columns={nodeColumns}
          keyExtractor={(row) => row.name}
          searchPlaceholder="Search nodes..."
          loading={nodesLoading}
          isError={nodesError}
          onRetry={() => refetchNodes()}
          pageSize={10}
        />
      </div>

      <div className="space-y-3">
        <h2 className="text-lg font-medium text-foreground">Namespace Utilization</h2>
        <DataTable
          data={namespaces || []}
          columns={nsColumns}
          keyExtractor={(row) => row.name}
          searchPlaceholder="Search namespaces..."
          loading={namespacesLoading}
          isError={namespacesError}
          onRetry={() => refetchNamespaces()}
          pageSize={10}
        />
      </div>
    </PageShell>
  );
}
