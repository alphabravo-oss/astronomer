import { act, cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ComponentType, ReactNode } from "react";
import type { Column } from "@/components/ui/data-table";
import type { DeliverySystemComponent } from "@/lib/api/delivery-system";
import { getClusterDeliveryInventory } from "@/lib/api/delivery-system";
import { queryKeys } from "@/lib/query-keys";
import { setLiveStatus } from "@/lib/live/status-store";
import { Route } from "./index";
import { SystemComponentContent } from "./$componentId/-content";
import { systemComponentColumns } from "./-columns";
vi.mock("@tanstack/react-router", async (original) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await original<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
    createFileRoute: () => (options: object) => ({
      options,
      useParams: () => ({ id: "cluster" }),
    }),
  };
});
vi.mock("@/lib/hooks/auth", () => ({
  useCurrentUser: () => ({ data: { id: "operator" } }),
}));
vi.mock("@/lib/permissions", () => ({ can: () => true }));
vi.mock("@/components/delivery/shared", async (original) => ({
  ...(await original<typeof import("@/components/delivery/shared")>()),
  useDeliveryProjectScope: () => ({
    projectId: "project",
    projects: [{}],
    projectQuery: { isLoading: false },
  }),
}));
vi.mock("@/lib/api/delivery-system", () => ({
  getClusterDeliveryInventory: vi.fn(),
}));
// Exercise actual column accessors without unrelated table virtualization/preferences.
vi.mock("@/components/ui/data-table", () => ({
  DataTable: ({
    data,
    columns,
  }: {
    data: { id?: string; name: string }[];
    columns: Column<{ id?: string; name: string }>[];
  }) => (
    <div>
      <div>
        {data.map((row) => (
          <div key={row.id || row.name}>
            {columns.map((column) => (
              <span key={column.key}>{column.accessor(row) as ReactNode}</span>
            ))}
          </div>
        ))}
      </div>
    </div>
  ),
}));
const instant = "2026-10-06T12:00:00Z";
function fixture(): DeliverySystemComponent {
  return {
    id: "controller",
    name: "Controller",
    category: "delivery",
    owner: "flux",
    managementMethod: "flux",
    kind: "Deployment",
    health: "healthy",
    highAvailability: false,
    observation: { state: "current", observedAt: instant },
    resources: [
      {
        name: "child",
        kind: "Deployment",
        version: "v1",
        plural: "deployments",
        health: "healthy",
      },
    ],
    volumes: [
      {
        name: "claim",
        namespace: "default",
        phase: "Bound",
        expansionAllowed: false,
      },
    ],
  };
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(instant);
  setLiveStatus("open");
  vi.clearAllMocks();
});
afterEach(() => {
  cleanup();
  setLiveStatus("idle");
  vi.useRealTimers();
});
it("ages list badges and counts without new query data or polling", async () => {
  const component = fixture();
  const client = new QueryClient({
    defaultOptions: {
      queries: { staleTime: Infinity, gcTime: Infinity, retry: false },
    },
  });
  client.setQueryData(
    queryKeys.delivery.clusterInventory("project", "cluster"),
    {
      controllerInventory: {
        observedAt: instant,
        systemComponents: [component],
      },
    },
  );
  const Page = Route.options.component as ComponentType & {
    preload?: () => Promise<void>;
  };
  await Page.preload?.();
  const timers = vi.getTimerCount();
  const view = render(
    <QueryClientProvider client={client}>
      <Page />
    </QueryClientProvider>,
  );
  expect(screen.getByText("healthy", { exact: true })).toBeInTheDocument();
  expect(screen.getByText("1 / 0", { exact: true })).toBeInTheDocument();
  expect(vi.getTimerCount() - timers).toBe(1);
  act(() => {
    vi.advanceTimersByTime(330_000);
  });
  expect(
    screen.queryByText("healthy", { exact: true }),
  ).not.toBeInTheDocument();
  expect(screen.getByText("stale", { exact: true })).toBeInTheDocument();
  expect(screen.getByText("0 / 1", { exact: true })).toBeInTheDocument();
  expect(getClusterDeliveryInventory).not.toHaveBeenCalled();
  expect(component.health).toBe("healthy");
  const healthColumn = systemComponentColumns("cluster", Date.now()).find(
    (column) => column.key === "health",
  )!;
  expect(healthColumn.sortAccessor!(component)).toBe("stale");
  view.unmount();
  expect(vi.getTimerCount()).toBe(timers);
  client.clear();
});
it("ages detail and nested evidence together using one cleaned-up clock", () => {
  const timers = vi.getTimerCount();
  const view = render(
    <SystemComponentContent
      component={fixture()}
      clusterId="cluster"
      workloadHref="/workload"
    />,
  );
  expect(screen.getAllByText("healthy", { exact: true })).toHaveLength(2);
  expect(screen.getByText("Bound", { exact: true })).toBeInTheDocument();
  expect(vi.getTimerCount() - timers).toBe(1);
  act(() => {
    vi.advanceTimersByTime(330_000);
  });
  expect(
    screen.queryByText("healthy", { exact: true }),
  ).not.toBeInTheDocument();
  expect(screen.queryByText("Bound", { exact: true })).not.toBeInTheDocument();
  expect(screen.getAllByText("stale", { exact: true })).toHaveLength(3);
  expect(screen.getByRole("link", { name: "Open workload" })).toHaveAttribute(
    "href",
    "/workload",
  );
  view.unmount();
  expect(vi.getTimerCount()).toBe(timers);
});
it("labels legacy source time as unknown and preserves health", () => {
  const component = fixture();
  delete component.observation;
  render(
    <SystemComponentContent
      component={component}
      clusterId="cluster"
      workloadHref=""
    />,
  );
  act(() => {
    vi.advanceTimersByTime(600_000);
  });
  expect(
    screen.getByText("Source observation time unavailable (legacy agent)"),
  ).toBeInTheDocument();
  expect(screen.getAllByText("healthy", { exact: true })).toHaveLength(2);
});
