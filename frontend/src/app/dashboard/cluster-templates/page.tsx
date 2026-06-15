'use client';

/**
 * Cluster Templates — top-level list page.
 *
 * A cluster template captures the "shape" of a managed cluster: environment
 * tag, labels, tools (with presets + values overrides), default project
 * policy, and a registration policy. New clusters can be bootstrapped from
 * a template to inherit all of the above.
 *
 * The list is read-gated on `cluster_templates:read` and the
 * create/edit/delete actions are gated on `cluster_templates:write`. When
 * the user lacks read we still mount the page — we just render an explainer
 * instead of the list, so the sidebar link remains a stable target.
 */
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { Plus, Trash2, Layers } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { LoadingState, PermissionState } from '@/components/ui/empty-state';
import { useCurrentUser } from '@/lib/hooks';
import {
  useClusterTemplates,
  useDeleteClusterTemplate,
  canReadClusterTemplates,
  canWriteClusterTemplates,
} from '@/components/projects/hooks';
import { formatRelativeTime } from '@/lib/utils';
import type { ClusterTemplate } from '@/lib/api/project-detail';

export default function ClusterTemplatesPage() {
  const router = useRouter();
  const { data: user } = useCurrentUser();
  const canRead = canReadClusterTemplates(user);
  const canWrite = canWriteClusterTemplates(user);

  const { data, isLoading } = useClusterTemplates();
  const deleteMutation = useDeleteClusterTemplate();

  const templates = data?.data || [];

  if (!canRead) {
    return (
      <div className="space-y-4">
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Cluster Templates</h1>
        <PermissionState
          permission="cluster_templates:read"
          description={
            <>
              You need <span className="font-mono">cluster_templates:read</span> to view templates.
              Ask an administrator to grant the role.
            </>
          }
          className="rounded-lg border border-border bg-muted/30 p-6"
        />
      </div>
    );
  }

  const columns: Column<ClusterTemplate>[] = [
    {
      key: 'name',
      header: 'Template',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Layers className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium text-foreground">{row.displayName}</p>
            <p className="text-xs text-muted-foreground font-mono">{row.name}</p>
          </div>
        </div>
      ),
    },
    {
      key: 'description',
      header: 'Description',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground truncate max-w-[320px] block">
          {row.description || '—'}
        </span>
      ),
      sortable: false,
    },
    {
      key: 'environment',
      header: 'Environment',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.spec.environment}
        </span>
      ),
      sortAccessor: (row) => row.spec.environment,
    },
    {
      key: 'clusters',
      header: 'Clusters bound',
      accessor: (row) => (
        <span className="text-sm tabular-nums">{row.clustersBound}</span>
      ),
      sortAccessor: (row) => row.clustersBound,
      align: 'center',
    },
    {
      key: 'createdBy',
      header: 'Created by',
      accessor: (row) => (
        <div className="text-xs text-muted-foreground">
          <p>{row.createdBy || '—'}</p>
          <p>{formatRelativeTime(row.createdAt)}</p>
        </div>
      ),
      sortable: false,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1 justify-end" onClick={(e) => e.stopPropagation()}>
          {canWrite && (
            <button
              type="button"
              onClick={() => {
                if (
                  confirm(`Delete template "${row.displayName}"? This action cannot be undone.`)
                ) {
                  deleteMutation.mutate(row.id);
                }
              }}
              className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
              title="Delete template"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">
            Cluster Templates
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Reusable specs for bootstrapping managed clusters with the right tools, policy, and
            project defaults.
          </p>
        </div>
        {canWrite && (
          <Link
            href="/dashboard/cluster-templates/new"
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
          >
            <Plus className="h-4 w-4" />
            New template
          </Link>
        )}
      </div>

      {isLoading ? (
        <LoadingState title="Loading cluster templates" className="h-32 py-0" />
      ) : (
        <DataTable
          data={templates}
          columns={columns}
          keyExtractor={(row) => row.id}
          searchPlaceholder="Search templates..."
          loading={isLoading}
          emptyMessage="No cluster templates yet."
          onRowClick={(row) => router.push(`/dashboard/cluster-templates/${row.id}`)}
        />
      )}
    </div>
  );
}
