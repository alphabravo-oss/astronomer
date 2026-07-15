import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Per-cluster Apps tab — sprint 082+.
 *
 * Three sections (radio-toggled) over the existing helm catalog
 * infrastructure:
 *
 *   1. Installed — calls GET /api/v1/clusters/{id}/apps/ which LEFT-
 *      JOINs installed_charts → helm_chart_versions → helm_charts →
 *      helm_repositories so every row carries display name, version,
 *      icon, and repo provenance without N+1.
 *   2. Browse — wraps GET /api/v1/catalog/charts/. Cards link into the
 *      install modal (sprint 27).
 *   3. Recommended — GET /catalog/recommendations/popular/. Same card
 *      layout as Browse.
 *
 * Tool-installed rows (source_kind="tool") get a "Managed by Tools"
 * pivot pill so the Tools tab remains the canonical place to manage
 * Platform Baseline installs while everything still appears here. This
 * matches the explicit decision in the planning conversation: Apps +
 * Tools coexist, share installed_charts, surface provenance to user.
 *
 * Install, upgrade, uninstall, and failed-row cleanup actions mirror
 * the backend catalog RBAC contract so the UI does not advertise
 * operations that the API will reject.
 */

import { useState, useEffect } from 'react';
import { useParams, useSearchParams, useRouter } from '@/lib/navigation';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useDebouncedValue } from '@tanstack/react-pacer';
import { toastApiError, toastSuccess, toastWarning } from '@/lib/toast';
import {
  Package,
  Loader2,
  Search,
  ExternalLink,
  Wrench,
  Star,
  AlertTriangle,
  Box,
  ArrowUpCircle,
  Trash2,
} from 'lucide-react';
import { Link } from '@/lib/link';

import { queryKeys, useCluster } from '@/lib/hooks';
import { usePermissionDecision } from '@/lib/permission-hooks';
import type { PermissionDecision } from '@/lib/permissions';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { ActionButton } from '@/components/ui/action-button';
import {
  listClusterApps,
  listCatalogCharts,
  listRecommendedCharts,
  uninstallCatalogRelease,
  deleteFailedClusterApps,
  type ClusterAppRow,
} from '@/lib/api/cluster-detail';
import { AppInstallModal, AppUninstallModal } from '@/components/clusters/app-install-modal';

type Section = 'installed' | 'browse' | 'recommended';

function permissionDeniedReason(decision: PermissionDecision): string {
  return decision.disabledReason || decision.reason;
}

function toastPermissionDenied(decision: PermissionDecision) {
  toastWarning(permissionDeniedReason(decision));
}

// Coarse status → tone mapping. We don't try to enumerate every
// helm-release state; just bucket into the four colors operators
// recognise: green=happy, blue=in flight, amber=needs attention,
// red=broken. Anything we don't know maps to muted.
function statusTone(status: string): string {
  const s = status.toLowerCase();
  if (s === 'installed' || s === 'adopted' || s === 'ready') {
    return 'bg-emerald-500/10 text-emerald-600 border-emerald-500/30';
  }
  if (s.startsWith('installing') || s.startsWith('upgrading') || s === 'pending_install' || s === 'pending_upgrade') {
    return 'bg-sky-500/10 text-sky-600 border-sky-500/30';
  }
  if (s.startsWith('uninstalling') || s === 'pending_uninstall') {
    return 'bg-amber-500/10 text-amber-600 border-amber-500/30';
  }
  if (s.includes('fail') || s === 'errored' || s === 'broken') {
    return 'bg-red-500/10 text-red-600 border-red-500/30';
  }
  return 'bg-muted text-muted-foreground border-border';
}

// Cheap "stale install" detector. A release in a transient state
// (installing / pending_*) should converge to installed within
// minutes — the platform-baseline tools resolve in seconds, even
// kube-prom-stack settles in <10 min. Anything still pending past
// the threshold is either failed silently or stuck on the agent
// side; surface that as an amber warning so the operator notices.
//
// We don't try to compute the *real* helm-release age here — we'd
// need an API change to expose status_changed_at separately. The
// updated_at proxy is fine for v1: catalog operations bump it on
// every state transition, so "updated_at far in the past + transient
// status" is a strong signal that something stalled.
const TRANSIENT_STATES = new Set([
  'installing', 'upgrading', 'uninstalling',
  'pending_install', 'pending_upgrade', 'pending_uninstall',
]);
const STALE_THRESHOLD_MS = 10 * 60 * 1000;

function isStale(row: ClusterAppRow): { stale: boolean; ageMin: number } {
  const s = row.status.toLowerCase();
  if (!TRANSIENT_STATES.has(s)) return { stale: false, ageMin: 0 };
  const updated = Date.parse(row.updatedAt);
  if (Number.isNaN(updated)) return { stale: false, ageMin: 0 };
  const ageMs = Date.now() - updated;
  return { stale: ageMs > STALE_THRESHOLD_MS, ageMin: Math.round(ageMs / 60_000) };
}

// Modal control state hoisted into the page so any of the three
// sections (Installed row Upgrade/Uninstall, Browse / Recommended
// card Install) can open the right modal without prop-drilling
// onClose/onSuccess handlers everywhere.
type ModalState =
  | { kind: 'none' }
  | { kind: 'install'; chartId: string; chartName: string }
  | {
      kind: 'upgrade';
      installedChartId: string;
      chartId: string;
      chartName: string;
      currentVersionId: string;
      currentValues: string;
      releaseName: string;
      namespace: string;
    }
  | {
      kind: 'uninstall';
      installedChartId: string;
      releaseName: string;
      chartName: string;
      namespace: string;
    };

function ClusterAppsPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const { data: cluster } = useCluster(clusterId);
  const qc = useQueryClient();
  // Deep-link support: feature pages (image-scans, monitoring, etc.)
  // can drop the user here with ?install=<chartName> to auto-open the
  // install modal for that chart. Reads the search params once and
  // resolves the chart on browse-data arrival.
  const searchParams = useSearchParams();
  const router = useRouter();
  const requestedInstall = searchParams?.get('install') ?? '';
  const requestedSection = searchParams?.get('section') as Section | null;

  // Default to Browse when a deep-link asks for an install — we
  // need the browse query to populate so the auto-open effect can
  // find the chart id by name.
  const [section, setSection] = useState<Section>(
    requestedSection ?? (requestedInstall ? 'browse' : 'installed'),
  );
  const [searchQ, setSearchQ] = useState(requestedInstall || '');
  const [modal, setModal] = useState<ModalState>({ kind: 'none' });
  const catalogScope = { type: 'cluster' as const, id: clusterId };
  const catalogCreateDecision = usePermissionDecision('catalog', 'create', catalogScope);
  const catalogUpdateDecision = usePermissionDecision('catalog', 'update', catalogScope);
  const catalogDeleteDecision = usePermissionDecision('catalog', 'delete', catalogScope);

  const installed = useQuery({
    queryKey: queryKeys.clusterPages.appsInstalled(clusterId),
    queryFn: () => listClusterApps(clusterId, { limit: 100 }),
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });

  const uninstall = useMutation({
    mutationFn: (id: string) => uninstallCatalogRelease(id),
    onSuccess: () => {
      toastSuccess('Uninstall dispatched');
      qc.invalidateQueries({ queryKey: queryKeys.clusterPages.appsInstalled(clusterId) });
      setModal({ kind: 'none' });
    },
    onError: (err) => toastApiError('Uninstall failed', err),
  });

  const deleteFailed = useMutation({
    mutationFn: () => deleteFailedClusterApps(clusterId),
    onSuccess: ({ deleted }) => {
      toastSuccess(deleted === 1 ? 'Deleted 1 failed install' : `Deleted ${deleted} failed installs`);
      qc.invalidateQueries({ queryKey: queryKeys.clusterPages.appsInstalled(clusterId) });
      setShowDeleteFailed(false);
    },
    onError: (err) => toastApiError('Delete failed', err),
  });
  const [showDeleteFailed, setShowDeleteFailed] = useState(false);

  // Browse is fetched on mount but the (200ms-debounced) query string
  // updates the key so typing in the search box re-fetches without
  // hammering the catalog endpoint on every keystroke.
  const [debouncedSearchQ] = useDebouncedValue(searchQ, { wait: 200 });
  const browse = useQuery({
    queryKey: queryKeys.clusterPages.appCatalogBrowse(debouncedSearchQ),
    queryFn: () => listCatalogCharts({ limit: 60, search: debouncedSearchQ || undefined }),
    enabled: section === 'browse',
  });

  const recommended = useQuery({
    queryKey: queryKeys.clusterPages.appCatalogRecommended,
    queryFn: () => listRecommendedCharts(12),
    enabled: section === 'recommended',
  });

  // Deep-link auto-open: when ?install=<chartName> is present and we
  // haven't already opened a modal (so refreshes/re-navigations don't
  // re-trigger), look up the chart in browse results and pop the
  // install modal. Strips the query param after consuming so a manual
  // refresh doesn't replay the auto-open.
  useEffect(() => {
    if (!requestedInstall) return;
    if (modal.kind !== 'none') return;
    if (browse.isLoading || !browse.data) return;
    const match = browse.data.items.find((c) => c.name === requestedInstall);
    if (match) {
      if (!catalogCreateDecision.allowed) {
        toastPermissionDenied(catalogCreateDecision);
        router.replace(`/dashboard/clusters/${clusterId}/apps`);
        return;
      }
      setModal({ kind: 'install', chartId: match.id, chartName: match.name });
      // Drop the query param so a back-button + re-navigate doesn't loop.
      router.replace(`/dashboard/clusters/${clusterId}/apps`);
    }
  }, [requestedInstall, browse.data, browse.isLoading, modal.kind, router, clusterId, catalogCreateDecision]);

  const openInstall = (chartId: string, chartName: string) => {
    if (!catalogCreateDecision.allowed) {
      toastPermissionDenied(catalogCreateDecision);
      return;
    }
    setModal({ kind: 'install', chartId, chartName });
  };

  const openUpgrade = (row: ClusterAppRow) => {
    if (!catalogUpdateDecision.allowed) {
      toastPermissionDenied(catalogUpdateDecision);
      return;
    }
    setModal({
      kind: 'upgrade',
      installedChartId: row.id,
      chartId: row.chartId,
      chartName: row.chartName || row.toolSlug || row.releaseName,
      currentVersionId: row.chartVersionId,
      currentValues: row.valuesOverride,
      releaseName: row.releaseName,
      namespace: row.namespace,
    });
  };

  const openUninstall = (row: ClusterAppRow) => {
    if (!catalogDeleteDecision.allowed) {
      toastPermissionDenied(catalogDeleteDecision);
      return;
    }
    setModal({
      kind: 'uninstall',
      installedChartId: row.id,
      releaseName: row.releaseName,
      chartName: row.chartName || row.toolSlug || row.releaseName,
      namespace: row.namespace,
    });
  };

  const openDeleteFailed = () => {
    if (!catalogDeleteDecision.allowed) {
      toastPermissionDenied(catalogDeleteDecision);
      return;
    }
    setShowDeleteFailed(true);
  };

  return (
    <div className="space-y-6 p-4">
      <header className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold flex items-center gap-2">
            <Package className="h-6 w-6" /> Apps
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Browse, install, and manage helm-packaged applications on
            {cluster?.displayName ? <> <span className="font-medium text-foreground">{cluster.displayName}</span></> : ' this cluster'}.
            Releases managed by the <Link href={`/dashboard/clusters/${clusterId}/tools`} className="underline">Tools tab</Link> appear here too with a &quot;Managed by Tools&quot; pivot.
          </p>
        </div>
        <Link
          href={`/dashboard/catalog?cluster_id=${clusterId}`}
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          Manage catalog repos <ExternalLink className="h-3.5 w-3.5" />
        </Link>
      </header>

      <nav className="flex items-center gap-1 border-b border-border">
        {(['installed', 'browse', 'recommended'] as Section[]).map((s) => {
          const active = section === s;
          const count =
            s === 'installed' ? installed.data?.total :
            s === 'browse' ? browse.data?.total :
            recommended.data?.length;
          return (
            <button
              key={s}
              onClick={() => setSection(s)}
              className={
                'px-3 py-2 text-sm border-b-2 -mb-px transition-colors ' +
                (active
                  ? 'border-primary text-foreground font-medium'
                  : 'border-transparent text-muted-foreground hover:text-foreground')
              }
            >
              {s[0].toUpperCase() + s.slice(1)}
              {count != null && (
                <span className="ml-1.5 text-xs text-muted-foreground tabular-nums">
                  ({count})
                </span>
              )}
            </button>
          );
        })}
      </nav>

      {section === 'installed' && (
        <InstalledView
          clusterId={clusterId}
          q={installed}
          updateDecision={catalogUpdateDecision}
          deleteDecision={catalogDeleteDecision}
          onUpgrade={openUpgrade}
          onUninstall={openUninstall}
          onDeleteFailed={openDeleteFailed}
        />
      )}
      {section === 'browse' && (
        <BrowseView
          q={browse}
          search={searchQ}
          setSearch={setSearchQ}
          installed={installed.data?.items ?? []}
          clusterId={clusterId}
          installDecision={catalogCreateDecision}
          onInstall={openInstall}
        />
      )}
      {section === 'recommended' && (
        <RecommendedView
          q={recommended}
          installed={installed.data?.items ?? []}
          installDecision={catalogCreateDecision}
          onInstall={openInstall}
        />
      )}

      {/* Modal layer */}
      {modal.kind === 'install' && (
        <AppInstallModal
          clusterId={clusterId}
          mode={{ kind: 'install', chartId: modal.chartId, chartName: modal.chartName }}
          submitDecision={catalogCreateDecision}
          onClose={() => setModal({ kind: 'none' })}
        />
      )}
      {modal.kind === 'upgrade' && (
        <AppInstallModal
          clusterId={clusterId}
          mode={{
            kind: 'upgrade',
            installedChartId: modal.installedChartId,
            chartId: modal.chartId,
            chartName: modal.chartName,
            currentVersionId: modal.currentVersionId,
            currentValues: modal.currentValues,
            releaseName: modal.releaseName,
            namespace: modal.namespace,
          }}
          submitDecision={catalogUpdateDecision}
          onClose={() => setModal({ kind: 'none' })}
        />
      )}
      {modal.kind === 'uninstall' && (
        <AppUninstallModal
          clusterId={clusterId}
          installedChartId={modal.installedChartId}
          releaseName={modal.releaseName}
          chartName={modal.chartName}
          namespace={modal.namespace}
          pending={uninstall.isPending}
          confirmDecision={catalogDeleteDecision}
          onClose={() => setModal({ kind: 'none' })}
          onConfirm={() => uninstall.mutate(modal.installedChartId)}
        />
      )}
      {showDeleteFailed && (
        <DeleteFailedModal
          count={
            installed.data?.items.filter((r) => {
              const s = r.status.toLowerCase();
              return s === 'failed_install' || s === 'failed_uninstall';
            }).length ?? 0
          }
          pending={deleteFailed.isPending}
          confirmDecision={catalogDeleteDecision}
          onClose={() => setShowDeleteFailed(false)}
          onConfirm={() => deleteFailed.mutate()}
        />
      )}
    </div>
  );
}

// DeleteFailedModal — confirmation dialog for the bulk action. Plain
// modal (not the AppUninstallModal) because there's no per-row context
// to surface; we're nuking every failed_* row on this cluster.
function DeleteFailedModal({
  count,
  pending,
  onClose,
  onConfirm,
  confirmDecision,
}: {
  count: number;
  pending: boolean;
  onClose: () => void;
  onConfirm: () => void;
  confirmDecision: PermissionDecision;
}) {
  const blockedReason = !confirmDecision.allowed ? permissionDeniedReason(confirmDecision) : undefined;

  return (
    <OverlayShell onClose={onClose}>
      <div className="bg-popover border border-border rounded-lg shadow-xl max-w-md w-full mx-4 p-5 space-y-3">
        <h2 className="text-lg font-semibold text-foreground flex items-center gap-2">
          <Trash2 className="h-4 w-4 text-red-600" /> Delete failed installs
        </h2>
        <p className="text-sm text-muted-foreground">
          Hard-delete {count} <code className="font-mono">installed_charts</code> row{count === 1 ? '' : 's'} in <code className="font-mono">failed_install</code> / <code className="font-mono">failed_uninstall</code> on this cluster.
        </p>
        <p className="text-xs text-muted-foreground">
          No helm release uninstall is attempted — by definition these rows never deployed (or already failed to uninstall). If you suspect a stale release exists in-cluster, run <code className="font-mono">helm uninstall</code> via the kubectl shell first.
        </p>
        <div className="flex items-center justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            disabled={pending}
            className="h-9 px-3 rounded-md border border-border text-sm hover:bg-accent disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => {
              if (!confirmDecision.allowed) {
                toastPermissionDenied(confirmDecision);
                return;
              }
              onConfirm();
            }}
            disabled={pending || !confirmDecision.allowed || count === 0}
            title={blockedReason}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-red-600 text-white text-sm font-medium hover:bg-red-700 disabled:opacity-50"
          >
            {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
            Delete {count} row{count === 1 ? '' : 's'}
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}

// ---------------------------------------------------------------------
// Installed view
// ---------------------------------------------------------------------
function InstalledView({
  clusterId,
  q,
  onUpgrade,
  onUninstall,
  onDeleteFailed,
  updateDecision,
  deleteDecision,
}: {
  clusterId: string;
  q: ReturnType<typeof useQuery<{ items: ClusterAppRow[]; total: number }>>;
  onUpgrade: (row: ClusterAppRow) => void;
  onUninstall: (row: ClusterAppRow) => void;
  onDeleteFailed: () => void;
  updateDecision: PermissionDecision;
  deleteDecision: PermissionDecision;
}) {
  if (q.isLoading) {
    return (
      <div className="flex items-center justify-center h-32 text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin mr-2" /> Loading installed apps…
      </div>
    );
  }
  const items = q.data?.items ?? [];
  const staleCount = items.filter((r) => isStale(r).stale).length;
  const failedCount = items.filter((r) => {
    const s = r.status.toLowerCase();
    return s === 'failed_install' || s === 'failed_uninstall';
  }).length;
  if (items.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border p-8 text-center space-y-3">
        <Box className="h-8 w-8 mx-auto text-muted-foreground" />
        <p className="text-sm font-medium text-foreground">No apps installed yet</p>
        <p className="text-xs text-muted-foreground max-w-md mx-auto">
          Browse the catalog and install your first chart. The
          Platform Baseline tools (trivy-operator, kube-state-metrics,
          fluent-bit, ingress-nginx, cert-manager, gatekeeper) are managed via the Tools tab and
          will also appear here once installed.
        </p>
        <div className="flex items-center justify-center gap-2 pt-2">
          <Link
            href={`/dashboard/clusters/${clusterId}/apps?section=browse`}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-primary text-primary-foreground text-xs font-medium hover:opacity-90"
            onClick={(e) => {
              e.preventDefault();
              const btn = document.querySelector<HTMLButtonElement>('nav button:nth-of-type(2)');
              btn?.click();
            }}
          >
            Browse catalog
          </Link>
          <Link
            href={`/dashboard/clusters/${clusterId}/tools`}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border text-xs font-medium hover:bg-muted"
          >
            Open Tools
          </Link>
        </div>
      </div>
    );
  }
  return (
    <div className="space-y-3">
      {failedCount > 0 && (
        <div className="rounded-md border border-red-500/40 bg-red-500/5 px-3 py-2 text-xs flex items-start gap-2">
          <AlertTriangle className="h-4 w-4 text-red-600 flex-shrink-0 mt-0.5" />
          <div className="flex-1">
            <div className="font-medium text-foreground">
              {failedCount} failed install{failedCount === 1 ? '' : 's'} on this cluster
            </div>
            <p className="text-muted-foreground mt-0.5">
              Releases in <code className="font-mono">failed_install</code> / <code className="font-mono">failed_uninstall</code> never deployed cleanly. The helm release itself is either missing or already gone, so they can&apos;t be uninstalled through the normal flow — use the bulk delete to clear them.
            </p>
          </div>
          <ActionButton
            type="button"
            onClick={onDeleteFailed}
            size="sm"
            icon={<Trash2 className="h-3 w-3" />}
            disabled={!deleteDecision.allowed}
            disabledReason={!deleteDecision.allowed ? permissionDeniedReason(deleteDecision) : undefined}
            className="border-red-500/40 text-red-600 hover:bg-red-500/10"
          >
            Delete {failedCount} failed
          </ActionButton>
        </div>
      )}
      {staleCount > 0 && (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-xs flex items-start gap-2">
          <AlertTriangle className="h-4 w-4 text-amber-600 flex-shrink-0 mt-0.5" />
          <div>
            <div className="font-medium text-foreground">
              {staleCount} release{staleCount === 1 ? '' : 's'} stuck in a transient state for over 10 minutes
            </div>
            <p className="text-muted-foreground mt-0.5">
              The helm operation may have stalled. Common causes: the agent tunnel dropped, the
              helm chart failed validation, or a long-running install (kube-prom-stack, istio)
              is still pulling images. Check the worker queue or re-trigger the operation.
            </p>
          </div>
        </div>
      )}
    <div className="border border-border rounded-lg overflow-hidden">
      <Table className="w-full text-sm">
        <TableHeader className="bg-muted/50 text-left text-xs uppercase tracking-wide">
          <TableRow>
            <TableHead className="px-3 py-2">Release</TableHead>
            <TableHead className="px-3 py-2">Chart</TableHead>
            <TableHead className="px-3 py-2">Namespace</TableHead>
            <TableHead className="px-3 py-2">Version</TableHead>
            <TableHead className="px-3 py-2">Status</TableHead>
            <TableHead className="px-3 py-2 text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((row) => (
            <InstalledRow
              key={row.id}
              row={row}
              clusterId={clusterId}
              onUpgrade={onUpgrade}
              onUninstall={onUninstall}
              updateDecision={updateDecision}
              deleteDecision={deleteDecision}
            />
          ))}
        </TableBody>
      </Table>
    </div>
    </div>
  );
}

function InstalledRow({
  row,
  clusterId,
  onUpgrade,
  onUninstall,
  updateDecision,
  deleteDecision,
}: {
  row: ClusterAppRow;
  clusterId: string;
  onUpgrade: (row: ClusterAppRow) => void;
  onUninstall: (row: ClusterAppRow) => void;
  updateDecision: PermissionDecision;
  deleteDecision: PermissionDecision;
}) {
  const isTool = row.sourceKind === 'tool';
  // Upgrade requires the parent chartId; Tools installs (chart_version_id
  // null) have no chartId so we can't drive the version dropdown.
  const canUpgrade = !isTool && !!row.chartId;
  const { stale, ageMin } = isStale(row);
  return (
    <TableRow className="border-t border-border hover:bg-muted/40">
      <TableCell className="px-3 py-2 font-mono text-xs">{row.releaseName}</TableCell>
      <TableCell className="px-3 py-2">
        <div className="flex items-center gap-2">
          {row.chartIconUrl ? (
            <img src={row.chartIconUrl} alt="" className="h-5 w-5 rounded" />
          ) : (
            <Box className="h-5 w-5 text-muted-foreground" />
          )}
          {/* Backend's displayName falls back to releaseName when chart
              metadata is missing, which duplicates the Release column for
              failed installs that never resolved a chart_version. Drop that
              shape to "—" with a hover hint so the operator sees "no chart
              info" rather than "same string twice". */}
          {row.displayName && row.displayName !== row.releaseName ? (
            <span className="text-foreground">{row.displayName}</span>
          ) : (
            <span
              className="text-muted-foreground italic"
              title="No chart metadata recorded for this release — the install likely failed before the chart version was resolved."
            >
              —
            </span>
          )}
          {isTool && (
            <Link
              href={`/dashboard/clusters/${clusterId}/tools`}
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded border border-border bg-muted text-muted-foreground hover:bg-accent"
              title="This release is managed by the Tools tab. Open Tools to upgrade or uninstall."
            >
              <Wrench className="h-3 w-3" /> Tools
            </Link>
          )}
        </div>
        {row.repoName && (
          <div className="text-[11px] text-muted-foreground mt-0.5">
            {row.repoName}{row.chartCategory ? ` · ${row.chartCategory}` : ''}
          </div>
        )}
      </TableCell>
      <TableCell className="px-3 py-2 text-xs text-muted-foreground font-mono">{row.namespace}</TableCell>
      <TableCell className="px-3 py-2 text-xs tabular-nums">
        {row.chartVersion || <span className="text-muted-foreground">—</span>}
      </TableCell>
      <TableCell className="px-3 py-2">
        <div className="inline-flex items-center gap-1.5">
          <span className={`inline-flex items-center px-2 py-0.5 rounded border text-[11px] font-medium ${statusTone(row.status)}`}>
            {row.status}
          </span>
          {stale && (
            <span
              className="inline-flex items-center gap-1 text-[10px] text-amber-600"
              title={`Stuck in '${row.status}' for ${ageMin} min. The helm operation may have stalled — check the worker queue or the cluster's agent connectivity.`}
            >
              <AlertTriangle className="h-3 w-3" /> stale {ageMin}m
            </span>
          )}
        </div>
      </TableCell>
      <TableCell className="px-3 py-2 text-right">
        {isTool ? (
          <Link
            href={`/dashboard/clusters/${clusterId}/tools`}
            className="text-xs text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
          >
            Manage <ExternalLink className="h-3 w-3" />
          </Link>
        ) : (
          <div className="inline-flex items-center gap-1">
            <ActionButton
              onClick={() => onUpgrade(row)}
              disabled={!canUpgrade || !updateDecision.allowed}
              disabledReason={
                !canUpgrade
                  ? 'Upgrade unavailable for this release'
                  : !updateDecision.allowed
                    ? permissionDeniedReason(updateDecision)
                    : undefined
              }
              title="Upgrade to a newer chart version or edit values"
              size="sm"
              icon={<ArrowUpCircle className="h-3 w-3" />}
              className="h-7 px-2"
            >
              Upgrade
            </ActionButton>
            <ActionButton
              onClick={() => onUninstall(row)}
              disabled={!deleteDecision.allowed}
              disabledReason={!deleteDecision.allowed ? permissionDeniedReason(deleteDecision) : undefined}
              title="Uninstall this release"
              size="sm"
              icon={<Trash2 className="h-3 w-3" />}
              className="h-7 px-2 border-red-500/40 text-red-600 hover:bg-red-500/10"
            >
              Uninstall
            </ActionButton>
          </div>
        )}
      </TableCell>
    </TableRow>
  );
}

// ---------------------------------------------------------------------
// Browse view
// ---------------------------------------------------------------------
function BrowseView({
  q,
  search,
  setSearch,
  installed,
  clusterId,
  installDecision,
  onInstall,
}: {
  q: ReturnType<typeof useQuery<{ items: import('@/lib/api/cluster-detail').CatalogChartSummary[]; total: number }>>;
  search: string;
  setSearch: (s: string) => void;
  installed: ClusterAppRow[];
  clusterId: string;
  installDecision: PermissionDecision;
  onInstall: (chartId: string, chartName: string) => void;
}) {
  // Build a name→releases index so each Browse card knows whether
  // it's already on this cluster (and via what install path). This
  // is the cheap version of "drift detection" — we don't reconcile
  // helm releases, we just notice when the catalog browse offers
  // something the cluster already has.
  const installedByChart = new Map<string, ClusterAppRow>();
  for (const r of installed) {
    if (r.chartName) installedByChart.set(r.chartName, r);
    else if (r.toolSlug) installedByChart.set(r.toolSlug, r);
  }

  return (
    <div className="space-y-3">
      <div className="relative max-w-md">
        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search charts (kube-prometheus, loki, …)"
          className="w-full h-9 pl-8 pr-3 rounded-md border border-border bg-background text-sm
            placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
        />
      </div>
      {q.isLoading ? (
        <div className="flex items-center justify-center h-32 text-muted-foreground">
          <Loader2 className="h-5 w-5 animate-spin mr-2" /> Loading catalog…
        </div>
      ) : (q.data?.items.length ?? 0) === 0 ? (
        <div className="rounded-lg border border-dashed border-border p-6 text-center">
          <p className="text-sm font-medium text-foreground">No matching charts</p>
          <p className="text-xs text-muted-foreground mt-1">
            Try a broader search, or add a new repository under{' '}
            <Link href={`/dashboard/catalog?cluster_id=${clusterId}`} className="underline">catalog repos</Link>.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {q.data!.items.map((c) => {
            const existing = installedByChart.get(c.name);
            return (
              <article
                key={c.id}
                className="border border-border rounded-lg p-3 flex gap-3 bg-card hover:border-muted-foreground/40 transition-colors"
              >
                <div className="h-10 w-10 flex-shrink-0 rounded-md bg-muted flex items-center justify-center overflow-hidden">
                  {c.iconUrl ? (
                    <img src={c.iconUrl} alt="" className="h-10 w-10 object-contain" />
                  ) : (
                    <Box className="h-5 w-5 text-muted-foreground" />
                  )}
                </div>
                <div className="flex-1 min-w-0 space-y-1">
                  <div className="flex items-start justify-between gap-2">
                    <div className="font-medium text-sm text-foreground truncate">
                      {c.displayName || c.name}
                    </div>
                    {c.deprecated && (
                      <span className="text-[10px] text-amber-600 border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 rounded">
                        deprecated
                      </span>
                    )}
                  </div>
                  {c.description && (
                    <p className="text-xs text-muted-foreground line-clamp-2">{c.description}</p>
                  )}
                  <div className="flex items-center justify-between gap-2 pt-1">
                    {existing ? (
                      <span className="text-[11px] text-emerald-600 font-medium inline-flex items-center gap-1">
                        Installed
                        {existing.sourceKind === 'tool' && (
                          <span className="text-muted-foreground font-normal">(via Tools)</span>
                        )}
                      </span>
                    ) : (
                      <button
                        className="text-[11px] inline-flex items-center gap-1 text-primary hover:underline disabled:cursor-not-allowed disabled:text-muted-foreground disabled:no-underline"
                        disabled={!installDecision.allowed}
                        title={!installDecision.allowed ? permissionDeniedReason(installDecision) : 'Install chart'}
                        onClick={() => onInstall(c.id, c.name)}
                      >
                        Install →
                      </button>
                    )}
                    {c.homeUrl && (
                      <a
                        href={c.homeUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-[11px] text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
                      >
                        Docs <ExternalLink className="h-2.5 w-2.5" />
                      </a>
                    )}
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Recommended view
// ---------------------------------------------------------------------
function RecommendedView({
  q,
  installed,
  installDecision,
  onInstall,
}: {
  q: ReturnType<typeof useQuery<import('@/lib/api/cluster-detail').RecommendedChart[]>>;
  installed: ClusterAppRow[];
  installDecision: PermissionDecision;
  onInstall: (chartId: string, chartName: string) => void;
}) {
  const installedByChart = new Set(installed.map((r) => r.chartName).filter(Boolean));

  if (q.isLoading) {
    return (
      <div className="flex items-center justify-center h-32 text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin mr-2" /> Loading recommendations…
      </div>
    );
  }
  const items = q.data ?? [];
  if (items.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border p-6 text-center">
        <AlertTriangle className="h-6 w-6 mx-auto text-muted-foreground mb-2" />
        <p className="text-sm text-foreground">No recommendations yet</p>
        <p className="text-xs text-muted-foreground mt-1 max-w-sm mx-auto">
          The recommendation engine needs at least a handful of installs
          across the fleet to surface popular charts. Try the Browse tab
          for the full catalog.
        </p>
      </div>
    );
  }
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      {items.map((c) => {
        const isInstalled = installedByChart.has(c.name);
        return (
          <article
            key={c.chartId || c.name}
            className="border border-border rounded-lg p-3 bg-card space-y-2"
          >
            <div className="flex items-center gap-2">
              <Star className="h-4 w-4 text-amber-500" />
              <div className="font-medium text-sm text-foreground">{c.name}</div>
            </div>
            <div className="text-xs text-muted-foreground space-y-0.5">
              <div>Score: <span className="tabular-nums text-foreground">{c.score.toFixed(2)}</span></div>
              <div>Installs across fleet: <span className="tabular-nums text-foreground">{c.installCount}</span></div>
              {c.ratingAvg > 0 && (
                <div>Avg rating: <span className="tabular-nums text-foreground">{c.ratingAvg.toFixed(1)}</span></div>
              )}
            </div>
            {isInstalled ? (
              <span className="text-[11px] text-emerald-600 font-medium">Already installed</span>
            ) : (
              <button
                className="text-[11px] text-primary hover:underline disabled:cursor-not-allowed disabled:text-muted-foreground disabled:no-underline"
                disabled={!installDecision.allowed}
                title={!installDecision.allowed ? permissionDeniedReason(installDecision) : 'Install chart'}
                onClick={() => onInstall(c.chartId, c.name)}
              >
                Install →
              </button>
            )}
          </article>
        );
      })}
    </div>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/apps/')({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as { install?: string; section?: string } & Record<string, unknown>,
  component: ClusterAppsPage,
});
