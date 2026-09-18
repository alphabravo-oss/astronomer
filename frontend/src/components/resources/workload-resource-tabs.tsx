import { useState } from "react";
import { Box } from "lucide-react";

import { MetricsChart } from "@/components/monitoring/metrics-chart";
import { Link as RouterLink } from "@tanstack/react-router";
import { StatusBadge } from "@/components/ui/status-badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { PodLogsViewer } from "@/components/workloads/pod-logs-viewer";
import { useWorkloadMetrics, useWorkloadPods } from "@/lib/hooks/workloads";
import { detailHref } from "@/lib/k8s-paths";
import { useNavigate } from "@tanstack/react-router";
import { cn, formatRelativeTime } from "@/lib/utils";
import type { Pod } from "@/types";

export type WorkloadResourceTabId =
  "workload-pods" | "workload-logs" | "workload-metrics";

interface WorkloadTabProps {
  clusterId: string;
  resourceType: string;
  namespace: string;
  name: string;
}

export function WorkloadResourceTabPanel({
  tab,
  ...props
}: WorkloadTabProps & { tab: WorkloadResourceTabId }) {
  switch (tab) {
    case "workload-pods":
      return <WorkloadPodsTab {...props} />;
    case "workload-logs":
      return <WorkloadLogsTab {...props} />;
    case "workload-metrics":
      return <ResourceMetricsTab {...props} />;
  }
}

function WorkloadPodsTab({
  clusterId,
  resourceType,
  namespace,
  name,
}: WorkloadTabProps) {
  const navigate = useNavigate();
  const query = useWorkloadPods(clusterId, resourceType, namespace, name);
  const columns: Column<Pod>[] = [
    {
      key: "name",
      header: "Name",
      accessor: (pod) => (
        <RouterLink
          to={detailHref(
            clusterId,
            "pods",
            pod.namespace ?? namespace,
            pod.name,
          )}
          onClick={(event) => event.stopPropagation()}
          className="font-mono text-xs text-foreground hover:underline"
        >
          {pod.name}
        </RouterLink>
      ),
    },
    {
      key: "status",
      header: "Status",
      accessor: (pod) => <StatusBadge status={pod.phase} />,
    },
    {
      key: "ready",
      header: "Ready",
      accessor: (pod) => (
        <span className="font-mono text-xs tabular-nums">{pod.ready}</span>
      ),
    },
    {
      key: "restarts",
      header: "Restarts",
      accessor: (pod) => (
        <span
          className={cn(
            "text-xs tabular-nums",
            pod.restarts > 0 ? "text-status-warning" : "text-muted-foreground",
          )}
        >
          {pod.restarts}
        </span>
      ),
      sortAccessor: (pod) => pod.restarts,
      align: "center",
    },
    {
      key: "node",
      header: "Node",
      accessor: (pod) => (
        <span className="font-mono text-xs text-muted-foreground">
          {pod.node || "—"}
        </span>
      ),
    },
    {
      key: "age",
      header: "Age",
      accessor: (pod) => (
        <span className="text-xs text-muted-foreground">
          {pod.age || formatRelativeTime(pod.createdAt)}
        </span>
      ),
    },
  ];

  return (
    <QueryStates
      query={query}
      loadingTitle="Loading workload pods"
      permission="pods:read"
      errorTitle="Failed to load workload pods"
      isEmpty={(pods) => pods.length === 0}
      empty={
        <EmptyState
          icon={Box}
          title="No pods for this workload"
          description="This workload has not created any pods yet. Check its conditions and desired replica count."
        />
      }
    >
      {(pods) => (
        <DataTable
          data={pods}
          columns={columns}
          keyExtractor={(pod) => pod.name}
          searchPlaceholder="Search pods..."
          emptyState={{
            title: "No pods available",
            description:
              "Resources will appear here when they are available in this scope.",
          }}
          onRowClick={(pod) =>
            void navigate({
              to: detailHref(
                clusterId,
                "pods",
                pod.namespace ?? namespace,
                pod.name,
              ),
            })
          }
        />
      )}
    </QueryStates>
  );
}

function WorkloadLogsTab(props: WorkloadTabProps) {
  const [selectedPod, setSelectedPod] = useState("");
  const query = useWorkloadPods(
    props.clusterId,
    props.resourceType,
    props.namespace,
    props.name,
  );

  return (
    <QueryStates
      query={query}
      loadingTitle="Loading workload pods"
      permission="pods:read"
      errorTitle="Failed to load workload pods"
      isEmpty={(pods) => pods.length === 0}
      empty={
        <EmptyState
          icon={Box}
          title="No pods available for logs"
          description="Logs become available after this workload creates a pod."
        />
      }
    >
      {(pods) => (
        <PodLogsViewer
          clusterId={props.clusterId}
          namespace={props.namespace}
          pods={pods}
          selectedPod={selectedPod}
          onPodChange={setSelectedPod}
        />
      )}
    </QueryStates>
  );
}

export function ResourceMetricsTab(props: WorkloadTabProps) {
  const [range, setRange] = useState("1h");
  const query = useWorkloadMetrics(
    props.clusterId,
    props.resourceType,
    props.namespace,
    props.name,
    range,
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <span className="text-sm text-muted-foreground">Time range:</span>
        {["1h", "6h", "24h", "7d"].map((value) => (
          <button
            key={value}
            type="button"
            onClick={() => setRange(value)}
            className={cn(
              "rounded-md px-2.5 py-1 text-xs font-medium transition-colors",
              range === value
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-accent hover:text-foreground",
            )}
          >
            {value}
          </button>
        ))}
      </div>
      <QueryStates
        query={query}
        loadingTitle="Loading resource metrics"
        permission="monitoring:read"
        errorTitle="Failed to load resource metrics"
      >
        {(metrics) => (
          <div className="space-y-4">
            {metrics.available === false ? (
              <div className="rounded-lg border border-status-warning/30 bg-status-warning/5 px-4 py-3 text-xs text-muted-foreground">
                Time-series monitoring is not configured for this cluster.
                Install or connect the monitoring stack to populate these
                charts.
              </div>
            ) : null}
            <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
              <MetricsChart
                title="CPU usage and limit"
                series={[metrics.cpuUsage, metrics.cpuCapacity]}
                unit="cores"
              />
              <MetricsChart
                title="Memory usage and limit"
                series={[metrics.memoryUsage, metrics.memoryCapacity]}
                unit="bytes"
              />
              <MetricsChart
                title="Network throughput"
                series={[metrics.networkReceive, metrics.networkTransmit]}
                unit="bytes/s"
              />
              <MetricsChart
                title="Filesystem usage"
                series={[metrics.diskUsage]}
                unit="bytes"
              />
            </div>
          </div>
        )}
      </QueryStates>
    </div>
  );
}
