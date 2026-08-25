import { useCallback, useMemo, useState } from "react";
import {
  useClusterEvents,
  useClusterNamespaces,
  useClusterNodes,
  useDeletePod,
  useNodeOperation,
  useK8sDelete,
} from "@/lib/hooks";
import * as apiClient from "@/lib/api";
import { useLiveQuery } from "@tanstack/react-db";
import {
  k8sCollection,
  podRowFromRaw,
  type RawPod,
} from "@/lib/db/collections";
import { useRouter } from "@/lib/navigation";
import { useWindowManagerStore } from "@/lib/window-manager-store";
import { ActionButton } from "@/components/ui/action-button";
import { ActionMenu } from "@/components/ui/action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import {
  eventColumns,
  nodeColumns,
  nsColumns,
  podColumns,
} from "@/components/resources/resource-list-columns";
import {
  nodeDrainImpact,
  resourceDeletionImpact,
} from "@/components/resources/resource-deletion-impact";
import { useClusterResourcePermissions } from "@/components/resources/resource-action-policy";
import {
  StopRowClick,
  makeRowClick,
  nameColumn,
} from "@/components/resources/resource-table-primitives";
import { k8sResourcePath } from "@/lib/k8s-paths";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import type { ClusterNode, Namespace, Pod } from "@/types";
import {
  Code,
  FileText,
  Pencil,
  Plus,
  ShieldBan,
  ShieldCheck,
  Terminal,
  Trash2,
  Unplug,
} from "lucide-react";
import { toastApiError, toastSuccess, toastWarning } from "@/lib/toast";
import { OperationPartialError } from "@/lib/api/operation-polling";

export function NodesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useClusterNodes(clusterId);
  const router = useRouter();
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
        accessor: (row) => {
          const isCordonable = row.status !== "SchedulingDisabled";
          return (
            <ActionMenu
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
        align: "center" as const,
      },
    ],
    [
      handleCordon,
      handleUncordon,
      permissions.manage,
      permissions.read,
      permissions.update,
    ],
  );

  return (
    <>
      <p className="sr-only" role="status" aria-live="polite">
        {nodeOperation.isPending
          ? `Node operation ${nodeOperation.operationState.phase}`
          : ""}
      </p>
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => r.name}
        searchPlaceholder="Search nodes..."
        loading={isLoading}
        emptyMessage="No nodes found"
        onRowClick={(row) => {
          if (!permissions.read.allowed) {
            toastPermissionDenied(permissions.read);
            return;
          }
          router.push(`/dashboard/clusters/${clusterId}/nodes/${row.name}`);
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

export function NamespacesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useClusterNamespaces(clusterId);
  const router = useRouter();
  const k8sDeleteMut = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, "namespaces");
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Namespace | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [newNsName, setNewNsName] = useState("");
  const k8sCreate = apiClient.k8sCreate;

  const handleCreateNamespace = async () => {
    if (!permissions.create.allowed) {
      toastPermissionDenied(permissions.create);
      return;
    }
    if (!newNsName.trim()) return;
    try {
      await k8sCreate(clusterId, "api/v1/namespaces", {
        apiVersion: "v1",
        kind: "Namespace",
        metadata: { name: newNsName.trim() },
      });
      toastSuccess(`Namespace ${newNsName} created`);
      setShowCreate(false);
      setNewNsName("");
    } catch (error) {
      toastApiError("Failed to create namespace", error);
    }
  };

  const columns = useMemo<Column<Namespace>[]>(
    () => [
      nameColumn<Namespace>(clusterId, "namespaces"),
      ...nsColumns.slice(1),
      {
        key: "actions",
        header: "",
        accessor: (row) => (
          <StopRowClick>
            <ActionMenu
              items={[
                {
                  label: "View YAML",
                  icon: <Code className="h-3.5 w-3.5" />,
                  onClick: () =>
                    setYamlTarget({
                      path: k8sResourcePath("namespaces", row.name),
                      title: `Namespace: ${row.name}`,
                    }),
                  disabled: !permissions.read.allowed,
                  disabledReason: permissionDeniedReason(permissions.read),
                },
                {
                  label: "Edit YAML",
                  icon: <Pencil className="h-3.5 w-3.5" />,
                  onClick: () =>
                    setYamlTarget({
                      path: k8sResourcePath("namespaces", row.name),
                      title: `Namespace: ${row.name}`,
                    }),
                  disabled: !permissions.update.allowed,
                  disabledReason: permissionDeniedReason(permissions.update),
                },
                {
                  label: "Delete",
                  icon: <Trash2 className="h-3.5 w-3.5" />,
                  onClick: () => setDeleteTarget(row),
                  variant: "destructive",
                  disabled: !permissions.delete.allowed,
                  disabledReason: permissionDeniedReason(permissions.delete),
                  separator: true,
                },
              ]}
            />
          </StopRowClick>
        ),
        sortable: false,
        align: "center" as const,
      },
    ],
    [clusterId, permissions.delete, permissions.read, permissions.update],
  );

  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <div />
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create Namespace
        </ActionButton>
      </div>

      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "namespaces",
          permissions.read,
        )}
        searchPlaceholder="Search namespaces..."
        loading={isLoading}
        emptyMessage="No namespaces found"
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
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) {
            k8sDeleteMut.mutate(
              {
                clusterId,
                path: k8sResourcePath("namespaces", deleteTarget.name),
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
          }
        }}
        title="Delete Namespace"
        description="This will delete the namespace and ALL resources within it. This action cannot be undone."
        impact={resourceDeletionImpact("namespaces", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDeleteMut.isPending}
      />

      {/* Create Namespace Dialog */}
      {showCreate && (
        <ModalShell
          title="Create Namespace"
          onClose={() => setShowCreate(false)}
          size="sm"
          footerClassName="flex items-center justify-end gap-2"
          footer={
            <>
              <ActionButton
                size="sm"
                intent="ghost"
                onClick={() => setShowCreate(false)}
              >
                Cancel
              </ActionButton>
              <ActionButton
                size="sm"
                intent="primary"
                onClick={handleCreateNamespace}
                disabled={!newNsName.trim() || !permissions.create.allowed}
                disabledReason={
                  !permissions.create.allowed
                    ? permissionDeniedReason(permissions.create)
                    : undefined
                }
              >
                Create
              </ActionButton>
            </>
          }
        >
          <label
            className="block text-xs text-muted-foreground mb-1.5"
            htmlFor="field-b9bb6b24-1104"
          >
            Name
          </label>
          <Input
            id="field-b9bb6b24-1104"
            type="text"
            value={newNsName}
            onChange={(e) => setNewNsName(e.target.value)}
            placeholder="my-namespace"
            className="h-8 font-mono"
            data-initial-focus
            onKeyDown={(e) => {
              if (e.key === "Enter") handleCreateNamespace();
            }}
          />
        </ModalShell>
      )}
    </>
  );
}

export function EventsTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useClusterEvents(clusterId, { limit: 200 });
  return (
    <DataTable
      data={data || []}
      columns={eventColumns}
      keyExtractor={(r) => r.id}
      searchPlaceholder="Search events..."
      loading={isLoading}
      emptyMessage="No events found"
    />
  );
}

export function PodsTable({ clusterId }: { clusterId: string }) {
  const router = useRouter();
  // Live pods collection (P4.7): raw pod objects seeded by a list and folded
  // from the pods SSE watch, shaped into display rows client-side. Deletes and
  // restarts land as watch frames, so no invalidation plumbing is needed.
  const pods = k8sCollection<RawPod>({ clusterId, source: { kind: "pods" } });
  const live = useLiveQuery(
    (q) => q.from({ p: pods.collection }),
    [pods.collection],
  );
  const data = useMemo(
    () => (live.data ?? []).map((p) => podRowFromRaw(clusterId, p)),
    [live.data, clusterId],
  );
  const isLoading = !live.isReady;
  const deletePod = useDeletePod();
  const permissions = useClusterResourcePermissions(clusterId, "pods");

  const [deleteTarget, setDeleteTarget] = useState<Pod | null>(null);
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);

  // Open a pod-logs / exec session inside the global WindowManager drawer.
  // The drawer mounts at the dashboard layout level and persists across
  // navigation, so we do NOT manage its open/close lifecycle here.
  const openLogs = useCallback(
    (pod: Pod) => {
      if (!permissions.logs.allowed) {
        toastPermissionDenied(permissions.logs);
        return;
      }
      useWindowManagerStore.getState().addTab({
        kind: "logs",
        clusterId,
        namespace: pod.namespace,
        pod: pod.name,
        container: pod.containers[0]?.name,
      });
    },
    [clusterId, permissions.logs],
  );
  const openExec = useCallback(
    (pod: Pod) => {
      if (!permissions.exec.allowed) {
        toastPermissionDenied(permissions.exec);
        return;
      }
      useWindowManagerStore.getState().addTab({
        kind: "exec",
        clusterId,
        namespace: pod.namespace,
        pod: pod.name,
        container: pod.containers[0]?.name,
      });
    },
    [clusterId, permissions.exec],
  );

  const columns = useMemo<Column<Pod>[]>(
    () => [
      // Override the shared name cell with a drill-down link into pod detail.
      nameColumn<Pod>(clusterId, "pods"),
      ...podColumns.slice(1),
      {
        key: "actions",
        header: "",
        accessor: (row) => (
          <StopRowClick>
            <ActionMenu
              items={[
                {
                  label: "Execute Shell",
                  icon: <Terminal className="h-3.5 w-3.5" />,
                  onClick: () => openExec(row),
                  disabled:
                    row.phase !== "Running" || !permissions.exec.allowed,
                  disabledReason:
                    row.phase !== "Running"
                      ? "Pod must be running."
                      : permissionDeniedReason(permissions.exec),
                },
                {
                  label: "View Logs",
                  icon: <FileText className="h-3.5 w-3.5" />,
                  onClick: () => openLogs(row),
                  disabled: !permissions.logs.allowed,
                  disabledReason: permissionDeniedReason(permissions.logs),
                },
                {
                  label: "View YAML",
                  icon: <Code className="h-3.5 w-3.5" />,
                  onClick: () =>
                    setYamlTarget({
                      path: k8sResourcePath("pods", row.name, row.namespace),
                      title: `Pod: ${row.namespace}/${row.name}`,
                    }),
                  disabled: !permissions.read.allowed,
                  disabledReason: permissionDeniedReason(permissions.read),
                  separator: true,
                },
                {
                  label: "Delete",
                  icon: <Trash2 className="h-3.5 w-3.5" />,
                  onClick: () => setDeleteTarget(row),
                  variant: "destructive",
                  disabled: !permissions.delete.allowed,
                  disabledReason: permissionDeniedReason(permissions.delete),
                  separator: true,
                },
              ]}
            />
          </StopRowClick>
        ),
        sortable: false,
        align: "center",
      },
    ],
    [
      clusterId,
      openExec,
      openLogs,
      permissions.delete,
      permissions.exec,
      permissions.logs,
      permissions.read,
    ],
  );

  return (
    <>
      <p className="sr-only" role="status" aria-live="polite">
        {deletePod.isPending
          ? `Pod deletion ${deletePod.operationState.phase}`
          : ""}
      </p>
      <DataTable
        data={data}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        searchPlaceholder="Search pods..."
        loading={isLoading}
        emptyMessage="No pods found"
        onRowClick={makeRowClick(router, clusterId, "pods", permissions.read)}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) {
            deletePod.mutate(
              {
                clusterId,
                namespace: deleteTarget.namespace,
                name: deleteTarget.name,
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
          }
        }}
        title="Delete Pod"
        description={`This will permanently delete the pod. The owning controller may recreate it.`}
        impact={resourceDeletionImpact("pods", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deletePod.isPending}
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
    </>
  );
}
