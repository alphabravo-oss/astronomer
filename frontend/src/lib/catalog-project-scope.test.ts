import { afterEach, describe, expect, it, vi } from "vitest";
import api, {
  getHelmCharts,
  getHelmChartVersions,
  installHelmChart,
} from "@/lib/api";
import {
  getChartDefaultValues,
  installChartOnCluster,
  listCatalogCharts,
  listRecommendedCharts,
  listChartVersions,
} from "@/lib/api/cluster-detail";

describe("catalog project isolation", () => {
  afterEach(() => vi.restoreAllMocks());

  it("scopes every chart browse and version request to a project", async () => {
    const get = vi.spyOn(api, "get").mockResolvedValue({
      data: { data: [], count: 0 },
    });

    await getHelmCharts({ projectId: "project-1", search: "metrics" });
    await getHelmChartVersions("project-1", "chart-1");
    await listCatalogCharts({ projectId: "project-1", limit: 60 });
    await listRecommendedCharts("project-1", 12);
    await listChartVersions("project-1", "chart-1");
    await getChartDefaultValues("project-1", "chart-1", "1.2.3");

    expect(get).toHaveBeenNthCalledWith(1, "/catalog/charts", {
      params: { project_id: "project-1", search: "metrics" },
    });
    expect(get).toHaveBeenNthCalledWith(
      2,
      "/catalog/charts/chart-1/versions",
      { params: { project_id: "project-1" } },
    );
    expect(String(get.mock.calls[2][0])).toContain("project_id=project-1");
    expect(get).toHaveBeenNthCalledWith(
      4,
      "/catalog/recommendations/popular/",
      { params: { project_id: "project-1", limit: 12 } },
    );
    expect(get).toHaveBeenNthCalledWith(
      5,
      "/catalog/charts/chart-1/versions/",
      { params: { project_id: "project-1", limit: 50 } },
    );
    expect(get).toHaveBeenNthCalledWith(
      6,
      "/catalog/charts/chart-1/values/",
      { params: { project_id: "project-1", version: "1.2.3" } },
    );
  });

  it("binds chart-version installs to the selected project", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({
      data: { data: { id: "installation-1" } },
    });
    const body = {
      project_id: "project-1",
      cluster_id: "cluster-1",
      chart_version_id: "version-1",
      release_name: "metrics",
      namespace: "monitoring",
      values_override: "replicas: 2",
    };

    await installHelmChart(body);
    await installChartOnCluster({
      projectId: "project-1",
      clusterId: "cluster-1",
      chartVersionId: "version-1",
      releaseName: "metrics",
      namespace: "monitoring",
      valuesOverride: "replicas: 2",
    });

    expect(post).toHaveBeenNthCalledWith(1, "/catalog/installed", body);
    expect(post).toHaveBeenNthCalledWith(2, "/catalog/installed/", body);
  });
});
