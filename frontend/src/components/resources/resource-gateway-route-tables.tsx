import { useMemo, useState } from "react";
import { useK8sDelete } from "@/lib/hooks/kubernetes-proxy";
import { useNavigate } from "@tanstack/react-router";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import type { Column } from "@/components/ui/data-table";
import type { TableEmptyState } from "@/components/ui/data-table-empty-state";
import { ServerResourceExplorerTable } from "@/components/resources/server-resource-explorer-table";
import type { NamedResourceType } from "@/lib/api/kubernetes-resources";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
import { useClusterResourcePermissions } from "@/components/resources/resource-action-policy";
import {
  makeRowClick,
  nameColumn,
} from "@/components/resources/resource-table-primitives";
import { withNameKind } from "@/components/resources/networking-table-cells";
import { k8sResourcePath } from "@/lib/k8s-paths";
import { toastPermissionDenied } from "@/lib/permission-hooks";
import type { GatewayRoute } from "@/types";
import { routeColumns } from "@/components/resources/resource-gateway-columns";
import { NamespacedActions } from "@/components/resources/resource-gateway-actions";

// Route tables share routeColumns + a delete confirm. The variants only
// differ in which hook supplies data and which resourceType the K8s path
// builder gets. Parameterizing keeps drift between them minimal.
function RouteTable<T extends GatewayRoute>({
  clusterId,
  kindLabel,
  resourceType,
  searchPlaceholder,
  emptyState,
}: {
  clusterId: string;
  kindLabel: string;
  resourceType: NamedResourceType;
  searchPlaceholder: string;
  emptyState: TableEmptyState;
}) {
  const navigate = useNavigate();
  const k8sDelete = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, resourceType);
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<T | null>(null);

  const columns = useMemo<Column<T>[]>(
    () => [
      withNameKind(nameColumn<T>(clusterId, resourceType)),
      ...(routeColumns.slice(1) as Column<T>[]),
      {
        key: "actions",
        header: "",
        rowActions: true,
        kind: "actions",
        accessor: (row) => (
          <NamespacedActions
            clusterId={clusterId}
            resourceType={resourceType}
            kindLabel={kindLabel}
            row={row}
            permissions={permissions}
            onView={setYamlTarget}
            onDelete={(r) => setDeleteTarget(r as T)}
          />
        ),
        sortable: false,
        align: "center" as const,
      },
    ],
    [clusterId, resourceType, kindLabel, permissions],
  );

  return (
    <>
      <ServerResourceExplorerTable<T>
        clusterId={clusterId}
        resourceType={resourceType}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          navigate,
          clusterId,
          resourceType,
          permissions.read,
        )}
        searchPlaceholder={searchPlaceholder}
        emptyState={emptyState}
        bulkDelete={{
          path: (row) => k8sResourcePath(resourceType, row.name, row.namespace),
          label: (row) => `${row.namespace}/${row.name}`,
          noun: kindLabel,
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
                  resourceType,
                  deleteTarget.name,
                  deleteTarget.namespace,
                ),
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
        }}
        title={`Delete ${kindLabel}`}
        description={`This will permanently delete the ${kindLabel.toLowerCase()} ${deleteTarget?.name}.`}
        impact={resourceDeletionImpact(resourceType, deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDelete.isPending}
      />
    </>
  );
}

export function HTTPRoutesTable({ clusterId }: { clusterId: string }) {
  return (
    <RouteTable<GatewayRoute>
      clusterId={clusterId}
      kindLabel="HTTPRoute"
      resourceType="httproutes"
      searchPlaceholder="Search HTTPRoutes..."
      emptyState={{
        title: "No HTTPRoutes found",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
    />
  );
}

export function GRPCRoutesTable({ clusterId }: { clusterId: string }) {
  return (
    <RouteTable<GatewayRoute>
      clusterId={clusterId}
      kindLabel="GRPCRoute"
      resourceType="grpcroutes"
      searchPlaceholder="Search GRPCRoutes..."
      emptyState={{
        title: "No GRPCRoutes found",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
    />
  );
}

export function TLSRoutesTable({ clusterId }: { clusterId: string }) {
  return (
    <RouteTable<GatewayRoute>
      clusterId={clusterId}
      kindLabel="TLSRoute"
      resourceType="tlsroutes"
      searchPlaceholder="Search TLSRoutes..."
      emptyState={{
        title: "No TLSRoutes found",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
    />
  );
}

export function TCPRoutesTable({ clusterId }: { clusterId: string }) {
  return (
    <RouteTable<GatewayRoute>
      clusterId={clusterId}
      kindLabel="TCPRoute"
      resourceType="tcproutes"
      searchPlaceholder="Search TCPRoutes..."
      emptyState={{
        title: "No TCPRoutes found",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
    />
  );
}

export function UDPRoutesTable({ clusterId }: { clusterId: string }) {
  return (
    <RouteTable<GatewayRoute>
      clusterId={clusterId}
      kindLabel="UDPRoute"
      resourceType="udproutes"
      searchPlaceholder="Search UDPRoutes..."
      emptyState={{
        title: "No UDPRoutes found",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
    />
  );
}
