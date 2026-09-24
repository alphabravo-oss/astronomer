import { createFileRoute } from "@tanstack/react-router";

import { ClusterMetricsWorkspace } from "./-page";

function ClusterMetricsRoute() {
  const params = Route.useParams();
  return <ClusterMetricsWorkspace clusterId={params.id} />;
}

export const Route = createFileRoute("/dashboard/clusters/$id/metrics/")({
  component: ClusterMetricsRoute,
});
