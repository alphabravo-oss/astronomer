import { useCallback, useMemo, useState } from "react";
import { useClusterNodes, useNodeOperation } from "@/lib/hooks/clusters";
import { useNavigate } from "@tanstack/react-router";
import { ResourceActionMenu } from "./resource-action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import type { Column } from "@/components/ui/data-table";
import { ExplorerDataTable } from "@/components/resources/explorer-data-table";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { nodeColumns } from "@/components/resources/resource-list-columns";
import { nodeDrainImpact } from "@/components/resources/resource-deletion-impact";
import { useClusterResourcePermissions } from "@/components/resources/resource-action-policy";
import { k8sResourcePath } from "@/lib/k8s-paths";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import type { ClusterNode } from "@/types";
import { Code, ShieldBan, ShieldCheck, Unplug } from "lucide-react";
import { toastApiError, toastSuccess, toastWarning } from "@/lib/toast";
import { OperationPartialError } from "@/lib/api/operation-polling";

export function NodesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useClusterNodes(clusterId);
  const navigate = useNavigate();
  const nodeOperation = useNodeOperation();
  const permissions = useClusterResourcePermissions(clusterId, "nodes");
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [drainTarget, setDrainTarget] = useState<ClusterNode | null>(null);

  const handleCordon = useCallback(
    (node: ClusterNode) => {
      if (!permissions.update.allowed) {
        toastPermissionDenied(permissions.update);
        return;
      }
      nodeOperation.mutate(
        { clusterId, nodeName: node.name, action: "cordon" },
        {
          onSuccess: () => toastSuccess(`Node ${node.name} cordoned`),
          onError: (error) => toastApiError("Failed to cordon node", error),
        },
      );
    },
    [clusterId, nodeOperation, permissions.update],
  );

  const handleUncordon = useCallback(
    (node: ClusterNode) => {
      if (!permissions.update.allowed) {
        toastPermissionDenied(permissions.update);
        return;
      }
      nodeOperation.mutate(
        { clusterId, nodeName: node.name, action: "uncordon" },
        {
          onSuccess: () => toastSuccess(`Node ${node.name} uncordoned`),
          onError: (error) => toastApiError("Failed to uncordon node", error),
        },
      );
    },
    [clusterId, nodeOperation, permissions.update],
  );

  const handleDrain = async (node: ClusterNode) => {
    if (!permissions.manage.allowed) {
      toastPermissionDenied(permissions.manage);
      return;
    }
    try {
      await nodeOperation.mutateAsync({
        clusterId,
        nodeName: node.name,
        action: "drain",
        body: {
          ignore_daemonsets: true,
          delete_empty_dir_data: false,
        },
      });
      toastSuccess(`Node ${node.name} drained`);
      setDrainTarget(null);
    } catch (error) {
      if (error instanceof OperationPartialError) {
        toastWarning(
          `Drain incomplete; node remains cordoned: ${error.operation.errorMessage || "pods are blocked"}`,
        );
      } else {
        toastApiError("Failed to drain node", error);
      }
    }
  };

  const columns = useMemo<Column<ClusterNode>[]>(
    () => [
      ...nodeColumns,
      {
        key: "actions",
        header: "",
        rowActions: true,
        accessor: (row) => {
          const isCordonable = row.status !== "SchedulingDisabled";
          return (
            <ResourceActionMenu
              clusterId={clusterId}
              resourceType={"nodes"}
              row={row}
              permissions={permissions}
              items={[
                {
                  label: "View YAML",
                  icon: <Code className="h-3.5 w-3.5" />,
                  onClick: () =>
                    setYamlTarget({
                      path: k8sResourcePath("nodes", row.name),
                      title: `Node: ${row.name}`,
                    }),
                  disabled: !permissions.read.allowed,
                  disabledReason: permissionDeniedReason(permissions.read),
                },
                {
                  label: isCordonable ? "Cordon" : "Uncordon",
                  icon: isCordonable ? (
                    <ShieldBan className="h-3.5 w-3.5" />
                  ) : (
                    <ShieldCheck className="h-3.5 w-3.5" />
                  ),
                  onClick: () =>
                    isCordonable ? handleCordon(row) : handleUncordon(row),
                  disabled: !permissions.update.allowed,
                  disabledReason: permissionDeniedReason(permissions.update),
                  separator: true,
                },
                {
                  label: "Drain",
                  icon: <Unplug className="h-3.5 w-3.5" />,
                  onClick: () => setDrainTarget(row),
                  variant: "destructive",
                  disabled: !permissions.manage.allowed,
                  disabledReason: permissionDeniedReason(permissions.manage),
                },
              ]}
            />
          );
        },
        sortable: false,
      },
    ],
    [handleCordon, handleUncordon, clusterId, permissions],
  );

  return (
    <>
      <p className="sr-only" role="status" aria-live="polite">
        {nodeOperation.isPending
          ? `Node operation ${nodeOperation.operationState.phase}`
          : ""}
      </p>
      <ExplorerDataTable
        clusterId={clusterId}
        resourceType="nodes"
        data={data || []}
        columns={columns}
        keyExtractor={(r) => r.name}
        searchPlaceholder="Search nodes..."
        loading={isLoading}
        emptyState={{
          title: "No nodes found",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        onRowClick={(row) => {
          if (!permissions.read.allowed) {
            toastPermissionDenied(permissions.read);
            return;
          }
          void navigate({
            to: `/dashboard/clusters/${clusterId}/nodes/${row.name}`,
          });
        }}
      />

      {yamlTarget && (
        <YamlViewDialog
          open={!!yamlTarget}
          onClose={() => setYamlTarget(null)}
          clusterId={clusterId}
          k8sPath={yamlTarget.path}
          title={yamlTarget.title}
          allowEdit={permissions.update.allowed}
          forceConflictPermission={permissions.manage}
        />
      )}

      <ConfirmDialog
        open={!!drainTarget}
        onClose={() => setDrainTarget(null)}
        onConfirm={() => {
          if (drainTarget) void handleDrain(drainTarget);
        }}
        title="Drain Node"
        description={`This will cordon the node and evict all non-DaemonSet pods. Workloads will be rescheduled to other nodes.`}
        impact={nodeDrainImpact(drainTarget?.name)}
        confirmValue={drainTarget?.name}
        confirmText="Drain"
        variant="destructive"
      />
    </>
  );
}
