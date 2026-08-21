import { createFileRoute } from '@tanstack/react-router';
import { useParams } from '@/lib/navigation';
import { useCluster } from '@/lib/hooks';
import { ToolsTab } from '@/components/clusters/tools-tab';
import { PageHeader, PageShell } from '@/components/ui/page';
import { Loader2, Server } from 'lucide-react';

function ClusterToolsPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const { data: cluster, isLoading } = useCluster(clusterId);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!cluster) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Cluster not found</p>
      </div>
    );
  }

  return (
    <PageShell>
      <PageHeader
        title="Tools"
        description={`Manage operational tools for ${cluster.displayName}`}
      />
      <ToolsTab clusterId={clusterId} clusterEnvironment={cluster.environment} clusterStatus={cluster.status} />
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/tools/')({
  component: ClusterToolsPage,
});
