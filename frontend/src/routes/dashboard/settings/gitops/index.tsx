import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/gitops — GitOps cluster registration sources
 * (migration 060).
 *
 * Operators commit ClusterRegistration YAML to a tracked Git repo; the
 * sync worker reconciles every 60s. This page lists every source with
 * last-sync status; the detail page handles per-source actions.
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import {
  ArrowLeft,
  GitBranch,
  Plus,
  Trash2,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { formatRelativeTime } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  useDeleteGitOpsSource,
  useGitOpsSources,
} from '@/components/settings/hooks';
import type { GitOpsSource } from '@/lib/api/settings';

function GitOpsList() {
  const router = useRouter();
  const { data, isLoading } = useGitOpsSources();
  const del = useDeleteGitOpsSource();
  const [confirmDelete, setConfirmDelete] = useState<GitOpsSource | null>(null);

  const columns: Column<GitOpsSource>[] = [
    {
      key: 'name',
      header: 'Source',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <GitBranch className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium text-foreground">{row.name}</p>
            <p className="text-2xs font-mono text-muted-foreground">{row.branch}</p>
          </div>
        </div>
      ),
    },
    {
      key: 'repo_url',
      header: 'Repo',
      sortable: false,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground font-mono truncate max-w-[360px] block">
          {row.repo_url}
          {row.path_prefix ? ` · ${row.path_prefix}` : ''}
        </span>
      ),
    },
    {
      key: 'sync_mode',
      header: 'Mode',
      align: 'center',
      sortable: false,
      accessor: (row) => (
        <span className="text-xs font-mono uppercase text-muted-foreground">
          {row.sync_mode === 'manual'
            ? 'manual'
            : `every ${row.sync_interval_seconds}s`}
        </span>
      ),
    },
    {
      key: 'on_delete',
      header: 'On delete',
      align: 'center',
      sortable: false,
      accessor: (row) => (
        <span className="text-xs font-mono uppercase text-muted-foreground">
          {row.on_delete}
        </span>
      ),
    },
    {
      key: 'last_synced_at',
      header: 'Last sync',
      accessor: (row) => {
        if (row.last_error) {
          return <StatusBadge status="error" label="error" size="sm" />;
        }
        if (!row.last_synced_at) {
          return <span className="text-xs text-muted-foreground">Never synced</span>;
        }
        return (
          <span className="text-xs text-muted-foreground">
            {formatRelativeTime(row.last_synced_at)}
          </span>
        );
      },
    },
    {
      key: 'enabled',
      header: 'Enabled',
      align: 'center',
      sortable: false,
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? 'active' : 'inactive'}
          label={row.enabled ? 'on' : 'off'}
          size="sm"
        />
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
          title="Delete source"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      ),
    },
  ];

  return (
    <>
      <DataTable
        data={data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        onRowClick={(row) => router.push(`/dashboard/settings/gitops/${row.id}`)}
        emptyMessage="No GitOps sources configured"
        searchPlaceholder="Search sources..."
      />
      <ConfirmDialog
        open={!!confirmDelete}
        onClose={() => setConfirmDelete(null)}
        onConfirm={async () => {
          if (!confirmDelete) return;
          await del.mutateAsync(confirmDelete.id);
          setConfirmDelete(null);
        }}
        title="Delete GitOps source?"
        description={`This will remove "${confirmDelete?.name}" and stop syncing this repo. Cluster rows themselves are NOT deleted; the on_delete policy only applies on a per-tick missing-set comparison.`}
        confirmText="Delete"
        variant="destructive"
      />
    </>
  );
}

function GitOpsSourcesPage() {
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
        <div className="flex items-center justify-between">
          <div>
            <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
              Settings · GitOps
            </p>
            <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
              GitOps cluster registration
            </h1>
            <p className="text-sm text-muted-foreground mt-1">
              Operators commit ClusterRegistration YAML to a tracked repo;
              Astronomer reconciles every 60s.
            </p>
          </div>
          <Link
            href="/dashboard/settings/gitops/new"
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
          >
            <Plus className="h-4 w-4" />
            New source
          </Link>
        </div>
        <GitOpsList />
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/gitops/')({
  component: GitOpsSourcesPage,
});
