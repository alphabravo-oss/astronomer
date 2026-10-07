import { createFileRoute } from "@tanstack/react-router";
/**
 * Cluster Registries tab — private image-pull credentials, per cluster.
 *
 * Each registry materialises as a docker-registry Secret in the namespaces
 * the user picks (or every project namespace if the list is empty). The
 * "test" action exercises the registry through the agent tunnel so we
 * surface auth failures before they reach a Pod.
 */

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Container, Plus, Server } from "lucide-react";
import { queryKeys } from "@/lib/query-keys";
import { useCluster } from "@/lib/hooks/clusters";
import { useClustersUpdate } from "@/lib/permission-hooks";
import {
  listClusterRegistries,
  type ClusterRegistry,
} from "@/lib/api/cluster-registries";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  EmptyState,
  StatePanel,
  type EmptyStateActionProps,
} from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { liveFallback } from "@/lib/live/status-store";
import { RegistriesTable } from "./-registries-table";
import { RegistryDialog } from "./-registry-dialog";
import { useRegistryActions } from "./-use-registry-actions";

/** Read-only viewers can't add a registry, so the empty state has no action. */
function registriesEmptyAction(
  canWrite: boolean,
  onAdd: () => void,
): EmptyStateActionProps | { terminal: true } {
  return canWrite
    ? { actionLabel: "Add registry", actionIcon: Plus, onAction: onAdd }
    : { terminal: true };
}

function ClusterRegistriesPage() {
  const params = Route.useParams();
  const clusterId = params.id;
  const { canWrite, reason } = useClustersUpdate(clusterId);

  const clusterQuery = useCluster(clusterId);
  const registriesQuery = useQuery({
    queryKey: queryKeys.clusterPages.registries(clusterId),
    queryFn: () => listClusterRegistries(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30000),
    refetchIntervalInBackground: false,
  });

  const [newOpen, setNewOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<ClusterRegistry | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ClusterRegistry | null>(
    null,
  );

  const { testStatus, deleteMutation, testMutation } = useRegistryActions(
    clusterId,
    () => setDeleteTarget(null),
  );

  if (
    clusterQuery.isLoading ||
    clusterQuery.isError ||
    clusterQuery.data === undefined
  ) {
    return (
      <QueryStates
        query={clusterQuery}
        loadingTitle="Loading cluster"
        permission="clusters:read"
        errorTitle="Failed to load cluster"
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
        title="Registries"
        description={`Private image-pull credentials reconciled into namespaces on ${cluster.displayName}.`}
        actions={
          <ActionButton
            disabledReason={canWrite ? undefined : reason}
            intent="primary"
            icon={<Plus className="h-3.5 w-3.5" />}
            onClick={() => canWrite && setNewOpen(true)}
            disabled={!canWrite}
          >
            New Registry
          </ActionButton>
        }
      />

      <QueryStates
        query={registriesQuery}
        loadingTitle="Loading registries"
        permission="clusters:read"
        errorTitle="Failed to load registries"
        isEmpty={(rows) => rows.length === 0}
        empty={
          <EmptyState
            icon={Container}
            title="No private registries configured"
            description="Add a registry to reconcile image-pull credentials into namespaces on this cluster."
            {...registriesEmptyAction(canWrite, () => setNewOpen(true))}
          />
        }
      >
        {(registries) => (
          <RegistriesTable
            registries={registries}
            canWrite={canWrite}
            reason={reason}
            testStatus={testStatus}
            testPending={testMutation.isPending}
            onTest={(id) => testMutation.mutate(id)}
            onEdit={setEditTarget}
            onDelete={setDeleteTarget}
          />
        )}
      </QueryStates>

      {newOpen && (
        <RegistryDialog
          clusterId={clusterId}
          onClose={() => setNewOpen(false)}
        />
      )}
      {editTarget && (
        <RegistryDialog
          clusterId={clusterId}
          existing={editTarget}
          onClose={() => setEditTarget(null)}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title="Remove registry"
        description={
          deleteTarget
            ? `Delete the registry binding for "${deleteTarget.registryUrl}"? The associated docker-registry Secrets will also be removed from the cluster.`
            : ""
        }
        confirmText="Delete"
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/registries/")({
  component: ClusterRegistriesPage,
});
