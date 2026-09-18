import { useEffect } from "react";
import { Server, TerminalSquare } from "lucide-react";

import { StatePanel } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { useCluster } from "@/lib/hooks/clusters";
import { useParams } from "@tanstack/react-router";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { openClusterShellWindow } from "@/lib/window-manager-store";

export function ClusterShellDeepLink() {
  const params = useParams({ strict: false });
  const clusterId = params?.id as string;
  const clusterQuery = useCluster(clusterId);
  const { data: cluster, isLoading } = clusterQuery;
  const permission = usePermissionDecision("shell", "exec", {
    type: "cluster",
    id: clusterId,
  });
  const clusterName = cluster?.displayName || cluster?.name;
  const isLocal = cluster?.isLocal;

  useEffect(() => {
    if (!clusterName || isLocal || !permission.allowed) return;
    openClusterShellWindow(clusterId, clusterName);
  }, [clusterId, clusterName, isLocal, permission.allowed]);

  if (isLoading || clusterQuery.isError) {
    return (
      <QueryStates
        query={clusterQuery}
        permission="clusters:read"
        notFound={
          <div className="flex h-64 flex-col items-center justify-center text-muted-foreground">
            <Server className="mb-3 h-8 w-8" />
            <p>Cluster not found</p>
          </div>
        }
      >
        {() => null}
      </QueryStates>
    );
  }

  if (!cluster) {
    return (
      <div className="flex h-64 flex-col items-center justify-center text-muted-foreground">
        <Server className="mb-3 h-8 w-8" />
        <p>Cluster not found</p>
      </div>
    );
  }

  if (cluster.isLocal) {
    return (
      <div className="mx-auto flex h-64 max-w-md flex-col items-center justify-center gap-2 text-center text-muted-foreground">
        <TerminalSquare className="mb-2 h-8 w-8" />
        <p className="text-sm font-medium text-foreground">
          Shell isn&apos;t available on the management plane&apos;s own cluster.
        </p>
        <p className="text-xs">
          The kubectl shell flow needs a real remote agent and tunnel. Use
          <code className="mx-1 rounded-sm bg-muted px-1.5 py-0.5 font-mono">
            kubectl exec
          </code>
          directly against this cluster, or register a managed cluster and open
          a shell there.
        </p>
      </div>
    );
  }

  if (!permission.allowed) {
    return (
      <StatePanel
        icon={TerminalSquare}
        tone="warning"
        title="Shell access required"
        description={permission.disabledReason || permission.reason}
      />
    );
  }

  return (
    <StatePanel
      icon={TerminalSquare}
      tone="info"
      title="Cluster shell opened"
      description={`The audited kubectl shell for ${clusterName} is running in the global console drawer, so it stays available while you navigate.`}
      actionLabel="Focus shell"
      onAction={() => openClusterShellWindow(clusterId, clusterName)}
    />
  );
}
