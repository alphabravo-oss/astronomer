import { WorkloadTableDialogs } from "./workload-table-dialogs";
import { collectionScope } from "@/lib/cluster-scope-collection";
import { useClusterNamespaceScope } from "@/lib/cluster-scope";
import { useCallback, useMemo, useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import type { SortingState } from "@tanstack/react-table";
import { useCluster } from "@/lib/hooks/clusters";
import { useWorkloads, useRestartWorkload } from "@/lib/hooks/workloads";
import { getWorkloadPods, type WorkloadSort } from "@/lib/api/workloads";
import type { Column } from "@/components/ui/data-table";
import { ExplorerDataTable } from "@/components/resources/explorer-data-table";
import type { ActionMenuItem } from "@/components/ui/action-menu";
import { ResourceActionMenu } from "./resource-action-menu";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader } from "@/components/ui/page";
import { useWindowManagerStore } from "@/lib/window-manager-store";
import { GenericResourceTable } from "@/components/resources/generic-resource-table";
import {
  EventsTable,
  NamespacesTable,
  NodesTable,
  PodsTable,
} from "@/components/resources/resource-core-tables";
import {
  GatewayClassesTable,
  GatewaysTable,
  GRPCRoutesTable,
  HTTPRoutesTable,
  ReferenceGrantsTable,
  TCPRoutesTable,
  TLSRoutesTable,
  UDPRoutesTable,
} from "@/components/resources/resource-gateway-tables";
import {
  IngressesTable,
  NetworkPoliciesTable,
  ServicesTable,
} from "@/components/resources/resource-network-tables";
import {
  PVCsTable,
  PVsTable,
  StorageClassesTable,
} from "@/components/resources/resource-storage-tables";
import {
  configMapColumns,
  genericColumnMap,
  workloadColumns,
} from "@/components/resources/resource-list-columns";
import {
  k8sResourcePath,
  kindToResourceType,
  WORKLOAD_SCALABLE_KINDS,
} from "@/lib/k8s-paths";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import {
  firstDeniedDecision,
  useClusterResourcePermissions,
  type ResourcePermissionDecisions,
} from "@/components/resources/resource-action-policy";
import type { Workload } from "@/types";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Link as RouterLink } from "@tanstack/react-router";
import {
  RESOURCE_TITLES,
  WORKLOAD_KINDS,
  isGenericResourceType,
} from "@/components/resources/resource-route-config";
import {
  StopRowClick,
  workloadDetailHref,
} from "@/components/resources/resource-table-primitives";
import {
  Loader2,
  Server,
  Terminal,
  FileText,
  Trash2,
  RotateCw,
  Scaling,
  Code,
  Pencil,
  Plus,
} from "lucide-react";
import { toastError } from "@/lib/toast";
import { pageTableCount } from "@/lib/api/pagination";

const WORKLOAD_RESOURCE_PAGE_SIZE = 50;

// ── Per-resource components (each calls only its own hook) ──

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

function WorkloadsTable({
  clusterId,
  ...props
}: {
  clusterId: string;
  kind: string;
  title: string;
}) {
  const scope = useClusterNamespaceScope(clusterId);
  const selection = collectionScope(scope.selectedNamespaces);
  if (!selection.enabled)
    return (
      <p role="status" className="p-6 text-sm text-muted-foreground">
        {selection.message}
      </p>
    );
  return (
    <ScopedWorkloadsTable
      key={`${clusterId}/${props.kind}/${JSON.stringify(scope.selectedNamespaces)}`}
      clusterId={clusterId}
      namespace={selection.namespace}
      namespaces={selection.namespaces}
      {...props}
    />
  );
}

function ScopedWorkloadsTable({
  namespace,
  namespaces,
  clusterId,
  kind,
  title,
}: {
  clusterId: string;
  kind: string;
  title: string;
  namespace?: string;
  namespaces?: string[];
}) {
  const [pageIndex, setPageIndex] = useState(0);
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, { wait: 250 });
  const [sorting, setSorting] = useState<SortingState>([
    { id: "namespace", desc: false },
  ]);
  const sort = workloadSort(sorting);
  const workloadQuery = useWorkloads(clusterId, {
    namespace,
    namespaces,
    kind,
    search: debouncedSearch.trim() || undefined,
    sort,
    page: pageIndex + 1,
    pageSize: WORKLOAD_RESOURCE_PAGE_SIZE,
  });
  const { data, isLoading, isError, error, refetch } = workloadQuery;
  const resourceType = kindToResourceType(kind);
  const navigate = useNavigate();
  const workloads = data?.data || [];
  const restartWorkload = useRestartWorkload();
  const permissions = useClusterResourcePermissions(clusterId, "workloads");
  const podPermissions = useClusterResourcePermissions(clusterId, "pods");

  const [scaleTarget, setScaleTarget] = useState<Workload | null>(null);
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Workload | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const openWorkloadInWindowManager = useCallback(
    async (workload: Workload, kind: "logs" | "exec") => {
      const denied =
        kind === "exec"
          ? firstDeniedDecision(podPermissions.read, podPermissions.exec)
          : firstDeniedDecision(podPermissions.read, podPermissions.logs);
      if (denied) {
        toastPermissionDenied(denied);
        return;
      }
      try {
        const pods = await getWorkloadPods(
          clusterId,
          workload.kind,
          workload.namespace,
          workload.name,
        );
        const runningPod = pods.find((p) => p.phase === "Running") || pods[0];
        if (!runningPod) {
          toastError("No pods available for this workload");
          return;
        }
        useWindowManagerStore.getState().addTab({
          kind,
          clusterId,
          namespace: runningPod.namespace,
          pod: runningPod.name,
          container: runningPod.containers[0]?.name,
        });
      } catch {
        toastError("Failed to fetch workload pods");
      }
    },
    [clusterId, podPermissions.exec, podPermissions.logs, podPermissions.read],
  );

  const columns = useMemo<Column<Workload>[]>(
    () => [
      {
        key: "name",
        header: "Name",
        accessor: (row) => (
          <RouterLink
            to={workloadDetailHref(
              clusterId,
              row.kind,
              row.namespace,
              row.name,
            )}
            onClick={(e) => e.stopPropagation()}
            className="font-medium text-foreground font-mono text-xs hover:underline"
          >
            {row.name}
          </RouterLink>
        ),
      },
      ...workloadColumns.slice(1),
      {
        key: "actions",
        header: "",
        accessor: (row) => (
          <WorkloadActions
            row={row}
            permissions={permissions}
            podPermissions={podPermissions}
            onOpenStream={openWorkloadInWindowManager}
            onYaml={setYamlTarget}
            onScale={setScaleTarget}
            onRestart={(workload) =>
              restartWorkload.mutate({
                clusterId,
                kind: workload.kind,
                namespace: workload.namespace,
                name: workload.name,
              })
            }
            onDelete={setDeleteTarget}
          />
        ),
        sortable: false,
        align: "center",
      },
    ],
    [
      clusterId,
      openWorkloadInWindowManager,
      permissions,
      podPermissions,
      restartWorkload,
    ],
  );
  const serverColumns = workloadServerColumns(columns);

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
          Create {kind}
        </ActionButton>
      </div>
      <ExplorerDataTable
        clusterId={clusterId}
        resourceType={resourceType}
        data={workloads}
        columns={serverColumns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={(row) => {
          if (!permissions.read.allowed) {
            toastPermissionDenied(permissions.read);
            return;
          }
          void navigate({
            to: workloadDetailHref(
              clusterId,
              row.kind,
              row.namespace,
              row.name,
            ),
          });
        }}
        searchPlaceholder={`Search ${title.toLowerCase()}...`}
        pageSize={WORKLOAD_RESOURCE_PAGE_SIZE}
        loading={isLoading}
        isError={isError}
        error={error}
        onRetry={() => void refetch()}
        filtersActive={search.trim() !== ""}
        onClearFilters={() => {
          setSearch("");
          setPageIndex(0);
        }}
        serverSide={{
          ...pageTableCount(data),
          pagination: {
            pageIndex,
            pageSize: WORKLOAD_RESOURCE_PAGE_SIZE,
          },
          onPaginationChange: (next) => setPageIndex(next.pageIndex),
          search: {
            value: search,
            onChange: (value) => {
              setSearch(value);
              setPageIndex(0);
            },
          },
          sorting: {
            value: sorting,
            onChange: (next) => {
              setSorting(next.slice(0, 1));
              setPageIndex(0);
            },
          },
        }}
        emptyState={{
          title: `No ${title.toLowerCase()} found`,
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        bulkDelete={{
          path: (row) =>
            k8sResourcePath(
              kindToResourceType(row.kind),
              row.name,
              row.namespace,
            ),
          label: (row) => `${row.namespace}/${row.name}`,
          noun: kind,
        }}
        bulkWorkloads={{ target: workloadTarget }}
      />

      <WorkloadTableDialogs
        clusterId={clusterId}
        kind={kind}
        scaleTarget={scaleTarget}
        setScaleTarget={setScaleTarget}
        yamlTarget={yamlTarget}
        setYamlTarget={setYamlTarget}
        deleteTarget={deleteTarget}
        setDeleteTarget={setDeleteTarget}
        showCreate={showCreate}
        setShowCreate={setShowCreate}
      />
    </>
  );
}

function workloadTarget(row: Workload) {
  return { kind: row.kind, namespace: row.namespace, name: row.name };
}

function workloadServerColumns(columns: Column<Workload>[]) {
  const sortable = new Set(["name", "namespace", "age"]);
  return columns.map((column) => ({
    ...column,
    sortable: sortable.has(column.key),
    filter: undefined,
  }));
}

// ── Resource config ──

// ── Main Page Component ──

export function ClusterResourcePage() {
  const params = useParams({ from: "/dashboard/clusters/$id/$resource/" });
  const clusterId = params.id;
  const resource = params.resource;

  const title = RESOURCE_TITLES[resource];
  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);

  // Live updates (P4.6): the agent's informer fan-out emits per-kind
  // `cluster.k8s_changed` events which the central dispatcher routes
  // through `K8S_KIND_ROUTES` to exactly the keys these tables read,
  // paced by the shared invalidator. No per-page listener or dedicated
  // proxy watch is needed anymore (the old per-kind `?watch=true` stream
  // fought the ClassK8sProxy rate limiter); when the stream is down, the
  // tables' `liveFallback` polls keep them fresh.

  if (clusterLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!cluster || !title) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>{!title ? "Unknown resource type" : "Cluster not found"}</p>
      </div>
    );
  }

  const renderTable = () => {
    // Generic resources use the generic endpoint
    if (isGenericResourceType(resource)) {
      return (
        <GenericResourceTable
          clusterId={clusterId}
          resourceType={resource}
          title={title}
          baseColumns={genericColumnMap[resource] || configMapColumns}
        />
      );
    }
    switch (resource) {
      case "nodes":
        return <NodesTable clusterId={clusterId} />;
      case "namespaces":
        return <NamespacesTable clusterId={clusterId} />;
      case "events":
        return <EventsTable clusterId={clusterId} />;
      case "pods":
        return <PodsTable clusterId={clusterId} />;
      case "deployments":
      case "daemonsets":
      case "statefulsets":
        return (
          <WorkloadsTable
            clusterId={clusterId}
            kind={WORKLOAD_KINDS[resource]}
            title={title}
          />
        );
      case "services":
        return <ServicesTable clusterId={clusterId} />;
      case "ingresses":
        return <IngressesTable clusterId={clusterId} />;
      case "networkpolicies":
        return <NetworkPoliciesTable clusterId={clusterId} />;
      case "gateways":
        return <GatewaysTable clusterId={clusterId} />;
      case "httproutes":
        return <HTTPRoutesTable clusterId={clusterId} />;
      case "gatewayclasses":
        return <GatewayClassesTable clusterId={clusterId} />;
      case "grpcroutes":
        return <GRPCRoutesTable clusterId={clusterId} />;
      case "tlsroutes":
        return <TLSRoutesTable clusterId={clusterId} />;
      case "tcproutes":
        return <TCPRoutesTable clusterId={clusterId} />;
      case "udproutes":
        return <UDPRoutesTable clusterId={clusterId} />;
      case "referencegrants":
        return <ReferenceGrantsTable clusterId={clusterId} />;
      case "persistentvolumes":
        return <PVsTable clusterId={clusterId} />;
      case "persistentvolumeclaims":
        return <PVCsTable clusterId={clusterId} />;
      case "storageclasses":
        return <StorageClassesTable clusterId={clusterId} />;
      default:
        return (
          <div className="py-16 text-center text-muted-foreground">
            Unknown resource type
          </div>
        );
    }
  };

  return (
    <div className="space-y-4">
      <PageHeader title={title} />
      {renderTable()}
    </div>
  );
}

function workloadSort(sorting: SortingState): WorkloadSort {
  return (
    sorting[0]
      ? `${sorting[0].id === "age" ? "created" : sorting[0].id}_${sorting[0].desc ? "desc" : "asc"}`
      : "namespace_asc"
  ) as WorkloadSort;
}
