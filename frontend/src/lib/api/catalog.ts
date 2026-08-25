import {
  deleteCatalogInstalledById,
  deleteCatalogRepositoriesById,
  deleteChartsByChartIdRatingsByRatingId,
  getCatalogCharts,
  getCatalogChartsByIdVersions,
  getCatalogInstalled,
  getCatalogRecommendationsPopular,
  getCatalogRecommendationsSimilarByChartId,
  getCatalogRepositories,
  getChartsByChartIdRatings,
  getChartsByChartIdRatingsAggregate,
  getChartsByChartIdRatingsMine,
  postCatalogInstalled,
  postCatalogInstalledByIdRollback,
  postCatalogRepositories,
  postCatalogRepositoriesByIdSync,
  postChartsByChartIdRatings,
  putCatalogInstalledByIdUpgrade,
  putChartsByChartIdRatingsByRatingId,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type {
  HelmChart,
  HelmChartCategory,
  HelmChartVersion,
  HelmRepository,
  HelmRepoType,
  InstalledChart,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

type Schemas = OpenAPIComponents["schemas"];
type ChartRatingWire = Schemas["ChartRating"];
type ChartRatingAggregateWire = Schemas["ChartRatingAggregate"];
type ChartRecommendationWire = Schemas["ChartRecommendation"];

export type ChartRating = CamelizeKeys<
  OpenAPIComponents["schemas"]["ChartRating"]
>;
export type ChartRatingAggregate = CamelizeKeys<
  OpenAPIComponents["schemas"]["ChartRatingAggregate"]
>;
export type ChartScore = CamelizeKeys<ChartRecommendationWire>;

const CATALOG_PAGE_LIMIT = 200;
const CHART_CATEGORIES = new Set<HelmChartCategory>([
  "monitoring",
  "logging",
  "security",
  "database",
  "networking",
  "storage",
  "messaging",
  "ci-cd",
  "other",
]);

function requiredString(value: string | undefined, field: string): string {
  if (!value) throw new Error(`Catalog API response omitted ${field}`);
  return value;
}

function requiredNumber(value: number | undefined, field: string): number {
  if (value === undefined) throw new Error(`Catalog API response omitted ${field}`);
  return value;
}

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

function repositoryType(value: string | undefined): HelmRepoType {
  if (value === "helm" || value === "oci") return value;
  throw new Error(`Catalog API returned unsupported repository type: ${value ?? "missing"}`);
}

function chartCategory(value: string | undefined): HelmChartCategory {
  if (value && CHART_CATEGORIES.has(value as HelmChartCategory)) {
    return value as HelmChartCategory;
  }
  return "other";
}

/** Explicit generated-wire mapper; catalog responses never use global camelization. */
export function mapHelmRepository(
  wire: Schemas["HelmRepository"],
): HelmRepository {
  return {
    id: requiredString(wire.id, "repository.id"),
    name: requiredString(wire.name, "repository.name"),
    url: requiredString(wire.url, "repository.url"),
    repoType: repositoryType(wire.repo_type),
    description: wire.description || undefined,
    isDefault: wire.is_default ?? false,
    authType: wire.auth_type,
    authConfig: wire.auth_config,
    enabled: wire.enabled ?? false,
    lastSyncedAt: wire.last_synced_at ?? undefined,
    lastSyncAttemptedAt: wire.last_sync_attempted_at,
    lastSyncError: wire.last_sync_error,
    createdById: wire.created_by_id,
    createdAt: requiredString(wire.created_at, "repository.created_at"),
    updatedAt: requiredString(wire.updated_at, "repository.updated_at"),
    ownerProjectId: wire.owner_project_id,
    chartCount: wire.chart_count ?? 0,
  };
}

export function mapHelmChart(wire: Schemas["HelmChart"]): HelmChart {
  return {
    id: requiredString(wire.id, "chart.id"),
    repositoryId: requiredString(wire.repository_id, "chart.repository_id"),
    name: requiredString(wire.name, "chart.name"),
    displayName: wire.display_name || requiredString(wire.name, "chart.name"),
    description: wire.description || undefined,
    iconUrl: wire.icon_url || undefined,
    homeUrl: wire.home_url,
    category: chartCategory(wire.category),
    keywords: wire.keywords ?? [],
    maintainers: wire.maintainers,
    deprecated: wire.deprecated,
    createdAt: requiredString(wire.created_at, "chart.created_at"),
    updatedAt: requiredString(wire.updated_at, "chart.updated_at"),
  };
}

export function mapHelmChartVersion(
  wire: Schemas["HelmChartVersion"],
): HelmChartVersion {
  return {
    id: requiredString(wire.id, "chartVersion.id"),
    chartId: requiredString(wire.chart_id, "chartVersion.chart_id"),
    version: requiredString(wire.version, "chartVersion.version"),
    appVersion: wire.app_version ?? "",
    digest: wire.digest,
    urls: wire.urls,
    valuesSchema: wire.values_schema,
    defaultValues: wire.default_values,
    readme: wire.readme,
    createdAtUpstream: wire.created_at_upstream,
    createdAt: requiredString(wire.created_at, "chartVersion.created_at"),
    updatedAt: wire.updated_at,
  };
}

export function mapInstalledChart(
  wire: Schemas["InstalledChart"],
): InstalledChart {
  return {
    id: requiredString(wire.id, "installedChart.id"),
    clusterId: requiredString(wire.cluster_id, "installedChart.cluster_id"),
    chartVersionId: wire.chart_version_id,
    releaseName: requiredString(wire.release_name, "installedChart.release_name"),
    namespace: requiredString(wire.namespace, "installedChart.namespace"),
    valuesOverride: wire.values_override,
    status: requiredString(wire.status, "installedChart.status"),
    revision: requiredNumber(wire.revision, "installedChart.revision"),
    notes: wire.notes,
    installedById: wire.installed_by_id,
    requestId: wire.request_id,
    toolSlug: wire.tool_slug,
    presetUsed: wire.preset_used,
    createdAt: requiredString(wire.created_at, "installedChart.created_at"),
    updatedAt: requiredString(wire.updated_at, "installedChart.updated_at"),
  };
}

function mapChartRating(wire: ChartRatingWire): ChartRating {
  return {
    id: wire.id,
    chartId: wire.chart_id,
    installationId: wire.installation_id,
    userId: wire.user_id,
    stars: wire.stars,
    note: wire.note,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function mapChartRatingAggregate(
  wire: ChartRatingAggregateWire,
): ChartRatingAggregate {
  return {
    ratingCount: wire.rating_count,
    avgStars: wire.avg_stars,
    bayesianScore: wire.bayesian_score,
    histogram: wire.histogram,
  };
}

function mapChartScore(wire: ChartRecommendationWire): ChartScore {
  return {
    chartId: wire.chart_id,
    ratingCount: wire.rating_count,
    avgStars: wire.avg_stars,
    bayesianScore: wire.bayesian_score,
    weight: wire.weight,
  };
}

export async function getHelmRepositories(): Promise<HelmRepository[]> {
  const response = await getCatalogRepositories({
    query: { limit: CATALOG_PAGE_LIMIT },
  });
  const rows = Array.isArray(response) ? response : (response.data ?? []);
  return rows.map(mapHelmRepository);
}

export async function createHelmRepository(data: {
  name: string;
  url: string;
  repoType: HelmRepoType;
  description?: string;
  username?: string;
  password?: string;
}): Promise<HelmRepository> {
  const authConfig: Schemas["HelmRepositoryAuthConfig"] = {};
  if (data.username) authConfig.username = data.username;
  if (data.password) authConfig.password = data.password;
  const response = await postCatalogRepositories({
    body: {
      name: data.name,
      url: data.url,
      repo_type: data.repoType,
      description: data.description,
      enabled: true,
      auth_type: Object.keys(authConfig).length > 0 ? "basic" : "none",
      auth_config: authConfig,
    },
  });
  return mapHelmRepository(requireData(response, "createHelmRepository"));
}

export async function syncHelmRepository(id: string) {
  return postCatalogRepositoriesByIdSync({
    path: { id },
    headerParams: idempotencyHeaderParams(),
  });
}

export async function deleteHelmRepository(id: string): Promise<void> {
  await deleteCatalogRepositoriesById({ path: { id } });
}

export async function getHelmCharts(params: {
  projectId: string;
  repository?: string;
  category?: string;
  search?: string;
}): Promise<HelmChart[]> {
  const response = await getCatalogCharts({
    query: { project_id: params.projectId, limit: CATALOG_PAGE_LIMIT },
  });
  const search = params.search?.trim().toLocaleLowerCase();
  return (response.data ?? []).map(mapHelmChart).filter((chart) => {
    if (params.repository && chart.repositoryId !== params.repository) return false;
    if (params.category && chart.category !== params.category) return false;
    if (!search) return true;
    return [chart.name, chart.displayName, chart.description, ...chart.keywords]
      .filter(Boolean)
      .some((value) => value!.toLocaleLowerCase().includes(search));
  });
}

export async function getHelmChartVersions(
  projectId: string,
  chartId: string,
): Promise<HelmChartVersion[]> {
  const response = await getCatalogChartsByIdVersions({
    path: { id: chartId },
    query: { project_id: projectId, limit: 200 },
  });
  return (response.data ?? []).map(mapHelmChartVersion);
}

export async function getInstalledCharts(params?: {
  cluster?: string;
}): Promise<InstalledChart[]> {
  const response = await getCatalogInstalled({
    query: { cluster_id: params?.cluster, limit: CATALOG_PAGE_LIMIT },
  });
  return (response.data ?? []).map(mapInstalledChart);
}

export interface InstallHelmChartRequest {
  project_id: string;
  cluster_id: string;
  chart_version_id: string;
  release_name: string;
  namespace: string;
  values_override?: string;
}

export async function installHelmChart(
  data: InstallHelmChartRequest,
): Promise<InstalledChart> {
  const payload = requireData(
    await postCatalogInstalled({
      headerParams: idempotencyHeaderParams(),
      body: data,
    }),
    "installHelmChart",
  );
  return mapInstalledChart(payload.installation);
}

export async function upgradeInstalledChart(
  id: string,
  data: { chart_version_id: string; values_override?: string },
): Promise<InstalledChart> {
  const payload = requireData(
    await putCatalogInstalledByIdUpgrade({
      path: { id },
      headerParams: idempotencyHeaderParams(),
      body: data,
    }),
    "upgradeInstalledChart",
  );
  return mapInstalledChart(payload.installation);
}

export async function uninstallChart(id: string): Promise<void> {
  await deleteCatalogInstalledById({
    path: { id },
    headerParams: idempotencyHeaderParams(),
  });
}

export async function rollbackChart(id: string, revision: number): Promise<void> {
  await postCatalogInstalledByIdRollback({
    path: { id },
    headerParams: idempotencyHeaderParams(),
    body: { revision },
  });
}

export async function rateChart(
  chartId: string,
  payload: { stars: number; installation_id?: string; note?: string },
): Promise<ChartRating> {
  const response = await postChartsByChartIdRatings({
    path: { chart_id: chartId },
    body: payload,
  });
  return mapChartRating(requireData(response, "rateChart"));
}

export async function getChartRatings(
  chartId: string,
  params?: { limit?: number; offset?: number },
): Promise<ChartRating[]> {
  const response = await getChartsByChartIdRatings({
    path: { chart_id: chartId },
    query: params,
  });
  return (response.data ?? []).map(mapChartRating);
}

export async function getChartRatingAggregate(
  chartId: string,
): Promise<ChartRatingAggregate> {
  const response = await getChartsByChartIdRatingsAggregate({
    path: { chart_id: chartId },
  });
  return mapChartRatingAggregate(requireData(response, "getChartRatingAggregate"));
}

export async function getMyChartRating(
  chartId: string,
): Promise<ChartRating | null> {
  try {
    const response = await getChartsByChartIdRatingsMine({
      path: { chart_id: chartId },
    });
    return mapChartRating(requireData(response, "getMyChartRating"));
  } catch (error) {
    if ((error as { status?: number }).status === 404) return null;
    throw error;
  }
}

export async function updateChartRating(
  chartId: string,
  ratingId: string,
  payload: { stars: number; note?: string },
): Promise<ChartRating> {
  const response = await putChartsByChartIdRatingsByRatingId({
    path: { chart_id: chartId, rating_id: ratingId },
    body: payload,
  });
  return mapChartRating(requireData(response, "updateChartRating"));
}

export async function deleteChartRating(
  chartId: string,
  ratingId: string,
): Promise<void> {
  await deleteChartsByChartIdRatingsByRatingId({
    path: { chart_id: chartId, rating_id: ratingId },
  });
}

export async function getPopularCharts(limit = 6): Promise<ChartScore[]> {
  const response = await getCatalogRecommendationsPopular({ query: { limit } });
  return (response.data ?? []).map(mapChartScore);
}

export async function getSimilarCharts(
  chartId: string,
  limit = 5,
): Promise<ChartScore[]> {
  const response = await getCatalogRecommendationsSimilarByChartId({
    path: { chart_id: chartId },
    query: { limit },
  });
  return (response.data ?? []).map(mapChartScore);
}
