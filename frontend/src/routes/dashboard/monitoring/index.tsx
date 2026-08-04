import { createFileRoute } from '@tanstack/react-router';

import { useMemo, useState } from 'react';
import { useClusters, useClusterMetrics, useClusterMetricsSummary, useClusterNodes, useClusterNamespaces } from '@/lib/hooks';
import { useTabParam } from '@/lib/use-tab-param';
import { useRollingMetrics } from '@/lib/use-rolling-metrics';
import { Link } from '@/lib/link';
import { MetricCard } from '@/components/ui/metric-card';
import { MetricsChart } from '@/components/monitoring/metrics-chart';
import { DataTable, type Column } from '@/components/ui/data-table';
import { formatBytes, formatCPU, formatPercentage, cn } from '@/lib/utils';
import type { ClusterNode, Namespace } from '@/types';
import { ChevronDown, LineChart, ArrowRight } from 'lucide-react';
import {
  Cpu,
  MemoryStick,
  Network,
  HardDrive,
  Box,
  Server,
  Loader2,
} from 'lucide-react';

function MonitoringPage() {
  // The global "monitoring" route works against a single cluster via an in-page
  // picker. Both the selected cluster and the time range are deep-linked into
  // the URL (?cluster=<id>&range=<r>) so the view is shareable/refresh-stable —
  // matching how every other filter/tab in the app persists to the URL.
  const [pickerOpen, setPickerOpen] = useState(false);

  const { data: clustersData } = useClusters({ pageSize: 100 });
  const clusters = useMemo(() => clustersData?.data ?? [], [clustersData]);

  const clusterIds = useMemo(() => clusters.map((c) => c.id), [clusters]);
  // fallback = first cluster once the list resolves (empty string until then,
  // which disables the metrics queries below via their `enabled: !!clusterId`).
  const [selectedClusterId, setSelectedClusterId] = useTabParam(clusterIds, clusters[0]?.id ?? '', 'cluster');

  const timeRanges = [
    { value: '1h', label: '1H' },
    { value: '6h', label: '6H' },
    { value: '24h', label: '24H' },
    { value: '7d', label: '7D' },
  ];
  const [timeRange, setTimeRange] = useTabParam(
    timeRanges.map((r) => r.value),
    '1h',
    'range',
  );

  const selectedCluster = clusters.find((c) => c.id === selectedClusterId) || null;

  const { data: summary } = useClusterMetricsSummary(selectedClusterId || '');
  const { data: metrics, isLoading: metricsLoading } = useClusterMetrics(selectedClusterId || '', timeRange);
  const { data: nodes, isLoading: nodesLoading, isError: nodesError, refetch: refetchNodes } = useClusterNodes(selectedClusterId || '');
  const { data: namespaces, isLoading: namespacesLoading, isError: namespacesError, refetch: refetchNamespaces } = useClusterNamespaces(selectedClusterId || '');

  // A per-cluster Prometheus/Thanos backend unlocks stored history + disk/network.
  // Without it we still have live CPU/mem/pods from metrics-server, which we
  // accumulate into a rolling window so the charts are useful out of the box.
  const hasProm = metrics?.available === true;
  const rolling = useRollingMetrics(selectedClusterId || '', summary);

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

  if (!selectedClusterId) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Monitoring</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Cluster resource metrics and utilization
          </p>
        </div>
        <div className="flex items-center gap-3 p-4 rounded-lg border border-border bg-card">
          <Server className="h-5 w-5 text-muted-foreground flex-shrink-0" />
          <p className="text-sm text-muted-foreground">
            {clusters.length === 0
              ? 'No clusters registered yet. Register a cluster to view monitoring data.'
              : 'Loading clusters...'}
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Monitoring</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Real-time resource metrics and utilization
          </p>
        </div>
        <div className="flex items-center gap-2">
          {/* Cluster picker (in-page; this is the only route that isn't already
              cluster-scoped via the URL slug). */}
          <div className="relative">
            <button
              onClick={() => setPickerOpen((o) => !o)}
              onBlur={() => setTimeout(() => setPickerOpen(false), 150)}
              className="inline-flex items-center gap-2 h-8 px-3 rounded-md border border-border text-sm
                text-foreground hover:bg-accent transition-colors"
            >
              <Server className="h-3.5 w-3.5 text-muted-foreground" />
              <span className="max-w-[160px] truncate">
                {selectedCluster?.displayName || 'Select cluster'}
              </span>
              <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
            </button>
            {pickerOpen && clusters.length > 0 && (
              <div className="absolute right-0 top-full mt-1 w-64 rounded-lg border border-border bg-popover shadow-xl z-20 overflow-hidden">
                <div className="max-h-72 overflow-y-auto p-1">
                  {clusters.map((c) => (
                    <button
                      key={c.id}
                      onMouseDown={(e) => {
                        e.preventDefault();
                        setSelectedClusterId(c.id);
                        setPickerOpen(false);
                      }}
                      className={cn(
                        'w-full flex items-center gap-2 px-3 py-2 rounded-md text-sm text-left transition-colors',
                        selectedClusterId === c.id
                          ? 'bg-accent text-foreground'
                          : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                      )}
                    >
                      <Server className="h-3.5 w-3.5 flex-shrink-0" />
                      <span className="truncate">{c.displayName}</span>
                    </button>
                  ))}
                </div>
              </div>
            )}
          </div>
          <div className="flex items-center gap-1 rounded-lg border border-border p-0.5">
            {timeRanges.map((range) => (
              <button
                key={range.value}
                onClick={() => setTimeRange(range.value)}
                className={cn(
                  'px-3 py-1.5 rounded-md text-xs font-medium transition-colors',
                  timeRange === range.value
                    ? 'bg-primary text-primary-foreground'
                    : 'text-muted-foreground hover:text-foreground'
                )}
              >
                {range.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* Summary Cards — CPU/Mem/Pods come from metrics-server (always live).
          Disk/Network need node-exporter scraped by Prometheus, so they only
          appear once the per-cluster stack is installed (hasProm). */}
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

      {/* Charts */}
      {metricsLoading && !hasProm && rolling.count === 0 ? (
        <div className="flex items-center justify-center h-48">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground mr-2" />
          <span className="text-sm text-muted-foreground">Loading metrics...</span>
        </div>
      ) : hasProm && metrics ? (
        // Full history + disk/network from the per-cluster Prometheus/Thanos stack.
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
        // No Prometheus backend: draw a live rolling window from the summary poll
        // (CPU/mem/pods) so the page is useful immediately, and point operators to
        // the install flow for disk/network + durable long-term history.
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
            {selectedCluster && (
              <Link
                href={`/dashboard/clusters/${selectedCluster.id}/monitoring-stack`}
                className="inline-flex flex-shrink-0 items-center gap-1.5 h-8 px-3 rounded-md border border-border text-xs font-medium hover:bg-accent transition-colors"
              >
                Set up monitoring stack
                <ArrowRight className="h-3.5 w-3.5" />
              </Link>
            )}
          </div>
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            <MetricsChart title="CPU Usage (live)" series={[rolling.cpu]} unit="%" />
            <MetricsChart title="Memory Usage (live)" series={[rolling.mem]} unit="%" />
            <MetricsChart title="Pod Count (live)" series={[rolling.pods]} unit="" />
          </div>
        </div>
      )}

      {/* Node Utilization */}
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

      {/* Namespace Utilization */}
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
    </div>
  );
}

export const Route = createFileRoute('/dashboard/monitoring/')({
  component: MonitoringPage,
});
