import { useMemo, useState } from "react";
import {
  useDeletePV,
  useDeletePVC,
  usePersistentVolumeClaims,
  usePersistentVolumes,
  useStorageClasses,
} from "@/lib/hooks";
import { useRouter } from "@/lib/navigation";
import { ActionButton } from "@/components/ui/action-button";
import { ActionMenu } from "@/components/ui/action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { CreateResourceDialog } from "@/components/resources/create-resource-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import {
  pvColumns,
  pvcColumns,
  storageClassColumns,
} from "@/components/resources/resource-list-columns";
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
import { useClusterResourcePermissions } from "@/components/resources/resource-action-policy";
import {
  NameLink,
  StopRowClick,
  makeRowClick,
  nameColumn,
} from "@/components/resources/resource-table-primitives";
import { k8sResourcePath } from "@/lib/k8s-paths";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import type {
  PersistentVolume,
  PersistentVolumeClaim,
  StorageClass,
} from "@/types";
import { Code, Pencil, Plus, Trash2 } from "lucide-react";

export function PVsTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = usePersistentVolumes(clusterId);
  const router = useRouter();
  const deletePv = useDeletePV();
  const permissions = useClusterResourcePermissions(
    clusterId,
    "persistentvolumes",
  );
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<PersistentVolume | null>(
    null,
  );

  const columns = useMemo<Column<PersistentVolume>[]>(
    () => [
      nameColumn<PersistentVolume>(clusterId, "persistentvolumes"),
      ...pvColumns.slice(1),
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
                      path: k8sResourcePath("persistentvolumes", row.name),
                      title: `PV: ${row.name}`,
                    }),
                  disabled: !permissions.read.allowed,
                  disabledReason: permissionDeniedReason(permissions.read),
                },
                {
                  label: "Edit YAML",
                  icon: <Pencil className="h-3.5 w-3.5" />,
                  onClick: () =>
                    setYamlTarget({
                      path: k8sResourcePath("persistentvolumes", row.name),
                      title: `PV: ${row.name}`,
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
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "persistentvolumes",
          permissions.read,
        )}
        searchPlaceholder="Search persistent volumes..."
        loading={isLoading}
        emptyMessage="No persistent volumes found"
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
          if (deleteTarget)
            deletePv.mutate(
              { clusterId, name: deleteTarget.name },
              { onSuccess: () => setDeleteTarget(null) },
            );
        }}
        title="Delete Persistent Volume"
        description={`This will permanently delete the PV ${deleteTarget?.name}. Bound data may be lost.`}
        impact={resourceDeletionImpact("persistentvolumes", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deletePv.isPending}
      />
    </>
  );
}
export function PVCsTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = usePersistentVolumeClaims(clusterId);
  const router = useRouter();
  const deletePvc = useDeletePVC();
  const permissions = useClusterResourcePermissions(
    clusterId,
    "persistentvolumeclaims",
  );
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] =
    useState<PersistentVolumeClaim | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const columns = useMemo<Column<PersistentVolumeClaim>[]>(
    () => [
      nameColumn<PersistentVolumeClaim>(clusterId, "persistentvolumeclaims"),
      ...pvcColumns.slice(1),
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
                      path: k8sResourcePath(
                        "persistentvolumeclaims",
                        row.name,
                        row.namespace,
                      ),
                      title: `PVC: ${row.namespace}/${row.name}`,
                    }),
                  disabled: !permissions.read.allowed,
                  disabledReason: permissionDeniedReason(permissions.read),
                },
                {
                  label: "Edit YAML",
                  icon: <Pencil className="h-3.5 w-3.5" />,
                  onClick: () =>
                    setYamlTarget({
                      path: k8sResourcePath(
                        "persistentvolumeclaims",
                        row.name,
                        row.namespace,
                      ),
                      title: `PVC: ${row.namespace}/${row.name}`,
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
      <div className="mb-4 flex justify-end">
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create PVC
        </ActionButton>
      </div>
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "persistentvolumeclaims",
          permissions.read,
        )}
        searchPlaceholder="Search PVCs..."
        loading={isLoading}
        emptyMessage="No persistent volume claims found"
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
          if (deleteTarget)
            deletePvc.mutate(
              {
                clusterId,
                namespace: deleteTarget.namespace,
                name: deleteTarget.name,
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
        }}
        title="Delete PVC"
        description={`This will permanently delete the PVC ${deleteTarget?.name}. Bound data may be lost.`}
        impact={resourceDeletionImpact("persistentvolumeclaims", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deletePvc.isPending}
      />
      <CreateResourceDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        clusterId={clusterId}
        templateKey="persistentvolumeclaim"
        title="Create Persistent Volume Claim"
      />
    </>
  );
}

export function StorageClassesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useStorageClasses(clusterId);
  const router = useRouter();
  const permissions = useClusterResourcePermissions(
    clusterId,
    "storageclasses",
  );
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);

  const columns = useMemo<Column<StorageClass>[]>(
    () => [
      // SC name keeps its "default" badge alongside the drill-down link.
      {
        key: "name",
        header: "Name",
        accessor: (row) => (
          <div className="flex items-center gap-2">
            <NameLink
              clusterId={clusterId}
              resourceType="storageclasses"
              name={row.name}
            />
            {row.isDefault && (
              <span className="px-1.5 py-0.5 rounded text-2xs bg-status-info/10 text-status-info">
                default
              </span>
            )}
          </div>
        ),
        sortAccessor: (row) => row.name,
      },
      ...storageClassColumns.slice(1),
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
                      path: k8sResourcePath("storageclasses", row.name),
                      title: `StorageClass: ${row.name}`,
                    }),
                  disabled: !permissions.read.allowed,
                  disabledReason: permissionDeniedReason(permissions.read),
                },
              ]}
            />
          </StopRowClick>
        ),
        sortable: false,
        align: "center" as const,
      },
    ],
    [clusterId, permissions.read],
  );

  return (
    <>
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "storageclasses",
          permissions.read,
        )}
        searchPlaceholder="Search storage classes..."
        loading={isLoading}
        emptyMessage="No storage classes found"
      />
      {yamlTarget && (
        <YamlViewDialog
          open={!!yamlTarget}
          onClose={() => setYamlTarget(null)}
          clusterId={clusterId}
          k8sPath={yamlTarget.path}
          title={yamlTarget.title}
          forceConflictPermission={permissions.manage}
        />
      )}
    </>
  );
}
