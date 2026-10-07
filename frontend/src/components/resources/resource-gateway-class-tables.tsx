import { useMemo, useState } from "react";
import { useK8sDelete } from "@/lib/hooks/kubernetes-proxy";
import { useNavigate } from "@tanstack/react-router";
import { ResourceActionMenu } from "./resource-action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import type { Column } from "@/components/ui/data-table";
import { ServerResourceExplorerTable } from "@/components/resources/server-resource-explorer-table";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
import { useClusterResourcePermissions } from "@/components/resources/resource-action-policy";
import {
  StopRowClick,
  makeRowClick,
  nameColumn,
} from "@/components/resources/resource-table-primitives";
import { withNameKind } from "@/components/resources/networking-table-cells";
import { k8sResourcePath } from "@/lib/k8s-paths";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import type { GatewayClass, ReferenceGrant } from "@/types";
import { Code, Pencil, Trash2 } from "lucide-react";
import {
  gatewayClassColumns,
  referenceGrantColumns,
} from "@/components/resources/resource-gateway-columns";
import { NamespacedActions } from "@/components/resources/resource-gateway-actions";

export function GatewayClassesTable({ clusterId }: { clusterId: string }) {
  const navigate = useNavigate();
  const k8sDelete = useK8sDelete();
  const permissions = useClusterResourcePermissions(
    clusterId,
    "gatewayclasses",
  );
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<GatewayClass | null>(null);

  const columns = useMemo<Column<GatewayClass>[]>(
    () => [
      withNameKind(nameColumn<GatewayClass>(clusterId, "gatewayclasses")),
      ...gatewayClassColumns.slice(1),
      {
        key: "actions",
        header: "",
        rowActions: true,
        kind: "actions",
        accessor: (row) => {
          const path = k8sResourcePath("gatewayclasses", row.name);
          const title = `GatewayClass: ${row.name}`;
          return (
            <StopRowClick>
              <ResourceActionMenu
                clusterId={clusterId}
                resourceType="gatewayclasses"
                row={row}
                permissions={permissions}
                items={[
                  {
                    label: "View YAML",
                    icon: <Code className="h-3.5 w-3.5" />,
                    onClick: () => setYamlTarget({ path, title }),
                    disabled: !permissions.read.allowed,
                    disabledReason: permissionDeniedReason(permissions.read),
                  },
                  {
                    label: "Edit YAML",
                    icon: <Pencil className="h-3.5 w-3.5" />,
                    onClick: () => setYamlTarget({ path, title }),
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
          );
        },
        sortable: false,
        align: "center" as const,
      },
    ],
    [clusterId, permissions],
  );

  return (
    <>
      <ServerResourceExplorerTable<GatewayClass>
        clusterId={clusterId}
        resourceType="gatewayclasses"
        columns={columns}
        keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(
          navigate,
          clusterId,
          "gatewayclasses",
          permissions.read,
        )}
        searchPlaceholder="Search GatewayClasses..."
        emptyState={{
          title: "No GatewayClasses found",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        bulkDelete={{
          path: (row) => k8sResourcePath("gatewayclasses", row.name),
          label: (row) => row.name,
          noun: "gateway class",
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
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget)
            k8sDelete.mutate(
              {
                clusterId,
                path: k8sResourcePath("gatewayclasses", deleteTarget.name),
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
        }}
        title="Delete GatewayClass"
        description={`This will permanently delete the gateway class ${deleteTarget?.name}.`}
        impact={resourceDeletionImpact("gatewayclasses", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDelete.isPending}
      />
    </>
  );
}

export function ReferenceGrantsTable({ clusterId }: { clusterId: string }) {
  const navigate = useNavigate();
  const k8sDelete = useK8sDelete();
  const permissions = useClusterResourcePermissions(
    clusterId,
    "referencegrants",
  );
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ReferenceGrant | null>(null);

  const columns = useMemo<Column<ReferenceGrant>[]>(
    () => [
      withNameKind(nameColumn<ReferenceGrant>(clusterId, "referencegrants")),
      ...referenceGrantColumns.slice(1),
      {
        key: "actions",
        header: "",
        rowActions: true,
        kind: "actions",
        accessor: (row) => (
          <NamespacedActions
            clusterId={clusterId}
            resourceType="referencegrants"
            kindLabel="ReferenceGrant"
            row={row}
            permissions={permissions}
            onView={setYamlTarget}
            onDelete={setDeleteTarget}
          />
        ),
        sortable: false,
        align: "center" as const,
      },
    ],
    [clusterId, permissions],
  );

  return (
    <>
      <ServerResourceExplorerTable<ReferenceGrant>
        clusterId={clusterId}
        resourceType="referencegrants"
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          navigate,
          clusterId,
          "referencegrants",
          permissions.read,
        )}
        searchPlaceholder="Search ReferenceGrants..."
        emptyState={{
          title: "No ReferenceGrants found",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        bulkDelete={{
          path: (row) =>
            k8sResourcePath("referencegrants", row.name, row.namespace),
          label: (row) => `${row.namespace}/${row.name}`,
          noun: "reference grant",
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
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget)
            k8sDelete.mutate(
              {
                clusterId,
                path: k8sResourcePath(
                  "referencegrants",
                  deleteTarget.name,
                  deleteTarget.namespace,
                ),
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
        }}
        title="Delete ReferenceGrant"
        description={`This will permanently delete the reference grant ${deleteTarget?.name}.`}
        impact={resourceDeletionImpact("referencegrants", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDelete.isPending}
      />
    </>
  );
}
