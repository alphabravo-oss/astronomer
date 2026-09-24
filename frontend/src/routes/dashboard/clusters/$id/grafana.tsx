import { createFileRoute } from "@tanstack/react-router";
import { ClusterGrafanaView } from "@/components/monitoring/cluster-grafana-view";
function ClusterGrafanaPage() {
  const { id } = Route.useParams();
  return <ClusterGrafanaView clusterId={id} />;
}
export const Route = createFileRoute("/dashboard/clusters/$id/grafana")({
  component: ClusterGrafanaPage,
});
