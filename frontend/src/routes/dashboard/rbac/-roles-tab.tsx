import { Copy, Pencil, Shield, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { ActionMenu } from "@/components/ui/action-menu";
import type { GlobalRole } from "@/types";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { getRolePage } from "@/lib/api/rbac-role-page";
import { type RoleScope } from "@/lib/api/rbac";
import { queryKeys } from "@/lib/query-keys";
import { pageTableCount } from "@/lib/api/pagination";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { PermissionState } from "@/components/ui/empty-state";
import {
  crdGrantCount,
  isBuiltinRole,
  roleTitle,
  type RoleLike,
} from "@/components/rbac/binding-utils";
import { RelativeTime } from "@/components/admin/table-cells";

function TypeBadge({ builtin }: { builtin: boolean }) {
  return (
    <Badge variant={builtin ? "secondary" : "info"}>
      {builtin ? "Built-in" : "Custom"}
    </Badge>
  );
}

function roleColumns<T extends RoleLike & { id: string }>(actions: {
  onEdit: (role: T) => void;
  onDuplicate: (role: T) => void;
  onDelete: (role: T) => void;
}): Column<T>[] {
  return [
    {
      key: "name",
      header: "Role",
      kind: "name",
      accessor: (row) => (
        <div className="flex min-w-0 items-center gap-2">
          <Shield className="h-4 w-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0">
            <p className="truncate font-medium text-foreground">
              {roleTitle(row)}
            </p>
            <p className="truncate font-mono text-xs text-muted-foreground">
              {row.name}
            </p>
          </div>
        </div>
      ),
      sortAccessor: (row) => roleTitle(row),
    },
    {
      key: "description",
      header: "Description",
      kind: "text",
      size: 220,
      minSize: 160,
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">
          {row.description || "—"}
        </span>
      ),
      sortable: false,
    },
    {
      key: "builtin",
      header: "Type",
      kind: "badge",
      size: 105,
      accessor: (row) => <TypeBadge builtin={isBuiltinRole(row)} />,
      sortAccessor: (row) => (isBuiltinRole(row) ? "Built-in" : "Custom"),
      filter: { label: "Type" },
    },
    {
      key: "rules",
      header: "Rules",
      kind: "count",
      size: 96,
      accessor: (row) => (
        <span className="text-sm">{row.rules?.length ?? 0}</span>
      ),
      sortAccessor: (row) => row.rules?.length ?? 0,
    },
    {
      key: "crd",
      header: "CRD grants",
      kind: "count",
      size: 126,
      accessor: (row) => {
        const count = crdGrantCount(row.rules);
        return count > 0 ? (
          <span className="text-sm">{count}</span>
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        );
      },
      sortAccessor: (row) => crdGrantCount(row.rules),
    },
    {
      key: "created",
      header: "Created",
      kind: "age",
      size: 105,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          <RelativeTime value={row.createdAt} fallback="—" />
        </span>
      ),
      sortAccessor: (row) => row.createdAt || "",
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      rowActions: true,
      accessor: (row) => {
        const builtin = isBuiltinRole(row);
        return (
          <ActionMenu
            ariaLabel={`Actions for ${roleTitle(row)}`}
            items={[
              {
                label: "Edit role",
                icon: <Pencil className="h-3.5 w-3.5" />,
                onClick: () => actions.onEdit(row),
                disabled: builtin,
                disabledReason: "Built-in roles are immutable",
              },
              {
                label: "Duplicate role",
                icon: <Copy className="h-3.5 w-3.5" />,
                onClick: () => actions.onDuplicate(row),
              },
              {
                label: "Delete role",
                icon: <Trash2 className="h-3.5 w-3.5" />,
                onClick: () => actions.onDelete(row),
                variant: "destructive",
                separator: true,
                disabled: builtin,
                disabledReason: "Built-in roles are immutable",
              },
            ]}
          />
        );
      },
      sortable: false,
      align: "right",
    },
  ];
}

interface RolesTabProps<T extends RoleLike & { id: string }> {
  scope: RoleScope;
  onEdit: (role: T) => void;
  onDuplicate: (role: T) => void;
  onDelete: (role: T) => void;
}

export function RolesTab({
  scope,
  onEdit,
  onDuplicate,
  onDelete,
}: RolesTabProps<GlobalRole>) {
  const [pageIndex, setPageIndex] = useState(0);
  const read = usePermissionDecision("rbac", "read");
  const query = useQuery({
    queryKey: queryKeys.rbac.rolePage(scope, pageIndex),
    queryFn: ({ signal }) => getRolePage(scope, pageIndex * 25, signal),
    enabled: read.allowed,
    throwOnError: false,
  });
  return !read.allowed ? (
    <PermissionState permission="rbac:read" />
  ) : (
    <DataTable
      data={query.isError || !read.allowed ? [] : (query.data?.data ?? [])}
      columns={roleColumns<GlobalRole>({ onEdit, onDuplicate, onDelete }).map(
        (column) => ({ ...column, sortable: false, filter: undefined }),
      )}
      keyExtractor={(row) => row.id}
      searchable={false}
      loading={query.isLoading}
      isError={query.isError}
      error={query.error}
      permission="rbac:read"
      onRetry={() => void query.refetch()}
      serverSide={{
        ...pageTableCount(
          query.isError || !read.allowed ? undefined : query.data,
        ),
        pagination: { pageIndex, pageSize: 25 },
        onPaginationChange: (next) => setPageIndex(next.pageIndex),
      }}
      emptyState={{
        title: `No ${scope} roles defined`,
        description: "Create the first item to configure this feature.",
      }}
    />
  );
}
