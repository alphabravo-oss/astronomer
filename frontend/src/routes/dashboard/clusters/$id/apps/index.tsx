import { createFileRoute } from "@tanstack/react-router";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
/**
 * Per-cluster Apps tab — sprint 082+.
 *
 * Four sections over the existing helm catalog infrastructure, matching
 * Rancher Apps (Installed / Charts / Repositories):
 *
 *   1. Installed — calls GET /api/v1/clusters/{id}/apps/ which LEFT-
 *      JOINs installed_charts → helm_chart_versions → helm_charts →
 *      helm_repositories so every row carries display name, version,
 *      icon, and repo provenance without N+1.
 *   2. Browse — wraps GET /api/v1/catalog/charts/. Cards link into the
 *      install modal (sprint 27).
 *   3. Recommended — GET /catalog/recommendations/popular/. Same card
 *      layout as Browse.
 *   4. Repositories — fleet-wide Helm sources (Astronomer repos are
 *      global, unlike Rancher ClusterRepo CRs).
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

import { useState, useEffect, useMemo } from "react";
import { useParams, useSearchParams, useRouter } from "@/lib/navigation";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useDebouncedValue } from "@tanstack/react-pacer";
import { toastApiError, toastSuccess } from "@/lib/toast";
import {
  Package,
  Loader2,
  Search,
  ExternalLink,
  Wrench,
  Star,
  Clock3,
  ShieldCheck,
  ShieldAlert,
  AlertTriangle,
  Box,
  ArrowUpCircle,
  Trash2,
  Plus,
} from "lucide-react";
import { Link } from "@/lib/link";

import {
  queryKeys,
  useCluster,
} from "@/lib/hooks";
import {
  useHelmRepositories,
  useCatalogApplications,
  useApplicationCatalogSources,
  useCatalogUserDiscovery,
  useSetCatalogChartFavorite,
  useSyncHelmRepository,
  useDeleteHelmRepository,
} from "@/lib/hooks/catalog";
import { AddRepositoryModal } from "../../../catalog/-add-repository-modal";
import { RepositoriesTab } from "../../../catalog/-repositories-tab";
import { CatalogIcon } from "@/components/catalog/catalog-icon";
import { CatalogSourceBadge } from "@/components/catalog/catalog-source-badge";
import { buildCatalogPresentationIndex } from "@/lib/catalogs/astronomer";
import {
  catalogSourcePresentation,
  type CatalogSourceFamily,
} from "@/lib/catalogs/source";
import { liveFallback } from "@/lib/live/status-store";
import { cn } from "@/lib/utils";
import { formatRelativeTime } from "@/lib/utils";
import {
  usePermissionDecision,
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import {
  listAllClusterApps,
  listCatalogCharts,
  uninstallCatalogRelease,
  deleteFailedClusterApps,
  type ClusterAppRow,
} from "@/lib/api/cluster-detail";
import {
  AppInstallModal,
  AppUninstallModal,
} from "@/components/clusters/app-install-modal";

type Section = "installed" | "browse" | "repositories";

// Coarse status → tone mapping. We don't try to enumerate every
// helm-release state; just bucket into the four colors operators
// recognise: green=happy, blue=in flight, amber=needs attention,
// red=broken. Anything we don't know maps to muted.
function statusTone(status: string): string {
  const s = status.toLowerCase();
  if (s === "installed" || s === "adopted" || s === "ready") {
    return "bg-status-success/10 text-status-success border-status-success/30";
  }
  if (
    s.startsWith("installing") ||
    s.startsWith("upgrading") ||
    s === "pending_install" ||
    s === "pending_upgrade"
  ) {
    return "bg-status-info/10 text-status-info border-status-info/30";
  }
  if (s.startsWith("uninstalling") || s === "pending_uninstall") {
    return "bg-status-warning/10 text-status-warning border-status-warning/30";
  }
  if (s.includes("fail") || s === "errored" || s === "broken") {
    return "bg-status-error/10 text-status-error border-status-error/30";
  }
  return "bg-muted text-muted-foreground border-border";
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
  "installing",
  "upgrading",
  "uninstalling",
  "pending_install",
  "pending_upgrade",
  "pending_uninstall",
]);
const STALE_THRESHOLD_MS = 10 * 60 * 1000;

function isStale(row: ClusterAppRow): { stale: boolean; ageMin: number } {
  const s = row.status.toLowerCase();
  if (!TRANSIENT_STATES.has(s)) return { stale: false, ageMin: 0 };
  const updated = Date.parse(row.updatedAt);
  if (Number.isNaN(updated)) return { stale: false, ageMin: 0 };
  const ageMs = Date.now() - updated;
  return {
    stale: ageMs > STALE_THRESHOLD_MS,
    ageMin: Math.round(ageMs / 60_000),
  };
}

// Modal control state hoisted into the page so any of the three
// sections (Installed row Upgrade/Uninstall, Browse / Recommended
// card Install) can open the right modal without prop-drilling
// onClose/onSuccess handlers everywhere.
type ModalState =
  | { kind: "none" }
  | { kind: "install"; chartId: string; chartName: string }
  | {
      kind: "upgrade";
      installedChartId: string;
      chartId: string;
      chartName: string;
      currentVersionId: string;
      currentValues: string;
      releaseName: string;
      namespace: string;
    }
  | {
      kind: "uninstall";
      installedChartId: string;
      releaseName: string;
      chartName: string;
      namespace: string;
    };

export function ClusterAppsPage({
  initialSection = "installed",
}: {
  initialSection?: Section;
} = {}) {
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
  const requestedInstall = searchParams?.get("install") ?? "";
  const section = initialSection;
  const [searchQ, setSearchQ] = useState(requestedInstall || "");
  const [modal, setModal] = useState<ModalState>({ kind: "none" });
  const catalogScope = { type: "cluster" as const, id: clusterId };
  const catalogUpdateDecision = usePermissionDecision(
    "catalog",
    "update",
    catalogScope,
  );
  const catalogDeleteDecision = usePermissionDecision(
    "catalog",
    "delete",
    catalogScope,
  );

  const installed = useQuery({
    queryKey: queryKeys.clusterPages.appsInstalled(clusterId),
    queryFn: () => listAllClusterApps(clusterId),
    // `catalog_release.changed` (server writes) + the Helm-Secret k8s route
    // (cluster-side churn) refresh this while the stream is open.
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });

  const uninstall = useMutation({
    mutationFn: (id: string) => uninstallCatalogRelease(id),
    onSuccess: () => {
      toastSuccess("Uninstall dispatched");
      qc.invalidateQueries({
        queryKey: queryKeys.clusterPages.appsInstalled(clusterId),
      });
      setModal({ kind: "none" });
    },
    onError: (err) => toastApiError("Uninstall failed", err),
  });

  const deleteFailed = useMutation({
    mutationFn: () => deleteFailedClusterApps(clusterId),
    onSuccess: ({ deleted }) => {
      toastSuccess(
        deleted === 1
          ? "Deleted 1 failed install"
          : `Deleted ${deleted} failed installs`,
      );
      qc.invalidateQueries({
        queryKey: queryKeys.clusterPages.appsInstalled(clusterId),
      });
      setShowDeleteFailed(false);
    },
    onError: (err) => toastApiError("Delete failed", err),
  });
  const [showDeleteFailed, setShowDeleteFailed] = useState(false);
  const [showRepoModal, setShowRepoModal] = useState(false);
  const { data: repos, isLoading: reposLoading } = useHelmRepositories(clusterId);
  const { data: catalogApplications } = useCatalogApplications();
  const applicationSources = useApplicationCatalogSources();
  const discovery = useCatalogUserDiscovery();
  const setFavorite = useSetCatalogChartFavorite();
  const syncRepo = useSyncHelmRepository();
  const deleteRepo = useDeleteHelmRepository();

  // Browse is fetched on mount but the (200ms-debounced) query string
  // updates the key so typing in the search box re-fetches without
  // hammering the catalog endpoint on every keystroke.
  const [debouncedSearchQ] = useDebouncedValue(searchQ, { wait: 200 });
  const browse = useQuery({
    queryKey: queryKeys.clusterPages.appCatalogBrowse(
      clusterId,
      debouncedSearchQ,
    ),
    queryFn: () =>
      listCatalogCharts({
        clusterId,
        limit: 200,
        search: debouncedSearchQ || undefined,
      }),
    enabled: section === "browse" && !!clusterId,
  });

  // Deep links resolve to the full chart page instead of opening a modal.
  useEffect(() => {
    if (!requestedInstall) return;
    if (browse.isLoading || !browse.data) return;
    const match = browse.data.items.find((c) => c.name === requestedInstall);
    if (match) {
      router.replace(
        `/dashboard/clusters/${clusterId}/apps/charts/${match.id}`,
      );
    }
  }, [
    requestedInstall,
    browse.data,
    browse.isLoading,
    router,
    clusterId,
  ]);

  const openInstall = (chartId: string, _chartName: string) => {
    router.push(
      `/dashboard/clusters/${clusterId}/apps/charts/${chartId}`,
    );
  };

  const openUpgrade = (row: ClusterAppRow) => {
    if (!catalogUpdateDecision.allowed) {
      toastPermissionDenied(catalogUpdateDecision);
      return;
    }
    setModal({
      kind: "upgrade",
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
      kind: "uninstall",
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
      <header className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div>
          <h1 className="text-2xl font-semibold flex items-center gap-2">
            <Package className="h-6 w-6" /> Apps
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Browse, install, and manage helm-packaged applications on
            {cluster?.displayName ? (
              <>
                {" "}
                <span className="font-medium text-foreground">
                  {cluster.displayName}
                </span>
              </>
            ) : (
              " this cluster"
            )}
            . Releases managed by the{" "}
            <Link
              href={`/dashboard/clusters/${clusterId}/tools`}
              className="underline"
            >
              Tools tab
            </Link>{" "}
            appear here too with a &quot;Managed by Tools&quot; pivot.
          </p>
        </div>
        <div className="flex flex-col items-stretch gap-2 sm:flex-row sm:items-center">
          {section === "repositories" && (
            <ActionButton
              intent="primary"
              icon={<Plus className="h-4 w-4" />}
              onClick={() => setShowRepoModal(true)}
            >
              Add Catalog Source
            </ActionButton>
          )}
        </div>
      </header>

      {section === "installed" && (
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
      {section === "browse" && (
        <BrowseView
          clusterId={clusterId}
          q={browse}
          search={searchQ}
          setSearch={setSearchQ}
          installed={installed.data?.items ?? []}
          onInstall={openInstall}
          repositories={repos ?? []}
          catalogApplications={catalogApplications}
          applicationSources={applicationSources.data}
          discovery={discovery.data}
          favoritePending={setFavorite.isPending}
          onFavorite={(chartId, favorite) =>
            setFavorite.mutate({ clusterId, chartId, favorite })
          }
        />
      )}
      {section === "repositories" && (
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            Helm repositories are shared across the fleet. Charts from these
            sources are available to install on every cluster.
          </p>
          <RepositoriesTab
            repos={repos}
            loading={reposLoading}
            onSync={(id) => syncRepo.mutate(id)}
            onDelete={(id) => deleteRepo.mutate(id)}
            syncPending={syncRepo.isPending}
          />
        </div>
      )}
      {/* Modal layer */}
      {modal.kind === "upgrade" && (
        <AppInstallModal
          clusterId={clusterId}
          mode={{
            kind: "upgrade",
            installedChartId: modal.installedChartId,
            chartId: modal.chartId,
            chartName: modal.chartName,
            currentVersionId: modal.currentVersionId,
            currentValues: modal.currentValues,
            releaseName: modal.releaseName,
            namespace: modal.namespace,
          }}
          submitDecision={catalogUpdateDecision}
          onClose={() => setModal({ kind: "none" })}
        />
      )}
      {modal.kind === "uninstall" && (
        <AppUninstallModal
          clusterId={clusterId}
          installedChartId={modal.installedChartId}
          releaseName={modal.releaseName}
          chartName={modal.chartName}
          namespace={modal.namespace}
          pending={uninstall.isPending}
          confirmDecision={catalogDeleteDecision}
          onClose={() => setModal({ kind: "none" })}
          onConfirm={() => uninstall.mutate(modal.installedChartId)}
        />
      )}
      {showRepoModal && (
        <AddRepositoryModal onClose={() => setShowRepoModal(false)} />
      )}
      {showDeleteFailed && (
        <DeleteFailedModal
          count={
            installed.data?.items.filter((r) => {
              const s = r.status.toLowerCase();
              return s === "failed_install" || s === "failed_uninstall";
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
  const blockedReason = !confirmDecision.allowed
    ? permissionDeniedReason(confirmDecision)
    : undefined;

  return (
    <ModalShell
      title="Delete failed installs"
      onClose={onClose}
      size="sm"
      titleIcon={<Trash2 className="h-4 w-4 text-status-error" />}
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton type="button" onClick={onClose} disabled={pending}>
            Cancel
          </ActionButton>
          <ActionButton
            type="button"
            intent="destructive"
            onClick={() => {
              if (!confirmDecision.allowed) {
                toastPermissionDenied(confirmDecision);
                return;
              }
              onConfirm();
            }}
            disabled={pending || !confirmDecision.allowed || count === 0}
            disabledReason={blockedReason}
            loading={pending}
            icon={<Trash2 className="h-3.5 w-3.5" />}
          >
            Delete {count} row{count === 1 ? "" : "s"}
          </ActionButton>
        </>
      }
    >
      <p className="text-sm text-muted-foreground">
        Hard-delete {count} <code className="font-mono">installed_charts</code>{" "}
        row{count === 1 ? "" : "s"} in{" "}
        <code className="font-mono">failed_install</code> /{" "}
        <code className="font-mono">failed_uninstall</code> on this cluster.
      </p>
      <p className="text-xs text-muted-foreground">
        No helm release uninstall is attempted — by definition these rows never
        deployed (or already failed to uninstall). If you suspect a stale
        release exists in-cluster, run{" "}
        <code className="font-mono">helm uninstall</code> via the kubectl shell
        first.
      </p>
    </ModalShell>
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
        <Loader2 className="h-5 w-5 animate-spin mr-2" /> Loading installed
        apps…
      </div>
    );
  }
  const items = q.data?.items ?? [];
  const staleCount = items.filter((r) => isStale(r).stale).length;
  const failedCount = items.filter((r) => {
    const s = r.status.toLowerCase();
    return s === "failed_install" || s === "failed_uninstall";
  }).length;
  if (items.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border p-8 text-center space-y-3">
        <Box className="h-8 w-8 mx-auto text-muted-foreground" />
        <p className="text-sm font-medium text-foreground">
          No apps installed yet
        </p>
        <p className="text-xs text-muted-foreground max-w-md mx-auto">
          Browse the catalog and install your first chart. The Platform Baseline
          tools (trivy-operator, kube-state-metrics, fluent-bit, ingress-nginx,
          cert-manager, gatekeeper) are managed via the Tools tab and will also
          appear here once installed.
        </p>
        <div className="flex items-center justify-center gap-2 pt-2">
          <Link
            href={`/dashboard/clusters/${clusterId}/apps?section=browse`}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-primary text-primary-foreground text-xs font-medium hover:opacity-90"
            onClick={(e) => {
              e.preventDefault();
              const btn = document.querySelector<HTMLButtonElement>(
                "nav button:nth-of-type(2)",
              );
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
        <div className="rounded-md border border-status-error/40 bg-status-error/5 px-3 py-2 text-xs flex items-start gap-2">
          <AlertTriangle className="h-4 w-4 text-status-error flex-shrink-0 mt-0.5" />
          <div className="flex-1">
            <div className="font-medium text-foreground">
              {failedCount} failed install{failedCount === 1 ? "" : "s"} on this
              cluster
            </div>
            <p className="text-muted-foreground mt-0.5">
              Releases in <code className="font-mono">failed_install</code> /{" "}
              <code className="font-mono">failed_uninstall</code> never deployed
              cleanly. The helm release itself is either missing or already
              gone, so they can&apos;t be uninstalled through the normal flow —
              use the bulk delete to clear them.
            </p>
          </div>
          <ActionButton
            type="button"
            onClick={onDeleteFailed}
            size="sm"
            icon={<Trash2 className="h-3 w-3" />}
            disabled={!deleteDecision.allowed}
            disabledReason={
              !deleteDecision.allowed
                ? permissionDeniedReason(deleteDecision)
                : undefined
            }
            className="border-status-error/40 text-status-error hover:bg-status-error/10"
          >
            Delete {failedCount} failed
          </ActionButton>
        </div>
      )}
      {staleCount > 0 && (
        <div className="rounded-md border border-status-warning/40 bg-status-warning/5 px-3 py-2 text-xs flex items-start gap-2">
          <AlertTriangle className="h-4 w-4 text-status-warning flex-shrink-0 mt-0.5" />
          <div>
            <div className="font-medium text-foreground">
              {staleCount} release{staleCount === 1 ? "" : "s"} stuck in a
              transient state for over 10 minutes
            </div>
            <p className="text-muted-foreground mt-0.5">
              The helm operation may have stalled. Common causes: the agent
              tunnel dropped, the helm chart failed validation, or a
              long-running install (kube-prom-stack, istio) is still pulling
              images. Check the worker queue or re-trigger the operation.
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
  const isTool = row.sourceKind === "tool";
  // Upgrade requires the parent chartId; Tools installs (chart_version_id
  // null) have no chartId so we can't drive the version dropdown.
  const canUpgrade = !isTool && !!row.chartId;
  const { stale, ageMin } = isStale(row);
  return (
    <TableRow className="border-t border-border hover:bg-muted/40">
      <TableCell className="px-3 py-2 font-mono text-xs">
        {row.releaseName}
      </TableCell>
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
            {row.repoName}
            {row.chartCategory ? ` · ${row.chartCategory}` : ""}
          </div>
        )}
      </TableCell>
      <TableCell className="px-3 py-2 text-xs text-muted-foreground font-mono">
        {row.namespace}
      </TableCell>
      <TableCell className="px-3 py-2 text-xs tabular-nums">
        {row.chartVersion || <span className="text-muted-foreground">—</span>}
      </TableCell>
      <TableCell className="px-3 py-2">
        <div className="inline-flex items-center gap-1.5">
          <span
            className={`inline-flex items-center px-2 py-0.5 rounded border text-[11px] font-medium ${statusTone(row.status)}`}
          >
            {row.status}
          </span>
          {stale && (
            <span
              className="inline-flex items-center gap-1 text-[10px] text-status-warning"
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
                  ? "Upgrade unavailable for this release"
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
              disabledReason={
                !deleteDecision.allowed
                  ? permissionDeniedReason(deleteDecision)
                  : undefined
              }
              title="Uninstall this release"
              size="sm"
              icon={<Trash2 className="h-3 w-3" />}
              className="h-7 px-2 border-status-error/40 text-status-error hover:bg-status-error/10"
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
  clusterId,
  q,
  search,
  setSearch,
  installed,
  onInstall,
  repositories,
  catalogApplications,
  applicationSources,
  discovery,
  favoritePending,
  onFavorite,
}: {
  clusterId: string;
  q: ReturnType<
    typeof useQuery<{
      items: import("@/lib/api/cluster-detail").CatalogChartSummary[];
      total: number;
    }>
  >;
  search: string;
  setSearch: (s: string) => void;
  installed: ClusterAppRow[];
  onInstall: (chartId: string, chartName: string) => void;
  repositories: import("@/types").HelmRepository[];
  catalogApplications:
    | import("@/lib/api/catalog").CatalogApplicationPresentation[]
    | undefined;
  applicationSources:
    | import("@/lib/api/catalog").ApplicationCatalogSource[]
    | undefined;
  discovery: import("@/lib/api/catalog").CatalogUserDiscovery[] | undefined;
  favoritePending: boolean;
  onFavorite: (chartId: string, favorite: boolean) => void;
}) {
  const [sourceFamily, setSourceFamily] = useState<
    "all" | CatalogSourceFamily
  >("all");
  const [repositoryId, setRepositoryId] = useState("all");
  const [category, setCategory] = useState("all");
  const [featuredOnly, setFeaturedOnly] = useState(false);
  const [personalView, setPersonalView] = useState<"all" | "favorites" | "recent">("all");
  const [sortBy, setSortBy] = useState<"recommended" | "name" | "source">("recommended");
  const repositoriesById = useMemo(
    () => new Map(repositories.map((repository) => [repository.id, repository])),
    [repositories],
  );
  const presentations = useMemo(
    () => buildCatalogPresentationIndex(catalogApplications),
    [catalogApplications],
  );
  const discoveryByChart = useMemo(
    () => new Map((discovery ?? []).map((item) => [item.chartId, item])),
    [discovery],
  );
  const visibleCharts = useMemo(() => {
    return (q.data?.items ?? [])
      .map((chart) => {
        const repository = repositoriesById.get(chart.repositoryId);
        const presentation = presentations.get(
          `${repository?.name || ""}/${chart.name}`,
        );
        return {
          ...chart,
          displayName: presentation?.displayName || chart.displayName,
          description: presentation?.description || chart.description,
          iconUrl: presentation?.iconUrl || chart.iconUrl,
          category: presentation?.category || chart.category,
          featured: presentation?.featured ?? false,
          catalogSource: catalogSourcePresentation(
            repository,
            Boolean(presentation),
            {
              repositoryId: chart.repositoryId,
              repositoryName: repository?.name,
            },
            presentation?.supportTier === "Astronomer" &&
              presentation.slug === "constellation",
          ),
        };
      })
      .filter(
        (chart) =>
          (sourceFamily === "all" ||
            chart.catalogSource.family === sourceFamily) &&
          (repositoryId === "all" ||
            chart.catalogSource.repositoryId === repositoryId) &&
          (category === "all" || chart.category === category) &&
          (!featuredOnly || chart.featured) &&
          (personalView === "all" ||
            (personalView === "favorites" &&
              discoveryByChart.get(chart.id)?.favorite) ||
            (personalView === "recent" &&
              discoveryByChart.get(chart.id)?.lastViewedAt)),
      )
      .sort((left, right) => {
        if (personalView === "recent") {
          return Date.parse(discoveryByChart.get(right.id)?.lastViewedAt ?? "") -
            Date.parse(discoveryByChart.get(left.id)?.lastViewedAt ?? "");
        }
        if (sortBy === "name") return left.displayName.localeCompare(right.displayName);
        if (sortBy === "source") return left.catalogSource.label.localeCompare(right.catalogSource.label) || left.displayName.localeCompare(right.displayName);
        return Number(right.featured) - Number(left.featured) ||
          (left.catalogSource.family === "first-party" ? -1 : right.catalogSource.family === "first-party" ? 1 : 0) ||
          left.displayName.localeCompare(right.displayName);
      });
  }, [category, discoveryByChart, featuredOnly, personalView, presentations, q.data?.items, repositoriesById, repositoryId, sortBy, sourceFamily]);
  const sourceCounts = useMemo(() => {
    const counts = {
      all: 0,
      "first-party": 0,
      curated: 0,
      community: 0,
      custom: 0,
    };
    for (const chart of q.data?.items ?? []) {
      const repository = repositoriesById.get(chart.repositoryId);
      const curated = presentations.has(
        `${repository?.name || ""}/${chart.name}`,
      );
      const presentation = presentations.get(
        `${repository?.name || ""}/${chart.name}`,
      );
      const source = catalogSourcePresentation(
        repository,
        curated,
        {
          repositoryId: chart.repositoryId,
          repositoryName: repository?.name,
        },
        presentation?.supportTier === "Astronomer" &&
          presentation.slug === "constellation",
      );
      counts.all += 1;
      counts[source.family] += 1;
    }
    return counts;
  }, [presentations, q.data?.items, repositoriesById]);
  // Build a name→releases index so each Browse card knows whether
  // it's already on this cluster (and via what install path). This
  // is the cheap version of "drift detection" — we don't reconcile
  // helm releases, we just notice when the catalog browse offers
  // something the cluster already has.
  const installedByChart = new Map<string, ClusterAppRow[]>();
  for (const r of installed) {
    const name = r.chartName || r.toolSlug;
    if (!name) continue;
    installedByChart.set(name, [...(installedByChart.get(name) ?? []), r]);
  }

  const favoritesCount = [...discoveryByChart.values()].filter(
    (item) => item.favorite,
  ).length;
  const recentCount = [...discoveryByChart.values()].filter(
    (item) => item.lastViewedAt,
  ).length;
  const sourceFailures = (applicationSources ?? []).filter(
    (source) => Boolean(source.last_sync_error),
  );
  const repositoryFailures = repositories.filter(
    (repository) =>
      repository.enabled &&
      (Boolean(repository.lastSyncError) || !repository.lastSyncedAt),
  );
  const catalogIssueCount = sourceFailures.length + repositoryFailures.length;
  const verifiedSources = (applicationSources ?? []).filter((source) =>
    ["verified", "digest-verified"].includes(source.verification_status),
  );
  const latestCatalogSync = applicationSources
    ?.map((source) => source.last_synced_at)
    .filter(Boolean)
    .sort()
    .at(-1);

  return (
    <div className="space-y-3">
      <div
        className={cn(
          "flex flex-wrap items-center justify-between gap-3 rounded-lg border px-3 py-2.5",
          catalogIssueCount
            ? "border-status-warning/40 bg-status-warning/5"
            : "border-status-success/30 bg-status-success/5",
        )}
      >
        <div className="flex items-center gap-2">
          {catalogIssueCount ? (
            <ShieldAlert className="h-4 w-4 text-status-warning" />
          ) : (
            <ShieldCheck className="h-4 w-4 text-status-success" />
          )}
          <div>
            <p className="text-xs font-semibold text-foreground">
              {catalogIssueCount
                ? `${catalogIssueCount} catalog source refresh ${catalogIssueCount === 1 ? "issue" : "issues"}`
                : `${verifiedSources.length} verified catalog ${verifiedSources.length === 1 ? "source" : "sources"}`}
            </p>
            <p className="text-2xs text-table-secondary">
              {catalogIssueCount
                ? "Verified cached content remains available. Open Repositories for remediation details."
                : latestCatalogSync
                  ? `Last synchronized ${formatRelativeTime(latestCatalogSync)}`
                  : "Catalog synchronization has not completed yet."}
            </p>
          </div>
        </div>
        <Link
          href={`/dashboard/clusters/${clusterId}/apps/repositories`}
          className="text-xs font-medium text-primary hover:underline"
        >
          View trust &amp; sync →
        </Link>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-64 max-w-md flex-1">
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
        <div className="inline-flex rounded-md border border-border bg-background p-0.5">
          {(["first-party", "curated", "community", "custom", "all"] as const).map(
            (family) => (
              <button
                key={family}
                type="button"
                aria-pressed={sourceFamily === family}
                onClick={() => {
                  setSourceFamily(family);
                  setRepositoryId("all");
                }}
                className={cn(
                  "rounded px-2 py-1.5 text-xs font-medium transition-colors",
                  sourceFamily === family
                    ? "bg-muted text-foreground"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {family === "first-party"
                  ? "First Party"
                  : family === "curated"
                  ? "Curated"
                  : family === "community"
                    ? "Community"
                    : family === "custom"
                      ? "Custom"
                      : "All"}{" "}
                ({sourceCounts[family]})
              </button>
            ),
          )}
        </div>
        <select
          aria-label="Filter by repository"
          value={repositoryId}
          onChange={(event) => setRepositoryId(event.target.value)}
          className="h-9 max-w-52 rounded-md border border-border bg-background px-2 text-xs text-foreground"
        >
          <option value="all">All sources</option>
          {repositories.map((repository) => (
            <option key={repository.id} value={repository.id}>
              {repository.name}
            </option>
          ))}
        </select>
        <select aria-label="Filter by category" value={category} onChange={(event) => setCategory(event.target.value)} className="h-9 max-w-44 rounded-md border border-border bg-background px-2 text-xs text-foreground">
          <option value="all">All categories</option>
          {[...new Set((q.data?.items ?? []).map((chart) => chart.category))].sort().map((value) => <option key={value} value={value}>{value}</option>)}
        </select>
        <select aria-label="Sort charts" value={sortBy} onChange={(event) => setSortBy(event.target.value as typeof sortBy)} className="h-9 max-w-44 rounded-md border border-border bg-background px-2 text-xs text-foreground">
          <option value="recommended">Recommended first</option>
          <option value="name">Name</option>
          <option value="source">Source</option>
        </select>
        <label className="inline-flex h-9 items-center gap-2 rounded-md border border-border bg-background px-3 text-xs font-medium text-foreground">
          <input type="checkbox" checked={featuredOnly} onChange={(event) => setFeaturedOnly(event.target.checked)} className="h-4 w-4 rounded border-border" /> Featured
        </label>
        <div className="inline-flex h-9 items-center rounded-md border border-border bg-background p-0.5">
          {([
            ["all", "All", q.data?.total ?? 0],
            ["favorites", "Favorites", favoritesCount],
            ["recent", "Recent", recentCount],
          ] as const).map(([value, label, count]) => (
            <button
              key={value}
              type="button"
              aria-pressed={personalView === value}
              onClick={() => setPersonalView(value)}
              className={cn(
                "inline-flex h-7 items-center gap-1 rounded px-2 text-xs font-medium",
                personalView === value
                  ? "bg-muted text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {value === "favorites" && <Star className="h-3 w-3" />}
              {value === "recent" && <Clock3 className="h-3 w-3" />}
              {label} ({count})
            </button>
          ))}
        </div>
      </div>
      {q.isLoading ? (
        <div className="flex items-center justify-center h-32 text-muted-foreground">
          <Loader2 className="h-5 w-5 animate-spin mr-2" /> Loading catalog…
        </div>
      ) : visibleCharts.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border p-6 text-center">
          <p className="text-sm font-medium text-foreground">
            No matching charts
          </p>
          <p className="text-xs text-muted-foreground mt-1">
            Try a broader search, or add a repository on the Repositories tab.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {visibleCharts.map((c) => {
            const existing = installedByChart.get(c.name) ?? [];
            const discoveryEntry = discoveryByChart.get(c.id);
            const installedVersions = [...new Set(existing.map((row) => row.chartVersion).filter(Boolean))];
            const installedTargets = new Set(existing.map((row) => row.clusterId)).size;
            return (
              <article
                key={c.id}
                className="border border-border rounded-lg p-3 flex gap-3 bg-card hover:border-muted-foreground/40 transition-colors"
              >
                <CatalogIcon
                  src={c.iconUrl}
                  label={c.displayName || c.name}
                  className="h-10 w-10 rounded-md"
                  imageClassName="h-9 w-9"
                />
                <div className="flex-1 min-w-0 space-y-1">
                  <div className="flex items-start justify-between gap-2">
                    <div className="font-medium text-sm text-foreground truncate">
                      {c.displayName || c.name}
                    </div>
                    {c.deprecated && (
                      <span className="text-[10px] text-status-warning border border-status-warning/40 bg-status-warning/10 px-1.5 py-0.5 rounded">
                        deprecated
                      </span>
                    )}
                    <button
                      type="button"
                      aria-label={discoveryEntry?.favorite ? `Remove ${c.displayName} from favorites` : `Add ${c.displayName} to favorites`}
                      aria-pressed={Boolean(discoveryEntry?.favorite)}
                      disabled={favoritePending}
                      onClick={() => onFavorite(c.id, !discoveryEntry?.favorite)}
                      className={cn(
                        "rounded p-1 transition-colors hover:bg-muted disabled:opacity-50",
                        discoveryEntry?.favorite
                          ? "text-status-warning"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      <Star className={cn("h-4 w-4", discoveryEntry?.favorite && "fill-current")} />
                    </button>
                  </div>
                  {c.description && (
                    <p className="text-xs text-muted-foreground line-clamp-2">
                      {c.description}
                    </p>
                  )}
                  <CatalogSourceBadge source={c.catalogSource} compact />
                  <div className="flex items-center justify-between gap-2 pt-1">
                    {existing.length ? (
                      <span className="text-[11px] text-status-success font-medium">
                        {installedTargets} target{installedTargets === 1 ? "" : "s"}
                        {` · ${existing.length} release${existing.length === 1 ? "" : "s"}`}
                        {installedVersions.length ? ` · ${installedVersions.join(", ")}` : ""}
                        {existing.some((row) => row.sourceKind === "tool") && (
                          <span className="text-muted-foreground font-normal">
                            {" "}(via Tools)
                          </span>
                        )}
                      </span>
                    ) : (
                      <button
                        className="inline-flex h-9 items-center gap-1 rounded-md bg-primary px-4 text-sm font-semibold text-primary-foreground shadow-sm transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:bg-muted disabled:text-muted-foreground"
                        title="View details and install this chart"
                        onClick={() => onInstall(c.id, c.name)}
                      >
                        View &amp; install →
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
function _RecommendedView({
  q,
  installed,
  installDecision,
  onInstall,
}: {
  q: ReturnType<
    typeof useQuery<import("@/lib/api/cluster-detail").RecommendedChart[]>
  >;
  installed: ClusterAppRow[];
  installDecision: PermissionDecision;
  onInstall: (chartId: string, chartName: string) => void;
}) {
  const installedByChart = new Set(
    installed.map((r) => r.chartName).filter(Boolean),
  );

  if (q.isLoading) {
    return (
      <div className="flex items-center justify-center h-32 text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin mr-2" /> Loading
        recommendations…
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
          The recommendation engine needs at least a handful of installs across
          managed clusters to surface popular charts. Try the Browse tab for the
          full catalog.
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
              <Star className="h-4 w-4 text-status-warning" />
              <div className="font-medium text-sm text-foreground">
                {c.name}
              </div>
            </div>
            <div className="text-xs text-muted-foreground space-y-0.5">
              <div>
                Score:{" "}
                <span className="tabular-nums text-foreground">
                  {c.score.toFixed(2)}
                </span>
              </div>
              <div>
                Installs across clusters:{" "}
                <span className="tabular-nums text-foreground">
                  {c.installCount}
                </span>
              </div>
              {c.ratingAvg > 0 && (
                <div>
                  Avg rating:{" "}
                  <span className="tabular-nums text-foreground">
                    {c.ratingAvg.toFixed(1)}
                  </span>
                </div>
              )}
            </div>
            {isInstalled ? (
              <span className="text-[11px] text-status-success font-medium">
                Already installed
              </span>
            ) : (
              <button
                className="text-[11px] text-primary hover:underline disabled:cursor-not-allowed disabled:text-muted-foreground disabled:no-underline"
                disabled={!installDecision.allowed}
                title={
                  !installDecision.allowed
                    ? permissionDeniedReason(installDecision)
                    : "Install chart"
                }
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

export const Route = createFileRoute("/dashboard/clusters/$id/apps/")({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as { install?: string; section?: string } & Record<string, unknown>,
  component: ClusterAppsPage,
});
