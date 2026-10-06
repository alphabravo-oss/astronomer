import { useParams } from "@tanstack/react-router";
import {
  Terminal,
  FileText,
  Trash2,
  RotateCw,
  Scaling,
  Code,
  Pencil,
} from "lucide-react";
import type { ActionMenuItem } from "@/components/ui/action-menu";
import { ResourceActionMenu } from "./resource-action-menu";
import {
  firstDeniedDecision,
  type ResourcePermissionDecisions,
} from "@/components/resources/resource-action-policy";
import { StopRowClick } from "@/components/resources/resource-table-primitives";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import {
  k8sResourcePath,
  kindToResourceType,
  WORKLOAD_SCALABLE_KINDS,
} from "@/lib/k8s-paths";
import type { Workload } from "@/types";

export function WorkloadActions({
  row,
  permissions,
  podPermissions,
  onOpenStream,
  onYaml,
  onScale,
  onRestart,
  onDelete,
}: {
  row: Workload;
  permissions: ResourcePermissionDecisions;
  podPermissions: ResourcePermissionDecisions;
  onOpenStream: (workload: Workload, kind: "logs" | "exec") => void;
  onYaml: (target: { path: string; title: string }) => void;
  onScale: (workload: Workload) => void;
  onRestart: (workload: Workload) => void;
  onDelete: (workload: Workload) => void;
}) {
  // Read from the route rather than a threaded prop: WorkloadActions is a
  // leaf of the per-row action column, and threading clusterId down would
  // grow WorkloadsTable past its complexity-budget ceiling for no benefit —
  // every mount of this component already lives under the cluster route.
  const { id: clusterId } = useParams({ strict: false }) as { id: string };
  const resourceType = kindToResourceType(row.kind);
  const execDenied = firstDeniedDecision(
    podPermissions.read,
    podPermissions.exec,
  );
  const logsDenied = firstDeniedDecision(
    podPermissions.read,
    podPermissions.logs,
  );
  const items: ActionMenuItem[] = [
    {
      label: "Execute Shell",
      icon: <Terminal className="h-3.5 w-3.5" />,
      onClick: () => onOpenStream(row, "exec"),
      disabled: Boolean(execDenied),
      disabledReason: execDenied
        ? permissionDeniedReason(execDenied)
        : undefined,
    },
    {
      label: "View Logs",
      icon: <FileText className="h-3.5 w-3.5" />,
      onClick: () => onOpenStream(row, "logs"),
      disabled: Boolean(logsDenied),
      disabledReason: logsDenied
        ? permissionDeniedReason(logsDenied)
        : undefined,
    },
    {
      label: "View YAML",
      icon: <Code className="h-3.5 w-3.5" />,
      onClick: () =>
        onYaml({
          path: k8sResourcePath(resourceType, row.name, row.namespace),
          title: `${row.kind}: ${row.namespace}/${row.name}`,
        }),
      disabled: !permissions.read.allowed,
      disabledReason: permissionDeniedReason(permissions.read),
      separator: true,
    },
    {
      label: "Edit YAML",
      icon: <Pencil className="h-3.5 w-3.5" />,
      onClick: () =>
        onYaml({
          path: k8sResourcePath(resourceType, row.name, row.namespace),
          title: `${row.kind}: ${row.namespace}/${row.name}`,
        }),
      disabled: !permissions.update.allowed,
      disabledReason: permissionDeniedReason(permissions.update),
    },
  ];
  if (WORKLOAD_SCALABLE_KINDS.includes(row.kind)) {
    items.push({
      label: "Scale",
      icon: <Scaling className="h-3.5 w-3.5" />,
      onClick: () => onScale(row),
      disabled: !permissions.scale.allowed,
      disabledReason: permissionDeniedReason(permissions.scale),
      separator: true,
    });
  }
  items.push({
    label: "Restart",
    icon: <RotateCw className="h-3.5 w-3.5" />,
    onClick: () => {
      if (!permissions.restart.allowed) {
        toastPermissionDenied(permissions.restart);
        return;
      }
      onRestart(row);
    },
    disabled: !permissions.restart.allowed,
    disabledReason: permissionDeniedReason(permissions.restart),
    separator: !WORKLOAD_SCALABLE_KINDS.includes(row.kind),
  });
  items.push({
    label: "Delete",
    icon: <Trash2 className="h-3.5 w-3.5" />,
    onClick: () => onDelete(row),
    variant: "destructive",
    disabled: !permissions.delete.allowed,
    disabledReason: permissionDeniedReason(permissions.delete),
    separator: true,
  });
  return (
    <StopRowClick>
      <ResourceActionMenu
        clusterId={clusterId}
        resourceType={resourceType}
        row={row}
        permissions={permissions}
        items={items}
      />
    </StopRowClick>
  );
}
