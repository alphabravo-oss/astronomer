/** Installed applications and project-scoped catalog browsing APIs. */

import * as generated from "@/lib/api/generated/client";
import { createIdempotencyKey } from "@/lib/api/idempotency";
import { mapPage } from "@/lib/api/pagination";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { PaginatedResponse } from "@/types";

// ---------------------------------------------------------------------
// Sprint 082+ — per-cluster Apps tab.
//
// Wraps the enriched /clusters/{id}/apps/ endpoint plus the existing
// /catalog/* surfaces (browse charts, get chart values, install).
// Snake→camel mapping at the boundary so page components can stay
// camelCase end-to-end.
// ---------------------------------------------------------------------

export interface ClusterAppRow {
  id: string;
  clusterId: string;
  // chartId is the parent helm_charts UUID; needed by the Upgrade
  // modal to drive the version dropdown. Empty for Tools installs
  // (chart_version_id NULL → JOIN collapses to NULL).
  chartId: string;
  chartVersionId: string;
  releaseName: string;
  namespace: string;
  status: string;
  revision: number;
  // valuesOverride is the user's current helm values_override on
  // the release — pre-fills the Upgrade modal's YAML editor.
  valuesOverride: string;
  toolSlug: string;
  presetUsed: string;
  // sourceKind = 'app' for catalog-installed releases, 'tool' for
  // Platform Baseline / Tools-tab installs. Drives the "Managed by
  // Tools" pivot pill in the UI.
  sourceKind: "app" | "tool";
  displayName: string;
  chartName: string;
  chartVersion: string;
  chartAppVersion: string;
  chartDescription: string;
  chartIconUrl: string;
  chartCategory: string;
  repoName: string;
  repoType: string;
  createdAt: string;
  updatedAt: string;
}

export async function listClusterApps(
  clusterId: string,
  opts: { limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<PaginatedResponse<ClusterAppRow>> {
  const wire = await generated.getClustersByClusterIdApps({
    path: { cluster_id: clusterId },
    query: opts,
    signal,
  });
  const rows = (wire.data ??
    []) as OpenAPIComponents["schemas"]["InstalledAppEnriched"][];
  return mapPage(
    { data: rows, pagination: wire.pagination },
    (raw): ClusterAppRow => ({
      id: raw.id ?? "",
      clusterId: raw.cluster_id ?? "",
      chartId: raw.chart_id ?? "",
      chartVersionId: raw.chart_version_id ?? "",
      releaseName: raw.release_name ?? "",
      namespace: raw.namespace ?? "",
      status: raw.status ?? "",
      revision: raw.revision ?? 0,
      valuesOverride: raw.values_override ?? "",
      toolSlug: raw.tool_slug ?? "",
      presetUsed: raw.preset_used ?? "",
      sourceKind: raw.source_kind ?? "app",
      displayName: raw.display_name ?? "",
      chartName: raw.chart_name ?? "",
      chartVersion: raw.chart_version ?? "",
      chartAppVersion: raw.chart_app_version ?? "",
      chartDescription: raw.chart_description ?? "",
      chartIconUrl: raw.chart_icon_url ?? "",
      chartCategory: raw.chart_category ?? "",
      repoName: raw.repo_name ?? "",
      repoType: raw.repo_type ?? "",
      createdAt: raw.created_at ?? "",
      updatedAt: raw.updated_at ?? "",
    }),
  );
}

// Browse view: lists charts in the catalog. Wraps existing
// /catalog/charts/ but normalises the response shape and snake→camel.
export interface CatalogChartSummary {
  id: string;
  repositoryId: string;
  name: string;
  displayName: string;
  description: string;
  iconUrl: string;
  homeUrl: string;
  category: string;
  keywords: string[];
  deprecated: boolean;
}

export async function listCatalogCharts(params: {
  projectId: string;
  limit?: number;
  offset?: number;
  search?: string;
  signal?: AbortSignal;
}): Promise<PaginatedResponse<CatalogChartSummary>> {
  const wire = await generated.getCatalogCharts({
    query: {
      project_id: params.projectId,
      search: params.search?.trim() || undefined,
      limit: params.limit,
      offset: params.offset,
    },
    signal: params.signal,
  });
  const rows = (wire.data ?? []) as OpenAPIComponents["schemas"]["HelmChart"][];
  const page = mapPage(
    { data: rows, pagination: wire.pagination },
    (raw): CatalogChartSummary => ({
      id: raw.id ?? "",
      repositoryId: raw.repository_id ?? "",
      name: raw.name ?? "",
      displayName: raw.display_name ?? raw.name ?? "",
      description: raw.description ?? "",
      iconUrl: raw.icon_url ?? "",
      homeUrl: raw.home_url ?? "",
      category: raw.category ?? "",
      keywords: raw.keywords ?? [],
      deprecated: raw.deprecated ?? false,
    }),
  );
  return page;
}

// Recommended view: wraps /catalog/recommendations/popular which
// returns top charts by install + rating score.
export interface RecommendedChart {
  chartId: string;
  name: string;
  score: number;
  ratingAvg: number;
  installCount: number;
}

export async function listRecommendedCharts(
  projectId: string,
  limit = 10,
  signal?: AbortSignal,
): Promise<RecommendedChart[]> {
  const wire = await generated.getCatalogRecommendationsPopular({
    query: { project_id: projectId, limit },
    signal,
  });
  const rows = (wire.data ??
    []) as OpenAPIComponents["schemas"]["ChartRecommendation"][];
  return rows.map((raw) => ({
    chartId: raw.chart_id,
    name: "",
    score: raw.bayesian_score,
    ratingAvg: raw.avg_stars,
    installCount: raw.rating_count,
  }));
}

// Default values.yaml for the install modal's YAML editor pre-fill.
// First call on a given version triggers backend hydration (~1-2s);
// subsequent calls are cached in the DB row.
export async function getChartDefaultValues(
  projectId: string,
  chartId: string,
  version?: string,
  signal?: AbortSignal,
): Promise<{ chart: string; version: string; defaultValues: string }> {
  const wire = await generated.getCatalogChartsByIdValues({
    path: { id: chartId },
    query: { project_id: projectId, version },
    signal,
  });
  return {
    chart: wire.chart ?? "",
    version: wire.version ?? "",
    defaultValues: wire.default_values ?? "",
  };
}

// Kick off a fresh install on this cluster. Returns the created
// installed_charts row id; the helm install itself happens
// asynchronously via the tunnel + worker queue.
export async function installChartOnCluster(req: {
  projectId: string;
  clusterId: string;
  chartVersionId: string;
  releaseName: string;
  namespace: string;
  valuesOverride: string;
  idempotencyKey?: string;
  signal?: AbortSignal;
}): Promise<{
  id: string;
  operation: OpenAPIComponents["schemas"]["CatalogOperation"];
}> {
  const wire = await generated.postCatalogInstalled({
    headerParams: {
      "Idempotency-Key": req.idempotencyKey ?? createIdempotencyKey(),
    },
    body: {
      project_id: req.projectId,
      cluster_id: req.clusterId,
      chart_version_id: req.chartVersionId,
      release_name: req.releaseName,
      namespace: req.namespace,
      values_override: req.valuesOverride,
    },
    signal: req.signal,
  });
  return {
    id: wire.data.installation.id ?? "",
    operation: wire.data.operation,
  };
}

export async function uninstallCatalogRelease(
  installedChartId: string,
  options: { idempotencyKey?: string; signal?: AbortSignal } = {},
): Promise<OpenAPIComponents["schemas"]["CatalogOperation"]> {
  const wire = await generated.deleteCatalogInstalledById({
    path: { id: installedChartId },
    headerParams: {
      "Idempotency-Key": options.idempotencyKey ?? createIdempotencyKey(),
    },
    signal: options.signal,
  });
  return wire.data;
}

// Rancher-style bulk-delete of stuck releases. Backend hard-deletes any
// installed_charts rows in failed_install / failed_uninstall on this
// cluster and returns the affected row count.
export async function deleteFailedClusterApps(
  clusterId: string,
  signal?: AbortSignal,
): Promise<{ deleted: number }> {
  const wire = await generated.deleteClustersByClusterIdAppsFailed({
    path: { cluster_id: clusterId },
    signal,
  });
  return { deleted: wire.deleted ?? 0 };
}

export async function upgradeClusterApp(
  id: string,
  body: { chart_version_id: string; values_override?: string },
  idempotencyKey = createIdempotencyKey(),
) {
  const wire = await generated.putCatalogInstalledByIdUpgrade({
    path: { id },
    headerParams: { "Idempotency-Key": idempotencyKey },
    body,
  });
  return {
    id: wire.data.installation.id ?? "",
    operation: wire.data.operation,
  };
}

export async function getClusterAppValues(
  id: string,
  signal?: AbortSignal,
): Promise<string> {
  const wire = await generated.getCatalogInstalledByIdValues({
    path: { id },
    signal,
  });
  if (typeof wire.data.values_override !== "string")
    throw new Error("Release values are unavailable");
  return wire.data.values_override;
}
export async function getClusterAppHistory(id: string, signal?: AbortSignal) {
  const wire = await generated.getCatalogInstalledByIdRevisions({
    path: { id },
    signal,
  });
  return wire.data;
}

export async function getClusterApp(id: string, signal?: AbortSignal) {
  const wire = await generated.getCatalogInstalledById({
    path: { id },
    signal,
  });
  return wire.data;
}
