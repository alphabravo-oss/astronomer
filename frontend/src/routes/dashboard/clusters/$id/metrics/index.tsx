import { createFileRoute } from "@tanstack/react-router";

import { ClusterGrafanaView } from "@/components/monitoring/cluster-grafana-view";

function ClusterMetricsRoute() {
  const params = Route.useParams();
  return <ClusterGrafanaView clusterId={params.id} view="metrics" />;
}

export const Route = createFileRoute("/dashboard/clusters/$id/metrics/")({
  component: ClusterMetricsRoute,
});
