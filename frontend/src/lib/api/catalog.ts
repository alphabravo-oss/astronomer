import {
  deleteCatalogInstalledById,
  deleteCatalogRepositoriesById,
  deleteChartsByChartIdRatingsByRatingId,
  getCatalogApplications,
  getCatalogApplicationSources,
  getCatalogCharts,
  getCatalogChartsById,
  getCatalogChartsByIdReadme,
  getCatalogChartsByIdValues,
  getCatalogChartsByIdVersions,
  getCatalogDiscovery,
  getCatalogInstalled,
  getCatalogInstalledByIdUpgradeVersions,
  getCatalogOperations,
  getCatalogOperationsById,
  getCatalogRecommendationsPopular,
  getCatalogRecommendationsSimilarByChartId,
  getCatalogRepositories,
  getChartsByChartIdRatings,
  getChartsByChartIdRatingsAggregate,
  getChartsByChartIdRatingsMine,
  postCatalogInstalled,
  postCatalogInstalledByIdRollback,
  postCatalogApplicationsPreview,
  postCatalogOperationsByIdRetry,
  postCatalogRepositories,
  postCatalogRepositoriesByIdSync,
  postChartsByChartIdRatings,
  putCatalogInstalledByIdUpgrade,
  putCatalogChartsByIdFavorite,
  putChartsByChartIdRatingsByRatingId,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import { completePage, mapPage } from "@/lib/api/pagination";
import type {
  HelmChart,
  HelmChartCategory,
  HelmChartVersion,
  HelmRepository,
  HelmRepoType,
  InstalledChart,
  PaginatedResponse,
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
export type CatalogApplicationPresentation =
  OpenAPIComponents["schemas"]["CatalogApplicationPresentation"];
export type CatalogInstallationPreview =
  OpenAPIComponents["schemas"]["CatalogInstallationPreview"];
export type ApplicationCatalogSource =
  OpenAPIComponents["schemas"]["ApplicationCatalogSource"];
export type CatalogOperation =
  OpenAPIComponents["schemas"]["CatalogOperation"] & {
    events?: Schemas["CatalogOperationEvent"][];
  };

export interface CatalogInstallationReceipt {
  installation: InstalledChart;
  operation: CatalogOperation;
}

export type CatalogInstallationAccepted = CatalogInstallationReceipt;
export type CatalogUserDiscovery = CamelizeKeys<
  OpenAPIComponents["schemas"]["CatalogUserDiscovery"]
>;

const CATALOG_PAGE_LIMIT = 25;
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

function unwrapCollection<T>(response: unknown): T[] {
  let value = response;
  for (let depth = 0; depth < 3; depth += 1) {
    if (Array.isArray(value)) return value as T[];
    if (!value || typeof value !== "object" || !("data" in value)) break;
    value = (value as { data?: unknown }).data;
  }
  return [];
}

export async function getCatalogApplicationPresentations(
  signal?: AbortSignal,
): Promise<CatalogApplicationPresentation[]> {
  return unwrapCollection<CatalogApplicationPresentation>(
    await getCatalogApplications({ signal }),
  );
}

export async function getApplicationCatalogSources(
  signal?: AbortSignal,
): Promise<ApplicationCatalogSource[]> {
  return unwrapCollection<ApplicationCatalogSource>(
    await getCatalogApplicationSources({ signal }),
  );
}

export async function getCatalogUserDiscovery(
  signal?: AbortSignal,
): Promise<CatalogUserDiscovery[]> {
  const response = await getCatalogDiscovery({ signal });
  return (response.data ?? []).map((item) => ({
    chartId: item.chart_id,
    favorite: item.favorite,
    favoriteAt: item.favorite_at ?? undefined,
    lastViewedAt: item.last_viewed_at ?? undefined,
    viewCount: item.view_count,
  }));
}

export async function setCatalogChartFavorite(
  chartId: string,
  favorite: boolean,
  scope?: { clusterId?: string; projectId?: string },
): Promise<CatalogUserDiscovery> {
  const response = await putCatalogChartsByIdFavorite({
    path: { id: chartId },
    query: {
      cluster_id: scope?.clusterId,
      project_id: scope?.projectId,
    },
    body: { favorite },
  });
  const item = requireData(response, "setCatalogChartFavorite");
  return {
    chartId: item.chart_id,
    favorite: item.favorite,
    favoriteAt: item.favorite_at ?? undefined,
    lastViewedAt: item.last_viewed_at ?? undefined,
    viewCount: item.view_count,
  };
}

export async function previewCatalogInstallation(data: {
  cluster_id: string;
  chart_version_id: string;
  namespace: string;
  values_override?: string;
}): Promise<CatalogInstallationPreview> {
  return postCatalogApplicationsPreview({ body: data });
}

function requiredString(value: string | undefined, field: string): string {
  if (!value) throw new Error(`Catalog API response omitted ${field}`);
  return value;
}

function requiredNumber(value: number | undefined, field: string): number {
  if (value === undefined)
    throw new Error(`Catalog API response omitted ${field}`);
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
  throw new Error(
    `Catalog API returned unsupported repository type: ${value ?? "missing"}`,
  );
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
    projectId: wire.project_id ?? undefined,
    chartVersionId: wire.chart_version_id,
    releaseName: requiredString(
      wire.release_name,
      "installedChart.release_name",
    ),
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

export async function getHelmRepositories(
  clusterId?: string,
  signal?: AbortSignal,
  params: { limit?: number; offset?: number } = {},
): Promise<PaginatedResponse<HelmRepository>> {
  const response = await getCatalogRepositories({
    query: { cluster_id: clusterId, limit: CATALOG_PAGE_LIMIT, ...params },
    signal,
  });
  // The published OpenAPI contract also permits a complete legacy array.
  // Normalize that explicit contract once; never invent totals for paged data.
  return mapPage(
    Array.isArray(response) ? completePage(response) : response,
    mapHelmRepository,
  );
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

export async function getHelmCharts(
  params: {
    clusterId?: string;
    projectId?: string;
    repository?: string;
    category?: string;
    search?: string;
    limit?: number;
    offset?: number;
  },
  signal?: AbortSignal,
): Promise<PaginatedResponse<HelmChart>> {
  const response = await getCatalogCharts({
    query: {
      cluster_id: params.clusterId,
      project_id: params.projectId,
      limit: params.limit ?? CATALOG_PAGE_LIMIT,
      offset: params.offset,
    },
    signal,
  });
  const search = params.search?.trim().toLocaleLowerCase();
  const page = mapPage(response, mapHelmChart);
  return {
    ...page,
    data: page.data.filter((chart) => {
      if (params.repository && chart.repositoryId !== params.repository)
        return false;
      if (params.category && chart.category !== params.category) return false;
      if (!search) return true;
      return [
        chart.name,
        chart.displayName,
        chart.description,
        ...chart.keywords,
      ]
        .filter(Boolean)
        .some((value) => value!.toLocaleLowerCase().includes(search));
    }),
  };
}

export async function getHelmChartVersions(
  scopeId: string,
  chartId: string,
  scope: "cluster" | "project" = "project",
  signal?: AbortSignal,
  params: { limit?: number; offset?: number } = {},
): Promise<PaginatedResponse<HelmChartVersion>> {
  const response = await getCatalogChartsByIdVersions({
    path: { id: chartId },
    query: {
      cluster_id: scope === "cluster" ? scopeId : undefined,
      project_id: scope === "project" ? scopeId : undefined,
      limit: params.limit ?? CATALOG_PAGE_LIMIT,
      offset: params.offset,
    },
    signal,
  });
  return mapPage(response, mapHelmChartVersion);
}

export async function getHelmChart(
  scopeId: string,
  chartId: string,
  scope: "cluster" | "project" = "project",
  signal?: AbortSignal,
): Promise<HelmChart> {
  const response = await getCatalogChartsById({
    path: { id: chartId },
    query: {
      cluster_id: scope === "cluster" ? scopeId : undefined,
      project_id: scope === "project" ? scopeId : undefined,
    },
    signal,
  });
  return mapHelmChart(requireData(response, "getHelmChart"));
}

export async function getHelmChartReadme(
  scopeId: string,
  chartId: string,
  version?: string,
  scope: "cluster" | "project" = "project",
  signal?: AbortSignal,
): Promise<string> {
  const response = await getCatalogChartsByIdReadme({
    path: { id: chartId },
    query: {
      cluster_id: scope === "cluster" ? scopeId : undefined,
      project_id: scope === "project" ? scopeId : undefined,
      version,
    },
    signal,
  });
  return response.readme ?? "";
}

export async function getHelmChartValues(
  scopeId: string,
  chartId: string,
  version?: string,
  scope: "cluster" | "project" = "project",
  signal?: AbortSignal,
): Promise<{
  chart: string;
  version: string;
  defaultValues: string;
  valuesSchema: Record<string, unknown>;
}> {
  const response = await getCatalogChartsByIdValues({
    path: { id: chartId },
    query: {
      cluster_id: scope === "cluster" ? scopeId : undefined,
      project_id: scope === "project" ? scopeId : undefined,
      version,
    },
    signal,
  });
  return {
    chart: response.chart ?? "",
    version: response.version ?? "",
    defaultValues: response.default_values ?? "",
    valuesSchema: response.values_schema ?? {},
  };
}

export async function getInstalledChartUpgradeVersions(
  installationId: string,
  signal?: AbortSignal,
): Promise<HelmChartVersion[]> {
  const response = await getCatalogInstalledByIdUpgradeVersions({
    path: { id: installationId },
    signal,
  });
  return response.data.map(mapHelmChartVersion);
}

export async function getInstalledCharts(
  params?: {
    cluster?: string;
    limit?: number;
    offset?: number;
  },
  signal?: AbortSignal,
): Promise<PaginatedResponse<InstalledChart>> {
  const response = await getCatalogInstalled({
    query: {
      cluster_id: params?.cluster,
      limit: params?.limit ?? CATALOG_PAGE_LIMIT,
      offset: params?.offset,
    },
    signal,
  });
  return mapPage(response, mapInstalledChart);
}

export interface InstallHelmChartRequest {
  project_id?: string;
  cluster_id: string;
  chart_version_id: string;
  release_name: string;
  namespace: string;
  values_override?: string;
}

export async function installHelmChart(
  data: InstallHelmChartRequest,
): Promise<CatalogInstallationReceipt> {
  const payload = requireData(
    await postCatalogInstalled({
      headerParams: idempotencyHeaderParams(),
      body: data,
    }),
    "installHelmChart",
  );
  return {
    installation: mapInstalledChart(payload.installation),
    operation: payload.operation,
  };
}

/** Detail is the only catalog endpoint that exposes persisted stage events. */
export async function getCatalogOperation(
  id: string,
): Promise<CatalogOperation> {
  return getCatalogOperationsById({ path: { id } });
}

export async function listCatalogOperations(
  signal?: AbortSignal,
): Promise<CatalogOperation[]> {
  const response = await getCatalogOperations({
    query: { limit: 200 },
    signal,
  });
  return response.data ?? [];
}

export async function retryCatalogOperation(
  id: string,
): Promise<CatalogOperation> {
  return postCatalogOperationsByIdRetry({
    path: { id },
    headerParams: idempotencyHeaderParams(),
  });
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

export async function rollbackChart(
  id: string,
  revision: number,
): Promise<void> {
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
  return mapChartRatingAggregate(
    requireData(response, "getChartRatingAggregate"),
  );
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
