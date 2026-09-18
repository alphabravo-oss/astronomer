import { createFileRoute } from "@tanstack/react-router";


import { ClusterMonitoringStackPage } from "@/components/monitoring/cluster-stack-page";

function ClusterMonitoringStackRoute() {
  const params = Route.useParams();
  return <ClusterMonitoringStackPage clusterId={params.id} />;
}

export const Route = createFileRoute(
  "/dashboard/clusters/$id/monitoring-stack/",
)({
  component: ClusterMonitoringStackRoute,
});
