import { useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import type { SortingState } from "@tanstack/react-table";

import {
  ExplorerDataTable,
  type ExplorerBulkDelete,
} from "@/components/resources/explorer-data-table";
import type { Column, DataTableProps } from "@/components/ui/data-table";
import { pageTableCount } from "@/lib/api/pagination";
import type { NamedResourceType } from "@/lib/api/kubernetes-resources";
import { useClusterNamespaceScope } from "@/lib/cluster-scope";
import { useNamedResources } from "@/lib/hooks/kubernetes-resources";

const PAGE_SIZE = 20;
const SERVER_SORTABLE_COLUMNS = new Set([
  "name",
  "namespace",
  "age",
  "created",
  "status",
  "type",
  "clusterIP",
  "ip",
  "ports",
  "class",
  "hosts",
  "addresses",
  "policyTypes",
  "ingress",
  "egress",
  "capacity",
  "accessModes",
  "reclaimPolicy",
  "claimRef",
  "storageClass",
  "volumeName",
  "provisioner",
  "volumeBindingMode",
  "expansion",
  "programmed",
  "accepted",
  "controllerName",
  "listeners",
  "parents",
  "hostnames",
  "rules",
  "from",
  "to",
]);

interface ServerResourceExplorerTableProps<T extends object> extends Omit<
  DataTableProps<T>,
  | "data"
  | "columns"
  | "serverSide"
  | "loading"
  | "isError"
  | "error"
  | "onRetry"
  | "filtersActive"
  | "onClearFilters"
> {
  clusterId: string;
  resourceType: NamedResourceType;
  columns: Column<T>[];
  namespaceAccessor?: (row: T) => string | undefined;
  bulkDelete?: ExplorerBulkDelete<T>;
}

/** Shared server-backed collection policy for Networking, Storage, and Gateway tables. */
export function ServerResourceExplorerTable<T extends object>({
  clusterId,
  resourceType,
  columns,
  searchPlaceholder,
  ...props
}: ServerResourceExplorerTableProps<T>) {
  const scope = useClusterNamespaceScope(clusterId);
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, { wait: 250 });
  const [sorting, setSorting] = useState<SortingState>([
    { id: "namespace", desc: false },
  ]);
  const namespaceSelection = scope.selectedNamespaces;
  const namespaces =
    namespaceSelection === null ? undefined : namespaceSelection?.join(",");
  const pageContext = `${resourceType}\u0000${namespaces ?? "*"}`;
  const [pageState, setPageState] = useState({
    context: pageContext,
    pageIndex: 0,
  });
  const pageIndex = pageState.context === pageContext ? pageState.pageIndex : 0;
  const setPageIndex = (next: number) =>
    setPageState({ context: pageContext, pageIndex: next });
  const sort = sorting[0]
    ? `${sorting[0].id}_${sorting[0].desc ? "desc" : "asc"}`
    : "namespace_asc";
  const query = useNamedResources<T>(
    clusterId,
    resourceType,
    {
      namespaces,
      limit: PAGE_SIZE,
      offset: pageIndex * PAGE_SIZE,
      search: debouncedSearch.trim() || undefined,
      sort,
    },
    scope.ready,
  );

  const serverColumns = columns.map((column) => ({
    ...column,
    sortable:
      column.sortable !== false && SERVER_SORTABLE_COLUMNS.has(column.key),
    filter: undefined,
  }));

  return (
    <ExplorerDataTable
      {...props}
      clusterId={clusterId}
      resourceType={resourceType}
      data={query.data?.data ?? []}
      columns={serverColumns}
      searchPlaceholder={searchPlaceholder}
      pageSize={PAGE_SIZE}
      serverSide={{
        ...pageTableCount(query.data),
        pagination: { pageIndex, pageSize: PAGE_SIZE },
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
      loading={!scope.ready || query.isLoading}
      isError={query.isError}
      error={query.error}
      onRetry={() => void query.refetch()}
    />
  );
}
