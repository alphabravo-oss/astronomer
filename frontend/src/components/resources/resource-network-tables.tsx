import { useMemo, useState } from "react";
import {
  useDeleteIngress,
  useDeleteNetworkPolicy,
  useDeleteService,
  useIngresses,
  useNetworkPolicies,
  useServices,
} from "@/lib/hooks";
import { useRouter } from "@/lib/navigation";
import { ActionButton } from "@/components/ui/action-button";
import { ActionMenu } from "@/components/ui/action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { CreateResourceDialog } from "@/components/resources/create-resource-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import {
  ingressColumns,
  networkPolicyColumns,
  serviceColumns,
} from "@/components/resources/resource-list-columns";
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
import type { Ingress, K8sService, NetworkPolicy } from "@/types";
import { Code, Pencil, Plus, Trash2 } from "lucide-react";

export function ServicesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useServices(clusterId);
  const router = useRouter();
  const deleteService = useDeleteService();
  const permissions = useClusterResourcePermissions(clusterId, "services");
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<K8sService | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const columns = useMemo<Column<K8sService>[]>(
    () => [
      nameColumn<K8sService>(clusterId, "services"),
      ...serviceColumns.slice(1),
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
                        "services",
                        row.name,
                        row.namespace,
                      ),
                      title: `Service: ${row.namespace}/${row.name}`,
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
                        "services",
                        row.name,
                        row.namespace,
                      ),
                      title: `Service: ${row.namespace}/${row.name}`,
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
      <div className="flex justify-end mb-4">
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create Service
        </ActionButton>
      </div>
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "services",
          permissions.read,
        )}
        searchPlaceholder="Search services..."
        loading={isLoading}
        emptyMessage="No services found"
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
            deleteService.mutate(
              {
                clusterId,
                namespace: deleteTarget.namespace,
                name: deleteTarget.name,
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
        }}
        title="Delete Service"
        description={`This will permanently delete the service ${deleteTarget?.name}.`}
        impact={resourceDeletionImpact("services", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deleteService.isPending}
      />
      <CreateResourceDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        clusterId={clusterId}
        templateKey="service"
        title="Create Service"
      />
    </>
  );
}

export function IngressesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useIngresses(clusterId);
  const router = useRouter();
  const deleteIngress = useDeleteIngress();
  const permissions = useClusterResourcePermissions(clusterId, "ingresses");
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Ingress | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const columns = useMemo<Column<Ingress>[]>(
    () => [
      nameColumn<Ingress>(clusterId, "ingresses"),
      ...ingressColumns.slice(1),
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
                        "ingresses",
                        row.name,
                        row.namespace,
                      ),
                      title: `Ingress: ${row.namespace}/${row.name}`,
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
                        "ingresses",
                        row.name,
                        row.namespace,
                      ),
                      title: `Ingress: ${row.namespace}/${row.name}`,
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
      <div className="flex justify-end mb-4">
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create Ingress
        </ActionButton>
      </div>
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "ingresses",
          permissions.read,
        )}
        searchPlaceholder="Search ingresses..."
        loading={isLoading}
        emptyMessage="No ingresses found"
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
            deleteIngress.mutate(
              {
                clusterId,
                namespace: deleteTarget.namespace,
                name: deleteTarget.name,
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
        }}
        title="Delete Ingress"
        description={`This will permanently delete the ingress ${deleteTarget?.name}.`}
        impact={resourceDeletionImpact("ingresses", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deleteIngress.isPending}
      />
      <CreateResourceDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        clusterId={clusterId}
        templateKey="ingress"
        title="Create Ingress"
      />
    </>
  );
}

export function NetworkPoliciesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useNetworkPolicies(clusterId);
  const router = useRouter();
  const deleteNp = useDeleteNetworkPolicy();
  const permissions = useClusterResourcePermissions(
    clusterId,
    "networkpolicies",
  );
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<NetworkPolicy | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const columns = useMemo<Column<NetworkPolicy>[]>(
    () => [
      nameColumn<NetworkPolicy>(clusterId, "networkpolicies"),
      ...networkPolicyColumns.slice(1),
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
                        "networkpolicies",
                        row.name,
                        row.namespace,
                      ),
                      title: `NetworkPolicy: ${row.namespace}/${row.name}`,
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
                        "networkpolicies",
                        row.name,
                        row.namespace,
                      ),
                      title: `NetworkPolicy: ${row.namespace}/${row.name}`,
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
      <div className="flex justify-end mb-4">
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create Network Policy
        </ActionButton>
      </div>
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "networkpolicies",
          permissions.read,
        )}
        searchPlaceholder="Search network policies..."
        loading={isLoading}
        emptyMessage="No network policies found"
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
            deleteNp.mutate(
              {
                clusterId,
                namespace: deleteTarget.namespace,
                name: deleteTarget.name,
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
        }}
        title="Delete Network Policy"
        description={`This will permanently delete the network policy ${deleteTarget?.name}.`}
        impact={resourceDeletionImpact("networkpolicies", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deleteNp.isPending}
      />
      <CreateResourceDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        clusterId={clusterId}
        templateKey="networkpolicy"
        title="Create Network Policy"
      />
    </>
  );
}

// ── Gateway API ─────────────────────────────────────────────────────────────
//
// Read + YAML edit + delete. No bespoke create forms — users author YAML via
// the standard CreateResourceDialog only for the four resource types that
// have templates today (service / ingress / networkpolicy / pvc).

// Renders the True/False/Unknown values that Kubernetes publishes for
// gateway-api status conditions. Empty string means the controller hasn't
// emitted the condition yet.
