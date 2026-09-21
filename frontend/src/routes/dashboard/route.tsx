import { useEffect, useRef, useState } from "react";
import {
  createFileRoute,
  Outlet,
  redirect,
  type ErrorComponentProps,
} from "@tanstack/react-router";
import { Link as RouterLink } from "@tanstack/react-router";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { Sidebar } from "@/components/layout/sidebar";
import { Topbar } from "@/components/layout/topbar";
import { GlobalBanner } from "@/components/layout/global-banner";
import { CommandPalette } from "@/components/layout/command-palette";
import { WindowManager } from "@/components/window-manager/window-manager";
import { ExtensionProvider } from "@/components/extensions/ExtensionProvider";
import { EmptyState, StatePanel } from "@/components/ui/empty-state";
import { errorDigest, errorMessage } from "@/lib/error-message";
import { useAuthStore } from "@/lib/store";
import { useCharlieActivated, useFeatureFlags } from "@/lib/hooks/clusters";
import { useCurrentUser } from "@/lib/hooks/auth";
import type { FeatureFlags, FeatureFlagKey } from "@/lib/api/feature-flags";
import { useLiveClusterMetricsMerger } from "@/lib/live/cluster-merger";
import { useLiveEvents } from "@/lib/live/hooks";
import { hasSessionHint } from "@/lib/auth/session";
import { cn, hexToHslTriplet } from "@/lib/utils";
import { useBranding } from "@/lib/hooks/public-settings";
import { dashboardContentLayout } from "@/lib/dashboard-content-layout";
import { useUserPreferences } from "@/lib/user-preferences";
import { isNavigableLandingRoute } from "@/lib/api/user-preferences";
import { CharlieShell } from "@/components/charlie/charlie-shell";
import {
  AlertTriangle,
  Compass,
  LayoutDashboard,
  Lock,
  RotateCcw,
  WifiOff,
} from "lucide-react";

export const Route = createFileRoute("/dashboard")({
  // Synchronous cookie-presence guard with exact fidelity to the old Next
  // middleware: the JS-readable CSRF cookie is set/cleared in lockstep with
  // the HttpOnly session cookie. Async concerns (must_change_password,
  // feature flags) stay in the layout component below — in beforeLoad they
  // would block every navigation on query data.
  beforeLoad: ({ location }) => {
    if (!hasSessionHint()) {
      throw redirect({
        to: "/auth/login",
        search: { returnTo: location.href },
      });
    }
  },
  component: DashboardLayout,
  // Boundaries (F-04, P2.4): both render in the <Outlet/> position, so the
  // dashboard chrome (sidebar/topbar) stays mounted around them.
  notFoundComponent: DashboardNotFound,
  errorComponent: DashboardError,
});

/**
 * Dashboard 404 boundary (F-04). Keeps the dashboard chrome mounted while
 * telling the user the sub-route doesn't exist.
 */
function DashboardNotFound() {
  return (
    <div data-testid="route-not-found">
      <StatePanel
        icon={Compass}
        tone="info"
        title="Page not found"
        description="This dashboard route doesn't exist. It may have moved or been removed."
        actionLabel="Back to dashboard"
        actionHref="/dashboard"
      />
    </div>
  );
}

/**
 * Route-level error boundary for the dashboard segment (F-04). A render error
 * in any dashboard page is caught here instead of white-screening the whole
 * console — the sidebar/topbar stay mounted because the boundary only
 * replaces the segment's children.
 */
function DashboardError({ error, reset }: ErrorComponentProps) {
  useEffect(() => {
    // Surface to the console so it still reaches any error-reporting hook.
    console.error("Dashboard render error:", error);
  }, [error]);

  // Next.js attached a `digest` ref to server-thrown errors; keep reading it
  // defensively for anything that still tags one on.
  const digest = errorDigest(error);

  return (
    <div
      data-testid="route-error-boundary"
      className="flex flex-col items-center"
    >
      <StatePanel
        icon={AlertTriangle}
        tone="danger"
        title="Something went wrong"
        description={
          <>
            {errorMessage(
              error,
              "An unexpected error occurred while rendering this page.",
            )}
            {digest && (
              <span className="mt-1 block font-mono text-xs opacity-70">
                ref: {digest}
              </span>
            )}
          </>
        }
        role="alert"
        actionLabel="Try again"
        actionIcon={RotateCcw}
        onAction={reset}
      />
      <RouterLink
        to="/dashboard"
        className="-mt-6 inline-flex h-9 items-center gap-2 rounded-lg border border-border px-4 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      >
        <LayoutDashboard className="h-4 w-4" />
        Back to dashboard
      </RouterLink>
    </div>
  );
}

function DashboardLayout() {
  const navigate = useNavigate();
  const updateUser = useAuthStore((s) => s.updateUser);
  const {
    data: currentUser,
    isFetched: currentUserFetched,
    isError: currentUserError,
  } = useCurrentUser();
  const mustChangePassword = currentUser
    ? currentUser.mustChangePassword || currentUser.must_change_password
    : false;

  useEffect(() => {
    if (currentUser) updateUser(currentUser);
  }, [currentUser, updateUser]);

  useEffect(() => {
    if (currentUserFetched && mustChangePassword) {
      void navigate({ to: "/auth/change-password", replace: true });
    } else if (currentUserFetched && (currentUserError || !currentUser)) {
      void navigate({ to: "/auth/login", replace: true });
    }
  }, [
    currentUser,
    currentUserError,
    currentUserFetched,
    mustChangePassword,
    navigate,
  ]);

  // This is a pre-mount security gate: dashboard children, extensions,
  // feature queries and SSE transports do not exist until the session has
  // been resolved and is allowed past forced password rotation.
  if (!currentUserFetched || !currentUser || mustChangePassword) {
    return (
      <div className="flex h-screen items-center justify-center" role="status">
        Checking session…
      </div>
    );
  }

  return <DashboardAuthorizedShell />;
}

function DashboardAuthorizedShell() {
  const navigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });
  const searchString = useLocation({
    select: (location) => location.searchStr,
  });
  const mainRef = useRef<HTMLElement>(null);
  const { preferences, isServerOwned: preferencesLoaded } =
    useUserPreferences();
  const featureFlagsQuery = useFeatureFlags();
  const featureFlags = featureFlagsQuery.data;
  const { activated: charlieActivated } = useCharlieActivated();
  const requiredFeature = featureForPath(pathname);
  const disabledFeature = disabledFeatureForPath(pathname, featureFlags);
  const charlieNotActivated =
    (pathname === "/dashboard/charlie" ||
      pathname.startsWith("/dashboard/charlie/")) &&
    featureFlags?.["feature.charlie"] === true &&
    !charlieActivated;
  const contentLayout = dashboardContentLayout(pathname, searchString);
  // UX-05: surface browser offline so hung tables/mutations are explained.
  const [online, setOnline] = useState(true);
  useEffect(() => {
    if (typeof navigator === "undefined") return;
    const sync = () => setOnline(navigator.onLine);
    sync();
    window.addEventListener("online", sync);
    window.addEventListener("offline", sync);
    return () => {
      window.removeEventListener("online", sync);
      window.removeEventListener("offline", sync);
    };
  }, []);

  // Operator-configured brand color (public settings). Degrades to the
  // built-in theme on any failure or unparsable value — never blocks the
  // shell — and restores it on unmount so leaving the dashboard (e.g. back
  // to /auth/login) doesn't leak a stale override.
  const primaryColorHex = useBranding().data?.["branding.primary_color"];
  useEffect(() => {
    const triplet = primaryColorHex ? hexToHslTriplet(primaryColorHex) : null;
    if (!triplet) return;
    const root = document.documentElement;
    const previous = root.style.getPropertyValue("--primary");
    root.style.setProperty("--primary", triplet);
    return () => {
      if (previous) root.style.setProperty("--primary", previous);
      else root.style.removeProperty("--primary");
    };
  }, [primaryColorHex]);

  useEffect(() => {
    mainRef.current?.focus({ preventScroll: true });
  }, [pathname]);

  useEffect(() => {
    if (
      preferencesLoaded &&
      pathname === "/dashboard" &&
      isNavigableLandingRoute(preferences.landing_route)
    ) {
      void navigate({ to: preferences.landing_route, replace: true });
    }
  }, [pathname, preferences.landing_route, preferencesLoaded, navigate]);

  // Hold a single SSE connection open for the whole dashboard; per-page
  // hooks reuse this connection via refcount inside `lib/live/stream.ts`.
  useLiveEvents();
  // Patch React Query caches in place when cluster.metrics / status events
  // arrive so cards / tables tick without paying a refetch on every event.
  useLiveClusterMetricsMerger();

  const appShell = (
    <div
      data-testid="app-shell"
      className="flex h-screen overflow-hidden bg-background"
    >
      <a
        href="#main"
        className="sr-only fixed left-3 top-3 z-[var(--z-toast)] rounded-md bg-background px-3 py-2 text-sm font-medium text-foreground shadow-lg focus:not-sr-only"
      >
        Skip to main content
      </a>
      <Sidebar />
      <div className={cn("flex flex-col flex-1 min-w-0 overflow-hidden")}>
        <Topbar />
        <GlobalBanner />
        {!online && (
          <div
            role="status"
            className="flex items-center gap-2 bg-status-warning/15 text-status-warning border-b border-status-warning/30 px-4 py-2 text-sm"
          >
            <WifiOff className="h-4 w-4 shrink-0" />
            You are offline. Live updates and mutations will fail until
            connectivity returns.
          </div>
        )}
        <main
          id="main"
          ref={mainRef}
          tabIndex={-1}
          className="flex-1 min-h-0 overflow-y-auto outline-hidden"
        >
          <div
            data-content-layout={contentLayout}
            className={cn(
              "mx-auto w-full animate-fade-in px-4 py-6 sm:px-6 xl:px-8",
              contentLayout === "contained" && "max-w-[1800px]",
            )}
          >
            {requiredFeature && featureFlagsQuery.isPending ? (
              <FeatureLoadingState />
            ) : requiredFeature && featureFlagsQuery.isError ? (
              <FeatureUnavailableState />
            ) : disabledFeature ? (
              <FeatureDisabledState />
            ) : charlieNotActivated ? (
              <CharlieDormantState />
            ) : (
              <Outlet />
            )}
          </div>
        </main>
      </div>
      <CommandPalette />
      {/*
          Mounted once at the dashboard layout level so the bottom drawer
          persists across navigation between cluster, workload, and delivery pages.
          Renders nothing unless tabs are open.
        */}
      <WindowManager />
    </div>
  );

  return (
    // ExtensionProvider wraps the whole dashboard shell once: it fetches
    // GET /extensions/mounts/ a single time and exposes the indexed registry.
    <ExtensionProvider>
      <CharlieShell
        enabled={featureFlags?.["feature.charlie"] === true && charlieActivated}
      >
        {appShell}
      </CharlieShell>
    </ExtensionProvider>
  );
}

const featurePathPrefixes: Array<{ prefix: string; flag: FeatureFlagKey }> = [
  { prefix: "/dashboard/projects", flag: "feature.projects" },
  { prefix: "/dashboard/catalog", flag: "feature.catalog" },
  { prefix: "/dashboard/tools", flag: "feature.catalog" },
  { prefix: "/dashboard/monitoring", flag: "feature.monitoring" },
  { prefix: "/dashboard/settings/monitoring", flag: "feature.monitoring" },
  { prefix: "/dashboard/security", flag: "feature.security" },
  { prefix: "/dashboard/charlie", flag: "feature.charlie" },
  { prefix: "/dashboard/extensions", flag: "feature.extensions" },
];

const clusterMonitoringPath =
  /^\/dashboard\/clusters\/[^/]+\/(metrics|monitoring-stack)(\/|$)/;

function disabledFeatureForPath(
  pathname: string,
  flags?: FeatureFlags,
): FeatureFlagKey | null {
  if (!flags) return null;
  if (
    flags["feature.monitoring"] === false &&
    clusterMonitoringPath.test(pathname)
  ) {
    return "feature.monitoring";
  }
  const match = featurePathPrefixes.find(
    ({ prefix }) => pathname === prefix || pathname.startsWith(`${prefix}/`),
  );
  if (!match) return null;
  return flags[match.flag] === false ? match.flag : null;
}

function featureForPath(pathname: string): FeatureFlagKey | null {
  if (clusterMonitoringPath.test(pathname)) return "feature.monitoring";
  return (
    featurePathPrefixes.find(
      ({ prefix }) => pathname === prefix || pathname.startsWith(`${prefix}/`),
    )?.flag ?? null
  );
}

function FeatureLoadingState() {
  return (
    <div className="p-8 text-sm text-muted-foreground" role="status">
      Checking whether this section is available…
    </div>
  );
}

function FeatureUnavailableState() {
  return (
    <StatePanel
      icon={WifiOff}
      tone="warning"
      title="Section availability could not be verified"
      description="Feature settings are temporarily unavailable. Refresh to try again."
      role="alert"
    />
  );
}

function CharlieDormantState() {
  return (
    <EmptyState
      icon={Lock}
      title="Charlie is not connected"
      description="Charlie ships dormant in Astronomer. An administrator can connect it under Settings → Charlie. The Charlie agent is pulled only after that connection is accepted."
    />
  );
}

function FeatureDisabledState() {
  return (
    <EmptyState
      icon={Lock}
      title="Section disabled"
      description="This section is disabled by platform settings."
      className="rounded-lg border border-border bg-card p-8"
    />
  );
}
