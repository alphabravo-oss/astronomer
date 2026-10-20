import { Box, Cpu, MemoryStick, Server } from "lucide-react";
import { MetricCard } from "@/components/ui/metric-card";
import type { useClusterMetricsSummary } from "@/lib/hooks/workloads";
import { formatBytes, formatCPU } from "@/lib/utils";
import type { Cluster } from "@/types";
import { usageCardView } from "./-cluster-status";
import { useClusterInventoryCounts } from "@/components/layout/use-sidebar-resource-counts";

/**
 * Overview metric cards. When neither the live summary nor the cached cluster
 * row has a usage/percentage value the card shows "No data" with no number;
 * when only a percentage exists it shows the sample time instead.
 */
export function ClusterUsageCards({
  clusterId,
  cluster,
  metricsSummary,
  metricsError,
}: {
  clusterId: string;
  cluster: Cluster;
  metricsSummary: ReturnType<typeof useClusterMetricsSummary>["data"];
  metricsError: boolean;
}) {
  // Nodes/Pods read from the same inventory counts the Inventory section
  // (and formerly the sidebar) uses, so the KPI can never contradict them.
  const inventory = useClusterInventoryCounts(clusterId);
  const nodeCount =
    inventory.nodes ?? metricsSummary?.nodeCount ?? cluster.nodeCount ?? null;
  const podCount =
    inventory.pods ?? metricsSummary?.podCount ?? cluster.podCount ?? null;
  const cpuPct = metricsSummary?.cpuPercentage ?? cluster.cpuPercentage ?? null;
  const cpuUsage = metricsSummary?.cpuUsage ?? cluster.cpuUsage ?? null;
  const cpuCap = metricsSummary?.cpuCapacity ?? cluster.cpuCapacity ?? null;
  const memPct =
    metricsSummary?.memoryPercentage ?? cluster.memoryPercentage ?? null;
  const memUsage = metricsSummary?.memoryUsage ?? cluster.memoryUsage ?? null;
  const memCap =
    metricsSummary?.memoryCapacity ?? cluster.memoryCapacity ?? null;
  const cpuView = usageCardView({
    percentage: cpuPct,
    usage: cpuUsage,
    capacity: cpuCap,
    format: formatCPU,
    sampledAt: cluster.lastHeartbeat,
  });
  const memView = usageCardView({
    percentage: memPct,
    usage: memUsage,
    capacity: memCap,
    format: formatBytes,
    sampledAt: cluster.lastHeartbeat,
  });
  return (
    <div className="space-y-2">
      {/* Metrics are non-optional: the panel always renders. When the
            summary query errors (e.g. Prometheus unreachable) we surface a
            "metrics unavailable" banner rather than hiding the cards, so the
            distinction between "no data" and "couldn't reach metrics" is
            visible to operators. */}
      {metricsError ? (
        <p
          data-testid="metrics-unavailable"
          className="text-sm text-muted-foreground"
        >
          Metrics unavailable
        </p>
      ) : null}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <MetricCard
          title="CPU usage"
          href={`/dashboard/clusters/${clusterId}/metrics`}
          value={cpuView.value}
          percentage={cpuView.percentage}
          subtitle={cpuView.subtitle}
          icon={<Cpu className="h-4 w-4" />}
        />
        <MetricCard
          title="Memory usage"
          href={`/dashboard/clusters/${clusterId}/metrics`}
          value={memView.value}
          percentage={memView.percentage}
          subtitle={memView.subtitle}
          icon={<MemoryStick className="h-4 w-4" />}
        />
        <MetricCard
          title="Nodes"
          href={`/dashboard/clusters/${clusterId}/nodes`}
          value={nodeCount ?? "—"}
          icon={<Server className="h-4 w-4" />}
        />
        <MetricCard
          title="Pods"
          href={`/dashboard/clusters/${clusterId}/pods`}
          value={podCount ?? "—"}
          subtitle={
            metricsSummary && Number.isFinite(metricsSummary.podCapacity)
              ? `of ${metricsSummary.podCapacity} capacity`
              : undefined
          }
          icon={<Box className="h-4 w-4" />}
        />
      </div>
    </div>
  );
}
