import { catalogControllerWindowKey } from "./query-keys-catalog-controller";

export const catalogQueryKeys = {
  controllerWindow: catalogControllerWindowKey,
  release: (id: string) => ["catalog", "release", id] as const,
  releaseValues: (id: string) => ["catalog", "release", id, "values"] as const,
  releaseHistory: (id: string) =>
    ["catalog", "release", id, "history"] as const,
  all: ["catalog"] as const,
  applications: ["catalog", "applications"] as const,
  applicationSources: ["catalog", "application-sources"] as const,
  discovery: ["catalog", "discovery"] as const,
  repositories: ["catalog", "repositories"] as const,
  repository: (id: string) => ["catalog", "repositories", id] as const,
  repositoriesFor: (scopeId?: string, params?: Record<string, unknown>) =>
    [
      "catalog",
      "repositories",
      scopeId ?? "global",
      ...(params ? [params] : []),
    ] as const,
  charts: (params?: Record<string, unknown>) =>
    ["catalog", "charts", params] as const,
  chartVersions: (
    scopeId: string,
    chartId: string,
    scope: "cluster" | "project" = "project",
    params?: Record<string, unknown>,
  ) =>
    [
      "catalog",
      scope,
      scopeId,
      "charts",
      chartId,
      "versions",
      ...(params ? [params] : []),
    ] as const,
  installed: (params?: Record<string, unknown>) =>
    ["catalog", "installed", params] as const,
  // Prefix matching every `installed(params)` variant — used by the live
  // routing table on `catalog_release.changed` + Helm-Secret k8s events.
  installedAll: ["catalog", "installed"] as const,
  upgradeVersions: (installationId: string) =>
    ["catalog", "installed", installationId, "upgrade-versions"] as const,
  operations: ["catalog", "operations"] as const,
  operation: (id: string) => ["catalog", "operations", id] as const,
  installChartValues: (projectId: string, chartId: string, version?: string) =>
    ["catalog", projectId, "chart-values", chartId, version] as const,
  applicationPreview: (
    clusterId: string,
    chartVersionId: string,
    namespace: string,
    values: string,
    operation: "install" | "upgrade",
    fromChartVersionId?: string,
  ) =>
    [
      "catalog",
      "application-preview",
      clusterId,
      chartVersionId,
      namespace,
      values,
      operation,
      ...(fromChartVersionId ? [fromChartVersionId] : []),
    ] as const,
  chart: (scopeId: string, chartId: string) =>
    ["catalog", scopeId, "chart", chartId] as const,
  chartReadme: (scopeId: string, chartId: string, version?: string) =>
    ["catalog", scopeId, "chart", chartId, "readme", version] as const,
};
