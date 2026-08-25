import { useMemo, useState } from "react";
import {
  useGatewayClasses,
  useGateways,
  useGRPCRoutes,
  useHTTPRoutes,
  useK8sDelete,
  useReferenceGrants,
  useTCPRoutes,
  useTLSRoutes,
  useUDPRoutes,
} from "@/lib/hooks";
import { useRouter } from "@/lib/navigation";
import { formatRelativeTime } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";
import { ActionMenu } from "@/components/ui/action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { CreateResourceDialog } from "@/components/resources/create-resource-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
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
      <span className="text-xs px-1.5 py-0.5 rounded bg-status-success/10 text-status-success">
        {trueLabel}
      </span>
    );
  }
  if (status === "False") {
    return (
      <span className="text-xs px-1.5 py-0.5 rounded bg-status-error/10 text-status-error">
        {falseLabel}
      </span>
    );
  }
  return (
    <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
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
              className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground font-mono"
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
              className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground font-mono"
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
              className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground font-mono"
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
              className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground font-mono"
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
  const { data, isLoading } = useGateways(clusterId);
  const router = useRouter();
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
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "gateways",
          permissions.read,
        )}
        searchPlaceholder="Search gateways..."
        loading={isLoading}
        emptyMessage="No gateways found"
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
  data,
  isLoading,
  searchPlaceholder,
  emptyMessage,
}: {
  clusterId: string;
  kindLabel: string;
  resourceType: string;
  data: T[] | undefined;
  isLoading: boolean;
  searchPlaceholder: string;
  emptyMessage: string;
}) {
  const router = useRouter();
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
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          router,
          clusterId,
          resourceType,
          permissions.read,
        )}
        searchPlaceholder={searchPlaceholder}
        loading={isLoading}
        emptyMessage={emptyMessage}
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
  const { data, isLoading } = useHTTPRoutes(clusterId);
  return (
    <RouteTable
      clusterId={clusterId}
      kindLabel="HTTPRoute"
      resourceType="httproutes"
      data={data}
      isLoading={isLoading}
      searchPlaceholder="Search HTTPRoutes..."
      emptyMessage="No HTTPRoutes found"
    />
  );
}

export function GRPCRoutesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useGRPCRoutes(clusterId);
  return (
    <RouteTable
      clusterId={clusterId}
      kindLabel="GRPCRoute"
      resourceType="grpcroutes"
      data={data}
      isLoading={isLoading}
      searchPlaceholder="Search GRPCRoutes..."
      emptyMessage="No GRPCRoutes found"
    />
  );
}

export function TLSRoutesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useTLSRoutes(clusterId);
  return (
    <RouteTable
      clusterId={clusterId}
      kindLabel="TLSRoute"
      resourceType="tlsroutes"
      data={data}
      isLoading={isLoading}
      searchPlaceholder="Search TLSRoutes..."
      emptyMessage="No TLSRoutes found"
    />
  );
}

export function TCPRoutesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useTCPRoutes(clusterId);
  return (
    <RouteTable
      clusterId={clusterId}
      kindLabel="TCPRoute"
      resourceType="tcproutes"
      data={data}
      isLoading={isLoading}
      searchPlaceholder="Search TCPRoutes..."
      emptyMessage="No TCPRoutes found"
    />
  );
}

export function UDPRoutesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useUDPRoutes(clusterId);
  return (
    <RouteTable
      clusterId={clusterId}
      kindLabel="UDPRoute"
      resourceType="udproutes"
      data={data}
      isLoading={isLoading}
      searchPlaceholder="Search UDPRoutes..."
      emptyMessage="No UDPRoutes found"
    />
  );
}

export function GatewayClassesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useGatewayClasses(clusterId);
  const router = useRouter();
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
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "gatewayclasses",
          permissions.read,
        )}
        searchPlaceholder="Search GatewayClasses..."
        loading={isLoading}
        emptyMessage="No GatewayClasses found"
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
  const { data, isLoading } = useReferenceGrants(clusterId);
  const router = useRouter();
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
      <DataTable
        data={data || []}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(
          router,
          clusterId,
          "referencegrants",
          permissions.read,
        )}
        searchPlaceholder="Search ReferenceGrants..."
        loading={isLoading}
        emptyMessage="No ReferenceGrants found"
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
