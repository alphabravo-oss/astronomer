import { createFileRoute } from "@tanstack/react-router";

import { useCluster } from "@/lib/hooks/clusters";
import { ToolsTab } from "@/components/clusters/tools-tab";
import { PageHeader, PageShell } from "@/components/ui/page";
import { StatePanel } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { Server } from "lucide-react";

function ClusterToolsPage() {
  const params = Route.useParams();
  const clusterId = params.id;
  const clusterQuery = useCluster(clusterId);

  if (
    clusterQuery.isLoading ||
    clusterQuery.isError ||
    clusterQuery.data === undefined
  ) {
    return (
      <QueryStates
        query={clusterQuery}
        loadingTitle="Loading cluster tools"
        permission="clusters:read"
        errorTitle="Failed to load cluster tools"
        notFound={
          <StatePanel
            icon={Server}
            title="Cluster not found"
            description="The cluster may have been removed or is outside your access scope."
          />
        }
      >
        {null}
      </QueryStates>
    );
  }
  const cluster = clusterQuery.data;

  return (
    <PageShell>
      <PageHeader
        title="Tools"
        description={`Manage operational tools for ${cluster.displayName}`}
      />
      <ToolsTab
        clusterId={clusterId}
        clusterEnvironment={cluster.environment}
        clusterStatus={cluster.status}
      />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/tools/")({
  component: ClusterToolsPage,
});
