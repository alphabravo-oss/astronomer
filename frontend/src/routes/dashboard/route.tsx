import { useEffect, useRef, useState } from "react";
import {
  createFileRoute,
  Outlet,
  redirect,
  type ErrorComponentProps,
} from "@tanstack/react-router";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { DashboardSessionGate } from "@/components/auth/dashboard-session-gate";
import { Sidebar } from "@/components/layout/sidebar";
import { Topbar } from "@/components/layout/topbar";
import { GlobalBanner } from "@/components/layout/global-banner";
import { CommandPalette } from "@/components/layout/command-palette";
import { WindowManager } from "@/components/window-manager/window-manager";
import { ExtensionProvider } from "@/components/extensions/ExtensionProvider";
import { EmptyState, StatePanel } from "@/components/ui/empty-state";
import { errorDigest, errorMessage } from "@/lib/error-message";
import { useCharlieActivated, useFeatureFlags } from "@/lib/hooks/clusters";
import type { FeatureFlags, FeatureFlagKey } from "@/lib/api/feature-flags";
import { useLiveClusterMetricsMerger } from "@/lib/live/cluster-merger";
import { useLiveEvents } from "@/lib/live/hooks";
import { hasSessionHint } from "@/lib/auth/session";
import { useBranding } from "@/lib/hooks/public-settings";
import { dashboardContentLayout } from "@/lib/dashboard-content-layout";
import { useUserPreferences } from "@/lib/user-preferences";
import { isNavigableLandingRoute } from "@/lib/api/user-preferences";
import { CharlieShell } from "@/components/charlie/charlie-shell";
import { AppShell, NotFoundState, applyBrandAccent } from "@/ui";
import { Lock, RotateCcw, WifiOff } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { Link as RouterLink } from "@tanstack/react-router";

export const Route = createFileRoute("/dashboard")({
  // Synchronous cookie-presence guard: the JS-readable CSRF cookie is set and
  // cleared in lockstep with the HttpOnly session cookie. Async concerns
  // (must_change_password, feature flags) stay in the layout component.
  beforeLoad: ({ location }) => {
    if (!hasSessionHint()) {
      throw redirect({
        to: "/auth/login",
        search: { returnTo: location.href },
      });
    }
  },
  component: DashboardLayout,
  // Boundaries render in the <Outlet/> position, so the dashboard chrome
  // (sidebar/header) stays mounted around them.
  notFoundComponent: DashboardNotFound,
  errorComponent: DashboardError,
});

/** Dashboard 404 inside the shell. */
function DashboardNotFound() {
  return <NotFoundState homeTo="/dashboard" />;
}

/**
 * Route-level error boundary for the dashboard segment. A render error in any
 * dashboard page is caught here instead of white-screening the console.
 */
function DashboardError({ error, reset }: ErrorComponentProps) {
  useEffect(() => {
    console.error("Dashboard render error:", error);
  }, [error]);
  const digest = errorDigest(error);

  return (
    <div data-testid="route-error-boundary" className="flex flex-col items-center">
      <StatePanel
        icon={WifiOff}
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
                Reference: {digest}
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
        className="-mt-6 inline-flex h-(--control-h) items-center gap-2 rounded-lg border border-border px-4 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      >
        Back to overview
      </RouterLink>
    </div>
  );
}

function DashboardLayout() {
  return (
    <DashboardSessionGate>
      <DashboardAuthorizedShell />
    </DashboardSessionGate>
  );
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

  // Operator branding (public settings) is a theme-level accent: it colors
  // the mark, the active nav indicator and primary buttons only. Links,
  // selected rows and status stay neutral. Restored on unmount.
  const primaryColorHex = useBranding().data?.["branding.primary_color"];
  useEffect(
    () => applyBrandAccent(operatorBrandAccent(primaryColorHex)),
    [primaryColorHex],
  );

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

  // One SSE connection for the whole dashboard; per-page hooks reuse it.
  useLiveEvents();
  useLiveClusterMetricsMerger();

  const appShell = (
    <AppShell
      sidebar={<Sidebar />}
      header={<Topbar />}
      banners={<GlobalBanner />}
      offline={!online}
      contentLayout={contentLayout}
      mainRef={mainRef}
      overlays={
        <>
          <CommandPalette />
          {/* Mounted once so the bottom drawer persists across navigation. */}
          <WindowManager />
        </>
      }
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
    </AppShell>
  );

  return (
    <ExtensionProvider>
      <CharlieShell
        enabled={featureFlags?.["feature.charlie"] === true && charlieActivated}
      >
        {appShell}
      </CharlieShell>
    </ExtensionProvider>
  );
}

/**
 * `branding.primary_color` from `GET /api/v1/settings/branding/` is empty
 * until an operator chooses a color (backend default "", migration 00084).
 * Empty or blank means "unconfigured": the product accent applies.
 */
export function operatorBrandAccent(hex: string | undefined): string | null {
  const value = hex?.trim();
  return value ? value : null;
}

const featurePathPrefixes: Array<{ prefix: string; flag: FeatureFlagKey }> = [
  { prefix: "/dashboard/projects", flag: "feature.projects" },
  { prefix: "/dashboard/catalog", flag: "feature.catalog" },
  { prefix: "/dashboard/tools", flag: "feature.catalog" },
  { prefix: "/dashboard/monitoring", flag: "feature.monitoring" },
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
      actionLabel="Refresh"
      actionIcon={RotateCcw}
      onAction={() => window.location.reload()}
    />
  );
}

function CharlieDormantState() {
  return (
    <EmptyState
      icon={Lock}
      title="Charlie is not connected"
      description="Charlie ships dormant in Astronomer. An administrator can connect it under Settings › Charlie. The Charlie agent is pulled only after that connection is accepted."
      actionLabel="Go to Charlie settings"
      actionHref="/dashboard/settings/charlie"
    />
  );
}

function FeatureDisabledState() {
  return (
    <div className="rounded-lg border border-border bg-card p-8">
      <EmptyState
        icon={Lock}
        title="Section disabled"
        description="This section is disabled by platform settings."
        // terminal: gated by a platform feature flag, not something this viewer can change.
        terminal
      />
      <div className="flex justify-center">
        <ActionButton
          onClick={() => window.history.back()}
          icon={<RotateCcw className="h-4 w-4" />}
        >
          Go back
        </ActionButton>
      </div>
    </div>
  );
}
