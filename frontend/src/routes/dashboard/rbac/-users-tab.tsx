import { Lock, Pencil, RotateCcw, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { StatusBadge } from "@/components/ui/status-badge";
import { useNavigate } from "@tanstack/react-router";
import { formatRelativeTime } from "@/lib/utils";
import type { User } from "@/types";
import { adminUserHref, isUserLocked } from "@/components/rbac/binding-utils";
import { useState } from "react";
import { useUsers } from "@/lib/hooks/user-settings";
import { pageTableCount } from "@/lib/api/pagination";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { PermissionState } from "@/components/ui/empty-state";

interface UsersTabProps {
  onEdit: (user: User) => void;
  onResetPassword: (user: User) => void;
  onDelete: (user: User) => void;
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
      accessor: (row) => (
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-full bg-linear-to-br from-zinc-600 to-zinc-800 flex items-center justify-center shrink-0">
            <span className="text-xs font-medium text-primary-foreground">
              {(row.displayName || row.username || "?").charAt(0).toUpperCase()}
            </span>
          </div>
          <div>
            <p className="font-medium text-foreground">
              {row.displayName || row.username}
            </p>
            <p className="text-xs text-muted-foreground">{row.username}</p>
          </div>
        </div>
      ),
    },
    {
      key: "email",
      header: "Email",
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.email}</span>
      ),
    },
    {
      key: "provider",
      header: "Provider",
      accessor: (row) => (
        <Badge variant="secondary" className="capitalize">
          {row.provider}
        </Badge>
      ),
    },
    {
      key: "roles",
      header: "Global Roles",
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.isSuperuser && <Badge variant="warning">Superuser</Badge>}
          {(row.globalRoles ?? []).map((role) => (
            <Badge key={role} variant="secondary">
              {role}
            </Badge>
          ))}
          {!row.isSuperuser && (row.globalRoles ?? []).length === 0 && (
            <span className="text-xs text-muted-foreground">—</span>
          )}
        </div>
      ),
    },
    {
      key: "enabled",
      header: "Status",
      accessor: (row) => (
        <div className="flex items-center gap-1.5">
          <StatusBadge
            status={row.enabled ? "active" : "disconnected"}
            label={row.enabled ? "Enabled" : "Disabled"}
          />
          {isUserLocked(row) && (
            <span title="Account is locked out — open the user to unlock">
              <StatusBadge
                status="error"
                label="Locked"
                icon={<Lock className="h-3 w-3" />}
              />
            </span>
          )}
        </div>
      ),
    },
    {
      key: "lastLogin",
      header: "Last Login",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {formatRelativeTime(row.lastLogin)}
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => onEdit(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Edit user"
          >
            <Pencil className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => onResetPassword(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Reset password"
          >
            <RotateCcw className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => onDelete(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete user"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
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
