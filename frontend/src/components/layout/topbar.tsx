import { TopbarAccountMenu } from "./topbar-account-menu";
import { useHeaderPopover } from "./use-header-popover";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTheme } from "@/lib/theme";
import {
  Bell,
  ChevronRight,
  Sun,
  Moon,
  Monitor,
  AlertTriangle,
  AlertCircle,
  Info,
  Menu,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useUIStore, useAuthStore } from "@/lib/store";
import {
  useCluster,
  useClusters,
  useCharlieActivated,
  useFeatureFlags,
} from "@/lib/hooks/clusters";
import { useAlertEvents, useAlertEventSummary } from "@/lib/hooks/alerting";
import { StatusBadge } from "@/components/ui/status-badge";
import { formatRelativeTime } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";
import { GlobalSearch } from "@/components/layout/global-search";
import { listCharlieFindings } from "@/lib/api/charlie";
import { queryKeys } from "@/lib/query-keys";
import { selectImportantCharlieFindings } from "@/components/charlie/topbar-findings";
import { usePageBreadcrumbs } from "@/lib/use-page-breadcrumbs";
import type { Breadcrumb } from "@/lib/breadcrumbs";
import { liveFallback } from "@/lib/live/status-store";
import {
  ClusterScopeControls,
  clusterIdFromPath,
} from "@/components/layout/cluster-scope-controls";
import { clusterScopeApplicability } from "@/components/layout/cluster-scope-applicability";
import { ClusterShellLauncher } from "@/components/window-manager/cluster-shell-launcher";
import { ClusterSwitcherMenu } from "@/components/layout/cluster-switcher-menu";
import { LazyHeaderClusterActions as HeaderClusterActions } from "@/components/layout/lazy-header-cluster-actions";
import { useClusterScopeStore } from "@/lib/cluster-scope";
import { can } from "@/lib/permissions";
import { useClustersUpdate } from "@/lib/permission-hooks";

// --- Breadcrumb generation ---

const severityIcon: Record<string, React.ElementType> = {
  critical: AlertCircle,
  warning: AlertTriangle,
  info: Info,
};

const severityColor: Record<string, string> = {
  critical: "text-status-error",
  warning: "text-status-warning",
  info: "text-status-info",
};

/** Breadcrumb trail shown outside cluster context (see Topbar). */
function TopbarBreadcrumbs({
  breadcrumbs,
  navigate,
}: {
  breadcrumbs: Breadcrumb[];
  navigate: (opts: { to: string }) => unknown;
}) {
  return (
    <nav className="flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden text-sm">
      {breadcrumbs.map((crumb, i) => {
        const isLast = i === breadcrumbs.length - 1;
        return (
          <div
            key={crumb.href}
            // Ancestor crumbs collapse below `sm`: the always-mounted
            // cluster switcher chip leaves too little room on narrow
            // viewports for their clickable text to clear the 24px
            // touch-target minimum. The current page's plain-text crumb
            // always stays visible.
            className={cn(
              "items-center gap-1.5 min-w-0",
              isLast ? "flex" : "hidden sm:flex",
            )}
          >
            {i > 0 && (
              <ChevronRight className="hidden h-3.5 w-3.5 shrink-0 text-muted-foreground sm:block" />
            )}
            {isLast ? (
              <span className="text-foreground font-medium truncate">
                {crumb.label}
              </span>
            ) : (
              <button
                onClick={() => void navigate({ to: crumb.href })}
                className="text-muted-foreground hover:text-foreground transition-colors truncate"
              >
                {crumb.label}
              </button>
            )}
          </div>
        );
      })}
    </nav>
  );
}

export function Topbar() {
  const location = useLocation({
    select: (current) => ({
      pathname: current.pathname,
      searchStr: current.searchStr,
    }),
  });
  const pathname = location.pathname;
  const currentClusterId = clusterIdFromPath(pathname);
  const applicableScope = clusterScopeApplicability(
    pathname,
    location.searchStr,
  );
  const rememberedClusterId = useClusterScopeStore(
    (state) => state.lastClusterId,
  );
  const activeClusterId = currentClusterId ?? rememberedClusterId ?? undefined;
  const directPermission = useClustersUpdate(currentClusterId ?? "");
  const navigate = useNavigate();
  const { setMobileSidebarOpen } = useUIStore();
  const { user } = useAuthStore();
  const {
    open: notificationOpen,
    setOpen: setNotificationOpen,
    root: notificationRef,
  } = useHeaderPopover();
  // Clusters are still fetched here so breadcrumbs can resolve the
  // /dashboard/clusters/{id}/... slug into the human-readable cluster name.
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const {
    data: activeCluster,
    isLoading: activeClusterLoading,
    isError: activeClusterError,
  } = useCluster(activeClusterId ?? "");
  const { data: alertEventsPage } = useAlertEvents({
    status: "firing",
    limit: 5,
  });
  const { data: alertEventSummary } = useAlertEventSummary();
  const { data: featureFlags } = useFeatureFlags();
  const { activated: charlieActivated } = useCharlieActivated();
  const { data: charlieFindings } = useQuery({
    queryKey: queryKeys.charlie.findings,
    queryFn: listCharlieFindings,
    enabled: featureFlags?.["feature.charlie"] === true && charlieActivated,
    retry: false,
    // The findings endpoint performs a bounded, authorization-filtered sync
    // before returning durable local summaries. Polling while an operator is
    // signed in makes non-executed diagnoses visible in the existing alert
    // bell without opening a browser-to-Charlie transport or running anything
    // while the product feature is disabled.
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });

  const { theme, setTheme } = useTheme();

  const clusterMap = useMemo(() => {
    // Breadcrumbs use the technical RFC-1123 cluster name rather than the
    // pretty displayName — it matches the URL slug, the kubeconfig context
    // name, and the kubectl prompt the user is likely to compare against.
    const map: Record<string, string> = {};
    for (const c of clustersData?.data || []) {
      map[c.id] = c.name || c.displayName;
    }
    return map;
  }, [clustersData?.data]);

  const breadcrumbs = usePageBreadcrumbs(pathname, clusterMap);

  const recentAlerts = alertEventsPage?.data ?? [];
  const actionableCharlieFindings = selectImportantCharlieFindings(
    charlieFindings || [],
  );
  const importantFindings = actionableCharlieFindings.slice(0, 5);
  const notificationCount =
    (alertEventSummary?.firing ?? 0) + actionableCharlieFindings.length;
  const canOpenClusterShell = activeClusterId
    ? can(user, "shell", "exec", { type: "cluster", id: activeClusterId })
    : false;
  const shellDisabledReason = activeClusterLoading
    ? "Loading cluster details"
    : activeClusterError || (activeClusterId && !activeCluster)
      ? "The active cluster is unavailable"
      : activeCluster?.isLocal
        ? "Cluster shell is unavailable on the management plane cluster"
        : !canOpenClusterShell
          ? "You need shell:exec access for this cluster"
          : undefined;

  const cycleTheme = () => {
    if (theme === "light") setTheme("dark");
    else if (theme === "dark") setTheme("system");
    else setTheme("light");
  };

  const visibleTheme = theme || "system";
  const ThemeIcon =
    visibleTheme === "dark" ? Moon : visibleTheme === "light" ? Sun : Monitor;

  return (
    <header className="sticky top-0 z-30 flex min-h-14 flex-wrap items-center gap-2 py-2 border-b border-border bg-background/80 px-3 backdrop-blur-lg sm:px-6">
      <button
        type="button"
        onClick={() => setMobileSidebarOpen(true)}
        className="mr-2 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground lg:hidden"
        aria-label="Open navigation"
      >
        <Menu className="h-4 w-4" />
      </button>
      {/* Left: always-mounted cluster switcher, then either the cluster
          scope controls (cluster context — the chip already says which
          cluster, so breadcrumbs would be redundant noise) or breadcrumbs
          (everywhere else). */}
      <ClusterSwitcherMenu
        clusterId={currentClusterId}
        clusterName={activeCluster?.displayName || activeCluster?.name}
      />
      {currentClusterId &&
      (applicableScope.project || applicableScope.namespaces) ? (
        <ClusterScopeControls
          clusterId={currentClusterId}
          applicability={applicableScope}
        />
      ) : currentClusterId ? null : (
        <TopbarBreadcrumbs breadcrumbs={breadcrumbs} navigate={navigate} />
      )}

      {/* Center: Cross-cluster Global Search (Phase A3). Its own kbd hint
          covers the command palette shortcut, so the topbar no longer needs
          a separate ⌘K chip. */}
      <div className="flex min-w-44 max-w-xs flex-1 justify-center px-2">
        <GlobalSearch />
      </div>

      {/* Right: Actions */}
      <div className="ml-auto flex flex-wrap items-center justify-end gap-1">
        <ActionButton
          size="sm"
          onClick={() => useUIStore.getState().setCommandPaletteOpen(true)}
        >
          Go to page
        </ActionButton>
        <ClusterShellLauncher
          clusterId={activeClusterId}
          clusterName={
            activeCluster?.displayName || activeCluster?.name || undefined
          }
          disabled={Boolean(shellDisabledReason)}
          disabledReason={shellDisabledReason}
        />
        {currentClusterId ? (
          <HeaderClusterActions
            clusterId={currentClusterId}
            cluster={activeCluster}
            directPermission={directPermission}
          />
        ) : null}

        {/* Theme Toggle */}
        <button
          onClick={cycleTheme}
          className="relative inline-flex items-center justify-center h-8 w-8 rounded-md
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          title={`Theme: ${visibleTheme}`}
        >
          <ThemeIcon className="h-4 w-4" />
        </button>

        {/* Notifications */}
        <div ref={notificationRef} className="relative">
          <button
            onClick={() => setNotificationOpen(!notificationOpen)}
            aria-label={
              notificationCount > 0
                ? `Notifications, ${notificationCount} unread`
                : "Notifications"
            }
            aria-expanded={notificationOpen}
            aria-haspopup="menu"
            className="relative inline-flex items-center justify-center h-8 w-8 rounded-md
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            <Bell className="h-4 w-4" />
            {notificationCount > 0 && (
              <span className="absolute top-0.5 right-0.5 flex items-center justify-center h-4 min-w-[16px] px-1 rounded-full bg-status-error text-[10px] font-bold text-white">
                {notificationCount > 99 ? "99+" : notificationCount}
              </span>
            )}
          </button>

          {notificationOpen && (
            <div
              data-header-popover
              className="fixed right-3 top-24 mt-1 w-80 max-w-[calc(100vw-1.5rem)] sm:absolute sm:right-0 sm:top-full rounded-lg border border-border bg-popover shadow-xl z-50 overflow-hidden"
            >
              <div className="flex items-center justify-between px-4 py-3 border-b border-border">
                <h4 className="text-sm font-medium text-foreground">
                  Notifications
                </h4>
                {(alertEventSummary?.firing ?? 0) > 0 && (
                  <span className="text-xs px-2 py-0.5 rounded-full bg-status-error/10 text-status-error font-medium">
                    {alertEventSummary?.firing} firing
                  </span>
                )}
              </div>

              <div className="max-h-80 overflow-y-auto">
                {importantFindings.map((finding) => (
                  <button
                    key={`charlie:${finding.id}`}
                    onClick={() => {
                      void navigate({
                        to: `/dashboard/charlie?tab=findings&finding=${encodeURIComponent(finding.id)}`,
                      });
                      setNotificationOpen(false);
                    }}
                    className="flex w-full items-start gap-3 border-b border-border px-4 py-3 text-left hover:bg-accent/50"
                  >
                    <AlertCircle
                      className={cn(
                        "mt-0.5 h-4 w-4 shrink-0",
                        finding.severity === "critical"
                          ? "text-status-error"
                          : "text-status-warning",
                      )}
                    />
                    <span className="min-w-0">
                      <span className="block truncate text-sm font-medium">
                        {finding.title}
                      </span>
                      <span className="block truncate text-xs text-muted-foreground">
                        {finding.affectedResource.type}:{" "}
                        {finding.affectedResource.id}
                        {finding.confidence == null
                          ? ""
                          : ` · ${Math.round(finding.confidence * 100)}% confidence`}
                      </span>
                      {finding.reasonNoAction && (
                        <span className="block truncate text-xs text-muted-foreground">
                          No action: {finding.reasonNoAction}
                        </span>
                      )}
                    </span>
                  </button>
                ))}
                {recentAlerts.length === 0 && importantFindings.length === 0 ? (
                  <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                    No recent alerts
                  </div>
                ) : (
                  recentAlerts.map((alert) => {
                    const SevIcon = severityIcon[alert.severity] || Info;
                    return (
                      <div
                        key={alert.id}
                        className="flex items-start gap-3 px-4 py-3 border-b border-border last:border-0 hover:bg-accent/50 transition-colors"
                      >
                        <SevIcon
                          className={cn(
                            "h-4 w-4 shrink-0 mt-0.5",
                            severityColor[alert.severity] ||
                              "text-muted-foreground",
                          )}
                        />
                        <div className="flex-1 min-w-0">
                          <p className="text-sm text-foreground font-medium truncate">
                            {alert.ruleName}
                          </p>
                          <p className="text-xs text-muted-foreground truncate mt-0.5">
                            {alert.message}
                          </p>
                          <div className="flex items-center gap-2 mt-1">
                            <StatusBadge status={alert.status} size="sm" />
                            <span className="text-2xs text-muted-foreground">
                              {formatRelativeTime(alert.firedAt)}
                            </span>
                          </div>
                        </div>
                      </div>
                    );
                  })
                )}
              </div>

              <div className="px-4 py-2 border-t border-border">
                <button
                  onClick={() => {
                    void navigate({ to: "/dashboard/alerting" });
                    setNotificationOpen(false);
                  }}
                  className="w-full text-center text-xs text-muted-foreground hover:text-foreground transition-colors py-1"
                >
                  View all alerts
                </button>
              </div>
            </div>
          )}
        </div>

        <TopbarAccountMenu />
      </div>
    </header>
  );
}
