import { TopbarAccountMenu } from "./topbar-account-menu";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Bell,
  AlertTriangle,
  AlertCircle,
  Info,
  Sparkles,
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
import { listCharlieFindings } from "@/lib/api/charlie";
import { queryKeys } from "@/lib/query-keys";
import { selectImportantCharlieFindings } from "@/components/charlie/topbar-findings";
import { usePageBreadcrumbs } from "@/lib/use-page-breadcrumbs";
import { breadcrumbLabel } from "@/lib/breadcrumbs";
import { liveFallback } from "@/lib/live/status-store";
import {
  ClusterScopeControls,
  clusterIdFromPath,
} from "@/components/layout/cluster-scope-controls";
import { clusterScopeApplicability } from "@/components/layout/cluster-scope-applicability";
import { ClusterSwitcherMenu } from "@/components/layout/cluster-switcher-menu";
import { LazyHeaderClusterActions as HeaderClusterActions } from "@/components/layout/lazy-header-cluster-actions";
import { can } from "@/lib/permissions";
import { useClustersUpdate } from "@/lib/permission-hooks";
import { useCharlieOptional } from "@/components/charlie/charlie-context";
import { useSidebarNavigation } from "./use-sidebar-navigation";
import { navGroupItems } from "./nav-group-items";
import {
  Breadcrumbs,
  Header,
  HeaderIconButton,
  Popover,
  PopoverContent,
  PopoverTrigger,
  SearchTrigger,
  ThemeToggle,
  type BreadcrumbItem,
} from "@/ui";
import { useSlashToOpenPalette } from "@/components/layout/global-search";
import { BareButton } from "@/components/form/bare-button";

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

function TopbarNotifications() {
  const navigate = useNavigate();
  const [notificationOpen, setNotificationOpen] = useState(false);
  const { data: alertEventsPage } = useAlertEvents({
    status: "firing",
    limit: 5,
  });
  const { data: alertEventSummary } = useAlertEventSummary();
  const { data: featureFlags } = useFeatureFlags();
  const { activated: charlieActivated } = useCharlieActivated();
  const { data: charlieFindings } = useQuery({
    queryKey: queryKeys.charlie.findings,
    queryFn: ({ signal }) => listCharlieFindings({ signal }),
    enabled: featureFlags?.["feature.charlie"] === true && charlieActivated,
    retry: false,
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });
  const recentAlerts = alertEventsPage?.data ?? [];
  const actionableCharlieFindings = selectImportantCharlieFindings(
    charlieFindings || [],
  );
  const importantFindings = actionableCharlieFindings.slice(0, 5);
  const notificationCount =
    (alertEventSummary?.firing ?? 0) + actionableCharlieFindings.length;

  return (
    <Popover open={notificationOpen} onOpenChange={setNotificationOpen}>
      <PopoverTrigger asChild>
        <HeaderIconButton
          aria-label={
            notificationCount > 0
              ? `Notifications, ${notificationCount} unread`
              : "Notifications"
          }
          badge={notificationCount}
        >
          <Bell className="h-4 w-4" />
        </HeaderIconButton>
      </PopoverTrigger>
      <PopoverContent
        data-header-popover
        align="end"
        className="w-80 max-w-[calc(100vw-1.5rem)] overflow-hidden rounded-lg p-0"
      >
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h4 className="text-sm font-medium text-foreground">Notifications</h4>
          {(alertEventSummary?.firing ?? 0) > 0 && (
            <span className="rounded-full bg-status-error/10 px-2 py-0.5 text-xs font-medium text-status-error">
              {alertEventSummary?.firing} firing
            </span>
          )}
        </div>
        <div className="max-h-80 overflow-y-auto">
          {importantFindings.map((finding) => (
            <BareButton
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
                  {finding.affectedResource.type}: {finding.affectedResource.id}
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
            </BareButton>
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
                  className="flex items-start gap-3 border-b border-border px-4 py-3 transition-colors last:border-0 hover:bg-accent/50"
                >
                  <SevIcon
                    className={cn(
                      "mt-0.5 h-4 w-4 shrink-0",
                      severityColor[alert.severity] || "text-muted-foreground",
                    )}
                  />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-foreground">
                      {alert.ruleName}
                    </p>
                    <p className="mt-0.5 truncate text-xs text-muted-foreground">
                      {alert.message}
                    </p>
                    <div className="mt-1 flex items-center gap-2">
                      <StatusBadge status={alert.status} size="sm" />
                      <span className="text-micro text-muted-foreground">
                        {formatRelativeTime(alert.firedAt)}
                      </span>
                    </div>
                  </div>
                </div>
              );
            })
          )}
        </div>
        <div className="border-t border-border px-4 py-2">
          <BareButton
            onClick={() => {
              void navigate({ to: "/dashboard/alerting" });
              setNotificationOpen(false);
            }}
            className="w-full py-1 text-center text-xs text-muted-foreground transition-colors hover:text-foreground"
          >
            View all alerts
          </BareButton>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function CharlieHeaderButton() {
  const charlie = useCharlieOptional();
  if (!charlie) return null;
  return (
    <HeaderIconButton
      tooltip="Charlie (⌘J)"
      aria-label="Open Charlie assistant"
      aria-expanded={charlie.open}
      aria-controls="charlie-assistant-drawer"
      onClick={() => charlie.setOpen(true)}
      data-testid="charlie-header-button"
    >
      <Sparkles className="h-4 w-4" />
    </HeaderIconButton>
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
  const directPermission = useClustersUpdate(currentClusterId ?? "");
  const { setMobileSidebarOpen, setCommandPaletteOpen } = useUIStore();
  const { user } = useAuthStore();
  // Clusters are fetched so breadcrumbs can resolve the /clusters/{id} slug
  // into the human-readable cluster name.
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const {
    data: activeCluster,
    isLoading: activeClusterLoading,
    isError: activeClusterError,
  } = useCluster(currentClusterId ?? "");
  const { navGroups: clusterGroups } = useSidebarNavigation(currentClusterId);
  useSlashToOpenPalette();

  const clusterMap = useMemo(() => {
    const map: Record<string, string> = {};
    for (const c of clustersData?.data || []) {
      map[c.id] = c.displayName || c.name;
    }
    return map;
  }, [clustersData?.data]);

  const pageCrumbs = usePageBreadcrumbs(pathname, clusterMap);

  const breadcrumbs = useMemo<BreadcrumbItem[]>(() => {
    if (currentClusterId) {
      // "All clusters" is the real breadcrumb back out of the cluster; the
      // chip already names the cluster, so the trail continues with the
      // page path inside it (labels from the cluster panel registry).
      const base = `/dashboard/clusters/${currentClusterId}`;
      const rest = pathname.slice(base.length).split("/").filter(Boolean);
      const items = clusterGroups.flatMap(navGroupItems);
      const trail: BreadcrumbItem[] = [
        { label: "All clusters", to: "/dashboard/clusters" },
      ];
      if (rest.length === 0) {
        trail.push({ label: "Overview" });
        return trail;
      }
      let path = base;
      rest.forEach((segment, index) => {
        path += `/${segment}`;
        const registered = items.find((item) => item.href === path);
        const label = registered?.label ?? breadcrumbLabel(segment);
        trail.push(
          index === rest.length - 1 ? { label } : { label, to: path },
        );
      });
      return trail;
    }
    // Global: drop the always-generic root ("Overview") so depth counts from
    // the pillar; the kit renders nothing below two crumbs.
    return pageCrumbs.slice(1).map((crumb, index, all) =>
      index === all.length - 1
        ? { label: crumb.label }
        : { label: crumb.label, to: crumb.href },
    );
  }, [clusterGroups, currentClusterId, pageCrumbs, pathname]);

  const canOpenClusterShell = currentClusterId
    ? can(user, "shell", "exec", { type: "cluster", id: currentClusterId })
    : false;
  const shellDisabledReason = activeClusterLoading
    ? "Loading cluster details"
    : activeClusterError || (currentClusterId && !activeCluster)
      ? "The active cluster is unavailable"
      : activeCluster?.isLocal
        ? "Cluster shell is unavailable on the management plane cluster"
        : !canOpenClusterShell
          ? "You need shell:exec access for this cluster"
          : undefined;

  return (
    <Header
      onOpenMobileNav={() => setMobileSidebarOpen(true)}
      scope={
        currentClusterId ? (
          <>
            <ClusterSwitcherMenu
              clusterId={currentClusterId}
              clusterName={activeCluster?.displayName || activeCluster?.name}
            />
            <ClusterScopeControls
              clusterId={currentClusterId}
              applicability={applicableScope}
            />
          </>
        ) : null
      }
      breadcrumbs={<Breadcrumbs items={breadcrumbs} />}
      search={
        <SearchTrigger
          onOpen={() => setCommandPaletteOpen(true)}
          placeholder="Search pages, clusters and resources"
        />
      }
      actions={
        <>
          {currentClusterId ? (
            <HeaderClusterActions
              clusterId={currentClusterId}
              cluster={activeCluster}
              directPermission={directPermission}
              shellDisabledReason={shellDisabledReason}
            />
          ) : null}
          <CharlieHeaderButton />
          <ThemeToggle />
          <TopbarNotifications />
          <TopbarAccountMenu />
        </>
      }
    />
  );
}
