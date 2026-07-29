import { createFileRoute } from '@tanstack/react-router';
import { useParams } from '@/lib/navigation';

import { ClusterMonitoringStackPage } from '@/components/monitoring/cluster-stack-page';

function ClusterMonitoringStackRoute() {
  const params = useParams();
  return <ClusterMonitoringStackPage clusterId={params.id as string} />;
}

export const Route = createFileRoute('/dashboard/clusters/$id/monitoring-stack/')({
  component: ClusterMonitoringStackRoute,
});
