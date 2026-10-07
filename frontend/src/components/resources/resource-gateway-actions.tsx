import { ResourceActionMenu } from "./resource-action-menu";
import type { NamedResourceType } from "@/lib/api/kubernetes-resources";
import { type ResourcePermissionDecisions } from "@/components/resources/resource-action-policy";
import { StopRowClick } from "@/components/resources/resource-table-primitives";
import { k8sResourcePath } from "@/lib/k8s-paths";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import { Code, Pencil, Trash2 } from "lucide-react";

// useK8sDelete provides the mutation; per-row dialog state lives in each
// table. Shared utility to render the action menu for a namespaced row.
export function NamespacedActions<
  T extends { name: string; namespace: string },
>({
  clusterId,
  resourceType,
  kindLabel,
  row,
  permissions,
  onView,
  onDelete,
}: {
  clusterId: string;
  resourceType: NamedResourceType;
  kindLabel: string;
  row: T;
  permissions: ResourcePermissionDecisions;
  onView: (target: { path: string; title: string }) => void;
  onDelete: (row: T) => void;
}) {
  const path = k8sResourcePath(resourceType, row.name, row.namespace);
  const title = `${kindLabel}: ${row.namespace}/${row.name}`;
  return (
    <StopRowClick>
      <ResourceActionMenu
        clusterId={clusterId}
        resourceType={resourceType}
        row={row}
        permissions={permissions}
        items={[
          {
            label: "View YAML",
            icon: <Code className="h-3.5 w-3.5" />,
            onClick: () => onView({ path, title }),
            disabled: !permissions.read.allowed,
            disabledReason: permissionDeniedReason(permissions.read),
          },
          {
            label: "Edit YAML",
            icon: <Pencil className="h-3.5 w-3.5" />,
            onClick: () => onView({ path, title }),
            disabled: !permissions.update.allowed,
            disabledReason: permissionDeniedReason(permissions.update),
          },
          {
            label: "Delete",
            icon: <Trash2 className="h-3.5 w-3.5" />,
            onClick: () => onDelete(row),
            variant: "destructive",
            disabled: !permissions.delete.allowed,
            disabledReason: permissionDeniedReason(permissions.delete),
            separator: true,
          },
        ]}
      />
    </StopRowClick>
  );
}
