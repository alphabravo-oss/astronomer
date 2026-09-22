import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/group-mappings — SSO group → RBAC role bindings.
 *
 * Each row maps `(connector, group_name)` to a role at a given scope:
 *   - `global`  — role applies platform-wide.
 *   - `cluster` — role applies inside one cluster (target = cluster UUID).
 *   - `project` — role applies inside one project (target = project name).
 *
 * Connectors come from the Dex connector list; an empty / "any" value
 * matches mappings regardless of source. Roles come from `useGlobalRoles`.
 */
import { useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, Plus, Trash2, Users } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader, PageShell } from "@/components/ui/page";
import { formatRelativeTime } from "@/lib/utils";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { useDeleteGroupMapping, useGroupMappings } from "@/components/settings/hooks";
import type { GroupMappingView } from "@/lib/api/settings";

function GroupMappingsTable() {
  const navigate = useNavigate();
  const { data, isLoading } = useGroupMappings();
  const del = useDeleteGroupMapping();
  const [confirmDelete, setConfirmDelete] = useState<GroupMappingView | null>(
    null,
  );

  const columns: Column<GroupMappingView>[] = [
    {
      key: "connector",
      header: "Connector",
      accessor: (row) => (
        <span className="text-xs font-mono px-2 py-0.5 rounded-sm bg-muted text-muted-foreground">
          {row.connector || "(any)"}
        </span>
      ),
    },
    {
      key: "groupName",
      header: "Group",
      accessor: (row) => (
        <span className="text-sm font-mono text-foreground">
          {row.groupName}
        </span>
      ),
    },
    {
      key: "scope",
      header: "Scope",
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded-sm border border-border text-foreground capitalize">
          {row.scope}
        </span>
      ),
    },
    {
      key: "role",
      header: "Role",
      accessor: (row) => (
        <span className="text-sm text-foreground">{row.role}</span>
      ),
    },
    {
      key: "target",
      header: "Target",
      accessor: (row) =>
        row.scope === "global" ? (
          <span className="text-xs text-muted-foreground italic">global</span>
        ) : (
          <span className="text-xs font-mono text-muted-foreground">
            {row.targetDisplay ?? row.target ?? "--"}
          </span>
        ),
    },
    {
      key: "createdAt",
      header: "Created",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {formatRelativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      sortable: false,
      accessor: (row) => (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            setConfirmDelete(row);
          }}
          className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
          title="Delete mapping"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      ),
    },
  ];

  return (
    <>
      <div className="flex items-center justify-end">
        <ActionButton
          type="button"
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() =>
            void navigate({ to: "/dashboard/settings/group-mappings/new" })
          }
        >
          New mapping
        </ActionButton>
      </div>
      <DataTable
        data={data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        emptyState={{
          title: "No group mappings configured",
          description: "Create the first item to configure this feature.",
        }}
        searchPlaceholder="Search by group or role..."
      />
      <ConfirmDialog
        open={!!confirmDelete}
        onClose={() => setConfirmDelete(null)}
        onConfirm={async () => {
          if (!confirmDelete) return;
          await del.mutateAsync(confirmDelete.id);
          setConfirmDelete(null);
        }}
        title="Delete group mapping?"
        description={`Members of "${confirmDelete?.groupName}" from "${confirmDelete?.connector || "any connector"}" will lose the "${confirmDelete?.role}" role on their next sync.`}
        confirmText="Delete"
        variant="destructive"
      />
    </>
  );
}

function GroupMappingsPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <RouterLink
          to="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </RouterLink>
        <PageHeader
          title={
            <span className="inline-flex items-center gap-2">
              <Users className="h-5 w-5 text-muted-foreground" />
              SSO group mappings
            </span>
          }
          description="Bind an SSO group to a platform role, optionally scoped to one cluster or project."
        />
        <GroupMappingsTable />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/group-mappings/")({
  component: GroupMappingsPage,
});
