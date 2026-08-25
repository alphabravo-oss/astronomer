import { useMemo, useState } from "react";

import { Code, Pencil, Plus, Trash2 } from "lucide-react";

import { CreateResourceDialog } from "@/components/resources/create-resource-dialog";
import { ConfigMapFormDialog } from "@/components/resources/configmap-form";
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
import {
  genericResourceActionPolicy,
  useClusterResourcePermissions,
} from "@/components/resources/resource-action-policy";
import { CREATABLE_GENERIC_RESOURCES } from "@/components/resources/resource-route-config";
import {
  StopRowClick,
  makeRowClick,
  nameColumn,
} from "@/components/resources/resource-table-primitives";
import { ActionButton } from "@/components/ui/action-button";
import { ActionMenu, type ActionMenuItem } from "@/components/ui/action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { useGenericResources, useK8sDelete } from "@/lib/hooks";
import { k8sResourcePath } from "@/lib/k8s-paths";
import { useRouter } from "@/lib/navigation";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import { toastError } from "@/lib/toast";
import type { GenericK8sResource } from "@/types";

export interface GenericResourceTableProps {
  clusterId: string;
  resourceType: string;
  title: string;
  baseColumns: Column<GenericK8sResource>[];
}

/**
 * Adapter for Kubernetes resources served through the generic resource API.
 *
 * It owns the common read/edit/delete/create behavior, while the route page
 * supplies only the family-specific column schema. Authorization decisions
 * gate both visible controls and the mutation callbacks themselves.
 */
export function GenericResourceTable({
  clusterId,
  resourceType,
  title,
  baseColumns,
}: GenericResourceTableProps) {
  const query = useGenericResources(clusterId, resourceType);
  const router = useRouter();
  const k8sDeleteMut = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, resourceType);
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<GenericK8sResource | null>(
    null,
  );
  const [showCreate, setShowCreate] = useState(false);
  const creatableConfig = CREATABLE_GENERIC_RESOURCES[resourceType];
  const { deletable: isDeletable, editable: isEditable } =
    genericResourceActionPolicy(resourceType);

  const columns = useMemo<Column<GenericK8sResource>[]>(
    () => [
      nameColumn<GenericK8sResource>(clusterId, resourceType),
      ...baseColumns.slice(1),
      {
        key: "actions",
        header: "",
        accessor: (row) => {
          const items: ActionMenuItem[] = [
            {
              label: "View YAML",
              icon: <Code className="h-3.5 w-3.5" />,
              onClick: () => {
                try {
                  const path = row.namespace
                    ? k8sResourcePath(resourceType, row.name, row.namespace)
                    : k8sResourcePath(resourceType, row.name);
                  setYamlTarget({
                    path,
                    title: `${title}: ${row.namespace ? `${row.namespace}/` : ""}${row.name}`,
                  });
                } catch {
                  toastError("YAML view not available for this resource type");
                }
              },
              disabled: !permissions.read.allowed,
              disabledReason: permissionDeniedReason(permissions.read),
            },
          ];

          if (isEditable) {
            items.push({
              label: "Edit YAML",
              icon: <Pencil className="h-3.5 w-3.5" />,
              onClick: () => {
                try {
                  const path = row.namespace
                    ? k8sResourcePath(resourceType, row.name, row.namespace)
                    : k8sResourcePath(resourceType, row.name);
                  setYamlTarget({
                    path,
                    title: `${title}: ${row.namespace ? `${row.namespace}/` : ""}${row.name}`,
                  });
                } catch {
                  toastError("Edit not available for this resource type");
                }
              },
              disabled: !permissions.update.allowed,
              disabledReason: permissionDeniedReason(permissions.update),
            });
          }

          if (isDeletable) {
            items.push({
              label: "Delete",
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteTarget(row),
              variant: "destructive",
              disabled: !permissions.delete.allowed,
              disabledReason: permissionDeniedReason(permissions.delete),
              separator: true,
            });
          }

          return (
            <StopRowClick>
              <ActionMenu items={items} />
            </StopRowClick>
          );
        },
        sortable: false,
        align: "center" as const,
      },
    ],
    [
      baseColumns,
      clusterId,
      isDeletable,
      isEditable,
      permissions.delete,
      permissions.read,
      permissions.update,
      resourceType,
      title,
    ],
  );

  return (
    <>
      {creatableConfig && (
        <div className="flex justify-end mb-4">
          <ActionButton
            size="sm"
            intent="primary"
            icon={<Plus className="h-3.5 w-3.5" />}
            onClick={() => setShowCreate(true)}
            disabled={!permissions.create.allowed}
            disabledReason={permissionDeniedReason(permissions.create)}
          >
            {creatableConfig.label}
          </ActionButton>
        </div>
      )}
      <DataTable
        data={query.data || []}
        columns={columns}
        keyExtractor={(row) =>
          row.namespace ? `${row.namespace}/${row.name}` : row.name
        }
        onRowClick={makeRowClick(
          router,
          clusterId,
          resourceType,
          permissions.read,
        )}
        searchPlaceholder={`Search ${title.toLowerCase()}...`}
        loading={query.isLoading}
        isError={query.isError}
        errorMessage={`Failed to load ${title.toLowerCase()}`}
        onRetry={() => void query.refetch()}
        emptyMessage={`No ${title.toLowerCase()} found`}
      />
      {yamlTarget && (
        <YamlViewDialog
          open
          onClose={() => setYamlTarget(null)}
          clusterId={clusterId}
          k8sPath={yamlTarget.path}
          title={yamlTarget.title}
          allowEdit={isEditable && permissions.update.allowed}
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
          if (!deleteTarget) return;
          const path = deleteTarget.namespace
            ? k8sResourcePath(
                resourceType,
                deleteTarget.name,
                deleteTarget.namespace,
              )
            : k8sResourcePath(resourceType, deleteTarget.name);
          k8sDeleteMut.mutate(
            { clusterId, path },
            { onSuccess: () => setDeleteTarget(null) },
          );
        }}
        title={`Delete ${title.replace(/s$/, "")}`}
        description={`This will permanently delete ${deleteTarget?.name}.`}
        impact={resourceDeletionImpact(resourceType, deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDeleteMut.isPending}
      />
      {creatableConfig && resourceType === "configmaps" && (
        <ConfigMapFormDialog
          open={showCreate}
          onClose={() => setShowCreate(false)}
          clusterId={clusterId}
        />
      )}
      {creatableConfig && resourceType !== "configmaps" && (
        <CreateResourceDialog
          open={showCreate}
          onClose={() => setShowCreate(false)}
          clusterId={clusterId}
          templateKey={creatableConfig.templateKey}
          title={creatableConfig.label}
        />
      )}
    </>
  );
}
