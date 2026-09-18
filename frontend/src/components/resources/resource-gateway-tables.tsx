import { useMemo, useState } from "react";
import { useK8sDelete } from "@/lib/hooks/kubernetes-proxy";
import { useNavigate } from "@tanstack/react-router";
import { formatRelativeTime } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";
import { ActionMenu } from "@/components/ui/action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { CreateResourceDialog } from "@/components/resources/create-resource-dialog";
import type { Column } from "@/components/ui/data-table";
import type { TableEmptyState } from "@/components/ui/data-table-empty-state";
import { ServerResourceExplorerTable } from "@/components/resources/server-resource-explorer-table";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
import {
  useClusterResourcePermissions,
  type ResourcePermissionDecisions,
} from "@/components/resources/resource-action-policy";
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
import type {
  Gateway,
  GatewayClass,
  GatewayRoute,
  ReferenceGrant,
} from "@/types";
import { Code, Pencil, Plus, Trash2 } from "lucide-react";

function ConditionPill({
  status,
  trueLabel,
  falseLabel,
}: {
  status: string;
  trueLabel: string;
  falseLabel: string;
}) {
  if (!status) return <span className="text-xs text-muted-foreground">—</span>;
  if (status === "True") {
    return (
      <span className="text-xs px-1.5 py-0.5 rounded-sm bg-status-success/10 text-status-success">
        {trueLabel}
      </span>
    );
  }
  if (status === "False") {
    return (
      <span className="text-xs px-1.5 py-0.5 rounded-sm bg-status-error/10 text-status-error">
        {falseLabel}
      </span>
    );
  }
  return (
    <span className="text-xs px-1.5 py-0.5 rounded-sm bg-muted text-muted-foreground">
      {status}
    </span>
  );
}

const gatewayColumns: Column<Gateway>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "class",
    header: "Class",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.gatewayClassName || "-"}
      </span>
    ),
  },
  {
    key: "listeners",
    header: "Listeners",
    accessor: (row) => (
      <div className="flex gap-1 flex-wrap">
        {row.listenerSummary?.length ? (
          row.listenerSummary.map((s, i) => (
            <span
              key={`${s}-${i}`}
              className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground font-mono"
            >
              {s}
            </span>
          ))
        ) : (
          <span className="text-xs text-muted-foreground">-</span>
        )}
      </div>
    ),
    sortable: false,
  },
  {
    key: "addresses",
    header: "Addresses",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[200px] block">
        {row.addresses?.join(", ") || "-"}
      </span>
    ),
    sortable: false,
  },
  {
    key: "programmed",
    header: "Programmed",
    accessor: (row) => (
      <ConditionPill
        status={row.programmed}
        trueLabel="Programmed"
        falseLabel="Failed"
      />
    ),
    align: "center",
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

// Shared column definition for HTTPRoute / GRPCRoute / TLSRoute / TCPRoute /
// UDPRoute. The L4 routes (TCP/UDP) won't populate hostnames; their column
// just renders empty.
const routeColumns: Column<GatewayRoute>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "parents",
    header: "Parent Gateways",
    accessor: (row) => (
      <div className="flex gap-1 flex-wrap">
        {row.parentSummary?.length ? (
          row.parentSummary.map((p, i) => (
            <span
              key={`${p}-${i}`}
              className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground font-mono"
            >
              {p}
            </span>
          ))
        ) : (
          <span className="text-xs text-muted-foreground">-</span>
        )}
      </div>
    ),
    sortable: false,
  },
  {
    key: "hostnames",
    header: "Hostnames",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[200px] block">
        {row.hostnames?.join(", ") || "-"}
      </span>
    ),
    sortable: false,
  },
  {
    key: "rules",
    header: "Rules",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.ruleCount}</span>
    ),
    align: "center",
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

const gatewayClassColumns: Column<GatewayClass>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "controllerName",
    header: "Controller",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[280px] block">
        {row.controllerName}
      </span>
    ),
  },
  {
    key: "accepted",
    header: "Accepted",
    accessor: (row) => (
      <ConditionPill
        status={row.accepted}
        trueLabel="Accepted"
        falseLabel="Rejected"
      />
    ),
    align: "center",
  },
  {
    key: "description",
    header: "Description",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground truncate max-w-[260px] block">
        {row.description || "-"}
      </span>
    ),
    sortable: false,
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

const referenceGrantColumns: Column<ReferenceGrant>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "from",
    header: "From",
    accessor: (row) => (
      <div className="flex gap-1 flex-wrap">
        {row.from?.length ? (
          row.from.map((f, i) => (
            <span
              key={`${f.kind}-${f.namespace}-${i}`}
              className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground font-mono"
            >
              {f.kind}@{f.namespace}
            </span>
          ))
        ) : (
          <span className="text-xs text-muted-foreground">-</span>
        )}
      </div>
    ),
    sortable: false,
  },
  {
    key: "to",
    header: "To",
    accessor: (row) => (
      <div className="flex gap-1 flex-wrap">
        {row.to?.length ? (
          row.to.map((t, i) => (
            <span
              key={`${t.kind}-${t.name}-${i}`}
              className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground font-mono"
            >
              {t.kind}
              {t.name ? `/${t.name}` : ""}
            </span>
          ))
        ) : (
          <span className="text-xs text-muted-foreground">-</span>
        )}
      </div>
    ),
    sortable: false,
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

// useK8sDelete provides the mutation; per-row dialog state lives in each
// table. Shared utility to render the action menu for a namespaced row.
function NamespacedActions<T extends { name: string; namespace: string }>({
  resourceType,
  kindLabel,
  row,
  permissions,
  onView,
  onDelete,
}: {
  resourceType: string;
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
      <ActionMenu
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
      nameColumn<Gateway>(clusterId, "gateways"),
      ...gatewayColumns.slice(1),
      {
        key: "actions",
        header: "",
        accessor: (row) => (
          <NamespacedActions
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
  resourceType: string;
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
      nameColumn<T>(clusterId, resourceType),
      ...(routeColumns.slice(1) as Column<T>[]),
      {
        key: "actions",
        header: "",
        accessor: (row) => (
          <NamespacedActions
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
      nameColumn<GatewayClass>(clusterId, "gatewayclasses"),
      ...gatewayClassColumns.slice(1),
      {
        key: "actions",
        header: "",
        accessor: (row) => {
          const path = k8sResourcePath("gatewayclasses", row.name);
          const title = `GatewayClass: ${row.name}`;
          return (
            <StopRowClick>
              <ActionMenu
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
    [clusterId, permissions.delete, permissions.read, permissions.update],
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
      nameColumn<ReferenceGrant>(clusterId, "referencegrants"),
      ...referenceGrantColumns.slice(1),
      {
        key: "actions",
        header: "",
        accessor: (row) => (
          <NamespacedActions
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
