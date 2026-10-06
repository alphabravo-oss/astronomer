import { Lock, Pencil, RotateCcw, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { StatusBadge } from "@/components/ui/status-badge";
import { useNavigate } from "@tanstack/react-router";
import type { User } from "@/types";
import { adminUserHref, isUserLocked } from "@/components/rbac/binding-utils";
import { useState } from "react";
import { useUsers } from "@/lib/hooks/user-settings";
import { pageTableCount } from "@/lib/api/pagination";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { PermissionState } from "@/components/ui/empty-state";
import { Tooltip } from "@/components/ui/tooltip";
import { BareButton } from "@/components/form/bare-button";
import { CappedChips, RelativeTime } from "@/components/admin/table-cells";

interface UsersTabProps {
  onEdit: (user: User) => void;
  onResetPassword: (user: User) => void;
  onDelete: (user: User) => void;
}

function userRoleLabels(row: User): string[] {
  return [
    ...(row.isSuperuser ? ["Superuser"] : []),
    ...(row.globalRoles ?? []),
  ];
}

export function UsersTab({ onEdit, onResetPassword, onDelete }: UsersTabProps) {
  const navigate = useNavigate();
  const [pageIndex, setPageIndex] = useState(0);
  const [search, setSearch] = useState("");
  const read = usePermissionDecision("users", "read");
  const query = useUsers(
    { page: pageIndex + 1, pageSize: 25, search },
    { enabled: read.allowed },
  );

  const userColumns: Column<User>[] = [
    {
      key: "name",
      header: "User",
      kind: "name",
      accessor: (row) => (
        <div className="flex min-w-0 items-center gap-3">
          <div className="w-8 h-8 rounded-full bg-linear-to-br from-zinc-600 to-zinc-800 flex items-center justify-center shrink-0">
            <span className="text-xs font-medium text-primary-foreground">
              {(row.displayName || row.username || "?").charAt(0).toUpperCase()}
            </span>
          </div>
          <div className="min-w-0">
            <p className="truncate font-medium text-foreground">
              {row.displayName || row.username}
            </p>
            <p className="truncate text-xs text-muted-foreground">
              {row.username}
            </p>
          </div>
        </div>
      ),
    },
    {
      key: "email",
      header: "Email",
      kind: "text",
      size: 176,
      minSize: 150,
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.email}</span>
      ),
    },
    {
      key: "provider",
      header: "Provider",
      kind: "badge",
      size: 112,
      accessor: (row) => (
        <Badge variant="secondary" className="capitalize">
          {row.provider}
        </Badge>
      ),
    },
    {
      key: "roles",
      header: "Global Roles",
      kind: "badge",
      size: 136,
      minSize: 110,
      accessor: (row) => (
        <CappedChips
          items={userRoleLabels(row)}
          renderChip={(role) => (
            <Badge variant={role === "Superuser" ? "warning" : "secondary"}>
              {role}
            </Badge>
          )}
        />
      ),
    },
    {
      key: "enabled",
      header: "Status",
      kind: "status",
      size: 140,
      minSize: 120,
      maxSize: 220,
      accessor: (row) => (
        <div className="flex items-center gap-1.5">
          <StatusBadge
            status={row.enabled ? "active" : "disconnected"}
            label={row.enabled ? "Enabled" : "Disabled"}
          />
          {isUserLocked(row) && (
            <Tooltip
              content={"Account is locked out — open the user to unlock"}
            >
              <span>
                <StatusBadge
                  status="error"
                  label="Locked"
                  icon={<Lock className="h-3 w-3" />}
                />
              </span>
            </Tooltip>
          )}
        </div>
      ),
    },
    {
      key: "lastLogin",
      header: "Last Login",
      kind: "age",
      size: 105,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          <RelativeTime value={row.lastLogin} />
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      size: 120,
      maxSize: 120,
      accessor: (row) => (
        <div className="flex items-center gap-1">
          <BareButton
            aria-label="Edit user"
            onClick={() => onEdit(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            tooltip="Edit user"
          >
            <Pencil className="h-3.5 w-3.5" />
          </BareButton>
          <BareButton
            aria-label="Reset password"
            onClick={() => onResetPassword(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            tooltip="Reset password"
          >
            <RotateCcw className="h-3.5 w-3.5" />
          </BareButton>
          <BareButton
            aria-label="Delete user"
            onClick={() => onDelete(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            tooltip="Delete user"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </BareButton>
        </div>
      ),
      sortable: false,
    },
  ];

  return !read.allowed ? (
    <PermissionState permission="users:read" />
  ) : (
    <DataTable
      data={query.isError || !read.allowed ? [] : (query.data?.data ?? [])}
      columns={userColumns.map((column) => ({ ...column, sortable: false }))}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search users..."
      loading={query.isLoading}
      isError={query.isError}
      error={query.error}
      permission="users:read"
      onRetry={() => void query.refetch()}
      serverSide={{
        ...pageTableCount(
          query.isError || !read.allowed ? undefined : query.data,
        ),
        pagination: { pageIndex, pageSize: 25 },
        onPaginationChange: (next) => setPageIndex(next.pageIndex),
        search: {
          value: search,
          onChange: (term) => {
            setSearch(term);
            setPageIndex(0);
          },
        },
      }}
      onRowClick={(row) => void navigate({ to: adminUserHref(row.id) })}
      emptyState={{
        title: "No users found",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
    />
  );
}
