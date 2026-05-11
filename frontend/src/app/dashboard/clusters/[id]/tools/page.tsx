'use client';

import { useParams } from 'next/navigation';
import { useCluster } from '@/lib/hooks';
import { ToolsTab } from '@/components/clusters/tools-tab';
import { Loader2, Server } from 'lucide-react';

export default function ClusterToolsPage() {
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
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Tools</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Manage operational tools for {cluster.displayName}
        </p>
      </div>

      {/* Tools Grid */}
      <ToolsTab clusterId={clusterId} clusterEnvironment={cluster.environment} clusterStatus={cluster.status} />
    </div>
  );
}
