import { createFileRoute } from '@tanstack/react-router';
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
import { useState } from 'react';
import { Link } from '@/lib/link';
import {
  ArrowLeft,
  Loader2,
  Plus,
  Trash2,
  Users,
  X,
} from 'lucide-react';
import { toastError } from '@/lib/toast';
import { useAppForm, useStore } from '@/lib/form';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { cn, formatRelativeTime } from '@/lib/utils';
import { useDexConnectors } from '@/components/auth/hooks';
import { useGlobalRoles, useClusters, useProjects } from '@/lib/hooks';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  useCreateGroupMapping,
  useDeleteGroupMapping,
  useGroupMappings,
} from '@/components/settings/hooks';
import type { GroupMapping, GroupScope } from '@/lib/api/settings';

function GroupMappingsTable() {
  const { data, isLoading } = useGroupMappings();
  const del = useDeleteGroupMapping();
  const [showCreate, setShowCreate] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<GroupMapping | null>(null);

  const columns: Column<GroupMapping>[] = [
    {
      key: 'connector',
      header: 'Connector',
      accessor: (row) => (
        <span className="text-xs font-mono px-2 py-0.5 rounded bg-muted text-muted-foreground">
          {row.connector || '(any)'}
        </span>
      ),
    },
    {
      key: 'groupName',
      header: 'Group',
      accessor: (row) => <span className="text-sm font-mono text-foreground">{row.groupName}</span>,
    },
    {
      key: 'scope',
      header: 'Scope',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded border border-border text-foreground capitalize">
          {row.scope}
        </span>
      ),
    },
    {
      key: 'role',
      header: 'Role',
      accessor: (row) => <span className="text-sm text-foreground">{row.role}</span>,
    },
    {
      key: 'target',
      header: 'Target',
      accessor: (row) =>
        row.scope === 'global' ? (
          <span className="text-xs text-muted-foreground italic">global</span>
        ) : (
          <span className="text-xs font-mono text-muted-foreground">
            {row.targetDisplay ?? row.target ?? '--'}
          </span>
        ),
    },
    {
      key: 'createdAt',
      header: 'Created',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>
      ),
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      accessor: (row) => (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            setConfirmDelete(row);
          }}
          className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
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
        <button
          type="button"
          onClick={() => setShowCreate(true)}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          New mapping
        </button>
      </div>
      <DataTable
        data={data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        emptyMessage="No group mappings configured"
        searchPlaceholder="Search by group or role..."
      />
      <CreateGroupMappingModal open={showCreate} onClose={() => setShowCreate(false)} />
      <ConfirmDialog
        open={!!confirmDelete}
        onClose={() => setConfirmDelete(null)}
        onConfirm={async () => {
          if (!confirmDelete) return;
          await del.mutateAsync(confirmDelete.id);
          setConfirmDelete(null);
        }}
        title="Delete group mapping?"
        description={`Members of "${confirmDelete?.groupName}" from "${confirmDelete?.connector || 'any connector'}" will lose the "${confirmDelete?.role}" role on their next sync.`}
        confirmText="Delete"
        variant="destructive"
      />
    </>
  );
}

function CreateGroupMappingModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const create = useCreateGroupMapping();
  const { data: connectors } = useDexConnectors();
  const { data: roles } = useGlobalRoles();
  const { data: clustersData } = useClusters();
  const { data: projectsData } = useProjects();

  const form = useAppForm({
    defaultValues: {
      connector: '',
      groupName: '',
      scope: 'global' as GroupScope,
      role: '',
      target: '',
    },
    validators: {
      // Old checks (imperative, pre-submit): group name required, role
      // required, target required for scoped mappings → ported 1:1 as a
      // form-level onSubmit validator; same messages, same order.
      onSubmit: ({ value }) =>
        !value.groupName
          ? 'Group name is required'
          : !value.role
            ? 'Role is required'
            : value.scope !== 'global' && !value.target
              ? 'Target is required for scoped mappings'
              : undefined,
    },
    // Same UX as before: the failed check surfaces as a toast, not inline.
    onSubmitInvalid: ({ formApi }) => {
      const err = formApi.state.errors.find((e) => typeof e === 'string');
      if (err) toastError(err);
    },
    onSubmit: async ({ value }) => {
      try {
        await create.mutateAsync({
          ...(value.connector ? { connector_id: value.connector } : {}),
          group_name: value.groupName,
          scope: value.scope,
          role_id: value.role,
          ...(value.scope === 'cluster' ? { cluster_id: value.target } : {}),
          ...(value.scope === 'project' ? { project_id: value.target } : {}),
        });
        onClose();
        form.reset();
      } catch {
        // toast handled
      }
    },
  });
  const scope = useStore(form.store, (s) => s.values.scope);

  if (!open) return null;

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl p-6 space-y-5">
        <div className="flex items-center justify-between">
          <h3 className="text-lg font-semibold text-foreground">New group mapping</h3>
          <button
            type="button"
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Connector</label>
          <form.Field name="connector">
            {(field) => (
              <select
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
              >
                <option value="">Any connector</option>
                {(connectors ?? []).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.displayName} ({c.type})
                  </option>
                ))}
              </select>
            )}
          </form.Field>
        </div>

        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Group name</label>
          <form.Field name="groupName">
            {(field) => (
              <input
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="platform-admins"
                className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                autoFocus
              />
            )}
          </form.Field>
        </div>

        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Scope</label>
          <form.Field name="scope">
            {(field) => (
              <select
                value={field.state.value}
                onChange={(e) => {
                  field.handleChange(e.target.value as GroupScope);
                  form.setFieldValue('target', '');
                }}
                onBlur={field.handleBlur}
                className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
              >
                <option value="global">Global</option>
                <option value="cluster">Cluster</option>
                <option value="project">Project</option>
              </select>
            )}
          </form.Field>
        </div>

        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Role</label>
          <form.Field name="role">
            {(field) => (
              <select
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
              >
                <option value="" disabled>
                  Pick a role…
                </option>
                {(roles ?? []).map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.displayName} ({r.name})
                  </option>
                ))}
              </select>
            )}
          </form.Field>
        </div>

        {scope !== 'global' && (
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground capitalize">{scope} target</label>
            <form.Field name="target">
              {(field) => (
                <select
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                >
                  <option value="" disabled>
                    Pick a {scope}…
                  </option>
                  {scope === 'cluster' &&
                    (clustersData?.data ?? []).map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name}
                      </option>
                    ))}
                  {scope === 'project' &&
                    (projectsData?.data ?? []).map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.displayName} ({p.name})
                      </option>
                    ))}
                </select>
              )}
            </form.Field>
          </div>
        )}

        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => void form.handleSubmit()}
            disabled={create.isPending}
            className={cn(
              'inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50',
            )}
          >
            {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create mapping
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}

function GroupMappingsPage() {
  return (
    <SettingsAuthGate>
      <div className="space-y-6">
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>

        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Group mappings</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1 flex items-center gap-2">
            <Users className="h-5 w-5 text-muted-foreground" />
            SSO group mappings
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Bind an SSO group to a platform role, optionally scoped to one cluster or project.
          </p>
        </div>

        <GroupMappingsTable />
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/group-mappings/')({
  component: GroupMappingsPage,
});
