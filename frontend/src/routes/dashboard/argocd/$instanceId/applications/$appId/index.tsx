import { createFileRoute } from '@tanstack/react-router';

// Application detail. Shows:
//   - sync status + revision SHA
//   - Sync / Refresh / Hard Refresh buttons
//   - Tabs: Resources, History
//
// Live updates:
//   - The /applications/{id}/manifests/ payload changes whenever a sync
//     finishes; we re-fetch on cluster.k8s_changed (proxy event).
//   - The operations table for this app polls every 5s while a sync is
//     in flight (status pending/running) and falls back to 30s otherwise.

import { useMemo, useState } from 'react';
import { useParams, useRouter } from '@/lib/navigation';
import { useTabParam } from '@/lib/use-tab-param';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import {
  ArrowLeft,
  ChevronRight,
  ExternalLink,
  Loader2,
  RefreshCw,
  RotateCcw,
  Rocket,
  SearchCheck,
  Zap,
} from 'lucide-react';
import api, {
  getArgoAppHistory,
  getArgoAppManifests,
  refreshArgoApplicationById,
  listArgoOperations,
  listArgoApplicationsLive,
  syncArgoApplicationById,
} from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import { liveFallback } from '@/lib/live/status-store';
import { useLiveQueryInvalidation } from '@/lib/live/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { SyncStatusBadge, HealthStatusBadge } from '@/components/argocd/sync-status-badge';
import { SyncAppDialog } from '@/components/argocd/sync-app-dialog';
import { flattenArgoApp, shortRepo } from '@/components/argocd/argo-utils';
import { formatRelativeTime } from '@/lib/utils';
import type {
  ArgoAppHistoryEntry,
  ArgoLiveApplication,
  ArgoOperation,
} from '@/types';

interface DBApp {
  id: string;
  name: string;
  argocdInstanceId: string;
  syncStatus?: string;
  healthStatus?: string;
}

function ApplicationDetailPage() {
  const params = useParams();
  const router = useRouter();
  const instanceId = params.instanceId as string;
  const appId = params.appId as string;

  const queryClient = useQueryClient();
  const [showSync, setShowSync] = useState(false);
  const [tab, setTab] = useTabParam(['resources', 'history', 'events'] as const, 'resources');

  // Resolve the DB app to learn its name (needed for the live-app lookup).
  const { data: dbApp } = useQuery({
    queryKey: queryKeys.argocd.dbApp(appId),
    queryFn: async () => {
      const res = await api.get<DBApp>(`/argocd/applications/${appId}`);
      return res.data;
    },
    enabled: !!appId,
    refetchInterval: liveFallback(30000),
  });

  // Live application from upstream — pull the full list, find by name.
  // (The backend doesn't expose a single live-app endpoint; the list query
  // is cheap and ArgoCD already paginates.)
  const { data: liveApps } = useQuery({
    queryKey: queryKeys.argocd.liveApps(instanceId),
    queryFn: () => listArgoApplicationsLive(instanceId),
    refetchInterval: 15000,
  });
  const liveApp: ArgoLiveApplication | undefined = useMemo(
    () => liveApps?.find((a) => a.metadata?.name === dbApp?.name),
    [liveApps, dbApp?.name],
  );
  const flat = liveApp ? flattenArgoApp(liveApp) : null;

  // Operations specific to this app — used to drive the convergence poll.
  const opsForApp = useQuery({
    queryKey: queryKeys.argocd.appOperations(appId),
    queryFn: () => listArgoOperations({ targetType: 'application', targetKey: appId, limit: 25 }),
    // `argocd.changed` (scope: operation) drives freshness while the stream
    // is open; both cadences are the stream-down fallback (P4.5).
    refetchInterval: (q) => {
      const ops = (q.state.data as ArgoOperation[] | undefined) ?? [];
      const inFlight = ops.some((o) => o.status === 'pending' || o.status === 'running');
      return inFlight ? liveFallback(5000)() : liveFallback(20000)();
    },
  });

  useLiveQueryInvalidation(
    ['cluster.k8s_changed', 'cluster.connected', 'cluster.disconnected'],
    [
      queryKeys.argocd.liveApps(instanceId),
      queryKeys.argocd.appOperations(appId),
      queryKeys.argocd.appManifests(appId),
      queryKeys.argocd.appHistory(appId),
    ],
  );

  const refresh = useMutation({
    mutationFn: (hard: boolean) => refreshArgoApplicationById(appId, hard),
    onSuccess: (_data, hard) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.liveApps(instanceId) });
      toastSuccess(hard ? 'Hard refresh requested' : 'Refresh requested');
    },
    onError: (err: Error) => toastApiError('Refresh failed', err),
  });

  if (!dbApp) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <button
          onClick={() => router.push(`/dashboard/argocd/${instanceId}`)}
          className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3 w-3" />
          Instance overview
        </button>
        <div className="mt-2 flex items-start justify-between gap-4 flex-wrap">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span className="font-mono">{flat?.project ?? 'default'}</span>
              <ChevronRight className="h-3 w-3" />
              <span>{flat?.destinationNamespace || flat?.namespace || dbApp.name}</span>
            </div>
            <h1 className="text-2xl font-semibold text-foreground tracking-tight font-mono">
              {dbApp.name}
            </h1>
            {flat?.repoURL && (
              <a
                href={flat.repoURL}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground font-mono mt-1"
              >
                {shortRepo(flat.repoURL)}
                <ExternalLink className="h-3 w-3" />
              </a>
            )}
          </div>

          <div className="flex items-center gap-2">
            {flat && <SyncStatusBadge syncStatus={flat.syncStatus} />}
            {flat && <HealthStatusBadge healthStatus={flat.healthStatus} />}
            {flat?.revision && (
              <span className="px-2 py-1 rounded-md bg-muted text-xs font-mono text-muted-foreground">
                {flat.revision}
              </span>
            )}
          </div>
        </div>
      </div>

      <div className="flex items-center gap-2 flex-wrap">
        <button
          onClick={() => setShowSync(true)}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Rocket className="h-4 w-4" />
          Sync
        </button>
        <button
          onClick={() => refresh.mutate(false)}
          disabled={refresh.isPending}
          className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-border text-sm
            text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          {refresh.isPending && refresh.variables === false ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <RefreshCw className="h-3.5 w-3.5" />
          )}
          Refresh
        </button>
        <button
          onClick={() => refresh.mutate(true)}
          disabled={refresh.isPending}
          className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-border text-sm
            text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          {refresh.isPending && refresh.variables === true ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Zap className="h-3.5 w-3.5" />
          )}
          Hard Refresh
        </button>
      </div>

      <div className="flex items-center gap-1 border-b border-border">
        {(['resources', 'history', 'events'] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`inline-flex items-center px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors capitalize ${
              tab === t
                ? 'border-primary text-foreground'
                : 'border-transparent text-muted-foreground hover:text-foreground'
            }`}
          >
            {t}
          </button>
        ))}
      </div>

      {tab === 'resources' && <ResourcesTab appId={appId} />}
      {tab === 'history' && <HistoryTab appId={appId} />}
      {tab === 'events' && <EventsTab ops={opsForApp.data ?? []} loading={opsForApp.isLoading} />}

      {showSync && (
        <SyncAppDialog
          appId={appId}
          appName={dbApp.name}
          defaultRevision={flat?.targetRevision}
          onClose={() => setShowSync(false)}
        />
      )}
    </div>
  );
}

// ============================================================

function ResourcesTab({ appId }: { appId: string }) {
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.argocd.appManifests(appId),
    queryFn: () => getArgoAppManifests(appId),
    // KEEP (D8): Argo-side truth with no event source at content
    // granularity — deliberately NOT converted to liveFallback.
    refetchInterval: 30000,
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-32">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  const manifests = (data?.manifests as string[] | undefined) ?? [];
  if (manifests.length === 0) {
    return <p className="text-sm text-muted-foreground py-8 text-center">No manifests rendered yet. Try a Refresh.</p>;
  }
  return (
    <div className="space-y-2">
      {manifests.map((m, i) => (
        <details
          key={i}
          className="rounded-md border border-border bg-card open:bg-muted/40 transition-colors"
        >
          <summary className="px-3 py-2 cursor-pointer text-xs font-mono text-muted-foreground hover:text-foreground">
            Resource #{i + 1}
          </summary>
          <pre className="px-3 py-2 text-xs font-mono text-muted-foreground overflow-x-auto whitespace-pre-wrap">
            {m}
          </pre>
        </details>
      ))}
    </div>
  );
}

// ============================================================

function HistoryTab({ appId }: { appId: string }) {
  const queryClient = useQueryClient();
  const [rollbackTarget, setRollbackTarget] = useState<ArgoAppHistoryEntry | null>(null);
  const { data: history = [], isLoading } = useQuery({
    queryKey: queryKeys.argocd.appHistory(appId),
    queryFn: () => getArgoAppHistory(appId),
    // KEEP (D8): Argo-side truth with no event source at content
    // granularity — deliberately NOT converted to liveFallback.
    refetchInterval: 60000,
  });

  const syncRevision = useMutation({
    mutationFn: ({ revision, dryRun }: { revision: string; dryRun: boolean }) =>
      syncArgoApplicationById(appId, { revision, dryRun, prune: true }),
    onSuccess: (_op, variables) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.appOperations(appId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.liveApps() });
      toastSuccess(variables.dryRun ? 'Rollback preview queued' : 'Rollback queued');
      setRollbackTarget(null);
    },
    onError: (err: Error) => toastApiError('Rollback failed', err),
  });

  const columns: Column<ArgoAppHistoryEntry>[] = [
    {
      key: 'id',
      header: 'ID',
      accessor: (row) => <span className="font-mono text-xs text-muted-foreground">#{row.id}</span>,
      sortAccessor: (row) => row.id,
      align: 'center',
    },
    {
      key: 'rev',
      header: 'Revision',
      accessor: (row) => <span className="font-mono text-xs text-foreground">{(row.revision ?? '').slice(0, 12)}</span>,
    },
    {
      key: 'when',
      header: 'Deployed',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.deployedAt ? formatRelativeTime(row.deployedAt) : '—'}
        </span>
      ),
      sortAccessor: (row) => row.deployedAt ?? '',
    },
    {
      key: 'src',
      header: 'Source',
      accessor: (row) => (
        <span className="text-xs font-mono text-muted-foreground">
          {row.source?.repoURL ? shortRepo(row.source.repoURL) : '—'}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      align: 'center',
      accessor: (row) => {
        const revision = row.revision?.trim();
        if (!revision) return null;
        return (
          <div className="flex items-center justify-end gap-1">
            <button
              type="button"
              title="Preview rollback"
              onClick={(event) => {
                event.stopPropagation();
                syncRevision.mutate({ revision, dryRun: true });
              }}
              disabled={syncRevision.isPending}
              className="inline-flex h-7 w-7 items-center justify-center rounded border border-border text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50"
            >
              <SearchCheck className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              title="Rollback to revision"
              onClick={(event) => {
                event.stopPropagation();
                setRollbackTarget(row);
              }}
              disabled={syncRevision.isPending}
              className="inline-flex h-7 w-7 items-center justify-center rounded border border-border text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50"
            >
              <RotateCcw className="h-3.5 w-3.5" />
            </button>
          </div>
        );
      },
    },
  ];
  const rollbackRevision = rollbackTarget?.revision?.trim() ?? '';
  const confirmValue = rollbackRevision.slice(0, 12);
  return (
    <>
      <DataTable
        data={history}
        columns={columns}
        keyExtractor={(row) => String(row.id)}
        searchable={false}
        loading={isLoading}
        emptyMessage="No deploy history yet."
      />
      <ConfirmDialog
        open={!!rollbackTarget}
        onClose={() => setRollbackTarget(null)}
        onConfirm={() => {
          if (rollbackRevision) syncRevision.mutate({ revision: rollbackRevision, dryRun: false });
        }}
        title="Rollback Application"
        description={`Queue an audited ArgoCD sync to revision ${confirmValue}. Run the preview action first when you need a dry-run.`}
        confirmText="Rollback"
        confirmValue={confirmValue}
        loading={syncRevision.isPending}
      />
    </>
  );
}

// ============================================================

function EventsTab({ ops, loading }: { ops: ArgoOperation[]; loading: boolean }) {
  const columns: Column<ArgoOperation>[] = [
    {
      key: 'op',
      header: 'Operation',
      accessor: (row) => <span className="text-sm text-foreground capitalize">{row.operationType}</span>,
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={mapOperationStatus(row.status)} label={titleCase(row.status)} />,
    },
    {
      key: 'attempt',
      header: 'Attempts',
      accessor: (row) => <span className="tabular-nums text-sm text-muted-foreground">{row.attemptCount}</span>,
      align: 'center',
    },
    {
      key: 'msg',
      header: 'Message',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground line-clamp-1" title={row.errorMessage || ''}>
          {row.errorMessage || '—'}
        </span>
      ),
    },
    {
      key: 'started',
      header: 'Started',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.startedAt ? formatRelativeTime(row.startedAt) : '—'}
        </span>
      ),
    },
  ];
  return (
    <DataTable
      data={ops}
      columns={columns}
      keyExtractor={(row) => row.id}
      searchable={false}
      loading={loading}
      emptyMessage="No sync events for this application yet."
    />
  );
}

function mapOperationStatus(s: string): string {
  switch (s) {
    case 'completed':
      return 'healthy';
    case 'running':
      return 'progressing';
    case 'pending':
      return 'connecting';
    case 'failed':
    case 'superseded':
      return 'error';
    default:
      return 'unknown';
  }
}

function titleCase(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

export const Route = createFileRoute('/dashboard/argocd/$instanceId/applications/$appId/')({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: ApplicationDetailPage,
});
