import { useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink, useLocation } from "@tanstack/react-router";
import {
  ArrowLeft,
  BookOpen,
  ChevronLeft,
  ChevronRight,
  Orbit,
} from "lucide-react";

import { ExtensionNavItems } from "@/components/extensions/ExtensionNavItems";
import { SearchableClusterSwitcher } from "@/components/layout/cluster-scope-controls";
import {
  InstalledToolLinks,
  SidebarGroup,
} from "@/components/layout/sidebar-navigation-view";
import {
  filterNavGroups,
  getClusterNavGroups,
  globalNavGroups,
  INSTALLED_TOOLS_NAV_GROUP,
  withFavoriteNavigation,
} from "@/components/layout/sidebar-navigation";
import { useOpenNavGroups } from "@/components/layout/nav-open-groups";
import { OverlayBackdrop } from "@/components/ui/overlay-shell";
import { getVeleroStatus } from "@/lib/api/cluster-velero";
import { APP_VERSION } from "@/lib/env";
import {
  useCharlieActivated,
  useCluster,
  useFeatureFlags,
} from "@/lib/hooks/clusters";
import { queryKeys } from "@/lib/query-keys";
import { useAuthStore, useUIStore } from "@/lib/store";
import { useUserPreferences } from "@/lib/user-preferences";
import { cn, formatK8sVersion } from "@/lib/utils";
import { useSidebarResourceCounts } from "@/components/layout/use-sidebar-resource-counts";
import { useClusterStackStatus } from "@/components/monitoring/hooks";
import { useProductName } from "@/lib/hooks/public-settings";

// Vite stamps APP_VERSION from the release tag; local builds use the current
// package fallback in lib/env.ts.

export function Sidebar() {
  const pathname = useLocation({ select: (location) => location.pathname });
  const {
    sidebarCollapsed,
    mobileSidebarOpen,
    toggleSidebarCollapsed,
    setMobileSidebarOpen,
  } = useUIStore();
  const user = useAuthStore((s) => s.user);
  const { data: featureFlags } = useFeatureFlags();
  const { activated: charlieActivated } = useCharlieActivated();
  const { preferences } = useUserPreferences();
  const productName = useProductName();

  // Detect cluster context from URL. Static sub-routes (new, register) are NOT
  // cluster ids — treating them as such fires cluster-detail queries with a
  // bogus id (e.g. /clusters/register/resources/... -> 400/503).
  const clusterMatch = pathname.match(/^\/dashboard\/clusters\/([^/]+)/);
  const clusterSegment = clusterMatch?.[1];
  const clusterId =
    clusterSegment && clusterSegment !== "new" && clusterSegment !== "register"
      ? clusterSegment
      : undefined;
  const isClusterContext = !!clusterId;

  useEffect(() => {
    setMobileSidebarOpen(false);
  }, [pathname, setMobileSidebarOpen]);

  useEffect(() => {
    if (!mobileSidebarOpen) return;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMobileSidebarOpen(false);
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [mobileSidebarOpen, setMobileSidebarOpen]);

  const collapsed = sidebarCollapsed && !mobileSidebarOpen;

  const { data: cluster } = useCluster(clusterId || ""); // cluster name for header
  const { data: veleroStatus } = useQuery({
    queryKey: queryKeys.clusterPages.veleroStatus(clusterId || ""),
    queryFn: ({ signal }) => getVeleroStatus(clusterId!, signal),
    enabled: isClusterContext && !!clusterId && !cluster?.isLocal,
    staleTime: 30_000,
  });
  const { data: monitoringStatus } = useClusterStackStatus(clusterId);

  const navGroups = useMemo(() => {
    const baseGroups = isClusterContext
      ? getClusterNavGroups(clusterId!, {
          isLocal: cluster?.isLocal,
          veleroInstalled: !!veleroStatus?.installed,
          grafanaAvailable: monitoringStatus?.grafanaAvailable === true,
        })
      : globalNavGroups;
    const groups = isClusterContext
      ? baseGroups
      : withFavoriteNavigation(baseGroups, preferences.favorites);
    return filterNavGroups(groups, user, featureFlags, charlieActivated);
  }, [
    charlieActivated,
    cluster?.isLocal,
    clusterId,
    featureFlags,
    isClusterContext,
    preferences.favorites,
    monitoringStatus?.grafanaAvailable,
    user,
    veleroStatus?.installed,
  ]);

  // Sidebar sections stay open across navigation (multi-open, not an
  // accordion); open state is remembered per scope in localStorage.
  const { openGroups, toggleGroup } = useOpenNavGroups(
    isClusterContext ? "cluster" : "global",
    navGroups,
    pathname,
  );

  // Fetch resource counts when in cluster context — only for groups that are
  // currently expanded (see useResourceCounts).
  const counts = useSidebarResourceCounts(
    isClusterContext ? clusterId! : "",
    openGroups,
  );

  return (
    <>
      {mobileSidebarOpen && (
        <OverlayBackdrop
          ariaLabel="Close navigation"
          className="backdrop-blur-[1px] lg:hidden"
          onClose={() => setMobileSidebarOpen(false)}
        />
      )}
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-50 flex h-screen w-60 -translate-x-full flex-col border-r border-sidebar-border bg-sidebar transition-transform duration-200 ease-in-out lg:static lg:z-auto lg:translate-x-0 lg:transition-[width]",
          mobileSidebarOpen && "translate-x-0",
          sidebarCollapsed ? "lg:w-16" : "lg:w-60",
        )}
      >
        {/* Logo + collapse toggle */}
        <div className="flex items-center h-14 px-4 border-b border-sidebar-border">
          {!collapsed && (
            <RouterLink
              to="/dashboard"
              className="flex items-center gap-2.5 min-w-0"
            >
              <div className="shrink-0 w-7 h-7 rounded-lg bg-linear-to-br from-blue-500 to-violet-600 flex items-center justify-center">
                <Orbit className="h-4 w-4 text-white" />
              </div>
              <div className="flex flex-col min-w-0">
                <span className="text-sm font-semibold text-foreground tracking-tight truncate leading-tight">
                  {productName}
                </span>
                <span className="text-[10px] text-muted-foreground leading-tight">
                  by AlphaBravo
                </span>
              </div>
            </RouterLink>
          )}
          <button
            onClick={toggleSidebarCollapsed}
            className={cn(
              "nav-item",
              collapsed
                ? "w-full justify-center px-0"
                : "ml-auto hidden lg:flex",
            )}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          >
            {collapsed ? (
              <ChevronRight className="h-4 w-4" />
            ) : (
              <ChevronLeft className="h-4 w-4" />
            )}
          </button>
        </div>

        {/* Cluster context header */}
        {isClusterContext && !collapsed && (
          <div className="px-2 py-2 border-b border-sidebar-border">
            <RouterLink
              to="/dashboard/clusters"
              className="flex items-center gap-2 px-2 py-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors rounded-md hover:bg-accent/50"
            >
              <ArrowLeft className="h-3.5 w-3.5" />
              <span>All Clusters</span>
            </RouterLink>
            <div className="px-2 mt-1">
              <SearchableClusterSwitcher
                clusterId={clusterId!}
                fallbackName={
                  cluster?.displayName || cluster?.name || "Cluster"
                }
              />
              {cluster?.kubernetesVersion && (
                <p className="text-2xs text-muted-foreground mt-1 px-1">
                  {formatK8sVersion(cluster.kubernetesVersion)}
                </p>
              )}
            </div>
          </div>
        )}
        {isClusterContext && collapsed && (
          <div className="px-2 py-2 border-b border-sidebar-border">
            <RouterLink
              to="/dashboard/clusters"
              className="nav-item group justify-center px-0"
              title="Back to Clusters"
            >
              <ArrowLeft className="h-4 w-4 text-muted-foreground group-hover:text-foreground" />
            </RouterLink>
          </div>
        )}

        {/* Navigation */}
        <nav className="flex-1 overflow-y-auto py-2 px-1 no-scrollbar">
          {navGroups.map((group) => (
            <SidebarGroup
              key={group.label}
              group={group}
              pathname={pathname}
              collapsed={collapsed}
              counts={isClusterContext ? counts : undefined}
              isOpen={openGroups.has(group.label)}
              onToggle={() => toggleGroup(group.label)}
            />
          ))}
          {isClusterContext && (
            <InstalledToolLinks
              clusterId={clusterId!}
              collapsed={collapsed}
              isOpen={openGroups.has(INSTALLED_TOOLS_NAV_GROUP)}
              onToggle={() => toggleGroup(INSTALLED_TOOLS_NAV_GROUP)}
            />
          )}
          {/* §HostMounts mount point 1 — enabled `sidebar` extensions append
            full-page nav links here (global context only; routes are
            host-fixed under /dashboard/extensions/{name}). The component
            renders nothing (header included) when no extension declares a
            sidebar point, so the nav is unchanged on a fresh install. */}
          {!isClusterContext && (
            <ExtensionNavItems pathname={pathname} collapsed={collapsed} />
          )}
        </nav>

        {/* Bottom links */}
        <div className="mt-auto px-2 py-2 border-t border-sidebar-border space-y-1">
          <a
            href="/astronomer-docs/"
            target="_blank"
            rel="noopener noreferrer"
            className="nav-item w-full"
            title="Documentation"
          >
            <BookOpen className="h-4 w-4" />
            {!collapsed && <span className="text-xs">Documentation</span>}
          </a>
          {!collapsed && (
            <div className="px-3 py-1 space-y-0.5">
              <p className="text-[10px] text-muted-foreground">
                {productName} {APP_VERSION}
              </p>
              <p className="text-[10px] text-muted-foreground">
                Built by{" "}
                <a
                  href="https://alphabravo.io"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="hover:text-foreground underline-offset-2 hover:underline"
                >
                  AlphaBravo
                </a>
              </p>
            </div>
          )}
        </div>
      </aside>
    </>
  );
}
