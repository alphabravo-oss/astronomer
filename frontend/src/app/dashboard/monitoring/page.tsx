'use client';

import { useEffect, useState } from 'react';
import { useClusters, useClusterMetrics, useClusterMetricsSummary, useClusterNodes, useClusterNamespaces } from '@/lib/hooks';
import { MetricCard } from '@/components/ui/metric-card';
import { MetricsChart } from '@/components/monitoring/metrics-chart';
import { DataTable, type Column } from '@/components/ui/data-table';
import { formatBytes, formatCPU, formatPercentage, cn } from '@/lib/utils';
import type { ClusterNode, Namespace } from '@/types';
import { ChevronDown } from 'lucide-react';
import {
  Cpu,
  MemoryStick,
  Network,
  HardDrive,
  Box,
  Server,
  Loader2,
} from 'lucide-react';

export default function MonitoringPage() {
  // The global "monitoring" route works against a single cluster, so it
  // carries its own in-page picker. Cluster context lives in the URL for
  // every other view, but this page is reachable from a top-level sidebar
  // link that isn't scoped to a cluster yet — so we default to whichever
  // cluster the API returns first and let the user switch from here.
  const [selectedClusterId, setSelectedClusterId] = useState<string | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [timeRange, setTimeRange] = useState('1h');

  const { data: clustersData } = useClusters({ pageSize: 100 });
  const clusters = clustersData?.data || [];

  // Auto-select first cluster once data arrives so the page is useful on first
  // navigation without forcing the user to open the picker.
  useEffect(() => {
    if (!selectedClusterId && clusters.length > 0) {
      setSelectedClusterId(clusters[0].id);
    }
  }, [selectedClusterId, clusters]);

  const selectedCluster = clusters.find((c) => c.id === selectedClusterId) || null;

  const { data: summary } = useClusterMetricsSummary(selectedClusterId || '');
  const { data: metrics, isLoading: metricsLoading } = useClusterMetrics(selectedClusterId || '', timeRange);
  const { data: nodes } = useClusterNodes(selectedClusterId || '');
  const { data: namespaces } = useClusterNamespaces(selectedClusterId || '');

  const timeRanges = [
    { value: '1h', label: '1H' },
    { value: '6h', label: '6H' },
    { value: '24h', label: '24H' },
    { value: '7d', label: '7D' },
  ];

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

      {/* Summary Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4">
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
        <MetricCard
          title="Network RX"
          value={summary ? formatBytes(summary.networkReceive) : '--'}
          unit="/s"
          icon={<Network className="h-4 w-4" />}
        />
        <MetricCard
          title="Disk Usage"
          value={summary ? formatBytes(summary.diskUsage) : '--'}
          subtitle={summary ? `of ${formatBytes(summary.diskCapacity)}` : undefined}
          icon={<HardDrive className="h-4 w-4" />}
        />
        <MetricCard
          title="Pod Count"
          value={summary ? summary.podCount : '--'}
          subtitle={summary ? `of ${summary.podCapacity} capacity` : undefined}
          icon={<Box className="h-4 w-4" />}
        />
      </div>

      {/* Charts */}
      {metricsLoading ? (
        <div className="flex items-center justify-center h-48">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground mr-2" />
          <span className="text-sm text-muted-foreground">Loading metrics...</span>
        </div>
      ) : metrics ? (
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
      ) : null}

      {/* Node Utilization */}
      <div className="space-y-3">
        <h2 className="text-lg font-medium text-foreground">Node Utilization</h2>
        <DataTable
          data={nodes || []}
          columns={nodeColumns}
          keyExtractor={(row) => row.name}
          searchPlaceholder="Search nodes..."
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
          pageSize={10}
        />
      </div>
    </div>
  );
}
