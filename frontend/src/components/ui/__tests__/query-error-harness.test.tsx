/**
 * Error-state harness (P023.9): ten pages, each rendered with its primary
 * query forced to fail, asserting a real error surface (role="alert") shows
 * up instead of an empty state. See query-error-harness.tsx for the shared
 * assertion helper.
 *
 * All `vi.mock` calls sit at module top level (Vitest hoists them here
 * regardless of nesting, so declaring them inside `it()` would silently
 * apply every mock to every test); each mocked function is a `vi.fn()`
 * reconfigured per test via `mockRejectedValueOnce`/`mockResolvedValue`.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, it, vi } from "vitest";
import { expectErrorSurfaceNotEmptyState } from "./query-error-harness";

const fns = vi.hoisted(() => ({
  getClusters: vi.fn(),
  getClusterEstateSummary: vi.fn(),
  getCluster: vi.fn(),
  listClusterApps: vi.fn(),
  getDeliveryEstate: vi.fn(),
  getClusterAgents: vi.fn(),
  useCISScans: vi.fn(),
  useAlertEvents: vi.fn(),
  getGlobalRoles: vi.fn(),
  useManagementBackupStatus: vi.fn(),
}));

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
    getRouteApi: () => ({
      useParams: () => ({ id: "c1", name: "cost" }),
      useSearch: () => ({}),
    }),
    useNavigate: () => vi.fn(),
    useLocation: <T,>({
      select,
    }: {
      select: (location: { pathname: string; searchStr: string }) => T;
    }) => select({ pathname: "/dashboard", searchStr: "" }),
    createFileRoute: () => (routeOptions: Record<string, unknown>) => ({
      useParams: () => ({ id: "c1", name: "cost" }),
      useSearch: () => ({}),
      options: routeOptions,
    }),
  };
});

vi.mock("@/lib/live/hooks", () => ({
  useLiveQueryInvalidation: () => undefined,
  useLiveEvents: () => undefined,
}));

vi.mock("@/lib/hooks/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/hooks/auth")>()),
  useCurrentUser: () => ({ data: { id: "operator" } }),
}));
vi.mock("@/lib/permissions", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/permissions")>()),
  can: () => true,
}));

vi.mock("@/lib/api/clusters", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/clusters")>()),
  getClusters: fns.getClusters,
  getClusterEstateSummary: fns.getClusterEstateSummary,
  getCluster: fns.getCluster,
}));
vi.mock("@/lib/hooks/audit", () => ({
  useActivityFeed: () => ({ data: undefined, isLoading: false }),
}));
vi.mock("@/lib/hooks/tools", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/hooks/tools")>()),
  useTools: () => ({ data: undefined }),
  useClusterToolsStatus: () => ({ data: undefined }),
}));
vi.mock("@/lib/api/dashboards", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/dashboards")>()),
  renderGlobal: vi.fn().mockResolvedValue([]),
}));

vi.mock("@/lib/api/cluster-apps", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/cluster-apps")>()),
  listClusterApps: fns.listClusterApps,
}));

vi.mock("@/lib/api/delivery-system", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/delivery-system")>()),
  getDeliveryEstate: fns.getDeliveryEstate,
}));
vi.mock("@/components/delivery/shared", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/components/delivery/shared")>()),
  DeliveryShell: ({ children }: { children: ReactNode }) => children,
  useDeliveryProjectScope: () => ({
    projectId: "",
    projects: [],
    projectQuery: { isLoading: false, isError: false, refetch: vi.fn() },
    setProjectId: vi.fn(),
  }),
}));

vi.mock("@/lib/api/cluster-agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/cluster-agents")>()),
  getClusterAgents: fns.getClusterAgents,
}));

vi.mock("@/components/security/hooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/components/security/hooks")>()),
  useCISScans: fns.useCISScans,
}));

vi.mock("@/lib/hooks/alerting", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/hooks/alerting")>()),
  useAlertEvents: fns.useAlertEvents,
  useAlertEventSummary: () => ({ data: undefined }),
}));

vi.mock("@/lib/api/rbac", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/rbac")>()),
  getGlobalRoles: fns.getGlobalRoles,
}));

vi.mock("@/components/settings/hooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/components/settings/hooks")>()),
  useIsSuperuser: () => ({ isSuperuser: true, ready: true }),
  useBackupDrillHistory: () => ({
    data: undefined,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
  useManagementBackupStatus: fns.useManagementBackupStatus,
}));

vi.mock("@/components/settings/backup-drill-hooks", () => ({
  useLatestBackupDrill: () => ({ data: undefined, isLoading: false }),
}));

import { DashboardPage } from "@/routes/dashboard/-page";
import { ClustersPage } from "@/routes/dashboard/clusters/-page";
import { ClusterDetailPage } from "@/routes/dashboard/clusters/$id/-page";
import { ClusterAppsPage } from "@/routes/dashboard/clusters/$id/apps/-page";
import { DeliveryOverviewPage } from "@/routes/dashboard/delivery/-page";
import { ClusterAgentsPage } from "@/routes/dashboard/agents/-page";
import { SecurityPage } from "@/routes/dashboard/security/-page";
import { AlertingPage } from "@/routes/dashboard/alerting/-page";
import RBACPage from "@/routes/dashboard/rbac/-page";
import { AstronomerBackupPage } from "@/routes/dashboard/settings/backup/-page";

function mount(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

const emptyPage = {
  data: [],
  pagination: {
    total: 0,
    limit: 50,
    offset: 0,
    has_more: false,
    next_offset: null,
  },
};

beforeEach(() => {
  vi.clearAllMocks();
  fns.getClusters.mockResolvedValue(emptyPage);
  fns.getClusterEstateSummary.mockResolvedValue({
    total: 0,
    healthy: 0,
    warning: 0,
    error: 0,
    disconnected: 0,
  });
  fns.getCluster.mockResolvedValue(undefined);
  fns.listClusterApps.mockResolvedValue(emptyPage);
  fns.getDeliveryEstate.mockResolvedValue({
    summary: {
      managedClusters: 0,
      fluxReady: 0,
      incompatible: 0,
      disconnected: 0,
      stale: 0,
      assignments: 0,
      drifted: 0,
      failed: 0,
      degraded: 0,
      activeRollouts: 0,
    },
    clusters: [],
    attention: [],
    distributions: { compatibility: [], privilege: [], assignmentPhases: [] },
  });
  fns.getClusterAgents.mockResolvedValue(emptyPage);
  fns.useCISScans.mockReturnValue({
    data: emptyPage,
    isLoading: false,
    isError: false,
    error: undefined,
    refetch: vi.fn(),
  });
  fns.useAlertEvents.mockReturnValue({
    data: emptyPage,
    isLoading: false,
    isError: false,
    error: undefined,
    refetch: vi.fn(),
  });
  fns.getGlobalRoles.mockResolvedValue([]);
  fns.useManagementBackupStatus.mockReturnValue({
    data: undefined,
    isLoading: false,
    isError: false,
    error: undefined,
    refetch: vi.fn(),
  });
});

describe("error-state harness", () => {
  it("dashboard index: clusters query failure shows an alert, not the empty roster", async () => {
    fns.getClusters.mockRejectedValue(new Error("clusters down"));
    mount(<DashboardPage />);
    await vi.waitFor(() =>
      expectErrorSurfaceNotEmptyState(["No clusters registered yet"]),
    );
  });

  it("clusters index: list query failure shows an alert, not 'No clusters registered'", async () => {
    fns.getClusters.mockRejectedValue(new Error("clusters down"));
    mount(<ClustersPage />);
    await vi.waitFor(() =>
      expectErrorSurfaceNotEmptyState(["No clusters registered"]),
    );
  });

  it("cluster overview: cluster query failure shows an alert, not 'Cluster not found'", async () => {
    fns.getCluster.mockRejectedValue(new Error("cluster down"));
    mount(<ClusterDetailPage />);
    await vi.waitFor(() =>
      expectErrorSurfaceNotEmptyState(["Cluster not found"]),
    );
  });

  it("cluster apps: installed-apps query failure shows an alert", async () => {
    fns.listClusterApps.mockRejectedValue(new Error("catalog down"));
    mount(<ClusterAppsPage />);
    await vi.waitFor(() => expectErrorSurfaceNotEmptyState([]));
  });

  it("delivery index: estate query failure shows an alert", async () => {
    // The page's own query retries transient failures (up to 2 attempts
    // with backoff) regardless of the QueryClient's retry:false default —
    // give it room to exhaust those before asserting.
    fns.getDeliveryEstate.mockRejectedValue(new Error("estate down"));
    mount(<DeliveryOverviewPage />);
    await vi.waitFor(() => expectErrorSurfaceNotEmptyState([]), {
      timeout: 5000,
    });
  }, 10000);

  it("agents: list query failure shows an alert, not 'No agents available'", async () => {
    fns.getClusterAgents.mockRejectedValue(new Error("agents down"));
    mount(<ClusterAgentsPage />);
    await vi.waitFor(() =>
      expectErrorSurfaceNotEmptyState(["No agents available"]),
    );
  });

  it("security: CIS scans query failure shows an alert, not 'No CIS scans yet'", async () => {
    fns.useCISScans.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error("cis down"),
      refetch: vi.fn(),
    });
    mount(<SecurityPage />);
    await vi.waitFor(() =>
      expectErrorSurfaceNotEmptyState(["No CIS scans yet"]),
    );
  });

  it("alerting: active-events query failure shows an alert", async () => {
    fns.useAlertEvents.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error("events down"),
      refetch: vi.fn(),
    });
    mount(<AlertingPage />);
    await vi.waitFor(() => expectErrorSurfaceNotEmptyState([]));
  });

  it("rbac: global roles query failure shows an alert", async () => {
    fns.getGlobalRoles.mockRejectedValue(new Error("rbac down"));
    mount(<RBACPage />);
    await vi.waitFor(() => expectErrorSurfaceNotEmptyState([]));
  });

  it("settings/backup: backup status query failure shows an alert", async () => {
    fns.useManagementBackupStatus.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error("backup status down"),
      refetch: vi.fn(),
    });
    mount(<AstronomerBackupPage />);
    await vi.waitFor(() => expectErrorSurfaceNotEmptyState([]));
  });
});
