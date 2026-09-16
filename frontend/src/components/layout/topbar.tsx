import { useNavigate, useLocation } from "@tanstack/react-router";
import { useState, useRef, useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTheme } from "@/lib/theme";
import {
  Bell,
  ChevronDown,
  ChevronRight,
  LogOut,
  Settings,
  Shield,
  User,
  Command,
  Sun,
  Moon,
  Monitor,
  AlertTriangle,
  AlertCircle,
  Info,
  Menu,
  SlidersHorizontal,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useUIStore, useAuthStore } from "@/lib/store";
import {
  useCluster,
  useClusters,
  useCharlieActivated,
  useFeatureFlags,
} from "@/lib/hooks/clusters";
import { useAlertEvents } from "@/lib/hooks/alerting";
import { StatusBadge } from "@/components/ui/status-badge";
import { formatRelativeTime } from "@/lib/utils";
import { GlobalSearch } from "@/components/layout/global-search";
import { logoutCurrentSession } from "@/lib/api/account-security";
import { listCharlieFindings } from "@/lib/api/charlie";
import { queryKeys } from "@/lib/query-keys";
import { selectImportantCharlieFindings } from "@/components/charlie/topbar-findings";
import { generateBreadcrumbs } from "@/lib/breadcrumbs";
import { liveFallback } from "@/lib/live/status-store";
import {
  ClusterScopeControls,
  clusterIdFromPath,
} from "@/components/layout/cluster-scope-controls";
import { ClusterShellLauncher } from "@/components/window-manager/cluster-shell-launcher";
import { useClusterScopeStore } from "@/lib/cluster-scope";
import { can } from "@/lib/permissions";

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

export function Topbar() {
  const pathname = useLocation({ select: (location) => location.pathname });
  const currentClusterId = clusterIdFromPath(pathname);
  const rememberedClusterId = useClusterScopeStore(
    (state) => state.lastClusterId,
  );
  const activeClusterId = currentClusterId ?? rememberedClusterId ?? undefined;
  const navigate = useNavigate();
  const { setCommandPaletteOpen, setMobileSidebarOpen } = useUIStore();
  const { user, logout } = useAuthStore();
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [notificationOpen, setNotificationOpen] = useState(false);
  const userRef = useRef<HTMLDivElement>(null);
  const notificationRef = useRef<HTMLDivElement>(null);
  // Clusters are still fetched here so breadcrumbs can resolve the
  // /dashboard/clusters/{id}/... slug into the human-readable cluster name.
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const {
    data: activeCluster,
    isLoading: activeClusterLoading,
    isError: activeClusterError,
  } = useCluster(activeClusterId ?? "");
  const { data: alertEvents } = useAlertEvents({ status: "firing" });
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

  const breadcrumbs = generateBreadcrumbs(pathname, clusterMap);

  const firingAlerts = alertEvents?.filter((e) => e.status === "firing") || [];
  const recentAlerts = (alertEvents || []).slice(0, 5);
  const actionableCharlieFindings = selectImportantCharlieFindings(
    charlieFindings || [],
  );
  const importantFindings = actionableCharlieFindings.slice(0, 5);
  const notificationCount =
    firingAlerts.length + actionableCharlieFindings.length;
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

  // Close dropdowns on outside click
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (userRef.current && !userRef.current.contains(e.target as Node)) {
        setUserMenuOpen(false);
      }
      if (
        notificationRef.current &&
        !notificationRef.current.contains(e.target as Node)
      ) {
        setNotificationOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  const cycleTheme = () => {
    if (theme === "light") setTheme("dark");
    else if (theme === "dark") setTheme("system");
    else setTheme("light");
  };

  const visibleTheme = theme || "system";
  const ThemeIcon =
    visibleTheme === "dark" ? Moon : visibleTheme === "light" ? Sun : Monitor;

  return (
    <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-border bg-background/80 px-3 backdrop-blur-lg sm:px-6">
      <button
        type="button"
        onClick={() => setMobileSidebarOpen(true)}
        className="mr-2 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground lg:hidden"
        aria-label="Open navigation"
      >
        <Menu className="h-4 w-4" />
      </button>
      {/* Left: Breadcrumbs */}
      <nav className="flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden text-sm">
        {breadcrumbs.map((crumb, i) => (
          <div key={crumb.href} className="flex items-center gap-1.5 min-w-0">
            {i > 0 && (
              <ChevronRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
            )}
            {i === breadcrumbs.length - 1 ? (
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
        ))}
      </nav>

      {/* Center: Cross-cluster Global Search (Phase A3) */}
      <div className="hidden md:flex flex-1 justify-center px-6">
        <GlobalSearch />
      </div>

      {/* Right: Actions */}
      <div className="flex items-center gap-2">
        {currentClusterId ? (
          <ClusterScopeControls clusterId={currentClusterId} />
        ) : null}
        <ClusterShellLauncher
          clusterId={activeClusterId}
          clusterName={
            activeCluster?.displayName || activeCluster?.name || undefined
          }
          disabled={Boolean(shellDisabledReason)}
          disabledReason={shellDisabledReason}
        />
        {/* Command Palette Trigger */}
        <button
          onClick={() => setCommandPaletteOpen(true)}
          className="hidden items-center gap-1.5 h-8 px-2.5 rounded-md border border-border text-xs sm:inline-flex
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          <Command className="h-3.5 w-3.5" />
          <kbd className="font-mono text-[10px]">K</kbd>
        </button>

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
            <div className="absolute right-0 top-full mt-1 w-80 rounded-lg border border-border bg-popover shadow-xl z-50 overflow-hidden">
              <div className="flex items-center justify-between px-4 py-3 border-b border-border">
                <h4 className="text-sm font-medium text-foreground">
                  Notifications
                </h4>
                {firingAlerts.length > 0 && (
                  <span className="text-xs px-2 py-0.5 rounded-full bg-status-error/10 text-status-error font-medium">
                    {firingAlerts.length} firing
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

        {/* User Menu */}
        <div ref={userRef} className="relative">
          <button
            onClick={() => setUserMenuOpen(!userMenuOpen)}
            aria-label="User menu"
            className="flex items-center gap-2 h-8 pl-1 pr-2 rounded-md hover:bg-accent transition-colors"
          >
            <div className="w-6 h-6 rounded-full bg-linear-to-br from-zinc-600 to-zinc-800 flex items-center justify-center">
              <User className="h-3 w-3 text-zinc-300" />
            </div>
            <ChevronDown className="h-3 w-3 text-muted-foreground" />
          </button>

          {userMenuOpen && (
            <div className="absolute right-0 top-full mt-1 w-56 rounded-lg border border-border bg-popover shadow-xl z-50 overflow-hidden">
              <div className="px-3 py-2.5 border-b border-border">
                <p className="text-sm font-medium text-foreground">
                  {user?.displayName || user?.username}
                </p>
                <p className="text-xs text-muted-foreground">{user?.email}</p>
              </div>
              <div className="p-1">
                <button
                  onClick={() => {
                    void navigate({ to: "/dashboard/account/preferences" });
                    setUserMenuOpen(false);
                  }}
                  className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                >
                  <SlidersHorizontal className="h-4 w-4" />
                  Preferences
                </button>
                <button
                  onClick={() => {
                    void navigate({ to: "/dashboard/settings" });
                    setUserMenuOpen(false);
                  }}
                  className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                >
                  <Settings className="h-4 w-4" />
                  Settings
                </button>
                <button
                  onClick={() => {
                    void navigate({ to: "/dashboard/account/security" });
                    setUserMenuOpen(false);
                  }}
                  className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                >
                  <Shield className="h-4 w-4" />
                  Security
                </button>
                <button
                  onClick={async () => {
                    // POST /auth/logout first so the backend can revoke the
                    // session and (for SSO users) hand us a Dex end_session
                    // URL to bounce through. We clear local state regardless
                    // — even if the call fails the user has clicked "sign
                    // out" and shouldn't be left looking authenticated.
                    let redirectUrl: string | undefined;
                    try {
                      const res = await logoutCurrentSession();
                      redirectUrl = res.redirectUrl;
                    } catch {
                      // Network failure / 401 — still clear local state.
                    }
                    logout();
                    if (redirectUrl) {
                      // Top-level navigation to Dex's end_session endpoint.
                      // Dex eventually redirects back to /api/v1/auth/logout-done/
                      // which lands the SPA back on /auth/login.
                      window.location.href = redirectUrl;
                    } else {
                      void navigate({ to: "/auth/login" });
                    }
                  }}
                  className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                >
                  <LogOut className="h-4 w-4" />
                  Sign out
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </header>
  );
}
