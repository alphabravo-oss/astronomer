import { createFileRoute } from "@tanstack/react-router";

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

import { Link as RouterLink } from "@tanstack/react-router";
import { useAnomalyBaselines } from "@/lib/hooks/alerting";
import { useSearchParam } from "@/lib/use-search-param";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { PageHeader, PageShell } from "@/components/ui/page";
import { NumberCell, TimestampCell } from "@/components/ui/cell-primitives";
import type { AnomalyBaseline } from "@/types";
import { ArrowLeft, Activity, RefreshCw } from "lucide-react";
import { Tooltip } from "@/components/ui/tooltip";

function AnomalyBaselinesPage() {
  const [clusterFilter, setClusterFilter] = useSearchParam("cluster");
  const {
    data: rows,
    isLoading,
    refetch,
  } = useAnomalyBaselines(
    clusterFilter ? { clusterId: clusterFilter } : undefined,
  );

  const columns: Column<AnomalyBaseline>[] = [
    {
      key: "metric",
      header: "Metric",
      kind: "name",
      minSize: 240,
      accessor: (b: AnomalyBaseline) => (
        <span className="font-mono text-xs">{b.metric}</span>
      ),
      sortAccessor: (b: AnomalyBaseline) => b.metric,
    },
    {
      key: "clusterId",
      header: "Cluster",
      kind: "text",
      size: 128,
      minSize: 112,
      accessor: (b: AnomalyBaseline) => (
        <Tooltip content={b.clusterId}>
          <span className="font-mono text-xs text-muted-foreground">
            {b.clusterId.slice(0, 8)}
          </span>
        </Tooltip>
      ),
    },
    {
      key: "sampleCount",
      header: "Samples",
      kind: "count",
      accessor: (b: AnomalyBaseline) => (
        <span
          className={
            b.sampleCount < 50 ? "text-status-warning" : "text-foreground"
          }
        >
          {b.sampleCount}
        </span>
      ),
    },
    {
      key: "mean",
      header: "Mean",
      kind: "count",
      accessor: (b: AnomalyBaseline) => <NumberCell value={b.mean} />,
      sortAccessor: (b: AnomalyBaseline) => b.mean,
    },
    {
      key: "stddev",
      header: "Stddev",
      kind: "count",
      accessor: (b: AnomalyBaseline) => <NumberCell value={b.stddev} />,
      sortAccessor: (b: AnomalyBaseline) => b.stddev,
    },
    {
      key: "lastValue",
      header: "Last Value",
      kind: "count",
      size: 96,
      accessor: (b: AnomalyBaseline) => <NumberCell value={b.lastValue} />,
      sortAccessor: (b: AnomalyBaseline) => b.lastValue,
    },
    {
      key: "p95",
      header: "P95",
      kind: "count",
      accessor: (b: AnomalyBaseline) => <NumberCell value={b.p95} />,
      sortAccessor: (b: AnomalyBaseline) => b.p95,
    },
    {
      key: "windowSeconds",
      header: "Window",
      kind: "text",
      minSize: 96,
      size: 96,
      accessor: (b: AnomalyBaseline) => (
        <span className="text-xs text-muted-foreground">
          {formatWindow(b.windowSeconds)}
        </span>
      ),
    },
    {
      key: "updatedAt",
      header: "Updated",
      kind: "age",
      accessor: (b: AnomalyBaseline) => <TimestampCell value={b.updatedAt} />,
    },
  ];

  return (
    <PageShell>
      <PageHeader
        eyebrow={
          <RouterLink
            to="/dashboard/alerting"
            className="inline-flex items-center gap-1.5 hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" />
            Back to Alerting
          </RouterLink>
        }
        title={
          <span className="inline-flex items-center gap-2">
            <Activity className="h-6 w-6" />
            Anomaly Baselines
          </span>
        }
        description="Rolling-window statistics per (cluster, metric, window) tuple. Maintained by the anomaly:baseline_recompute worker every 5 minutes."
        actions={
          <ActionButton
            icon={<RefreshCw className="h-4 w-4" />}
            onClick={() => refetch()}
          >
            Refresh
          </ActionButton>
        }
      />

      <div className="flex items-center gap-3">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-6538f696-116"
        >
          Filter by cluster ID:
        </label>
        <Input
          id="field-6538f696-116"
          value={clusterFilter}
          onChange={(e) => setClusterFilter(e.target.value)}
          placeholder="UUID (leave empty for all)"
          className="max-w-md flex-1"
        />
      </div>

      <DataTable
        data={rows ?? []}
        columns={columns}
        keyExtractor={(b) => b.id}
        loading={isLoading}
        emptyState={{
          title: "No anomaly baselines have been computed yet",
          description:
            "The recompute worker provisions rows for each anomaly rule on its next tick.",
        }}
      />

      <p className="text-xs text-muted-foreground">
        Tip: a sample count under 50 (highlighted) means the cold-start gate
        will short-circuit any anomaly rule referencing this baseline to
        no-fire. That&apos;s expected for newly-created rules — wait until the
        window fills.
      </p>
    </PageShell>
  );
}

function formatWindow(s: number): string {
  if (s % 86400 === 0) return `${s / 86400}d`;
  if (s % 3600 === 0) return `${s / 3600}h`;
  if (s % 60 === 0) return `${s / 60}m`;
  return `${s}s`;
}

export const Route = createFileRoute("/dashboard/alerting/baselines/")({
  validateSearch: (search: Record<string, unknown>) =>
    search as { cluster?: string } & Record<string, unknown>,
  component: AnomalyBaselinesPage,
});
