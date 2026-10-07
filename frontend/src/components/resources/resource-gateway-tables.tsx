import { useMemo, useState } from "react";
import { useK8sDelete } from "@/lib/hooks/kubernetes-proxy";
import { useNavigate } from "@tanstack/react-router";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { CreateResourceDialog } from "@/components/resources/create-resource-dialog";
import type { Column } from "@/components/ui/data-table";
import { ServerResourceExplorerTable } from "@/components/resources/server-resource-explorer-table";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
import { useClusterResourcePermissions } from "@/components/resources/resource-action-policy";
import {
  makeRowClick,
  nameColumn,
} from "@/components/resources/resource-table-primitives";
import { withNameKind } from "@/components/resources/networking-table-cells";
import { k8sResourcePath } from "@/lib/k8s-paths";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import type { Gateway } from "@/types";
import { Plus } from "lucide-react";
import { gatewayColumns } from "@/components/resources/resource-gateway-columns";
import { NamespacedActions } from "@/components/resources/resource-gateway-actions";

export {
  HTTPRoutesTable,
  GRPCRoutesTable,
  TLSRoutesTable,
  TCPRoutesTable,
  UDPRoutesTable,
} from "@/components/resources/resource-gateway-route-tables";
export {
  GatewayClassesTable,
  ReferenceGrantsTable,
} from "@/components/resources/resource-gateway-class-tables";

export function GatewaysTable({ clusterId }: { clusterId: string }) {
  const navigate = useNavigate();
  const k8sDelete = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, "gateways");
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Gateway | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const columns = useMemo<Column<Gateway>[]>(
    () => [
      withNameKind(nameColumn<Gateway>(clusterId, "gateways"), 112),
      ...gatewayColumns.slice(1),
      {
        key: "actions",
        header: "",
        rowActions: true,
        kind: "actions",
        accessor: (row) => (
          <NamespacedActions
            clusterId={clusterId}
            resourceType="gateways"
            kindLabel="Gateway"
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
      <div className="mb-4 flex justify-end">
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create Gateway
        </ActionButton>
      </div>
      <ServerResourceExplorerTable<Gateway>
        clusterId={clusterId}
        resourceType="gateways"
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          navigate,
          clusterId,
          "gateways",
          permissions.read,
        )}
        searchPlaceholder="Search gateways..."
        emptyState={{
          title: "No gateways found",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        bulkDelete={{
          path: (row) => k8sResourcePath("gateways", row.name, row.namespace),
          label: (row) => `${row.namespace}/${row.name}`,
          noun: "gateway",
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
                  "gateways",
                  deleteTarget.name,
                  deleteTarget.namespace,
                ),
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
        }}
        title="Delete Gateway"
        description={`This will permanently delete the gateway ${deleteTarget?.name}.`}
        impact={resourceDeletionImpact("gateways", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDelete.isPending}
      />
      <CreateResourceDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        clusterId={clusterId}
        templateKey="gateway"
        title="Create Gateway"
      />
    </>
  );
}
