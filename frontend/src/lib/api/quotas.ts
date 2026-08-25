/** Generated quota-plan, fleet-usage, and effective-quota API boundary. */
import {
  deleteAdminQuotaPlansByName,
  getAdminQuotaPlans,
  getAdminQuotaPlansByName,
  getAdminQuotaUsage,
  getAuthMeQuota,
  getProjectsByIdQuota,
  postAdminQuotaPlans,
  putAdminQuotaPlansByName,
} from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

type Contracts = OpenAPIComponents["schemas"];
type QuotaPlanWire = Contracts["QuotaPlan"];
type ProjectQuotaWire = Contracts["ProjectQuotaSnapshot"];

export type QuotaEnforcement = QuotaPlanWire["enforcement"];
export type QuotaPlanView = CamelizeKeys<QuotaPlanWire>;
export type QuotaPlanWriteRequest = Required<
  Pick<
    Contracts["QuotaPlanRequest"],
    | "name"
    | "enforcement"
    | "description"
    | "max_clusters_per_project"
    | "max_namespaces_per_project"
    | "max_members_per_project"
    | "max_projects_per_user"
    | "max_tokens_per_user"
    | "max_streams_per_user"
    | "max_total_clusters"
    | "max_total_users"
  >
>;

export interface QuotaUsageRow {
  planName: string;
  scope: "project" | "user";
  scopeId: string;
  scopeName: string;
  usage: Record<string, number>;
  utilization: Record<string, number>;
}

export interface QuotaUsageSummary {
  rows: QuotaUsageRow[];
  fleetTotals: Record<string, number>;
  topOffenders: QuotaUsageRow[];
}

export interface ProjectEffectiveQuotaView {
  projectId: string;
  planName: string;
  enforcement: QuotaEnforcement;
  clustersUsed: number;
  clustersLimit: number;
  namespacesUsed: number;
  namespacesLimit: number;
  membersUsed: number;
  membersLimit: number;
  overrides: Record<string, unknown>;
}

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

export function mapQuotaPlan(wire: QuotaPlanWire): QuotaPlanView {
  return {
    name: wire.name,
    enforcement: wire.enforcement,
    description: wire.description,
    maxClustersPerProject: wire.max_clusters_per_project,
    maxNamespacesPerProject: wire.max_namespaces_per_project,
    maxMembersPerProject: wire.max_members_per_project,
    maxProjectsPerUser: wire.max_projects_per_user,
    maxTokensPerUser: wire.max_tokens_per_user,
    maxStreamsPerUser: wire.max_streams_per_user,
    maxTotalClusters: wire.max_total_clusters,
    maxTotalUsers: wire.max_total_users,
  };
}

function projectOffender(
  row: Contracts["ProjectQuotaOffender"],
): QuotaUsageRow {
  return {
    planName: row.quota_plan,
    scope: "project",
    scopeId: row.project_id,
    scopeName: row.project_name,
    usage: { [row.limit]: row.current },
    utilization: { [row.limit]: row.usage_pct },
  };
}

function userOffender(row: Contracts["UserQuotaOffender"]): QuotaUsageRow {
  return {
    planName: row.quota_plan,
    scope: "user",
    scopeId: row.user_id,
    scopeName: row.username,
    usage: { [row.limit]: row.current },
    utilization: { [row.limit]: row.usage_pct },
  };
}

export function mapQuotaUsage(
  wire: Contracts["QuotaUsageSnapshot"],
): QuotaUsageSummary {
  const topOffenders = [
    ...wire.project_offenders.map(projectOffender),
    ...wire.user_offenders.map(userOffender),
  ];
  return {
    rows: topOffenders,
    topOffenders,
    fleetTotals: {
      total_clusters: wire.global.total_clusters,
      max_total_clusters: wire.global.max_total_clusters,
      total_users: wire.global.total_users,
      max_total_users: wire.global.max_total_users,
    },
  };
}

export function mapProjectEffectiveQuota(
  wire: ProjectQuotaWire,
): ProjectEffectiveQuotaView {
  return {
    projectId: wire.project_id,
    planName: wire.quota_plan,
    enforcement: wire.enforcement,
    clustersUsed: wire.usage.max_clusters_per_project,
    clustersLimit: wire.limits.max_clusters_per_project,
    namespacesUsed: wire.usage.max_namespaces_per_project,
    namespacesLimit: wire.limits.max_namespaces_per_project,
    membersUsed: wire.usage.max_members_per_project,
    membersLimit: wire.limits.max_members_per_project,
    overrides: wire.overrides,
  };
}

export async function listQuotaPlans(): Promise<QuotaPlanView[]> {
  const page = await getAdminQuotaPlans({ query: { limit: 200 } });
  return page.data.map(mapQuotaPlan);
}

export async function getQuotaPlan(name: string): Promise<QuotaPlanView> {
  return mapQuotaPlan(
    requireData(
      await getAdminQuotaPlansByName({ path: { name } }),
      "getQuotaPlan",
    ),
  );
}

export async function createQuotaPlan(body: QuotaPlanWriteRequest) {
  return mapQuotaPlan(
    requireData(await postAdminQuotaPlans({ body }), "createQuotaPlan"),
  );
}

export async function updateQuotaPlan(
  name: string,
  body: QuotaPlanWriteRequest,
) {
  return mapQuotaPlan(
    requireData(
      await putAdminQuotaPlansByName({ path: { name }, body }),
      "updateQuotaPlan",
    ),
  );
}

export async function deleteQuotaPlan(name: string): Promise<void> {
  await deleteAdminQuotaPlansByName({ path: { name } });
}

export async function getQuotaUsage(): Promise<QuotaUsageSummary> {
  return mapQuotaUsage(
    requireData(await getAdminQuotaUsage(), "getQuotaUsage"),
  );
}

export async function getProjectEffectiveQuota(
  projectId: string,
): Promise<ProjectEffectiveQuotaView> {
  return mapProjectEffectiveQuota(
    requireData(
      await getProjectsByIdQuota({ path: { id: projectId } }),
      "getProjectEffectiveQuota",
    ),
  );
}

export async function getMyQuota() {
  return requireData(await getAuthMeQuota(), "getMyQuota");
}
