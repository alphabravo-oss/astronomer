import type { useQuery } from "@tanstack/react-query";
import { Package } from "lucide-react";
import type { PermissionDecision } from "@/lib/permissions";
import type { PaginatedResponse } from "@/types";
import type {
  CatalogChartSummary,
  ClusterAppRow,
  RecommendedChart,
} from "@/lib/api/cluster-apps";
import type { HelmRepository } from "@/types";
import { QueryStates, type QueryState } from "@/components/ui/query-states";
import { RepositoriesTab } from "../../../catalog/-repositories-tab";
import { InstalledView } from "./-installed-tab";
import { BrowseView } from "./-browse-tab";
import { RecommendedView } from "./-recommended-tab";
import type { Section } from "./-modal-state";

/**
 * Dispatches the active Apps section to its content component. Extracted
 * from the route body so the latter stays under the function-length budget.
 */
export function AppsTabContent({
  section,
  clusterId,
  projectId,
  installed,
  browse,
  recommended,
  reposQuery,
  onSyncRepo,
  onDeleteRepo,
  syncRepoPending,
  searchQ,
  setSearchQ,
  catalogCreateDecision,
  catalogUpdateDecision,
  catalogDeleteDecision,
  onUpgrade,
  onUninstall,
  onDeleteFailed,
  onInstall,
}: {
  section: Section;
  clusterId: string;
  projectId: string;
  installed: ReturnType<typeof useQuery<PaginatedResponse<ClusterAppRow>>>;
  browse: ReturnType<typeof useQuery<PaginatedResponse<CatalogChartSummary>>>;
  recommended: ReturnType<typeof useQuery<RecommendedChart[]>>;
  reposQuery: QueryState<PaginatedResponse<HelmRepository>>;
  onSyncRepo: (id: string) => void;
  onDeleteRepo: (id: string) => void;
  syncRepoPending: boolean;
  searchQ: string;
  setSearchQ: (value: string) => void;
  catalogCreateDecision: PermissionDecision;
  catalogUpdateDecision: PermissionDecision;
  catalogDeleteDecision: PermissionDecision;
  onUpgrade: (row: ClusterAppRow) => void;
  onUninstall: (row: ClusterAppRow) => void;
  onDeleteFailed: () => void;
  onInstall: (chartId: string, chartName: string) => void;
}) {
  if (section === "installed") {
    return (
      <InstalledView
        clusterId={clusterId}
        q={installed}
        updateDecision={catalogUpdateDecision}
        deleteDecision={catalogDeleteDecision}
        onUpgrade={onUpgrade}
        onUninstall={onUninstall}
        onDeleteFailed={onDeleteFailed}
      />
    );
  }

  if ((section === "browse" || section === "recommended") && !projectId) {
    return (
      <div className="rounded-lg border border-dashed border-border p-8 text-center">
        <Package className="mx-auto h-7 w-7 text-muted-foreground" />
        <p className="mt-3 text-sm font-medium text-foreground">
          Select a project
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          Chart visibility and install authorization are isolated by project.
        </p>
      </div>
    );
  }

  if (section === "browse" && projectId) {
    return (
      <BrowseView
        q={browse}
        search={searchQ}
        setSearch={setSearchQ}
        installed={installed.isError ? [] : (installed.data?.data ?? [])}
        installDecision={catalogCreateDecision}
        onInstall={onInstall}
      />
    );
  }

  if (section === "repositories") {
    return (
      <div className="space-y-3">
        <p className="text-xs text-muted-foreground">
          Helm repositories are shared across the fleet. Charts from these
          sources are available to install on every cluster.
        </p>
        <QueryStates
          query={reposQuery}
          permission="catalog:read"
          errorTitle="Could not load repositories"
        >
          <RepositoriesTab
            repos={reposQuery.data?.data}
            loading={reposQuery.isLoading}
            onSync={onSyncRepo}
            onDelete={onDeleteRepo}
            syncPending={syncRepoPending}
          />
        </QueryStates>
      </div>
    );
  }

  if (section === "recommended" && projectId) {
    return (
      <RecommendedView
        q={recommended}
        installed={installed.isError ? [] : (installed.data?.data ?? [])}
        installDecision={catalogCreateDecision}
        onInstall={onInstall}
      />
    );
  }

  return null;
}
