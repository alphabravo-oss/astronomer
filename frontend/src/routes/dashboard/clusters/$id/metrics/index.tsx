import { createFileRoute } from "@tanstack/react-router";

import { ClusterMetricsPage } from "@/components/monitoring/cluster-metrics-page";

function ClusterMetricsRoute() {
  const params = Route.useParams();
  return <ClusterMetricsPage clusterId={params.id} />;
}

export const Route = createFileRoute("/dashboard/clusters/$id/metrics/")({
  component: ClusterMetricsRoute,
});
