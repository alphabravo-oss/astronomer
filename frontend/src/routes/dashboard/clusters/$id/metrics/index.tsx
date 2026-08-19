import { createFileRoute } from '@tanstack/react-router';
import { useParams } from '@/lib/navigation';
import { ClusterMetricsPage } from '@/components/monitoring/cluster-metrics-page';

function ClusterMetricsRoute() {
  const params = useParams();
  return <ClusterMetricsPage clusterId={params.id as string} />;
}

export const Route = createFileRoute('/dashboard/clusters/$id/metrics/')({
  component: ClusterMetricsRoute,
});
