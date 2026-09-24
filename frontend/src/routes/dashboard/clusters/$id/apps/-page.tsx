import {
  CatalogProjectPicker,
  useCatalogProjectScope,
} from "@/components/catalog/project-scope";
import { getRouteApi } from "@tanstack/react-router";
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

import { useState, useEffect } from "react";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { Package, Plus } from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";

import { queryKeys } from "@/lib/query-keys";
import { useCluster } from "@/lib/hooks/clusters";
import {
  useSyncHelmRepository,
  useDeleteHelmRepository,
} from "@/lib/hooks/catalog";
import {
  usePermissionDecision,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader } from "@/components/ui/page";
import { TabStrip } from "@/components/ui/tabs";
import { useTabParam } from "@/lib/use-tab-param";
import {
  uninstallCatalogRelease,
  deleteFailedClusterApps,
  type ClusterAppRow,
} from "@/lib/api/cluster-apps";
import { SECTIONS, type Section, type ModalState } from "./-modal-state";
import { AppsTabContent } from "./-apps-tab-content";
import { AppsModals } from "./-apps-modals";
import { AppsPagination, useAppsQueries } from "./-queries";

export { InstalledView } from "./-installed-tab";
export { RecommendedView } from "./-recommended-tab";

const routeApi = getRouteApi("/dashboard/clusters/$id/apps/");

export function ClusterAppsPage() {
  const params = routeApi.useParams();
  const clusterId = params.id;
  const { data: cluster } = useCluster(clusterId);
  const qc = useQueryClient();
  // Deep-link support: feature pages (image-scans, monitoring, etc.)
  // can drop the user here with ?install=<chartName> to auto-open the
  // install modal for that chart. Reads the search params once and
  // resolves the chart on browse-data arrival.
  const searchParams = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const navigate = useNavigate();
  const requestedProjectId = searchParams.get("project") ?? "";
  const projectScope = useCatalogProjectScope(requestedProjectId, clusterId);
  const { projectId } = projectScope;
  const setProjectId = (nextProjectId: string) => {
    const next = new URLSearchParams(searchParams);
    if (nextProjectId) next.set("project", nextProjectId);
    else next.delete("project");
    void navigate({
      to: `/dashboard/clusters/${clusterId}/apps${next.size ? `?${next.toString()}` : ""}`,
      replace: true,
    });
    setModal({ kind: "none" });
  };
  const requestedInstall = searchParams?.get("install") ?? "";

  // Default to Browse when a deep-link asks for an install — we
  // need the browse query to populate so the auto-open effect can
  // find the chart id by name. Backed by the URL's ?section= so the
  // active section is deep-linkable and survives a refresh.
  const [section, setSection] = useTabParam<Section>(
    SECTIONS,
    requestedInstall ? "browse" : "installed",
    "section",
  );
  const [searchQ, setSearchQ] = useState(requestedInstall || "");
  const [modal, setModal] = useState<ModalState>({ kind: "none" });
  const catalogScope = { type: "cluster" as const, id: clusterId };
  const catalogProjectScope = { type: "project" as const, id: projectId };
  const catalogCreateDecision = usePermissionDecision(
    "catalog",
    "create",
    catalogProjectScope,
  );
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

  const queries = useAppsQueries(clusterId, projectId, section, searchQ);
  const { installed, browse, recommended, reposQuery } = queries;

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
  const syncRepo = useSyncHelmRepository();
  const deleteRepo = useDeleteHelmRepository();

  // Deep-link auto-open: when ?install=<chartName> is present and we
  // haven't already opened a modal (so refreshes/re-navigations don't
  // re-trigger), look up the chart in browse results and pop the
  // install modal. Strips the query param after consuming so a manual
  // refresh doesn't replay the auto-open.
  useEffect(() => {
    if (!requestedInstall) return;
    if (modal.kind !== "none") return;
    if (browse.isLoading || !browse.data) return;
    const match = browse.data.data.find((c) => c.name === requestedInstall);
    if (match) {
      // Preserve the Browse section in the URL — otherwise dropping
      // ?install= below would also drop the implied section and land
      // the user back on Installed.
      const remainingSearch = `project=${encodeURIComponent(projectId)}&section=browse`;
      if (!catalogCreateDecision.allowed) {
        toastPermissionDenied(catalogCreateDecision);
        void navigate({
          to: `/dashboard/clusters/${clusterId}/apps?${remainingSearch}`,
          replace: true,
        });
        return;
      }
      setModal({ kind: "install", chartId: match.id, chartName: match.name });
      // Drop the query param so a back-button + re-navigate doesn't loop.
      void navigate({
        to: `/dashboard/clusters/${clusterId}/apps?${remainingSearch}`,
        replace: true,
      });
    }
  }, [
    requestedInstall,
    browse.data,
    browse.isLoading,
    modal.kind,
    navigate,
    clusterId,
    projectId,
    catalogCreateDecision,
  ]);

  const openInstall = (chartId: string, chartName: string) => {
    if (!catalogCreateDecision.allowed) {
      toastPermissionDenied(catalogCreateDecision);
      return;
    }
    setModal({ kind: "install", chartId, chartName });
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

  const failedCount =
    installed.data?.data.filter((r) => {
      const s = r.status.toLowerCase();
      return s === "failed_install" || s === "failed_uninstall";
    }).length ?? 0;

  return (
    <div className="space-y-6 p-4">
      <PageHeader
        title={
          <span className="inline-flex items-center gap-2">
            <Package className="h-6 w-6" /> Apps
          </span>
        }
        description={
          <>
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
            <RouterLink
              to="/dashboard/clusters/$id/tools"
              params={{ id: clusterId }}
              className="underline"
            >
              Tools tab
            </RouterLink>{" "}
            appear here too with a &quot;Managed by Tools&quot; pivot.
          </>
        }
        actions={
          <>
            {section !== "repositories" && (
              <CatalogProjectPicker
                scope={projectScope}
                value={requestedProjectId || projectId}
                onChange={setProjectId}
                clusterId={clusterId}
              />
            )}
            {section === "repositories" && (
              <ActionButton
                intent="primary"
                icon={<Plus className="h-4 w-4" />}
                onClick={() => setShowRepoModal(true)}
              >
                Add Repository
              </ActionButton>
            )}
          </>
        }
      />

      <TabStrip
        tabs={SECTIONS.map((s) => ({
          key: s,
          label: s[0].toUpperCase() + s.slice(1),
          count:
            s === "installed"
              ? installed.data && !installed.isError
                ? installed.data.pagination.total
                : undefined
              : s === "browse"
                ? browse.data && !browse.isError
                  ? browse.data.pagination.total
                  : undefined
                : s === "recommended"
                  ? recommended.isError
                    ? undefined
                    : recommended.data?.length
                  : reposQuery.isError
                    ? undefined
                    : reposQuery.data?.pagination.total,
        }))}
        value={section}
        onChange={setSection}
        aria-label="Apps"
      />

      <AppsTabContent
        section={section}
        clusterId={clusterId}
        projectId={projectId}
        installed={installed}
        browse={browse}
        recommended={recommended}
        reposQuery={reposQuery}
        onSyncRepo={(id) => syncRepo.mutate(id)}
        onDeleteRepo={(id) => deleteRepo.mutate(id)}
        syncRepoPending={syncRepo.isPending}
        searchQ={searchQ}
        setSearchQ={setSearchQ}
        catalogCreateDecision={catalogCreateDecision}
        catalogUpdateDecision={catalogUpdateDecision}
        catalogDeleteDecision={catalogDeleteDecision}
        onUpgrade={openUpgrade}
        onUninstall={openUninstall}
        onDeleteFailed={openDeleteFailed}
        onInstall={openInstall}
      />

      <AppsPagination section={section} queries={queries} />
      <AppsModals
        modal={modal}
        onCloseModal={() => setModal({ kind: "none" })}
        projectId={projectId}
        clusterId={clusterId}
        catalogCreateDecision={catalogCreateDecision}
        catalogUpdateDecision={catalogUpdateDecision}
        catalogDeleteDecision={catalogDeleteDecision}
        uninstallPending={uninstall.isPending}
        onConfirmUninstall={(installedChartId) =>
          uninstall.mutate(installedChartId)
        }
        showRepoModal={showRepoModal}
        onCloseRepoModal={() => setShowRepoModal(false)}
        showDeleteFailed={showDeleteFailed && !installed.isError}
        onCloseDeleteFailed={() => setShowDeleteFailed(false)}
        deleteFailedCount={
          installed.data?.pagination.has_more ||
          installed.data?.pagination.offset
            ? undefined
            : failedCount
        }
        deleteFailedPending={deleteFailed.isPending}
        onConfirmDeleteFailed={() => deleteFailed.mutate()}
      />
    </div>
  );
}
