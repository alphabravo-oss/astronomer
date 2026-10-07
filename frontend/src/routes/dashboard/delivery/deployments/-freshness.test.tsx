import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { Column } from "@/components/ui/data-table";
import type {
  ClusterDeployment,
  ClusterDeploymentEvent,
} from "@/lib/api/delivery-deployments";
import * as api from "@/lib/api/delivery-deployments";
import { queryKeys } from "@/lib/query-keys";
import { setLiveStatus } from "@/lib/live/status-store";
import { DeploymentsPage } from "./-page";
import { DeploymentDetailPage } from "./$deploymentId/-page";
import { DeploymentObservationTime, DeploymentStatus } from "./-freshness";
const context = vi.hoisted(() => ({
  navigate: vi.fn(),
  search: "",
  canUpdate: false,
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  Link: (await import("@/test/router-link")).RouterLinkStub,
  useParams: () => ({ deploymentId: "deployment" }),
  useNavigate: () => context.navigate,
  useLocation: ({
    select,
  }: {
    select: (location: { searchStr: string }) => unknown;
  }) => select({ searchStr: context.search }),
}));
vi.mock("@/lib/hooks/auth", () => ({
  useCurrentUser: () => ({ data: { id: "operator" } }),
}));
vi.mock("@/lib/permissions", () => ({
  can: (_user: unknown, _resource: string, verb: string) =>
    verb !== "update" || context.canUpdate,
}));
vi.mock("@/lib/live/hooks", () => ({ useLiveQueryInvalidation: vi.fn() }));
vi.mock("@/components/delivery/shared", async (original) => ({
  ...(await original<typeof import("@/components/delivery/shared")>()),
  DeliveryShell: ({ children }: { children: ReactNode }) => children,
  useDeliveryPageIndex: () => [0, vi.fn()],
  useDeliveryWorkspace: () => ({
    projectId: "project",
    projects: [{}],
    projectQuery: { isLoading: false },
    listHref: () => "/dashboard/delivery/deployments",
    entityHref: (_kind: string, id: string) =>
      "/dashboard/delivery/deployments/" + id,
  }),
}));
vi.mock("@/lib/api/delivery-deployments", () => ({
  listClusterDeployments: vi.fn(),
  getClusterDeployment: vi.fn(),
  listClusterDeploymentEvents: vi.fn(),
  actOnClusterDeployment: vi.fn(),
}));
// Actual column rendering and toolbar; table persistence/virtualization is outside this clock test.
vi.mock("@/components/ui/data-table", () => ({
  DataTable: ({
    data,
    columns,
    toolbar,
  }: {
    data: object[];
    columns: Column<object>[];
    toolbar?: ReactNode;
  }) => (
    <div>
      {toolbar}
      {data.map((row, index) => (
        <div key={index}>
          {columns.map((column) => (
            <span key={column.key}>{column.accessor(row)}</span>
          ))}
        </div>
      ))}
    </div>
  ),
}));
const instant = "2026-10-06T12:00:00Z";
const now = Date.parse(instant);
function fixture(): ClusterDeployment {
  return {
    id: "deployment",
    targetId: "target",
    clusterId: "cluster",
    phase: "ready",
    action: "apply",
    desiredGeneration: 1,
    observedGeneration: 1,
    desiredSpecDigest: "digest",
    observedSpecDigest: "digest",
    desiredRevision: "revision",
    observedRevision: "revision",
    conditions: [
      {
        type: "Ready",
        status: "True",
        observedGeneration: 1,
        lastTransitionTime: instant,
      },
    ],
    sourceKind: "GitRepository",
    sourceName: "source",
    reconcilerKind: "Kustomization",
    reconcilerName: "reconciler",
    inventory: {
      entries: 1,
      ready: 1,
      failed: 0,
      observation: { state: "current", observedAt: instant },
    },
    agentSessionId: "session",
    agentSequence: 1,
    lastErrorCode: "",
    lastMessage: "",
    lastObservedAt: instant,
    createdAt: instant,
    updatedAt: instant,
  };
}
function client() {
  return new QueryClient({
    defaultOptions: {
      queries: { staleTime: Infinity, gcTime: Infinity, retry: false },
    },
  });
}
function mount(queryClient: QueryClient, page: ReactNode) {
  return render(
    <QueryClientProvider client={queryClient}>{page}</QueryClientProvider>,
  );
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(instant);
  setLiveStatus("open");
  context.search = "";
  context.canUpdate = false;
  vi.clearAllMocks();
});
afterEach(() => {
  cleanup();
  setLiveStatus("idle");
  vi.useRealTimers();
});
it("ages unchanged list data without fetching and retains reported-phase filters", () => {
  context.search = "?phase=ready";
  const queryClient = client();
  const deployment = fixture();
  queryClient.setQueryData(
    queryKeys.delivery.deployments("project", {
      limit: 50,
      offset: 0,
      phase: "ready",
    }),
    {
      data: [deployment],
      pagination: {
        total: 1,
        limit: 50,
        offset: 0,
        has_more: false,
        next_offset: null,
      },
    },
  );
  const timers = vi.getTimerCount();
  const view = mount(queryClient, <DeploymentsPage />);
  expect(screen.getByText("ready", { selector: "span" })).toBeInTheDocument();
  expect(screen.getByText("No drift reported")).toBeInTheDocument();
  expect(screen.getByRole("combobox", { name: "Reported phase" })).toHaveValue(
    "ready",
  );
  expect(
    screen.getByText(
      "Filters and totals use reported phase. Status reflects source freshness.",
    ),
  ).toBeInTheDocument();
  expect(vi.getTimerCount() - timers).toBe(1);
  act(() => {
    vi.advanceTimersByTime(330_000);
  });
  expect(screen.getByText("stale", { exact: true })).toBeInTheDocument();
  expect(screen.getByText("Reported phase: ready")).toBeInTheDocument();
  expect(screen.queryByText("No drift reported")).not.toBeInTheDocument();
  expect(screen.getByText("Drift evidence stale")).toBeInTheDocument();
  expect(api.listClusterDeployments).not.toHaveBeenCalled();
  expect(deployment.inventory.observation?.state).toBe("current");
  fireEvent.change(screen.getByRole("combobox", { name: "Reported phase" }), {
    target: { value: "failed" },
  });
  expect(context.navigate).toHaveBeenCalledWith({
    to: "/dashboard/delivery/deployments?phase=failed",
    replace: true,
  });
  view.unmount();
  expect(vi.getTimerCount()).toBe(timers);
  queryClient.clear();
});
it("ages detail phase and Ready condition while historical events and permissions stay unchanged", () => {
  const queryClient = client();
  queryClient.setQueryData(
    queryKeys.delivery.deployment("project", "deployment"),
    { etag: '"generation-1"', data: { deployment: fixture(), events: [] } },
  );
  const event: ClusterDeploymentEvent = {
    id: "event",
    deploymentId: "deployment",
    eventType: "phase_changed",
    fromPhase: "applying",
    toPhase: "ready",
    generation: 1,
    specDigest: "digest",
    reasonCode: "",
    message: "",
    observedAt: instant,
    createdAt: instant,
  };
  queryClient.setQueryData(
    queryKeys.delivery.deploymentEvents("project", "deployment", {
      limit: 20,
      offset: 0,
    }),
    {
      data: [event],
      pagination: {
        total: 1,
        limit: 20,
        offset: 0,
        has_more: false,
        next_offset: null,
      },
    },
  );
  const timers = vi.getTimerCount();
  const view = mount(queryClient, <DeploymentDetailPage />);
  expect(screen.getAllByText("ready", { exact: true })).toHaveLength(3);
  expect(screen.getByRole("button", { name: "Reconcile" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Suspend" })).toBeDisabled();
  expect(vi.getTimerCount() - timers).toBe(1);
  act(() => {
    vi.advanceTimersByTime(330_000);
  });
  expect(screen.getAllByText("stale", { exact: true })).toHaveLength(2);
  expect(screen.getAllByText("ready", { exact: true })).toHaveLength(1);
  expect(screen.getByText("Reported status: True")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Reconcile" })).toBeDisabled();
  expect(api.getClusterDeployment).not.toHaveBeenCalled();
  expect(api.listClusterDeploymentEvents).not.toHaveBeenCalled();
  expect(api.actOnClusterDeployment).not.toHaveBeenCalled();
  view.unmount();
  expect(vi.getTimerCount()).toBe(timers);
  queryClient.clear();
});
it.each(["failed", "deleting", "removed"] as const)(
  "presents local %s decisions without a fabricated source time",
  (phase) => {
    const deployment = {
      ...fixture(),
      phase,
      inventory: { entries: 0, ready: 0, failed: 0 },
      lastObservedAt: null,
    };
    render(
      <>
        <DeploymentStatus deployment={deployment} now={now} />
        <DeploymentObservationTime deployment={deployment} now={now} />
      </>,
    );
    expect(screen.getByText(phase, { exact: true })).toBeInTheDocument();
    expect(screen.getByText("Source freshness unknown")).toBeInTheDocument();
    expect(
      screen.getByText("Source observation time unknown"),
    ).toBeInTheDocument();
  },
);
it("uses populated legacy timestamp only as report time and never fills modern missing source time", () => {
  const deployment = {
    ...fixture(),
    inventory: { entries: 0, ready: 0, failed: 0 },
  };
  const view = render(
    <DeploymentObservationTime deployment={deployment} now={now} />,
  );
  expect(
    screen.getByText(/Reported .*source freshness unknown/),
  ).toBeInTheDocument();
  view.rerender(
    <DeploymentObservationTime
      deployment={{
        ...deployment,
        inventory: {
          ...deployment.inventory,
          observation: { state: "unsynced" },
        },
      }}
      now={now}
    />,
  );
  expect(
    screen.getByText("Source observation time unknown"),
  ).toBeInTheDocument();
  expect(screen.queryByText(/Reported /)).not.toBeInTheDocument();
});
