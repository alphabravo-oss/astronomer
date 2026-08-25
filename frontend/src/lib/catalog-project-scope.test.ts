import { afterEach, describe, expect, it, vi } from "vitest";
import * as generated from "@/lib/api/generated/client";
import {
  getHelmCharts,
  getHelmChartVersions,
  installHelmChart,
  upgradeInstalledChart,
} from "@/lib/api/catalog";
import {
  getChartDefaultValues,
  installChartOnCluster,
  listCatalogCharts,
  listRecommendedCharts,
  listChartVersions,
} from "@/lib/api/cluster-detail";

vi.mock("@/lib/api/generated/client", async (importOriginal) => {
  const actual = await importOriginal<
    typeof import("@/lib/api/generated/client")
  >();
  return {
    ...actual,
    getCatalogCharts: vi.fn(),
    getCatalogRecommendationsPopular: vi.fn(),
    getCatalogChartsByIdVersions: vi.fn(),
    getCatalogChartsByIdValues: vi.fn(),
    postCatalogInstalled: vi.fn(),
    putCatalogInstalledByIdUpgrade: vi.fn(),
  };
});

const installationWire = {
  id: "1fa85f64-5717-4562-b3fc-2c963f66afa6",
  cluster_id: "2fa85f64-5717-4562-b3fc-2c963f66afa6",
  chart_version_id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  release_name: "metrics",
  namespace: "monitoring",
  status: "pending_install",
  revision: 1,
  notes: "",
  tool_slug: null,
  preset_used: null,
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
};

const UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

describe("catalog project isolation", () => {
  afterEach(() => {
    vi.clearAllMocks();
    vi.restoreAllMocks();
  });

  it("scopes every generated chart browse and version request to a project", async () => {
    vi.mocked(generated.getCatalogCharts).mockResolvedValueOnce({
      data: [],
      count: 0,
      next: null,
      previous: null,
    });
    vi.mocked(generated.getCatalogChartsByIdVersions).mockResolvedValueOnce({
      data: [],
      pagination: { limit: 200, offset: 0, has_more: false, next_offset: null },
    });

    await getHelmCharts({ projectId: "project-1", search: "metrics" });
    await getHelmChartVersions("project-1", "chart-1");

    expect(generated.getCatalogCharts).toHaveBeenCalledWith({
      query: { project_id: "project-1", limit: 200 },
    });
    expect(generated.getCatalogChartsByIdVersions).toHaveBeenCalledWith({
      path: { id: "chart-1" },
      query: { project_id: "project-1", limit: 200 },
    });
  });

  it("keeps compatibility browse helpers project-scoped", async () => {
    vi.mocked(generated.getCatalogCharts).mockResolvedValueOnce({
      data: [], count: 0, next: null, previous: null,
    });
    vi.mocked(generated.getCatalogRecommendationsPopular).mockResolvedValueOnce({
      data: [], count: 0, next: null, previous: null,
    });
    vi.mocked(generated.getCatalogChartsByIdVersions).mockResolvedValueOnce({
      data: [], pagination: { limit: 50, offset: 0, has_more: false, next_offset: null },
    });
    vi.mocked(generated.getCatalogChartsByIdValues).mockResolvedValueOnce({
      chart: "metrics", version: "1.2.3", default_values: "",
    });

    await listCatalogCharts({ projectId: "project-1", limit: 60 });
    await listRecommendedCharts("project-1", 12);
    await listChartVersions("project-1", "chart-1");
    await getChartDefaultValues("project-1", "chart-1", "1.2.3");

    expect(generated.getCatalogCharts).toHaveBeenCalledWith({
      query: { project_id: "project-1", limit: 60, offset: undefined },
      signal: undefined,
    });
    expect(generated.getCatalogRecommendationsPopular).toHaveBeenCalledWith({
      query: { project_id: "project-1", limit: 12 }, signal: undefined,
    });
    expect(generated.getCatalogChartsByIdVersions).toHaveBeenCalledWith({
      path: { id: "chart-1" }, query: { project_id: "project-1", limit: 50 }, signal: undefined,
    });
    expect(generated.getCatalogChartsByIdValues).toHaveBeenCalledWith({
      path: { id: "chart-1" },
      query: { project_id: "project-1", version: "1.2.3" },
      signal: undefined,
    });
  });

  it("binds installs to the selected project", async () => {
    vi.mocked(generated.postCatalogInstalled).mockResolvedValueOnce({
      data: { installation: installationWire, operation: {} },
    });
    const body = {
      project_id: "project-1",
      cluster_id: installationWire.cluster_id,
      chart_version_id: installationWire.chart_version_id,
      release_name: "metrics",
      namespace: "monitoring",
      values_override: "replicas: 2",
    };

    await installHelmChart(body);
    expect(generated.postCatalogInstalled).toHaveBeenCalledWith({
      headerParams: { "Idempotency-Key": expect.stringMatching(UUID_V4) },
      body,
    });
  });

  it("sends the selected version during upgrades", async () => {
    vi.mocked(generated.putCatalogInstalledByIdUpgrade).mockResolvedValueOnce({
      data: { installation: installationWire, operation: {} },
    });

    await upgradeInstalledChart(installationWire.id, {
      chart_version_id: installationWire.chart_version_id,
      values_override: "replicas: 3",
    });

    expect(generated.putCatalogInstalledByIdUpgrade).toHaveBeenCalledWith({
      path: { id: installationWire.id },
      headerParams: { "Idempotency-Key": expect.stringMatching(UUID_V4) },
      body: {
        chart_version_id: installationWire.chart_version_id,
        values_override: "replicas: 3",
      },
    });
  });

  it("keeps the cluster install compatibility helper project-bound", async () => {
    vi.mocked(generated.postCatalogInstalled).mockResolvedValueOnce({
      data: { installation: { ...installationWire, id: "installation-1" }, operation: {} },
    });

    await installChartOnCluster({
      projectId: "project-1",
      clusterId: "cluster-1",
      chartVersionId: "version-1",
      releaseName: "metrics",
      namespace: "monitoring",
      valuesOverride: "replicas: 2",
    });

    expect(generated.postCatalogInstalled).toHaveBeenCalledWith({
      headerParams: { "Idempotency-Key": expect.stringMatching(UUID_V4) },
      body: {
        project_id: "project-1",
        cluster_id: "cluster-1",
        chart_version_id: "version-1",
        release_name: "metrics",
        namespace: "monitoring",
        values_override: "replicas: 2",
      },
      signal: undefined,
    });
  });
});
