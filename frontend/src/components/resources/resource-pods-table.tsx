import { useCallback, useMemo, useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import type { SortingState } from "@tanstack/react-table";
import { useClusterPods, useDeletePod } from "@/lib/hooks/clusters";
import type { PodSort } from "@/lib/api/workloads";
import { useNavigate } from "@tanstack/react-router";
import { useWindowManagerStore } from "@/lib/window-manager-store";
import { ResourceActionMenu } from "./resource-action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import type { Column } from "@/components/ui/data-table";
import { ExplorerDataTable } from "@/components/resources/explorer-data-table";
import { PodContainersSubRow } from "@/components/resources/pod-containers-subrow";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { podColumns } from "@/components/resources/resource-list-columns";
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
import { useClusterResourcePermissions } from "@/components/resources/resource-action-policy";
import {
  StopRowClick,
  makeRowClick,
  nameColumn,
} from "@/components/resources/resource-table-primitives";
import { k8sResourcePath } from "@/lib/k8s-paths";
import { pageTableCount } from "@/lib/api/pagination";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import type { Pod } from "@/types";
import { Code, FileText, Terminal, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { BareButton } from "@/components/form/bare-button";

function selectedPodSort(sorting: SortingState): PodSort {
  return sorting[0]
    ? (`${sorting[0].id}_${sorting[0].desc ? "desc" : "asc"}` as PodSort)
    : "namespace_asc";
}

type PodHealth = "all" | "attention" | "restarted";

function PodHealthFilter({
  value: selected,
  onChange,
}: {
  value: PodHealth;
  onChange: (value: PodHealth) => void;
}) {
  return (
    <div
      className="flex items-center gap-1 rounded-md border border-border bg-muted/20 p-1"
      aria-label="Pod health filter"
    >
      {(
        [
          ["all", "All"],
          ["attention", "Needs attention"],
          ["restarted", "Restarted"],
        ] as const
      ).map(([value, label]) => (
        <BareButton
          key={value}
          aria-pressed={selected === value}
          onClick={() => onChange(value)}
          className={cn(
            "inline-flex h-7 items-center gap-1.5 rounded px-2 text-xs transition-colors",
            selected === value
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {label}
        </BareButton>
      ))}
    </div>
  );
}

export function PodsTable({ clusterId }: { clusterId: string }) {
  const navigate = useNavigate();
  const [pageIndex, setPageIndex] = useState(0);
  const pageSize = 20;
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, { wait: 250 });
  const [sorting, setSorting] = useState<SortingState>([
    { id: "namespace", desc: false },
  ]);
  const [healthFilter, setHealthFilter] = useState<
    "all" | "attention" | "restarted"
  >("all");
  const sort = selectedPodSort(sorting);
  // Pod changes are routed by the shared SSE dispatcher to this Query key;
  // the hook polls only while the dashboard event stream is unavailable.
  const podsQuery = useClusterPods(clusterId, {
    limit: pageSize,
    offset: pageIndex * pageSize,
    search: debouncedSearch.trim() || undefined,
    sort,
    health: healthFilter,
  });
  const data = useMemo(() => podsQuery.data?.data ?? [], [podsQuery.data]);
  const isLoading = podsQuery.isLoading;
  const deletePod = useDeletePod();
  const permissions = useClusterResourcePermissions(clusterId, "pods");

  const [deleteTarget, setDeleteTarget] = useState<Pod | null>(null);
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);

  // Open a pod-logs / exec session inside the global WindowManager drawer.
  // The drawer mounts at the dashboard layout level and persists across
  // navigation, so we do NOT manage its open/close lifecycle here.
  const openLogs = useCallback(
    (pod: Pod) => {
      if (!permissions.logs.allowed) {
        toastPermissionDenied(permissions.logs);
        return;
      }
      useWindowManagerStore.getState().addTab({
        kind: "logs",
        clusterId,
        namespace: pod.namespace,
        pod: pod.name,
        container:
          pod.containers.find((container) => !container.init)?.name ??
          pod.containers[0]?.name,
      });
    },
    [clusterId, permissions.logs],
  );
  const openExec = useCallback(
    (pod: Pod) => {
      if (!permissions.exec.allowed) {
        toastPermissionDenied(permissions.exec);
        return;
      }
      useWindowManagerStore.getState().addTab({
        kind: "exec",
        clusterId,
        namespace: pod.namespace,
        pod: pod.name,
        container:
          pod.containers.find((container) => !container.init)?.name ??
          pod.containers[0]?.name,
      });
    },
    [clusterId, permissions.exec],
  );

  const columns = useMemo<Column<Pod>[]>(
    () => [
      // Override the shared name cell with a drill-down link into pod detail.
      nameColumn<Pod>(clusterId, "pods", { minSize: 110 }),
      ...podColumns.slice(1),
      {
        key: "actions",
        header: "",
        rowActions: true,
        accessor: (row) => (
          <PodRowActions
            clusterId={clusterId}
            row={row}
            permissions={permissions}
            openExec={openExec}
            openLogs={openLogs}
            setYamlTarget={setYamlTarget}
            setDeleteTarget={setDeleteTarget}
          />
        ),
        sortable: false,
      },
    ],
    [clusterId, openExec, openLogs, permissions],
  );
  const sortableKeys = new Set([
    "name",
    "namespace",
    "status",
    "restarts",
    "node",
    "age",
  ]);
  const serverColumns = columns.map((column) => ({
    ...column,
    sortable: sortableKeys.has(column.key),
    // Facet values derived from one server page would be incomplete. The
    // server-backed health/search controls below operate over the full set.
    filter: undefined,
  }));

  return (
    <>
      <p className="sr-only" role="status" aria-live="polite">
        {deletePod.isPending
          ? `Pod deletion ${deletePod.operationState.phase}`
          : ""}
      </p>
      <ExplorerDataTable
        clusterId={clusterId}
        resourceType="pods"
        data={data}
        columns={serverColumns}
        keyExtractor={(r) => `${r.namespace}/${r.name}`}
        searchPlaceholder="Search pods..."
        pageSize={pageSize}
        serverSide={{
          ...pageTableCount(podsQuery.data),
          pagination: { pageIndex, pageSize },
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
        filtersActive={healthFilter !== "all" || search.trim() !== ""}
        onClearFilters={() => {
          setHealthFilter("all");
          setSearch("");
          setPageIndex(0);
        }}
        toolbar={
          <PodHealthFilter
            value={healthFilter}
            onChange={(value) => {
              setHealthFilter(value);
              setPageIndex(0);
            }}
          />
        }
        loading={isLoading}
        isError={podsQuery.isError}
        error={podsQuery.error}
        onRetry={() => void podsQuery.refetch()}
        emptyState={{
          title: "No pods found",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        onRowClick={makeRowClick(navigate, clusterId, "pods", permissions.read)}
        renderSubRow={(pod) => <PodContainersSubRow pod={pod} />}
        exportCsv
        bulkDelete={{
          path: (row) => k8sResourcePath("pods", row.name, row.namespace),
          label: (row) => `${row.namespace}/${row.name}`,
          noun: "pod",
        }}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) {
            deletePod.mutate(
              {
                clusterId,
                namespace: deleteTarget.namespace,
                name: deleteTarget.name,
              },
              { onSuccess: () => setDeleteTarget(null) },
            );
          }
        }}
        title="Delete Pod"
        description={`This will permanently delete the pod. The owning controller may recreate it.`}
        impact={resourceDeletionImpact("pods", deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deletePod.isPending}
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
    </>
  );
}

function PodRowActions({
  clusterId,
  row,
  permissions,
  openExec,
  openLogs,
  setYamlTarget,
  setDeleteTarget,
}: {
  clusterId: string;
  row: Pod;
  permissions: ReturnType<typeof useClusterResourcePermissions>;
  openExec: (pod: Pod) => void;
  openLogs: (pod: Pod) => void;
  setYamlTarget: (target: { path: string; title: string }) => void;
  setDeleteTarget: (pod: Pod) => void;
}) {
  return (
    <StopRowClick>
      <ResourceActionMenu
        clusterId={clusterId}
        resourceType={"pods"}
        row={row}
        permissions={permissions}
        items={[
          {
            label: "Execute Shell",
            icon: <Terminal className="h-3.5 w-3.5" />,
            onClick: () => openExec(row),
            disabled: row.phase !== "Running" || !permissions.exec.allowed,
            disabledReason:
              row.phase !== "Running"
                ? "Pod must be running."
                : permissionDeniedReason(permissions.exec),
          },
          {
            label: "View Logs",
            icon: <FileText className="h-3.5 w-3.5" />,
            onClick: () => openLogs(row),
            disabled: !permissions.logs.allowed,
            disabledReason: permissionDeniedReason(permissions.logs),
          },
          {
            label: "View YAML",
            icon: <Code className="h-3.5 w-3.5" />,
            onClick: () =>
              setYamlTarget({
                path: k8sResourcePath("pods", row.name, row.namespace),
                title: `Pod: ${row.namespace}/${row.name}`,
              }),
            disabled: !permissions.read.allowed,
            disabledReason: permissionDeniedReason(permissions.read),
            separator: true,
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
}
