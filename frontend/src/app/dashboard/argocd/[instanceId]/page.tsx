'use client';

// ArgoCD instance detail page. Tabbed view:
//
//   Overview         live health + counts
//   Applications     live list from upstream + create / sync per row
//   AppProjects      list / create / delete
//   ApplicationSets  list / delete (create lives at .../applicationsets/new/)
//   Clusters         our managed clusters registered into this ArgoCD
//   Repos            list / add / test / delete
//   Operations       reconciler operation history (DB-backed)
//
// Live updates are wired via `useLiveQueryInvalidation` against
// cluster.* events — see the per-tab keys below. There's no `argocd:operation`
// bus event today; the prompt asked us to use `cluster.k8s_changed` as a
// proxy and that's what we do.

import { useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastError, toastSuccess } from '@/lib/toast';
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  CalendarClock,
  CheckCircle2,
  ExternalLink,
  GitBranch,
  GitFork,
  Layers,
  ListChecks,
  Loader2,
  Plus,
  Rocket,
  RotateCcw,
  Server,
  Trash2,
  XCircle,
  RefreshCw,
} from 'lucide-react';

import {
  getArgoInstanceB1,
  getArgoInstanceHealth,
  getArgoOrphanReport,
  listArgoCachedApplications,
  listArgoApplicationsLive,
  listArgoApplicationSets,
  listArgoManagedClusters,
  listArgoOperations,
  listArgoProjects,
  listArgoRepos,
  deleteArgoApplicationByName,
  deleteArgoApplicationSet,
  deleteArgoProject,
  deleteArgoRepo,
  refreshArgoManagedClusterLabels,
  unregisterArgoManagedCluster,
} from '@/lib/api';
import { queryKeys, useClusters } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { ActionMenu } from '@/components/ui/action-menu';
import { EmptyState } from '@/components/ui/empty-state';
import { SyncStatusBadge, HealthStatusBadge } from '@/components/argocd/sync-status-badge';
import { CreateApplicationDialog } from '@/components/argocd/create-application-dialog';
import { CreateProjectDialog } from '@/components/argocd/create-project-dialog';
import { AddRepoDialog } from '@/components/argocd/add-repo-dialog';
import { RegisterManagedClusterDialog } from '@/components/argocd/register-managed-cluster-dialog';
import { SyncWindowsDialog } from '@/components/argocd/sync-windows-dialog';
import { flattenArgoApp, shortRepo } from '@/components/argocd/argo-utils';
import { formatRelativeTime } from '@/lib/utils';
import type {
  ArgoApplicationSet,
  ArgoLiveApplication,
  ArgoManagedCluster,
  ArgoOperation,
  ArgoProject,
  ArgoRepository,
  Cluster,
} from '@/types';

type TabId = 'overview' | 'apps' | 'projects' | 'appsets' | 'clusters' | 'repos' | 'operations';

// isBrowserReachable returns false for cluster-internal URLs (`*.svc.cluster.local`,
// `localhost`, RFC1918 IPs) — those are valid for the in-cluster Astronomer
// server to reach but a browser can't follow them. Used to swap a clickable
// link for an explanatory chip.
function isBrowserReachable(url: string): boolean {
  if (!url) return false;
  try {
    const u = new URL(url);
    const h = u.hostname;
    if (h.endsWith('.svc.cluster.local') || h.endsWith('.svc')) return false;
    if (h === 'localhost' || h === '127.0.0.1' || h === '::1') return false;
    if (/^10\./.test(h) || /^192\.168\./.test(h)) return false;
    if (/^172\.(1[6-9]|2\d|3[0-1])\./.test(h)) return false;
    return true;
  } catch {
    return false;
  }
}

const TABS: { id: TabId; label: string; icon: typeof GitBranch }[] = [
  { id: 'overview', label: 'Overview', icon: Activity },
  { id: 'apps', label: 'Applications', icon: Rocket },
  { id: 'projects', label: 'AppProjects', icon: Layers },
  { id: 'appsets', label: 'ApplicationSets', icon: ListChecks },
  { id: 'clusters', label: 'Clusters', icon: Server },
  { id: 'repos', label: 'Repositories', icon: GitFork },
  { id: 'operations', label: 'Operations', icon: RefreshCw },
];

export default function InstanceDetailPage() {
  const params = useParams();
  const router = useRouter();
  const instanceId = params.instanceId as string;

  const [tab, setTab] = useState<TabId>('overview');

  const { data: instance, isLoading: instanceLoading } = useQuery({
    queryKey: queryKeys.argocd.instance(instanceId),
    queryFn: () => getArgoInstanceB1(instanceId),
    enabled: !!instanceId,
    refetchInterval: 30000,
  });

  if (instanceLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!instance) {
    return (
      <EmptyState
        icon={GitBranch}
        title="ArgoCD instance not found"
        description="The instance may have been removed or you may not have access to it."
        actionLabel="Back to ArgoCD"
        actionHref="/dashboard/argocd"
      />
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <button
            onClick={() => router.push('/dashboard/argocd')}
            className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors mb-2"
          >
            <ArrowLeft className="h-3 w-3" />
            All instances
          </button>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight truncate">
            {instance.name}
          </h1>
          <p className="text-xs text-muted-foreground font-mono break-all">
            {instance.apiUrl}
            {!isBrowserReachable(instance.apiUrl) && (
              <span
                className="ml-2 text-2xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground"
                title="This URL resolves only inside the cluster — Astronomer reaches it via its in-cluster network. To open the ArgoCD UI in your browser, expose argocd-server via an Ingress/HTTPRoute on your management cluster."
              >
                in-cluster
              </span>
            )}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <a
            href="/argocd/applications"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm font-medium hover:bg-accent transition-colors"
          >
            <ExternalLink className="h-3.5 w-3.5" />
            Open ArgoCD UI
          </a>
          <StatusBadge
            status={instance.isHealthy ? 'healthy' : 'unhealthy'}
            label={instance.isHealthy ? 'Healthy' : 'Unhealthy'}
            size="lg"
          />
        </div>
      </div>

      {/* Tab strip */}
      <div className="flex items-center gap-1 border-b border-border overflow-x-auto">
        {TABS.map((t) => {
          const Icon = t.icon;
          const active = tab === t.id;
          return (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={`inline-flex items-center gap-2 px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors whitespace-nowrap ${
                active
                  ? 'border-primary text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              }`}
            >
              <Icon className="h-4 w-4" />
              {t.label}
            </button>
          );
        })}
      </div>

      <div>
        {tab === 'overview' && <OverviewTab instanceId={instanceId} />}
        {tab === 'apps' && <ApplicationsTab instanceId={instanceId} />}
        {tab === 'projects' && <ProjectsTab instanceId={instanceId} />}
        {tab === 'appsets' && <ApplicationSetsTab instanceId={instanceId} />}
        {tab === 'clusters' && <ClustersTab instanceId={instanceId} />}
        {tab === 'repos' && <ReposTab instanceId={instanceId} />}
        {tab === 'operations' && <OperationsTab instanceId={instanceId} />}
      </div>
    </div>
  );
}

// ============================================================
// Overview tab
// ============================================================

function OverviewTab({ instanceId }: { instanceId: string }) {
  const { data: health } = useQuery({
    queryKey: queryKeys.argocd.instanceHealth(instanceId),
    queryFn: () => getArgoInstanceHealth(instanceId),
    refetchInterval: 30000,
  });
  const { data: apps } = useQuery({
    queryKey: queryKeys.argocd.liveApps(instanceId),
    queryFn: () => listArgoApplicationsLive(instanceId),
    refetchInterval: 30000,
  });
  const { data: projects } = useQuery({
    queryKey: queryKeys.argocd.projects(instanceId),
    queryFn: () => listArgoProjects(instanceId),
    refetchInterval: 60000,
  });
  const { data: repos } = useQuery({
    queryKey: queryKeys.argocd.repos(instanceId),
    queryFn: () => listArgoRepos(instanceId),
    refetchInterval: 60000,
  });
  const { data: clusters } = useQuery({
    queryKey: queryKeys.argocd.managedClusters(instanceId),
    queryFn: () => listArgoManagedClusters(instanceId),
    refetchInterval: 60000,
  });
  const { data: orphanReport } = useQuery({
    queryKey: queryKeys.argocd.orphanReport(instanceId),
    queryFn: () => getArgoOrphanReport(instanceId),
    refetchInterval: 60000,
  });

  const stats = [
    { label: 'Health', value: health?.isHealthy ? 'Healthy' : 'Unhealthy' },
    { label: 'Applications', value: String(apps?.length ?? 0) },
    { label: 'AppProjects', value: String(projects?.length ?? 0) },
    { label: 'Repositories', value: String(repos?.length ?? 0) },
    { label: 'Managed Clusters', value: String(clusters?.length ?? 0) },
    { label: 'Orphaned Baseline Apps', value: String(orphanReport?.orphanApplicationCount ?? 0) },
  ];

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 lg:grid-cols-6 gap-3">
        {stats.map((s) => (
          <div
            key={s.label}
            className="rounded-lg border border-border bg-card p-4"
          >
            <p className="text-xs uppercase tracking-wide text-muted-foreground">{s.label}</p>
            <p className="mt-1 text-2xl font-semibold text-foreground tabular-nums">{s.value}</p>
          </div>
        ))}
      </div>
      {!!orphanReport?.orphanApplicationCount && (
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4">
          <div className="flex items-start gap-3">
            <AlertTriangle className="h-4 w-4 text-amber-500 mt-0.5 shrink-0" />
            <div className="min-w-0 flex-1">
              <div className="flex items-center justify-between gap-3">
                <h3 className="text-sm font-medium text-foreground">Orphaned baseline Applications</h3>
                <span className="text-xs text-muted-foreground">
                  {formatRelativeTime(orphanReport.generatedAt)}
                </span>
              </div>
              {orphanReport.liveError ? (
                <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">
                  Live ArgoCD scan failed; showing cached findings only.
                </p>
              ) : null}
              <div className="mt-3 divide-y divide-border/60">
                {orphanReport.orphanApplications.slice(0, 5).map((app) => (
                  <div key={`${app.name}:${app.destinationCluster}`} className="py-2 first:pt-0 last:pb-0">
                    <div className="flex items-center justify-between gap-3">
                      <p className="text-sm font-medium text-foreground truncate">{app.name}</p>
                      <div className="flex shrink-0 items-center gap-1">
                        <span className="text-2xs px-1.5 py-0.5 rounded bg-background text-muted-foreground">
                          {app.source}
                        </span>
                        <span className="text-2xs px-1.5 py-0.5 rounded bg-background text-muted-foreground">
                          {app.reason.replaceAll('_', ' ')}
                        </span>
                      </div>
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground font-mono truncate">
                      {app.destinationCluster || 'missing destination'}
                    </p>
                    {app.applicationSetName ? (
                      <p className="mt-1 text-xs text-muted-foreground font-mono truncate">
                        {app.applicationSetName}
                      </p>
                    ) : null}
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// ============================================================
// Applications tab (live from upstream)
// ============================================================

function ApplicationsTab({ instanceId }: { instanceId: string }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [showCreate, setShowCreate] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<ArgoLiveApplication | null>(null);

  const { data: apps = [], isLoading } = useQuery({
    queryKey: queryKeys.argocd.liveApps(instanceId),
    queryFn: () => listArgoApplicationsLive(instanceId),
    refetchInterval: 15000,
  });

  // Pull DB-backed list to get the per-app UUID — that's what the row click
  // and sync endpoints expect. The DB list is paginated, so cap to 200 per
  // page; a single instance with more than that is unusual.
  const { data: dbApps } = useQuery({
    queryKey: queryKeys.argocd.dbApps(instanceId),
    queryFn: () => listArgoCachedApplications({ instanceId, limit: 200 }),
    refetchInterval: 30000,
  });

  // Live re-render on upstream changes — using cluster.k8s_changed as the
  // proxy invalidator per the prompt.
  useLiveQueryInvalidation(
    ['cluster.k8s_changed', 'cluster.connected', 'cluster.disconnected'],
    [queryKeys.argocd.liveApps(instanceId)],
  );

  const idForApp = (name: string): string | undefined =>
    dbApps?.find((a) => a.name === name)?.id;

  const flat = apps.map(flattenArgoApp);

  const deleteApp = useMutation({
    mutationFn: () => deleteArgoApplicationByName(instanceId, deleteTarget!.metadata.name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.liveApps(instanceId) });
      toastSuccess('Application deleted');
      setDeleteTarget(null);
    },
    onError: (err: Error) => toastApiError('Delete failed', err),
  });

  type Row = ReturnType<typeof flattenArgoApp>;
  const columns: Column<Row>[] = [
    {
      key: 'name',
      header: 'Application',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          <p className="text-xs text-muted-foreground">{row.namespace || row.destinationNamespace}</p>
        </div>
      ),
      sortAccessor: (row) => row.name,
    },
    {
      key: 'project',
      header: 'Project',
      accessor: (row) => <span className="text-muted-foreground text-sm">{row.project}</span>,
      sortAccessor: (row) => row.project,
    },
    {
      key: 'sync',
      header: 'Sync',
      accessor: (row) => <SyncStatusBadge syncStatus={row.syncStatus} />,
    },
    {
      key: 'health',
      header: 'Health',
      accessor: (row) => <HealthStatusBadge healthStatus={row.healthStatus} />,
    },
    {
      key: 'repo',
      header: 'Source',
      accessor: (row) => (
        <span className="text-xs font-mono text-muted-foreground" title={row.repoURL}>
          {shortRepo(row.repoURL)}
        </span>
      ),
    },
    {
      key: 'rev',
      header: 'Revision',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.revision || row.targetRevision}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      align: 'center',
      accessor: (row) => {
        const id = idForApp(row.name);
        return (
          <ActionMenu
            items={[
              {
                label: 'Open',
                icon: <Rocket className="h-3.5 w-3.5" />,
                onClick: () => {
                  if (id) router.push(`/dashboard/argocd/${instanceId}/applications/${id}`);
                  else toastError('Application not yet indexed locally — try again in a moment');
                },
              },
              {
                label: 'Delete',
                icon: <Trash2 className="h-3.5 w-3.5" />,
                variant: 'destructive',
                separator: true,
                onClick: () => setDeleteTarget(row.raw),
              },
            ]}
          />
        );
      },
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-end">
        <button
          onClick={() => setShowCreate(true)}
          className="inline-flex items-center gap-2 h-9 px-3 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          New Application
        </button>
      </div>

      <DataTable
        data={flat}
        columns={columns}
        keyExtractor={(row) => row.name}
        onRowClick={(row) => {
          const id = idForApp(row.name);
          if (id) router.push(`/dashboard/argocd/${instanceId}/applications/${id}`);
        }}
        searchPlaceholder="Search applications..."
        loading={isLoading}
        emptyMessage="No applications yet. Click 'New Application' to deploy one."
      />

      {showCreate && (
        <CreateApplicationDialog instanceId={instanceId} onClose={() => setShowCreate(false)} />
      )}
      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteApp.mutate()}
        title="Delete Application"
        description={`This will delete the Application ${deleteTarget?.metadata.name ?? ''} and cascade-remove its rendered resources.`}
        confirmText="Delete"
        confirmValue={deleteTarget?.metadata.name}
        variant="destructive"
        loading={deleteApp.isPending}
      />
    </div>
  );
}

// ============================================================
// AppProjects tab
// ============================================================

function ProjectsTab({ instanceId }: { instanceId: string }) {
  const queryClient = useQueryClient();
  const [showCreate, setShowCreate] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<ArgoProject | null>(null);
  const [syncWindowTarget, setSyncWindowTarget] = useState<ArgoProject | null>(null);

  const { data: projects = [], isLoading } = useQuery({
    queryKey: queryKeys.argocd.projects(instanceId),
    queryFn: () => listArgoProjects(instanceId),
    refetchInterval: 30000,
  });

  const del = useMutation({
    mutationFn: () => deleteArgoProject(instanceId, deleteTarget!.metadata.name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.projects(instanceId) });
      toastSuccess('AppProject deleted');
      setDeleteTarget(null);
    },
    onError: (err: Error) => toastApiError('Delete failed', err),
  });

  const columns: Column<ArgoProject>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => <span className="font-mono text-sm text-foreground">{row.metadata.name}</span>,
      sortAccessor: (row) => row.metadata.name,
    },
    {
      key: 'description',
      header: 'Description',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.spec.description || '—'}</span>
      ),
    },
    {
      key: 'sources',
      header: 'Source Repos',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground tabular-nums">
          {(row.spec.sourceRepos ?? []).length}
        </span>
      ),
      align: 'center',
    },
    {
      key: 'destinations',
      header: 'Destinations',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground tabular-nums">
          {(row.spec.destinations ?? []).length}
        </span>
      ),
      align: 'center',
    },
    {
      key: 'syncWindows',
      header: 'Sync Windows',
      accessor: (row) => {
        const windows = row.spec.syncWindows ?? [];
        const denyCount = windows.filter((window) => window.kind === 'deny').length;
        const allowCount = windows.filter((window) => window.kind === 'allow').length;
        if (windows.length === 0) {
          return <span className="text-xs text-muted-foreground">0</span>;
        }
        return (
          <div className="flex items-center justify-center gap-1">
            {denyCount > 0 && (
              <span className="text-2xs px-1.5 py-0.5 rounded bg-destructive/10 text-destructive">
                {denyCount} deny
              </span>
            )}
            {allowCount > 0 && (
              <span className="text-2xs px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-600 dark:text-emerald-400">
                {allowCount} allow
              </span>
            )}
          </div>
        );
      },
      align: 'center',
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      align: 'center',
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: 'Sync windows',
              icon: <CalendarClock className="h-3.5 w-3.5" />,
              onClick: () => setSyncWindowTarget(row),
            },
            {
              label: 'Delete',
              icon: <Trash2 className="h-3.5 w-3.5" />,
              separator: true,
              variant: 'destructive',
              onClick: () => setDeleteTarget(row),
            },
          ]}
        />
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-end">
        <button
          onClick={() => setShowCreate(true)}
          className="inline-flex items-center gap-2 h-9 px-3 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          New AppProject
        </button>
      </div>

      <DataTable
        data={projects}
        columns={columns}
        keyExtractor={(row) => row.metadata.name}
        searchPlaceholder="Search projects..."
        loading={isLoading}
        emptyMessage="No AppProjects yet."
      />

      {showCreate && (
        <CreateProjectDialog instanceId={instanceId} onClose={() => setShowCreate(false)} />
      )}
      {syncWindowTarget && (
        <SyncWindowsDialog
          instanceId={instanceId}
          project={syncWindowTarget}
          onClose={() => setSyncWindowTarget(null)}
        />
      )}
      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => del.mutate()}
        title="Delete AppProject"
        description={`Applications referencing this project must be moved or deleted first.`}
        confirmText="Delete"
        confirmValue={deleteTarget?.metadata.name}
        variant="destructive"
        loading={del.isPending}
      />
    </div>
  );
}

// ============================================================
// ApplicationSets tab
// ============================================================

function ApplicationSetsTab({ instanceId }: { instanceId: string }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [deleteTarget, setDeleteTarget] = useState<ArgoApplicationSet | null>(null);

  const { data: sets = [], isLoading } = useQuery({
    queryKey: queryKeys.argocd.appsets(instanceId),
    queryFn: () => listArgoApplicationSets(instanceId),
    refetchInterval: 30000,
  });

  const del = useMutation({
    mutationFn: () => deleteArgoApplicationSet(instanceId, deleteTarget!.metadata.name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.appsets(instanceId) });
      toastSuccess('ApplicationSet deleted');
      setDeleteTarget(null);
    },
    onError: (err: Error) => toastApiError('Delete failed', err),
  });

  const columns: Column<ArgoApplicationSet>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => <span className="font-mono text-sm text-foreground">{row.metadata.name}</span>,
      sortAccessor: (row) => row.metadata.name,
    },
    {
      key: 'generators',
      header: 'Generators',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.spec.generators.map((g, i) => {
            const kind = g.list ? 'list' : g.clusters ? 'clusters' : g.git ? 'git' : 'other';
            return (
              <span
                key={i}
                className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground"
              >
                {kind}
              </span>
            );
          })}
        </div>
      ),
    },
    {
      key: 'project',
      header: 'Template Project',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.spec.template?.spec?.project ?? '—'}</span>
      ),
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      align: 'center',
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: 'Delete',
              icon: <Trash2 className="h-3.5 w-3.5" />,
              variant: 'destructive',
              onClick: () => setDeleteTarget(row),
            },
          ]}
        />
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-end">
        <button
          onClick={() =>
            router.push(`/dashboard/argocd/${instanceId}/applicationsets/new`)
          }
          className="inline-flex items-center gap-2 h-9 px-3 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          New ApplicationSet
        </button>
      </div>

      <DataTable
        data={sets}
        columns={columns}
        keyExtractor={(row) => row.metadata.name}
        searchPlaceholder="Search ApplicationSets..."
        loading={isLoading}
        emptyMessage="No ApplicationSets yet."
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => del.mutate()}
        title="Delete ApplicationSet"
        description="Generated Applications may also be removed depending on the set's preserveResourcesOnDeletion policy."
        confirmText="Delete"
        confirmValue={deleteTarget?.metadata.name}
        variant="destructive"
        loading={del.isPending}
      />
    </div>
  );
}

// ============================================================
// Clusters tab — list managed clusters + register new ones
// ============================================================

function ClustersTab({ instanceId }: { instanceId: string }) {
  const queryClient = useQueryClient();
  const { data: managed = [], isLoading } = useQuery({
    queryKey: queryKeys.argocd.managedClusters(instanceId),
    queryFn: () => listArgoManagedClusters(instanceId),
    refetchInterval: 30000,
  });
  const { data: allClusters } = useClusters({ pageSize: 200 });

  const [registerCluster, setRegisterCluster] = useState<Cluster | null>(null);
  const [unregisterTarget, setUnregisterTarget] = useState<ArgoManagedCluster | null>(null);

  useLiveQueryInvalidation(
    ['cluster.connected', 'cluster.disconnected', 'cluster.k8s_changed'],
    [queryKeys.argocd.managedClusters(instanceId)],
  );

  const unregister = useMutation({
    mutationFn: () => unregisterArgoManagedCluster(instanceId, unregisterTarget!.clusterId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.managedClusters(instanceId) });
      toastSuccess('Cluster unregistered from ArgoCD');
      setUnregisterTarget(null);
    },
    onError: (err: Error) => toastApiError('Unregister failed', err),
  });

  const refreshLabels = useMutation({
    mutationFn: (row: ArgoManagedCluster) => refreshArgoManagedClusterLabels(instanceId, row.clusterId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.managedClusters(instanceId) });
      toastSuccess('Cluster labels refreshed');
    },
    onError: (err: Error) => toastApiError('Refresh labels failed', err),
  });

  const managedById = new Map(managed.map((m) => [m.clusterId, m]));
  const all = allClusters?.data ?? [];

  return (
    <div className="space-y-6">
      <section>
        <h3 className="text-sm font-medium text-foreground mb-2">Registered with this ArgoCD</h3>
        <DataTable
          data={managed}
          columns={[
            {
              key: 'name',
              header: 'Cluster',
              accessor: (row) => {
                const c = all.find((c) => c.id === row.clusterId);
                return (
                  <div>
                    <p className="font-medium text-foreground">{c?.displayName ?? row.clusterSecretName ?? row.clusterId}</p>
                    <p className="text-xs text-muted-foreground font-mono">
                      {row.labels?.['astronomer.io/cluster-id'] ?? row.clusterId}
                    </p>
                  </div>
                );
              },
            },
            {
              key: 'server',
              header: 'API Server',
              accessor: (row) => (
                <span className="text-xs font-mono text-muted-foreground">{row.server}</span>
              ),
            },
            {
              key: 'labels',
              header: 'Labels',
              accessor: (row) => (
                <div className="flex flex-wrap gap-1">
                  {Object.entries(row.labels ?? {}).slice(0, 4).map(([k, v]) => (
                    <span
                      key={k}
                      className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground font-mono"
                      title={`${k}=${v}`}
                    >
                      {k.includes('/') ? k.split('/').pop() : k}={v}
                    </span>
                  ))}
                </div>
              ),
              sortable: false,
            },
            {
              key: 'created',
              header: 'Registered',
              accessor: (row) => (
                <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>
              ),
            },
            {
              key: 'actions',
              header: '',
              sortable: false,
              align: 'center',
              accessor: (row) => (
                <ActionMenu
                  items={[
                    {
                      label: 'Refresh labels',
                      icon: <RefreshCw className="h-3.5 w-3.5" />,
                      onClick: () => refreshLabels.mutate(row),
                    },
                    {
                      label: 'Re-register',
                      icon: <RotateCcw className="h-3.5 w-3.5" />,
                      onClick: () => {
                        const cluster = all.find((c) => c.id === row.clusterId);
                        if (cluster) {
                          setRegisterCluster(cluster);
                        } else {
                          toastError('Cluster row is no longer available');
                        }
                      },
                    },
                    {
                      label: 'Unregister',
                      icon: <Trash2 className="h-3.5 w-3.5" />,
                      variant: 'destructive',
                      onClick: () => setUnregisterTarget(row),
                    },
                  ]}
                />
              ),
            },
          ]}
          keyExtractor={(row) => row.id}
          searchable={false}
          loading={isLoading}
          emptyMessage="No clusters registered yet."
        />
      </section>

      <section>
        <h3 className="text-sm font-medium text-foreground mb-2">Available clusters</h3>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {all
            .filter((c) => !managedById.has(c.id))
            .map((c) => (
              <div
                key={c.id}
                className="rounded-lg border border-border bg-card p-4 flex items-start justify-between gap-3"
              >
                <div className="min-w-0">
                  <p className="font-medium text-foreground truncate">{c.displayName}</p>
                  <p className="text-xs text-muted-foreground font-mono truncate">{c.name}</p>
                  <div className="flex items-center gap-2 mt-2">
                    <StatusBadge status={c.status} size="sm" />
                    <span className="text-xs text-muted-foreground">{c.environment}</span>
                  </div>
                </div>
                <button
                  onClick={() => setRegisterCluster(c)}
                  className="inline-flex items-center gap-1 h-8 px-3 rounded-md text-xs font-medium
                    bg-muted text-foreground hover:bg-accent transition-colors shrink-0"
                >
                  <Plus className="h-3.5 w-3.5" />
                  Register
                </button>
              </div>
            ))}
          {all.filter((c) => !managedById.has(c.id)).length === 0 && (
            <div className="col-span-full text-sm text-muted-foreground text-center py-6">
              All clusters are registered.
            </div>
          )}
        </div>
      </section>

      {registerCluster && (
        <RegisterManagedClusterDialog
          instanceId={instanceId}
          cluster={registerCluster}
          onClose={() => setRegisterCluster(null)}
        />
      )}
      <ConfirmDialog
        open={!!unregisterTarget}
        onClose={() => setUnregisterTarget(null)}
        onConfirm={() => unregister.mutate()}
        title="Unregister cluster"
        description="ArgoCD will stop deploying to this cluster. The cluster itself is unaffected."
        confirmText="Unregister"
        variant="destructive"
        loading={unregister.isPending}
      />
    </div>
  );
}

// ============================================================
// Repos tab
// ============================================================

function ReposTab({ instanceId }: { instanceId: string }) {
  const queryClient = useQueryClient();
  const { data: repos = [], isLoading } = useQuery({
    queryKey: queryKeys.argocd.repos(instanceId),
    queryFn: () => listArgoRepos(instanceId),
    refetchInterval: 60000,
  });
  const [showAdd, setShowAdd] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<ArgoRepository | null>(null);

  const del = useMutation({
    mutationFn: () => deleteArgoRepo(instanceId, deleteTarget!.repo),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.repos(instanceId) });
      toastSuccess('Repository removed');
      setDeleteTarget(null);
    },
    onError: (err: Error) => toastApiError('Delete failed', err),
  });

  const columns: Column<ArgoRepository>[] = [
    {
      key: 'repo',
      header: 'URL',
      accessor: (row) => (
        <div className="min-w-0">
          <p className="text-xs font-mono text-foreground truncate" title={row.repo}>
            {row.repo}
          </p>
          {row.name && (
            <p className="text-xs text-muted-foreground">{row.name}</p>
          )}
        </div>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs uppercase tracking-wide text-muted-foreground">
          {row.type ?? 'git'}
        </span>
      ),
    },
    {
      key: 'state',
      header: 'Connection',
      accessor: (row) => {
        const s = row.connectionState?.status;
        if (s === 'Successful')
          return (
            <span className="inline-flex items-center gap-1 text-xs text-status-success">
              <CheckCircle2 className="h-3 w-3" /> OK
            </span>
          );
        if (s === 'Failed')
          return (
            <span className="inline-flex items-center gap-1 text-xs text-status-error">
              <XCircle className="h-3 w-3" /> Failed
            </span>
          );
        return <span className="text-xs text-muted-foreground">{s ?? '—'}</span>;
      },
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      align: 'center',
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: 'Remove',
              icon: <Trash2 className="h-3.5 w-3.5" />,
              variant: 'destructive',
              onClick: () => setDeleteTarget(row),
            },
          ]}
        />
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-end">
        <button
          onClick={() => setShowAdd(true)}
          className="inline-flex items-center gap-2 h-9 px-3 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          Add Repository
        </button>
      </div>

      <DataTable
        data={repos}
        columns={columns}
        keyExtractor={(row) => row.repo}
        searchPlaceholder="Search repositories..."
        loading={isLoading}
        emptyMessage="No repositories registered."
      />

      {showAdd && <AddRepoDialog instanceId={instanceId} onClose={() => setShowAdd(false)} />}
      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => del.mutate()}
        title="Remove repository"
        description="Applications referencing this repository will fail to sync until it's re-added."
        confirmText="Remove"
        variant="destructive"
        loading={del.isPending}
      />
    </div>
  );
}

// ============================================================
// Operations tab
// ============================================================

function OperationsTab({ instanceId: _instanceId }: { instanceId: string }) {
  // Operations are global per-tenant; they're not currently filterable by
  // instance ID server-side. We still tag them with the app name when we
  // can resolve it from the DB-app list.
  const { data: ops = [], isLoading } = useQuery({
    queryKey: queryKeys.argocd.operations,
    queryFn: () => listArgoOperations({ limit: 100 }),
    refetchInterval: 10000,
  });

  useLiveQueryInvalidation(['cluster.k8s_changed'], [queryKeys.argocd.operations]);

  const columns: Column<ArgoOperation>[] = [
    {
      key: 'target',
      header: 'Target',
      accessor: (row) => (
        <div>
          <p className="text-xs font-mono text-foreground">{row.targetType}</p>
          <p className="text-xs text-muted-foreground font-mono">{row.targetKey.slice(0, 12)}…</p>
        </div>
      ),
    },
    {
      key: 'op',
      header: 'Operation',
      accessor: (row) => <span className="text-sm text-foreground capitalize">{row.operationType}</span>,
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={mapOperationStatus(row.status)} label={titleCase(row.status)} />,
      sortAccessor: (row) => row.status,
    },
    {
      key: 'attempts',
      header: 'Attempts',
      accessor: (row) => <span className="tabular-nums text-sm text-muted-foreground">{row.attemptCount}</span>,
      align: 'center',
    },
    {
      key: 'message',
      header: 'Message',
      accessor: (row) => (
        <span
          className="text-xs text-muted-foreground line-clamp-1"
          title={row.errorMessage || ''}
        >
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
      sortAccessor: (row) => row.startedAt ?? '',
    },
  ];

  return (
    <DataTable
      data={ops}
      columns={columns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search operations..."
      loading={isLoading}
      emptyMessage="No reconciler activity yet."
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
