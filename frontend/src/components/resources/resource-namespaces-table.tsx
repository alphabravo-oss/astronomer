import { useMemo, useState } from "react";
import { useClusterNamespaces } from "@/lib/hooks/clusters";
import { useK8sDelete } from "@/lib/hooks/kubernetes-proxy";
import { k8sCreate } from "@/lib/api/kubernetes-proxy";
import { useNavigate } from "@tanstack/react-router";
import { ActionButton } from "@/components/ui/action-button";
import { ResourceActionMenu } from "./resource-action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import type { Column } from "@/components/ui/data-table";
import { ExplorerDataTable } from "@/components/resources/explorer-data-table";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { nsColumns } from "@/components/resources/resource-list-columns";
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
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
import type { Namespace } from "@/types";
import { Code, Pencil, Plus, Trash2 } from "lucide-react";
import { toastApiError, toastSuccess } from "@/lib/toast";

export function NamespacesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useClusterNamespaces(clusterId);
  const navigate = useNavigate();
  const k8sDeleteMut = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, "namespaces");
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Namespace | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [newNsName, setNewNsName] = useState("");
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
        rowActions: true,
        accessor: (row) => (
          <StopRowClick>
            <ResourceActionMenu
              clusterId={clusterId}
              resourceType={"namespaces"}
              row={row}
              permissions={permissions}
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
      },
    ],
    [clusterId, permissions],
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

      <ExplorerDataTable
        clusterId={clusterId}
        resourceType="namespaces"
        data={data || []}
        columns={columns}
        keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(
          navigate,
          clusterId,
          "namespaces",
          permissions.read,
        )}
        searchPlaceholder="Search namespaces..."
        loading={isLoading}
        emptyState={{
          title: "No namespaces found",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        namespaceAccessor={(row) => row.name}
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
