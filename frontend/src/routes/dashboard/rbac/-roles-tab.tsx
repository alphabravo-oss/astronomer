import { Copy, Pencil, Shield, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { ActionMenu } from "@/components/ui/action-menu";
import { formatRelativeTime } from "@/lib/utils";
import type { ClusterRole, GlobalRole, ProjectRole } from "@/types";
import {
  crdGrantCount,
  isBuiltinRole,
  roleTitle,
  type RoleLike,
} from "@/components/rbac/binding-utils";

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
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Shield className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium text-foreground">{roleTitle(row)}</p>
            <p className="text-xs text-muted-foreground font-mono">
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
      accessor: (row) => <TypeBadge builtin={isBuiltinRole(row)} />,
      sortAccessor: (row) => (isBuiltinRole(row) ? "Built-in" : "Custom"),
      filter: { label: "Type" },
    },
    {
      key: "rules",
      header: "Rules",
      accessor: (row) => (
        <span className="tabular-nums text-sm">{row.rules?.length ?? 0}</span>
      ),
      sortAccessor: (row) => row.rules?.length ?? 0,
      align: "center",
    },
    {
      key: "crd",
      header: "CRD grants",
      accessor: (row) => {
        const count = crdGrantCount(row.rules);
        return count > 0 ? (
          <span className="tabular-nums text-sm">{count}</span>
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        );
      },
      sortAccessor: (row) => crdGrantCount(row.rules),
      align: "center",
    },
    {
      key: "created",
      header: "Created",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.createdAt ? formatRelativeTime(row.createdAt) : "—"}
        </span>
      ),
      sortAccessor: (row) => row.createdAt || "",
    },
    {
      key: "actions",
      header: "",
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
  data: T[];
  loading: boolean;
  isError: boolean;
  onRetry: () => void;
  onEdit: (role: T) => void;
  onDuplicate: (role: T) => void;
  onDelete: (role: T) => void;
}

export function GlobalRolesTab({
  data,
  loading,
  isError,
  onRetry,
  onEdit,
  onDuplicate,
  onDelete,
}: RolesTabProps<GlobalRole>) {
  return (
    <DataTable
      data={data}
      columns={roleColumns<GlobalRole>({ onEdit, onDuplicate, onDelete })}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search global roles..."
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyState={{
        title: "No global roles defined",
        description: "Create the first item to configure this feature.",
      }}
    />
  );
}

export function ClusterRolesTab({
  data,
  loading,
  isError,
  onRetry,
  onEdit,
  onDuplicate,
  onDelete,
}: RolesTabProps<ClusterRole>) {
  return (
    <DataTable
      data={data}
      columns={roleColumns<ClusterRole>({ onEdit, onDuplicate, onDelete })}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search cluster roles..."
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyState={{
        title: "No cluster roles defined",
        description: "Create the first item to configure this feature.",
      }}
    />
  );
}

export function ProjectRolesTab({
  data,
  loading,
  isError,
  onRetry,
  onEdit,
  onDuplicate,
  onDelete,
}: RolesTabProps<ProjectRole>) {
  return (
    <DataTable
      data={data}
      columns={roleColumns<ProjectRole>({ onEdit, onDuplicate, onDelete })}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search project roles..."
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyState={{
        title: "No project roles defined",
        description: "Create the first item to configure this feature.",
      }}
    />
  );
}
