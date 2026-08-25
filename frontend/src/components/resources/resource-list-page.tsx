import { useCallback, useMemo, useState } from "react";
import {
  useCluster,
  useWorkloads,
  useScaleWorkload,
  useRestartWorkload,
  useK8sDelete,
} from "@/lib/hooks";
import * as apiClient from "@/lib/api";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ActionMenu, type ActionMenuItem } from "@/components/ui/action-menu";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ScaleDialog } from "@/components/workloads/scale-dialog";
import { useWindowManagerStore } from "@/lib/window-manager-store";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { CreateResourceDialog } from "@/components/resources/create-resource-dialog";
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
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
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
import { useParams, useRouter } from "@/lib/navigation";
import { Link } from "@/lib/link";
import {
  RESOURCE_TITLES,
  WORKLOAD_KINDS,
  WORKLOAD_TEMPLATE_BY_KIND,
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

// ── Per-resource components (each calls only its own hook) ──

function WorkloadActions({
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
      <ActionMenu items={items} />
    </StopRowClick>
  );
}

function WorkloadsTable({
  clusterId,
  kind,
  title,
}: {
  clusterId: string;
  kind: string;
  title: string;
}) {
  const { data, isLoading } = useWorkloads(clusterId);
  const router = useRouter();
  const filtered = (data?.data || []).filter((w) => w.kind === kind);
  const scaleWorkload = useScaleWorkload();
  const restartWorkload = useRestartWorkload();
  const k8sDeleteMut = useK8sDelete();
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
        const pods = await apiClient.getWorkloadPods(
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
          <Link
            href={workloadDetailHref(
              clusterId,
              row.kind,
              row.namespace,
              row.name,
            )}
            onClick={(e) => e.stopPropagation()}
            className="font-medium text-foreground font-mono text-xs hover:underline"
          >
            {row.name}
          </Link>
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
      <DataTable
        data={filtered}
        columns={columns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={(row) => {
          if (!permissions.read.allowed) {
            toastPermissionDenied(permissions.read);
            return;
          }
          router.push(
            workloadDetailHref(clusterId, row.kind, row.namespace, row.name),
          );
        }}
        searchPlaceholder={`Search ${title.toLowerCase()}...`}
        loading={isLoading}
        emptyMessage={`No ${title.toLowerCase()} found`}
      />

      <ScaleDialog
        open={!!scaleTarget}
        onClose={() => setScaleTarget(null)}
        onScale={(replicas) => {
          if (!permissions.scale.allowed) {
            toastPermissionDenied(permissions.scale);
            return;
          }
          if (scaleTarget) {
            scaleWorkload.mutate(
              {
                clusterId,
                kind: scaleTarget.kind,
                namespace: scaleTarget.namespace,
                name: scaleTarget.name,
                replicas,
              },
              { onSuccess: () => setScaleTarget(null) },
            );
          }
        }}
        workloadName={scaleTarget?.name || ""}
        currentReplicas={scaleTarget?.replicas || 0}
        loading={scaleWorkload.isPending}
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
          if (deleteTarget) {
            const resType = kindToResourceType(deleteTarget.kind);
            k8sDeleteMut.mutate(
              {
                clusterId,
                path: k8sResourcePath(
                  resType,
                  deleteTarget.name,
                  deleteTarget.namespace,
                ),
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
          }
        }}
        title={`Delete ${deleteTarget?.kind || "Workload"}`}
        description={`This will permanently delete ${deleteTarget?.name}. Managed pods will also be terminated.`}
        impact={resourceDeletionImpact(
          deleteTarget ? kindToResourceType(deleteTarget.kind) : kind,
          deleteTarget,
        )}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDeleteMut.isPending}
      />

      {WORKLOAD_TEMPLATE_BY_KIND[kind] && (
        <CreateResourceDialog
          open={showCreate}
          onClose={() => setShowCreate(false)}
          clusterId={clusterId}
          templateKey={WORKLOAD_TEMPLATE_BY_KIND[kind]}
          title={`Create ${kind}`}
        />
      )}
    </>
  );
}

// ── Resource config ──

// ── Main Page Component ──

export function ClusterResourcePage() {
  const params = useParams();
  const clusterId = params.id as string;
  const resource = params.resource as string;

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
      <h1 className="text-xl font-semibold text-foreground tracking-tight">
        {title}
      </h1>
      {renderTable()}
    </div>
  );
}
