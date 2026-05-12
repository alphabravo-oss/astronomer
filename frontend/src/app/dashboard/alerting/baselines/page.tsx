'use client';

// Sprint 072 — Anomaly Baseline inspection page.
//
// Read-only. Operators land here to answer "why does my anomaly rule
// keep firing/not firing" — the page surfaces the mean / stddev /
// sample count / last update per (cluster, metric) tuple that the
// recompute worker has materialized.
//
// We deliberately do NOT expose the recent_samples ring buffer here
// — it would tempt the UI into rendering hundreds of points per
// row, blowing up page render time for active baselines.

import { useState } from 'react';
import Link from 'next/link';
import { useAnomalyBaselines } from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { formatRelativeTime } from '@/lib/utils';
import type { AnomalyBaseline } from '@/types';
import { ArrowLeft, Activity, RefreshCw } from 'lucide-react';

export default function AnomalyBaselinesPage() {
  const [clusterFilter, setClusterFilter] = useState('');
  const { data: rows, isLoading, refetch } = useAnomalyBaselines(
    clusterFilter ? { clusterId: clusterFilter } : undefined
  );

  const columns: Column<AnomalyBaseline>[] = [
    {
      key: 'metric',
      header: 'Metric',
      accessor: (b: AnomalyBaseline) => <span className="font-mono text-xs">{b.metric}</span>,
    },
    {
      key: 'clusterId',
      header: 'Cluster',
      accessor: (b: AnomalyBaseline) => (
        <span className="font-mono text-xs text-muted-foreground" title={b.clusterId}>
          {b.clusterId.slice(0, 8)}
        </span>
      ),
    },
    {
      key: 'sampleCount',
      header: 'Samples',
      accessor: (b: AnomalyBaseline) => (
        <span className={b.sampleCount < 50 ? 'text-status-warning' : 'text-foreground'}>
          {b.sampleCount}
        </span>
      ),
    },
    {
      key: 'mean',
      header: 'Mean',
      accessor: (b: AnomalyBaseline) => <span className="font-mono text-xs">{b.mean.toFixed(2)}</span>,
    },
    {
      key: 'stddev',
      header: 'Stddev',
      accessor: (b: AnomalyBaseline) => <span className="font-mono text-xs">{b.stddev.toFixed(2)}</span>,
    },
    {
      key: 'lastValue',
      header: 'Last Value',
      accessor: (b: AnomalyBaseline) => <span className="font-mono text-xs">{b.lastValue.toFixed(2)}</span>,
    },
    {
      key: 'p95',
      header: 'P95',
      accessor: (b: AnomalyBaseline) => <span className="font-mono text-xs">{b.p95.toFixed(2)}</span>,
    },
    {
      key: 'windowSeconds',
      header: 'Window',
      accessor: (b: AnomalyBaseline) => <span className="text-xs text-muted-foreground">{formatWindow(b.windowSeconds)}</span>,
    },
    {
      key: 'updatedAt',
      header: 'Updated',
      accessor: (b: AnomalyBaseline) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(b.updatedAt)}</span>
      ),
    },
  ];

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <Link
            href="/dashboard/alerting"
            className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" />
            Back to Alerting
          </Link>
          <h1 className="text-2xl font-semibold text-foreground mt-1 inline-flex items-center gap-2">
            <Activity className="h-6 w-6" />
            Anomaly Baselines
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Rolling-window statistics per (cluster, metric, window) tuple. Maintained by the
            anomaly:baseline_recompute worker every 5 minutes.
          </p>
        </div>
        <button
          onClick={() => refetch()}
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border bg-background text-sm hover:bg-muted"
        >
          <RefreshCw className="h-4 w-4" />
          Refresh
        </button>
      </div>

      <div className="flex items-center gap-3">
        <label className="text-sm font-medium text-foreground">Filter by cluster ID:</label>
        <input
          type="text"
          value={clusterFilter}
          onChange={(e) => setClusterFilter(e.target.value)}
          placeholder="UUID (leave empty for all)"
          className="flex-1 max-w-md h-9 px-3 rounded-md border border-border bg-background text-sm
            placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
        />
      </div>

      <DataTable
        data={rows ?? []}
        columns={columns}
        keyExtractor={(b) => b.id}
        loading={isLoading}
        emptyMessage="No anomaly baselines have been computed yet. The recompute worker provisions rows for each anomaly rule on its next tick."
      />

      <p className="text-xs text-muted-foreground">
        Tip: a sample count under 50 (highlighted) means the cold-start gate will short-circuit any
        anomaly rule referencing this baseline to no-fire. That&apos;s expected for newly-created
        rules — wait until the window fills.
      </p>
    </div>
  );
}

function formatWindow(s: number): string {
  if (s % 86400 === 0) return `${s / 86400}d`;
  if (s % 3600 === 0) return `${s / 3600}h`;
  if (s % 60 === 0) return `${s / 60}m`;
  return `${s}s`;
}
