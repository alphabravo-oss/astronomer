/**
 * Project-detail + cluster-template API client.
 *
 * Lives in its own sub-file so the policy / cloud-credentials / quota tabs
 * and the top-level cluster-templates page don't bloat the main `api.ts`.
 * Re-exported from `@/lib/api` (see `index.ts`) so call sites keep importing
 * from a single module.
 *
 * Ordinary calls use generated OpenAPI operations and explicit wire-to-view
 * mappings. No response casing interceptor is part of this boundary.
 */
import {
  deleteClusterTemplatesById,
  deleteProjectsByProjectIdCatalogsByCatalogId,
  deleteProjectsByProjectIdCloudCredentialsById,
  getCloudCredentialsProviders,
  getClusterTemplates,
  getClusterTemplatesById,
  getClusterTemplatesByIdClusters,
  getProjectsById,
  getProjectsByIdQuotaUsage,
  getProjectsByProjectIdCatalogs,
  getProjectsByProjectIdCatalogsByCatalogIdCharts,
  getProjectsByProjectIdCloudCredentials,
  getProjectsByProjectIdCloudCredentialsById,
  patchProjectsByIdPolicy,
  postClusterTemplates,
  postProjectsByIdAddNamespace,
  postProjectsByIdRemoveNamespace,
  postProjectsByProjectIdCatalogs,
  postProjectsByProjectIdCatalogsByCatalogIdSubscribe,
  postProjectsByProjectIdCloudCredentials,
  postProjectsByProjectIdCloudCredentialsByIdTest,
  putClusterTemplatesById,
  putProjectsByProjectIdCloudCredentialsById,
} from "@/lib/api/generated/client";
import { mapPage } from "@/lib/api/pagination";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { PaginatedResponse, Project } from "@/types";

type Schemas = OpenAPIComponents["schemas"];
export interface ProjectDetailRequestOptions {
  signal?: AbortSignal;
}

function requireData<T>(value: { data?: T }, operation: string): T {
  if (value.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return value.data;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : {};
}

function mapProject(wire: Schemas["Project"]): Project {
  const value = asRecord(wire);
  const name = typeof wire.name === "string" ? wire.name : "";
  return {
    id: typeof wire.id === "string" ? wire.id : "",
    name,
    displayName:
      typeof value.display_name === "string" ? value.display_name : name,
    description: wire.description,
    clusterId: wire.cluster_id,
    namespaces: wire.namespaces ?? [],
    members: [],
    createdAt: wire.created_at ?? "",
    updatedAt: wire.updated_at ?? "",
  };
}

// ============================================================
// Types
// ============================================================

export type PodSecurityProfile = "privileged" | "baseline" | "restricted";
export type NetworkPolicyMode = "isolated" | "allow-same-project" | "none";

/**
 * Wire shape for the policy block on a project. All fields are individually
 * optional so the PATCH endpoint can take partial updates. An empty string /
 * zero on the quota fields means "unlimited" — we send `null` for unlimited
 * over the wire so the backend can distinguish "no change" from "remove
 * the cap".
 */
export interface ProjectPolicy {
  podSecurityProfile: PodSecurityProfile;
  resourceQuotaCpu: string | null;
  resourceQuotaMemory: string | null;
  resourceQuotaPods: number | null;
  networkPolicyMode: NetworkPolicyMode;
}

export type ProjectPolicyPatch = Partial<ProjectPolicy>;

// ----- Namespace membership -----

/**
 * Assign a namespace to a project. The backend writes the project's namespaces
 * JSONB and the `project_namespaces` sidecar in one transaction, then flushes
 * the RBAC binding cache — so the project's members gain read access to that
 * namespace's workloads on the next request, not after a cache TTL.
 *
 * 409 means the namespace is already in this project or is owned by another
 * project on the same cluster (one namespace, one project).
 */
export async function addProjectNamespace(
  projectId: string,
  namespace: string,
  options?: ProjectDetailRequestOptions,
) {
  return mapProject(
    requireData(
      await postProjectsByIdAddNamespace({
        path: { id: projectId },
        body: { namespace },
        signal: options?.signal,
      }),
      "addProjectNamespace",
    ),
  );
}

/** Unassign a namespace. Revokes the project members' access to it. */
export async function removeProjectNamespace(
  projectId: string,
  namespace: string,
  options?: ProjectDetailRequestOptions,
) {
  return mapProject(
    requireData(
      await postProjectsByIdRemoveNamespace({
        path: { id: projectId },
        body: { namespace },
        signal: options?.signal,
      }),
      "removeProjectNamespace",
    ),
  );
}

/** Per-(cluster, namespace) live ResourceQuota.status.used vs hard. */
export interface ProjectQuotaUsageRow {
  clusterId: string;
  clusterName: string;
  namespace: string;
  cpuUsed: string;
  cpuLimit: string;
  memoryUsed: string;
  memoryLimit: string;
  podsUsed: number;
  podsLimit: number;
  allocation: ProjectResourceCap;
}

export interface ProjectResourceCap {
  cpu: string;
  memory: string;
  pods: number;
}

export interface ProjectResourceQuotaSummary {
  total: ProjectResourceCap;
  allocated: ProjectResourceCap;
  remaining: ProjectResourceCap;
}

export interface ProjectQuotaUsage {
  rows: ProjectQuotaUsageRow[];
  summary: ProjectResourceQuotaSummary;
}

export type { ProjectEffectiveQuotaView as ProjectEffectiveQuota } from "@/lib/api/quotas";

// ----- Cloud credentials -----

export type CloudProvider =
  "aws" | "gcp" | "azure" | "digitalocean" | "generic";

export interface CloudCredentialProviderField {
  name: string;
  label?: string;
  helper?: string;
  required: boolean;
  secret: boolean;
  placeholder?: string;
}

export interface CloudCredentialProviderSpec {
  provider: CloudProvider;
  displayName: string;
  description?: string;
  fields: CloudCredentialProviderField[];
}

export interface CloudCredentialTargetRef {
  clusterId: string;
  clusterName?: string;
  namespaces: string[];
}

export interface CloudCredential {
  id: string;
  name: string;
  provider: CloudProvider;
  description?: string;
  /** Non-secret fields. Secret fields come back redacted with `__<name>_set`. */
  config: Record<string, unknown>;
  targetRefs: CloudCredentialTargetRef[];
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}

export interface CloudCredentialWriteRequest {
  name: string;
  provider: CloudProvider;
  description?: string;
  config: Record<string, unknown>;
  targetRefs: CloudCredentialTargetRef[];
}

export interface CloudCredentialTestResult {
  ok: boolean;
  message?: string;
  detail?: string;
}

// ----- Cluster templates -----

export interface ClusterTemplateLabel {
  key: string;
  value: string;
}

export interface ClusterTemplateToolBinding {
  /** Catalog tool slug (for example, "monitoring"). */
  slug: string;
  /** Preset name from the catalog (e.g. "default", "production"). */
  preset?: string;
  /** Raw helm values overlay applied on top of the preset. */
  valuesOverride?: string;
}

export interface ClusterTemplateDefaultProject {
  /** Optional name template (supports `{cluster}` interpolation). */
  name?: string;
  podSecurityProfile: PodSecurityProfile;
  resourceQuotaCpu?: string | null;
  resourceQuotaMemory?: string | null;
  resourceQuotaPods?: number | null;
  networkPolicyMode: NetworkPolicyMode;
}

export interface ClusterTemplateRegistrationPolicy {
  tokenRotationDays: number;
  requireApproval?: boolean;
}

export interface ClusterTemplateSpec {
  environment: "development" | "staging" | "production" | "other";
  labels: ClusterTemplateLabel[];
  tools: ClusterTemplateToolBinding[];
  defaultProject: ClusterTemplateDefaultProject;
  registrationPolicy: ClusterTemplateRegistrationPolicy;
}

export interface ClusterTemplate {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  spec: ClusterTemplateSpec;
  clustersBound: number;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}

export interface ClusterTemplateWriteRequest {
  name: string;
  displayName: string;
  description?: string;
  spec: ClusterTemplateSpec;
}

/** One row of the "clusters bound to this template" table on the detail page. */
export interface ClusterTemplateBoundCluster {
  clusterId: string;
  clusterName: string;
  /** Last-known apply status (whether the template's tools / project are in sync). */
  status: "pending" | "applying" | "applied" | "failed";
  lastAppliedAt?: string;
  message?: string;
}

function mapProjectPolicy(wire: Schemas["Project"]): ProjectPolicy {
  const value = asRecord(wire);
  return {
    podSecurityProfile:
      wire.pod_security_profile === "privileged" ||
      wire.pod_security_profile === "restricted"
        ? wire.pod_security_profile
        : "baseline",
    resourceQuotaCpu: wire.resource_quota_cpu_limit || null,
    resourceQuotaMemory: wire.resource_quota_memory_limit || null,
    resourceQuotaPods: wire.resource_quota_pod_count || null,
    networkPolicyMode:
      value.network_policy_mode === "isolated" ||
      value.network_policy_mode === "none"
        ? value.network_policy_mode
        : "allow-same-project",
  };
}

function mapTargetRef(value: unknown): CloudCredentialTargetRef {
  const wire = asRecord(value);
  return {
    clusterId: typeof wire.cluster_id === "string" ? wire.cluster_id : "",
    clusterName:
      typeof wire.cluster_name === "string" ? wire.cluster_name : undefined,
    namespaces:
      Array.isArray(wire.namespaces) &&
      wire.namespaces.every((item) => typeof item === "string")
        ? wire.namespaces
        : typeof wire.namespace === "string"
          ? [wire.namespace]
          : [],
  };
}

const cloudProviders = new Set<CloudProvider>([
  "aws",
  "gcp",
  "azure",
  "digitalocean",
  "generic",
]);

function cloudProvider(value: unknown): CloudProvider {
  return typeof value === "string" && cloudProviders.has(value as CloudProvider)
    ? (value as CloudProvider)
    : "generic";
}

function mapCloudCredential(wire: Schemas["CloudCredential"]): CloudCredential {
  return {
    id: wire.id ?? "",
    name: wire.name ?? "",
    provider: cloudProvider(wire.provider),
    description: wire.description,
    config: wire.data ?? {},
    targetRefs: (wire.target_refs ?? []).map(mapTargetRef),
    createdAt: wire.created_at ?? "",
    updatedAt: wire.updated_at ?? "",
  };
}

function cloudCredentialBody(
  body: Partial<CloudCredentialWriteRequest>,
): Schemas["CloudCredentialRequest"] {
  return {
    name: body.name ?? "",
    provider: body.provider ?? "generic",
    description: body.description,
    data: Object.fromEntries(
      Object.entries(body.config ?? {}).map(([key, value]) => [
        key,
        typeof value === "string" ? value : String(value ?? ""),
      ]),
    ),
    target_refs: (body.targetRefs ?? []).flatMap((target) =>
      target.namespaces.map((namespace) => ({
        cluster_id: target.clusterId,
        namespace,
      })),
    ),
  };
}

function mapClusterTemplate(value: unknown): ClusterTemplate {
  const wire = asRecord(value);
  const name = typeof wire.name === "string" ? wire.name : "";
  return {
    id: typeof wire.id === "string" ? wire.id : "",
    name,
    displayName:
      typeof wire.display_name === "string" ? wire.display_name : name,
    description:
      typeof wire.description === "string" ? wire.description : undefined,
    spec: mapClusterTemplateSpec(wire.spec),
    clustersBound:
      typeof wire.clusters_bound === "number" ? wire.clusters_bound : 0,
    createdBy:
      typeof wire.created_by === "string" ? wire.created_by : undefined,
    createdAt: typeof wire.created_at === "string" ? wire.created_at : "",
    updatedAt: typeof wire.updated_at === "string" ? wire.updated_at : "",
  };
}

function mapClusterTemplateSpec(value: unknown): ClusterTemplateSpec {
  const wire = asRecord(value);
  const defaultProject = asRecord(wire.default_project);
  const registrationPolicy = asRecord(wire.registration_policy);
  const environment = wire.environment;
  return {
    environment:
      environment === "development" ||
      environment === "staging" ||
      environment === "production"
        ? environment
        : "other",
    labels: Object.entries(asRecord(wire.labels)).map(([key, labelValue]) => ({
      key,
      value: typeof labelValue === "string" ? labelValue : String(labelValue),
    })),
    tools: Array.isArray(wire.tools)
      ? wire.tools.map((item) => {
          const tool = asRecord(item);
          return {
            slug: typeof tool.slug === "string" ? tool.slug : "",
            preset: typeof tool.preset === "string" ? tool.preset : undefined,
            valuesOverride:
              typeof tool.values === "string"
                ? tool.values
                : tool.values === undefined
                  ? undefined
                  : JSON.stringify(tool.values, null, 2),
          };
        })
      : [],
    defaultProject: {
      name:
        typeof defaultProject.name === "string"
          ? defaultProject.name
          : undefined,
      podSecurityProfile:
        defaultProject.pod_security_profile === "privileged" ||
        defaultProject.pod_security_profile === "restricted"
          ? defaultProject.pod_security_profile
          : "baseline",
      resourceQuotaCpu:
        typeof defaultProject.resource_quota_cpu_limit === "string"
          ? defaultProject.resource_quota_cpu_limit || null
          : null,
      resourceQuotaMemory:
        typeof defaultProject.resource_quota_memory_limit === "string"
          ? defaultProject.resource_quota_memory_limit || null
          : null,
      resourceQuotaPods:
        typeof defaultProject.resource_quota_pod_count === "number"
          ? defaultProject.resource_quota_pod_count
          : null,
      networkPolicyMode:
        defaultProject.network_policy_mode === "none" ||
        defaultProject.network_policy_mode === "allow-same-project"
          ? defaultProject.network_policy_mode
          : "isolated",
    },
    registrationPolicy: {
      tokenRotationDays:
        typeof registrationPolicy.token_rotation_days === "number"
          ? registrationPolicy.token_rotation_days
          : 0,
      requireApproval:
        typeof registrationPolicy.require_approval === "boolean"
          ? registrationPolicy.require_approval
          : undefined,
    },
  };
}

function clusterTemplateBody(
  body: Partial<ClusterTemplateWriteRequest>,
): Schemas["CreateClusterTemplateRequest"] {
  return {
    name: body.name ?? "",
    description: body.description,
    spec: body.spec as unknown as Record<string, unknown>,
  };
}

function mapProjectCatalog(value: unknown): ProjectCatalog {
  const wire = asRecord(value);
  return {
    id: typeof wire.id === "string" ? wire.id : "",
    name: typeof wire.name === "string" ? wire.name : "",
    url: typeof wire.url === "string" ? wire.url : "",
    repoType: typeof wire.repo_type === "string" ? wire.repo_type : "",
    description: typeof wire.description === "string" ? wire.description : "",
    authType: typeof wire.auth_type === "string" ? wire.auth_type : "",
    enabled: Boolean(wire.enabled),
    ownerProjectId:
      typeof wire.owner_project_id === "string" ? wire.owner_project_id : null,
    visibility: (typeof wire.visibility === "string"
      ? wire.visibility
      : "public") as ProjectCatalog["visibility"],
    createdAt: typeof wire.created_at === "string" ? wire.created_at : "",
    updatedAt: typeof wire.updated_at === "string" ? wire.updated_at : "",
    lastSyncedAt:
      typeof wire.last_synced_at === "string" ? wire.last_synced_at : undefined,
  };
}

function mapHelmChart(wire: Schemas["HelmChart"]): HelmChartSummary {
  return {
    id: wire.id ?? "",
    repositoryId: wire.repository_id ?? "",
    name: wire.name ?? "",
    displayName: wire.display_name ?? wire.name ?? "",
    description: wire.description ?? "",
    iconUrl: wire.icon_url ?? "",
    homeUrl: wire.home_url ?? "",
    category: wire.category ?? "",
    deprecated: wire.deprecated ?? false,
  };
}

// ============================================================
// Project policy
// ============================================================

export async function getProjectPolicy(
  projectId: string,
  options?: ProjectDetailRequestOptions,
): Promise<ProjectPolicy> {
  return mapProjectPolicy(
    requireData(
      await getProjectsById({
        path: { id: projectId },
        signal: options?.signal,
      }),
      "getProjectPolicy",
    ),
  );
}

export async function updateProjectPolicy(
  projectId: string,
  patch: ProjectPolicyPatch,
  options?: ProjectDetailRequestOptions,
): Promise<ProjectPolicy> {
  const response = await patchProjectsByIdPolicy({
    path: { id: projectId },
    body: {
      pod_security_profile: patch.podSecurityProfile,
      resource_quota_cpu_limit:
        patch.resourceQuotaCpu === null ? "" : patch.resourceQuotaCpu,
      resource_quota_memory_limit:
        patch.resourceQuotaMemory === null ? "" : patch.resourceQuotaMemory,
      resource_quota_pod_count:
        patch.resourceQuotaPods === null ? 0 : patch.resourceQuotaPods,
      network_policy_mode: patch.networkPolicyMode,
    },
    signal: options?.signal,
  });
  return mapProjectPolicy(requireData(response, "updateProjectPolicy"));
}

export async function getProjectQuotaUsage(
  projectId: string,
  options?: ProjectDetailRequestOptions,
): Promise<ProjectQuotaUsage> {
  const data = requireData(
    await getProjectsByIdQuotaUsage({
      path: { id: projectId },
      signal: options?.signal,
    }),
    "getProjectQuotaUsage",
  );
  return {
    rows: (data.results ?? []).map((row) => {
      const used = asRecord(row.used);
      const hard = asRecord(row.hard);
      return {
        clusterId: row.cluster_id ?? "",
        clusterName: row.cluster_name ?? "",
        namespace: row.namespace ?? "",
        cpuUsed: quantityString(used, "limits.cpu"),
        cpuLimit: quantityString(hard, "limits.cpu"),
        memoryUsed: quantityString(used, "limits.memory"),
        memoryLimit: quantityString(hard, "limits.memory"),
        podsUsed: Number(used.pods ?? 0),
        podsLimit: Number(hard.pods ?? 0),
        allocation: row.allocation,
      };
    }),
    summary: data.project_cap,
  };
}
function quantityString(values: Record<string, unknown>, key: string): string {
  if (typeof values[key] === "string") return values[key];
  return "0";
}

// ============================================================
// Project effective quota
// ============================================================

export { getProjectEffectiveQuota } from "@/lib/api/quotas";

// ============================================================
// Cloud credentials
// ============================================================

export async function listCloudCredentialProviders(
  options?: ProjectDetailRequestOptions,
): Promise<CloudCredentialProviderSpec[]> {
  const data = requireData(
    await getCloudCredentialsProviders({ signal: options?.signal }),
    "listCloudCredentialProviders",
  );
  return (data.items ?? []).map((item) => {
    const wire = asRecord(item);
    const requiredKeys = stringArray(wire.required_keys);
    const optionalKeys = stringArray(wire.optional_keys);
    const secretKeys = new Set(stringArray(wire.secret_keys));
    return {
      provider: cloudProvider(wire.name),
      displayName:
        typeof wire.display_name === "string"
          ? wire.display_name
          : humanizeCredentialKey(String(wire.name ?? "generic")),
      fields: [...requiredKeys, ...optionalKeys].map((name) => ({
        name,
        label: humanizeCredentialKey(name),
        required: requiredKeys.includes(name),
        secret: secretKeys.has(name),
      })),
    };
  });
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];
}

const credentialInitialisms: Record<string, string> = {
  api: "API",
  arn: "ARN",
  id: "ID",
  json: "JSON",
  url: "URL",
};

function humanizeCredentialKey(value: string): string {
  return value
    .split("_")
    .filter(Boolean)
    .map((word) => credentialInitialisms[word] ?? word)
    .join(" ")
    .replace(/^./, (character) => character.toUpperCase());
}

export async function listProjectCloudCredentials(
  projectId: string,
  options?: ProjectDetailRequestOptions,
): Promise<CloudCredential[]> {
  const data = requireData(
    await getProjectsByProjectIdCloudCredentials({
      path: { project_id: projectId },
      signal: options?.signal,
    }),
    "listProjectCloudCredentials",
  );
  return (data.items ?? []).map(mapCloudCredential);
}

export async function getProjectCloudCredential(
  projectId: string,
  credentialId: string,
  options?: ProjectDetailRequestOptions,
): Promise<CloudCredential> {
  return mapCloudCredential(
    requireData(
      await getProjectsByProjectIdCloudCredentialsById({
        path: { project_id: projectId, id: credentialId },
        signal: options?.signal,
      }),
      "getProjectCloudCredential",
    ),
  );
}

export async function createProjectCloudCredential(
  projectId: string,
  body: CloudCredentialWriteRequest,
  options?: ProjectDetailRequestOptions,
): Promise<CloudCredential> {
  return mapCloudCredential(
    requireData(
      await postProjectsByProjectIdCloudCredentials({
        path: { project_id: projectId },
        body: cloudCredentialBody(body),
        signal: options?.signal,
      }),
      "createProjectCloudCredential",
    ),
  );
}

export async function updateProjectCloudCredential(
  projectId: string,
  credentialId: string,
  body: Partial<CloudCredentialWriteRequest>,
  options?: ProjectDetailRequestOptions,
): Promise<CloudCredential> {
  return mapCloudCredential(
    requireData(
      await putProjectsByProjectIdCloudCredentialsById({
        path: { project_id: projectId, id: credentialId },
        body: cloudCredentialBody(body),
        signal: options?.signal,
      }),
      "updateProjectCloudCredential",
    ),
  );
}

export async function deleteProjectCloudCredential(
  projectId: string,
  credentialId: string,
  options?: ProjectDetailRequestOptions,
): Promise<void> {
  await deleteProjectsByProjectIdCloudCredentialsById({
    path: { project_id: projectId, id: credentialId },
    signal: options?.signal,
  });
}

export async function testProjectCloudCredential(
  projectId: string,
  credentialId: string,
  options?: ProjectDetailRequestOptions,
): Promise<CloudCredentialTestResult> {
  const data = requireData(
    await postProjectsByProjectIdCloudCredentialsByIdTest({
      path: { project_id: projectId, id: credentialId },
      signal: options?.signal,
    }),
    "testProjectCloudCredential",
  );
  return { ok: data.ok ?? false, message: data.message };
}

// ============================================================
// Cluster templates
// ============================================================

export async function listClusterTemplates(params?: {
  search?: string;
  page?: number;
  pageSize?: number;
  offset?: number;
  signal?: AbortSignal;
}): Promise<PaginatedResponse<ClusterTemplate>> {
  const response = await getClusterTemplates({
    query: {
      limit: params?.pageSize,
      offset:
        params?.offset ??
        (params?.page && params.pageSize
          ? Math.max(0, params.page - 1) * params.pageSize
          : undefined),
    },
    signal: params?.signal,
  });
  return mapPage(response, mapClusterTemplate);
}

export async function getClusterTemplate(
  id: string,
  options?: ProjectDetailRequestOptions,
): Promise<ClusterTemplate> {
  return mapClusterTemplate(
    requireData(
      await getClusterTemplatesById({ path: { id }, signal: options?.signal }),
      "getClusterTemplate",
    ),
  );
}

export async function createClusterTemplate(
  body: ClusterTemplateWriteRequest,
  options?: ProjectDetailRequestOptions,
): Promise<ClusterTemplate> {
  return mapClusterTemplate(
    requireData(
      await postClusterTemplates({
        body: clusterTemplateBody(body),
        signal: options?.signal,
      }),
      "createClusterTemplate",
    ),
  );
}

export async function updateClusterTemplate(
  id: string,
  body: Partial<ClusterTemplateWriteRequest>,
  options?: ProjectDetailRequestOptions,
): Promise<ClusterTemplate> {
  return mapClusterTemplate(
    requireData(
      await putClusterTemplatesById({
        path: { id },
        body: clusterTemplateBody(body),
        signal: options?.signal,
      }),
      "updateClusterTemplate",
    ),
  );
}

export async function deleteClusterTemplate(
  id: string,
  options?: ProjectDetailRequestOptions,
): Promise<void> {
  await deleteClusterTemplatesById({ path: { id }, signal: options?.signal });
}

export async function getClusterTemplateBoundClusters(
  id: string,
  options?: ProjectDetailRequestOptions,
): Promise<ClusterTemplateBoundCluster[]> {
  return requireData(
    await getClusterTemplatesByIdClusters({
      path: { id },
      signal: options?.signal,
    }),
    "getClusterTemplateBoundClusters",
  ).map((wire) => ({
    clusterId: wire.cluster_id,
    clusterName: wire.cluster_name,
    status: wire.status,
    lastAppliedAt: wire.last_applied_at,
    message: wire.message,
  }));
}

// ============================================================
// Project catalogs (BYO Helm repos — migration 061)
// ============================================================

/**
 * Wire shape returned by /api/v1/projects/{id}/catalogs/. The `visibility`
 * field discriminates how the UI should badge each row:
 *
 *   - "own"               → project-owned (private) catalog.
 *   - "subscribed_public" → global catalog the project has explicitly
 *                            opted into; unsubscribe drops only the row.
 *   - "public"            → global catalog with no explicit subscription;
 *                            still browseable because globals are
 *                            universally visible.
 *   - "foreign_private"   → another project's private catalog. Only ever
 *                            returned to superusers via the admin
 *                            include_project_owned path.
 */
export interface ProjectCatalog {
  id: string;
  name: string;
  url: string;
  repoType: string;
  description: string;
  authType: string;
  enabled: boolean;
  ownerProjectId: string | null;
  visibility: "own" | "subscribed_public" | "public" | "foreign_private";
  createdAt: string;
  updatedAt: string;
  lastSyncedAt?: string;
}

export interface CreateProjectCatalogRequest {
  name: string;
  url: string;
  repoType?: string;
  description?: string;
  authType?: string;
  authConfig?: Record<string, unknown>;
  enabled?: boolean;
}

export interface HelmChartSummary {
  id: string;
  repositoryId: string;
  name: string;
  displayName: string;
  description: string;
  iconUrl: string;
  homeUrl: string;
  category: string;
  deprecated: boolean;
}

export async function listProjectCatalogs(
  projectId: string,
  options?: ProjectDetailRequestOptions,
): Promise<ProjectCatalog[]> {
  return (
    requireData(
      await getProjectsByProjectIdCatalogs({
        path: { project_id: projectId },
        signal: options?.signal,
      }),
      "listProjectCatalogs",
    ) ?? []
  ).map(mapProjectCatalog);
}

export async function createProjectCatalog(
  projectId: string,
  body: CreateProjectCatalogRequest,
  options?: ProjectDetailRequestOptions,
): Promise<ProjectCatalog> {
  return mapProjectCatalog(
    requireData(
      await postProjectsByProjectIdCatalogs({
        path: { project_id: projectId },
        body: {
          name: body.name,
          url: body.url,
          repo_type: body.repoType,
          description: body.description,
          auth_type: body.authType,
          auth_config: body.authConfig,
          enabled: body.enabled,
        },
        signal: options?.signal,
      }),
      "createProjectCatalog",
    ),
  );
}

export async function subscribeProjectCatalog(
  projectId: string,
  catalogId: string,
  options?: ProjectDetailRequestOptions,
): Promise<void> {
  await postProjectsByProjectIdCatalogsByCatalogIdSubscribe({
    path: { project_id: projectId, catalog_id: catalogId },
    signal: options?.signal,
  });
}

/**
 * Bifurcated semantics in one endpoint:
 *  - When the catalog is project-owned by `projectId`, this DELETES the
 *    catalog row entirely (CASCADE drops charts + subscriptions).
 *  - Otherwise it removes only the subscription row.
 * The audit trail emits distinct keys so the two cases stay distinguishable
 * after the fact.
 */
export async function deleteProjectCatalog(
  projectId: string,
  catalogId: string,
  options?: ProjectDetailRequestOptions,
): Promise<void> {
  await deleteProjectsByProjectIdCatalogsByCatalogId({
    path: { project_id: projectId, catalog_id: catalogId },
    signal: options?.signal,
  });
}

export async function listProjectCatalogCharts(
  projectId: string,
  catalogId: string,
  options?: ProjectDetailRequestOptions,
): Promise<HelmChartSummary[]> {
  return (
    requireData(
      await getProjectsByProjectIdCatalogsByCatalogIdCharts({
        path: { project_id: projectId, catalog_id: catalogId },
        signal: options?.signal,
      }),
      "listProjectCatalogCharts",
    ) ?? []
  ).map(mapHelmChart);
}
