import { Server } from "lucide-react";
import type {
  SearchableResourceType,
  SearchResultRow,
} from "@/lib/api/resource-search";
import type { Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";

export function searchColumns(
  resourceType: SearchableResourceType,
): Column<SearchResultRow>[] {
  return [
    {
      key: "cluster",
      header: "Cluster",
      kind: "text",
      size: 160,
      minSize: 136,
      maxSize: 240,
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Server className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
          <span className="font-medium text-foreground truncate">
            {row.clusterName}
          </span>
        </div>
      ),
      sortAccessor: (row) => row.clusterName,
    },
    {
      key: "namespace",
      header: "Namespace",
      kind: "text",
      size: 140,
      minSize: 120,
      maxSize: 240,
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.namespace || "—"}
        </span>
      ),
      sortAccessor: (row) => row.namespace || "",
    },
    {
      key: "name",
      header: "Name",
      kind: "name",
      accessor: (row) => (
        <span className="font-medium text-foreground">{row.name || "—"}</span>
      ),
      sortAccessor: (row) => row.name || "",
    },
    {
      key: "type",
      header: "Type",
      kind: "badge",
      accessor: (row) => (
        <span className="px-2 py-0.5 rounded-sm text-xs font-medium bg-muted text-muted-foreground">
          {row.type || resourceType}
        </span>
      ),
    },
    {
      key: "age",
      header: "Age",
      kind: "age",
      size: 88,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground tabular-nums">
          {row.age || "—"}
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      kind: "status",
      accessor: (row) =>
        row.status ? (
          <StatusBadge status={String(row.status)} />
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        ),
    },
  ];
}
