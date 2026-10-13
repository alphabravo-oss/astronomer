import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { PageHeader } from "@/components/ui/page";
import { render, screen } from "@testing-library/react";
import { ApplicationConfiguration } from "./-configuration-page";
import { getHelmChart } from "@/lib/api/catalog";
import type { HelmChart } from "@/types";

const state = vi.hoisted(() => ({ project: "project-a", navigate: vi.fn() }));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  useNavigate: () => state.navigate,
}));
vi.mock("@/lib/api/catalog", () => ({ getHelmChart: vi.fn() }));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: () => ({ allowed: true }),
}));
vi.mock("@/components/catalog/project-scope", () => ({
  useCatalogProjectScope: () => ({
    projectId: state.project,
    project: state.project
      ? { id: state.project, clusterId: "cluster-a" }
      : undefined,
  }),
  CatalogProjectPicker: () => <p>Choose project</p>,
}));
vi.mock("@/components/clusters/remote-cluster-picker", () => ({
  RemoteClusterPicker: () => <p>Choose cluster</p>,
}));
vi.mock("@/components/clusters/app-install-page", () => ({
  AppInstallPage: ({
    projectId,
    clusterId,
    mode,
  }: {
    projectId: string;
    clusterId: string;
    mode: { chartId: string };
  }) => (
    <PageHeader title={`Configure ${projectId}/${clusterId}/${mode.chartId}`} />
  ),
}));
function show(cluster = "cluster-a") {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <ApplicationConfiguration
        search={{ chart: "chart-a", project: "project-a", cluster }}
      />
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  vi.clearAllMocks();
  state.project = "project-a";
  vi.mocked(getHelmChart).mockResolvedValue({
    id: "chart-a",
    name: "loki",
  } as HelmChart);
});
it("resolves a direct link by exact chart and project instead of a browse-page row", async () => {
  show();
  expect(
    await screen.findByRole("heading", {
      name: "Configure project-a/cluster-a/chart-a",
    }),
  ).toBeVisible();
  expect(getHelmChart).toHaveBeenCalledWith(
    "project-a",
    "chart-a",
    "project",
    expect.any(AbortSignal),
  );
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});
it("withholds the editor when the selected project becomes unavailable", () => {
  state.project = "";
  show();
  expect(screen.getByText("Choose project")).toBeVisible();
  expect(getHelmChart).not.toHaveBeenCalled();
  expect(
    screen.queryByRole("heading", { name: /^Configure/ }),
  ).not.toBeInTheDocument();
});
it("requires an allowed target cluster before mounting the editor", () => {
  show("foreign-cluster");
  expect(screen.getByText("Choose cluster")).toBeVisible();
  expect(
    screen.queryByRole("heading", { name: /^Configure/ }),
  ).not.toBeInTheDocument();
});
