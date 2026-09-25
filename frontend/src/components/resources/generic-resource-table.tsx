import { useMemo, useState } from "react";

import { Code, Pencil, Plus, Trash2 } from "lucide-react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import type { SortingState } from "@tanstack/react-table";

import { CreateResourceDialog } from "@/components/resources/create-resource-dialog";
import { ConfigMapFormDialog } from "@/components/resources/configmap-form";
import { resourceDeletionImpact } from "@/components/resources/resource-deletion-impact";
import {
  genericResourceActionPolicy,
  useClusterResourcePermissions,
} from "@/components/resources/resource-action-policy";
import { CREATABLE_GENERIC_RESOURCES } from "@/components/resources/resource-route-config";
import {
  StopRowClick,
  makeRowClick,
  nameColumn,
} from "@/components/resources/resource-table-primitives";
import { ActionButton } from "@/components/ui/action-button";
import type { ActionMenuItem } from "@/components/ui/action-menu";
import { ResourceActionMenu } from "./resource-action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import type { Column } from "@/components/ui/data-table";
import { ExplorerDataTable } from "@/components/resources/explorer-data-table";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import {
  useGenericResources,
  useK8sDelete,
} from "@/lib/hooks/kubernetes-proxy";
import { k8sResourcePath } from "@/lib/k8s-paths";
import { useNavigate } from "@tanstack/react-router";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import { toastError } from "@/lib/toast";
import { pageTableCount } from "@/lib/api/pagination";
import { useClusterNamespaceScope } from "@/lib/cluster-scope";
import type { GenericK8sResource } from "@/types";

const GENERIC_RESOURCE_PAGE_SIZE = 20;
const SERVER_SORTABLE_GENERIC_COLUMNS = new Set([
  "name",
  "namespace",
  "age",
  "status",
  "type",
  "schedule",
  "lastSchedule",
  "data",
  "target",
  "minmax",
  "replicas",
  "active",
  "completions",
  "currentHealthy",
  "minAvailable",
  "maxUnavailable",
  "group",
  "kind",
  "scope",
  "version",
  "secrets",
  "rules",
  "role",
  "subjects",
  "endpoints",
  "ports",
  "desired",
  "ready",
  "available",
]);

export interface GenericResourceTableProps {
  clusterId: string;
  resourceType: string;
  title: string;
  baseColumns: Column<GenericK8sResource>[];
}

/**
 * Adapter for Kubernetes resources served through the generic resource API.
 *
 * It owns the common read/edit/delete/create behavior, while the route page
 * supplies only the family-specific column schema. Authorization decisions
 * gate both visible controls and the mutation callbacks themselves.
 */
export function GenericResourceTable({
  clusterId,
  resourceType,
  title,
  baseColumns,
}: GenericResourceTableProps) {
  const scope = useClusterNamespaceScope(clusterId);
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, { wait: 250 });
  const [sorting, setSorting] = useState<SortingState>([
    { id: "namespace", desc: false },
  ]);
  const sort = sorting[0]
    ? `${sorting[0].id}_${sorting[0].desc ? "desc" : "asc"}`
    : "namespace_asc";
  const namespaceSelection = scope.selectedNamespaces;
  const namespaceKey =
    namespaceSelection === null ? undefined : namespaceSelection?.join(",");
  const pageContext = `${resourceType}\u0000${namespaceKey ?? "*"}`;
  const [pageState, setPageState] = useState({
    context: pageContext,
    pageIndex: 0,
  });
  const pageIndex = pageState.context === pageContext ? pageState.pageIndex : 0;
  const setPageIndex = (next: number) =>
    setPageState({ context: pageContext, pageIndex: next });
  const query = useGenericResources(
    clusterId,
    resourceType,
    {
      namespaces: namespaceKey,
      limit: GENERIC_RESOURCE_PAGE_SIZE,
      offset: pageIndex * GENERIC_RESOURCE_PAGE_SIZE,
      search: debouncedSearch.trim() || undefined,
      sort,
    },
    scope.ready,
  );
  const navigate = useNavigate();
  const k8sDeleteMut = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, resourceType);
  const [yamlTarget, setYamlTarget] = useState<{
    path: string;
    title: string;
  } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<GenericK8sResource | null>(
    null,
  );
  const [showCreate, setShowCreate] = useState(false);
  const creatableConfig = CREATABLE_GENERIC_RESOURCES[resourceType];
  const { deletable: isDeletable, editable: isEditable } =
    genericResourceActionPolicy(resourceType);

  const columns = useMemo<Column<GenericK8sResource>[]>(
    () => [
      nameColumn<GenericK8sResource>(clusterId, resourceType),
      ...baseColumns.slice(1),
      {
        key: "actions",
        header: "",
        rowActions: true,
        accessor: (row) => {
          const items: ActionMenuItem[] = [
            {
              label: "View YAML",
              icon: <Code className="h-3.5 w-3.5" />,
              onClick: () => {
                try {
                  setYamlTarget(genericYamlTarget(resourceType, title, row));
                } catch {
                  toastError("YAML view not available for this resource type");
                }
              },
              disabled: !permissions.read.allowed,
              disabledReason: permissionDeniedReason(permissions.read),
            },
          ];

          if (isEditable) {
            items.push({
              label: "Edit YAML",
              icon: <Pencil className="h-3.5 w-3.5" />,
              onClick: () => {
                try {
                  setYamlTarget(genericYamlTarget(resourceType, title, row));
                } catch {
                  toastError("Edit not available for this resource type");
                }
              },
              disabled: !permissions.update.allowed,
              disabledReason: permissionDeniedReason(permissions.update),
            });
          }

          if (isDeletable) {
            items.push({
              label: "Delete",
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteTarget(row),
              variant: "destructive",
              disabled: !permissions.delete.allowed,
              disabledReason: permissionDeniedReason(permissions.delete),
              separator: true,
            });
          }

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
        },
        sortable: false,
        align: "center" as const,
      },
    ],
    [
      baseColumns,
      clusterId,
      isDeletable,
      isEditable,
      permissions,
      resourceType,
      title,
    ],
  );
  const serverColumns = columns.map((column) => ({
    ...column,
    sortable:
      column.key !== "actions" &&
      SERVER_SORTABLE_GENERIC_COLUMNS.has(column.key),
    filter: undefined,
  }));

  return (
    <>
      {creatableConfig && (
        <div className="flex justify-end mb-4">
          <ActionButton
            size="sm"
            intent="primary"
            icon={<Plus className="h-3.5 w-3.5" />}
            onClick={() => setShowCreate(true)}
            disabled={!permissions.create.allowed}
            disabledReason={permissionDeniedReason(permissions.create)}
          >
            {creatableConfig.label}
          </ActionButton>
        </div>
      )}
      <ExplorerDataTable
        clusterId={clusterId}
        resourceType={resourceType}
        data={query.data?.data || []}
        columns={serverColumns}
        keyExtractor={(row) =>
          row.namespace ? `${row.namespace}/${row.name}` : row.name
        }
        onRowClick={makeRowClick(
          navigate,
          clusterId,
          resourceType,
          permissions.read,
        )}
        searchPlaceholder={`Search ${title.toLowerCase()}...`}
        pageSize={GENERIC_RESOURCE_PAGE_SIZE}
        serverSide={{
          ...pageTableCount(query.data),
          pagination: {
            pageIndex,
            pageSize: GENERIC_RESOURCE_PAGE_SIZE,
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
        filtersActive={search.trim() !== ""}
        onClearFilters={() => {
          setSearch("");
          setPageIndex(0);
        }}
        loading={query.isLoading || !scope.ready}
        isError={query.isError}
        error={query.error}
        errorMessage={`Failed to load ${title.toLowerCase()}`}
        onRetry={() => void query.refetch()}
        emptyState={{
          title: `No ${title.toLowerCase()} found`,
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        bulkDelete={
          isDeletable
            ? {
                path: (row) =>
                  row.namespace
                    ? k8sResourcePath(resourceType, row.name, row.namespace)
                    : k8sResourcePath(resourceType, row.name),
                label: (row) =>
                  row.namespace ? `${row.namespace}/${row.name}` : row.name,
                noun: title.replace(/s$/, ""),
              }
            : undefined
        }
      />
      {yamlTarget && (
        <YamlViewDialog
          open
          onClose={() => setYamlTarget(null)}
          clusterId={clusterId}
          k8sPath={yamlTarget.path}
          title={yamlTarget.title}
          allowEdit={isEditable && permissions.update.allowed}
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
          if (!deleteTarget) return;
          const path = deleteTarget.namespace
            ? k8sResourcePath(
                resourceType,
                deleteTarget.name,
                deleteTarget.namespace,
              )
            : k8sResourcePath(resourceType, deleteTarget.name);
          k8sDeleteMut.mutate(
            { clusterId, path },
            { onSuccess: () => setDeleteTarget(null) },
          );
        }}
        title={`Delete ${title.replace(/s$/, "")}`}
        description={`This will permanently delete ${deleteTarget?.name}.`}
        impact={resourceDeletionImpact(resourceType, deleteTarget)}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDeleteMut.isPending}
      />
      {creatableConfig && resourceType === "configmaps" && (
        <ConfigMapFormDialog
          open={showCreate}
          onClose={() => setShowCreate(false)}
          clusterId={clusterId}
        />
      )}
      {creatableConfig && resourceType !== "configmaps" && (
        <CreateResourceDialog
          open={showCreate}
          onClose={() => setShowCreate(false)}
          clusterId={clusterId}
          templateKey={creatableConfig.templateKey}
          title={creatableConfig.label}
        />
      )}
    </>
  );
}

function genericYamlTarget(
  resourceType: string,
  title: string,
  row: GenericK8sResource,
) {
  return {
    path: k8sResourcePath(resourceType, row.name, row.namespace || undefined),
    title: `${title}: ${row.namespace ? row.namespace + "/" : ""}${row.name}`,
  };
}
