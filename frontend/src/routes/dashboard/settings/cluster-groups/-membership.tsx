import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Server } from "lucide-react";

import { ActionButton } from "@/components/ui/action-button";
import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ModalShell } from "@/components/ui/modal-shell";
import { PageSection } from "@/components/ui/page";
import {
  listClustersInGroup,
  moveClustersToGroup,
  type ClusterGroupTreeNode,
} from "@/lib/api/cluster-groups";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";

type ClusterGroupMember = { id: string; name: string };

const memberColumns: Column<ClusterGroupMember>[] = [
  {
    key: "name",
    header: "Cluster",
    accessor: (cluster) => (
      <div className="flex items-center gap-2">
        <Server className="h-4 w-4 text-muted-foreground" />
        <span className="font-medium">{cluster.name}</span>
      </div>
    ),
    sortAccessor: (cluster) => cluster.name,
  },
  {
    key: "id",
    header: "Cluster ID",
    accessor: (cluster) => (
      <span className="font-mono text-xs text-muted-foreground">
        {cluster.id}
      </span>
    ),
  },
];

export function ClusterGroupMembership({
  group,
}: {
  group: ClusterGroupTreeNode;
}) {
  const [assigning, setAssigning] = useState(false);
  const members = useQuery({
    queryKey: queryKeys.clusterGroups.members(group.id),
    queryFn: ({ signal }) => listClustersInGroup(group.id, { signal }),
  });

  return (
    <PageSection
      title={group.name}
      description={
        group.description ||
        "Clusters in this group and its child groups. Subtree totals include every nested group."
      }
      actions={
        <ActionButton
          type="button"
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() => setAssigning(true)}
        >
          Assign clusters
        </ActionButton>
      }
    >
      <div className="mb-3 flex flex-wrap gap-4 text-sm text-muted-foreground">
        <span>{group.clusterCount} direct members</span>
        <span>{group.clusterCountTree} members in subtree</span>
        <span className="font-mono">{group.slug}</span>
      </div>
      <DataTable
        data={members.data ?? []}
        columns={memberColumns}
        keyExtractor={(cluster) => cluster.id}
        loading={members.isLoading}
        isError={members.isError}
        error={members.error}
        permission="clusters:update"
        onRetry={() => void members.refetch()}
        emptyState={{
          title: "No clusters in this group",
          description:
            "Assign an adopted cluster to this group to manage its placement.",
        }}
        persistKey="cluster-group-members"
      />
      {assigning && (
        <ClusterMembershipDialog
          group={group}
          memberIds={new Set((members.data ?? []).map((member) => member.id))}
          onClose={() => setAssigning(false)}
        />
      )}
    </PageSection>
  );
}

function ClusterMembershipDialog({
  group,
  memberIds,
  onClose,
}: {
  group: ClusterGroupTreeNode;
  memberIds: Set<string>;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const mutation = useMutation({
    mutationFn: () => moveClustersToGroup(group.id, [...selected]),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.clusterGroups.all,
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.clusterGroups.members(group.id),
      });
      toastSuccess(
        `${result.moved} cluster${result.moved === 1 ? "" : "s"} assigned`,
      );
      onClose();
    },
    onError: (error: Error) =>
      toastApiError("Failed to assign clusters", error),
  });

  return (
    <ModalShell
      title={`Assign clusters to ${group.name}`}
      subtitle="Moving a cluster updates its direct group membership. Rollout previews explain the resulting placement before launch."
      size="lg"
      onClose={onClose}
      footer={
        <div className="flex justify-end gap-2">
          <ActionButton type="button" intent="ghost" onClick={onClose}>
            Cancel
          </ActionButton>
          <ActionButton
            type="button"
            intent="primary"
            disabled={selected.size === 0}
            loading={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            Assign {selected.size || "selected"}
          </ActionButton>
        </div>
      }
    >
      <RemoteClusterPicker
        value=""
        onChange={(id) => setSelected((current) => new Set([...current, id]))}
        excludedClusterIds={[...memberIds, ...selected]}
        ariaLabel="Cluster to assign"
        disabled={mutation.isPending}
      />
      <ul className="mt-4 max-h-64 space-y-2 overflow-auto">
        {[...selected].map((id) => (
          <li key={id} className="flex items-center justify-between gap-2">
            <span className="font-mono text-xs">{id}</span>
            <ActionButton
              disabled={mutation.isPending}
              aria-label={`Remove ${id} from selection`}
              onClick={() =>
                setSelected((current) => {
                  const next = new Set(current);
                  next.delete(id);
                  return next;
                })
              }
            >
              Remove
            </ActionButton>
          </li>
        ))}
      </ul>
    </ModalShell>
  );
}
