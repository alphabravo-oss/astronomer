/**
 * Project-detail + cluster-template API client.
 *
 * Lives in its own sub-file so the policy / cloud-credentials / quota tabs
 * and the top-level cluster-templates page don't bloat the main `api.ts`.
 * Re-exported from `@/lib/api` (see `index.ts`) so call sites keep importing
 * from a single module.
 *
 * The axios instance is reused from the parent so JWT refresh, the trailing
 * slash interceptor, and the snake_case to camelCase response rewriter all
 * apply transparently here.
 */
import api from '../api';
import type { APIResponse, PaginatedResponse } from '@/types';

// ============================================================
// Types
// ============================================================

export type PodSecurityProfile = 'privileged' | 'baseline' | 'restricted';
export type NetworkPolicyMode = 'isolated' | 'allow-same-project' | 'none';

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
}

export interface ProjectQuotaUsage {
  rows: ProjectQuotaUsageRow[];
}

/** Effective project-level quota plan (clusters/namespaces/members caps). */
export interface ProjectEffectiveQuota {
  planId: string;
  planName: string;
  enforcement: 'soft' | 'hard';
  clustersUsed: number;
  clustersLimit: number;
  namespacesUsed: number;
  namespacesLimit: number;
  membersUsed: number;
  membersLimit: number;
}

// ----- Cloud credentials -----

export type CloudProvider = 'aws' | 'gcp' | 'azure' | 'generic';

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
  /** Catalog tool slug (e.g. "argocd", "monitoring"). */
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
  environment: 'development' | 'staging' | 'production' | 'other';
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
  status: 'pending' | 'applied' | 'drift' | 'failed';
  lastAppliedAt?: string;
  message?: string;
}

// ============================================================
// Project policy
// ============================================================

export async function getProjectPolicy(projectId: string): Promise<ProjectPolicy> {
  const res = await api.get<APIResponse<ProjectPolicy>>(`/projects/${projectId}/policy`);
  return res.data.data;
}

export async function updateProjectPolicy(
  projectId: string,
  patch: ProjectPolicyPatch,
): Promise<ProjectPolicy> {
  const res = await api.patch<APIResponse<ProjectPolicy>>(`/projects/${projectId}/policy`, patch);
  return res.data.data;
}

export async function getProjectQuotaUsage(projectId: string): Promise<ProjectQuotaUsage> {
  const res = await api.get<APIResponse<ProjectQuotaUsage>>(`/projects/${projectId}/quota-usage`);
  return res.data.data;
}

// ============================================================
// Project effective quota
// ============================================================

export async function getProjectEffectiveQuota(projectId: string): Promise<ProjectEffectiveQuota> {
  const res = await api.get<APIResponse<ProjectEffectiveQuota>>(`/projects/${projectId}/quota`);
  return res.data.data;
}

// ============================================================
// Cloud credentials
// ============================================================

export async function listCloudCredentialProviders(): Promise<CloudCredentialProviderSpec[]> {
  const res = await api.get<APIResponse<CloudCredentialProviderSpec[]>>(
    '/cloud-credentials/providers',
  );
  return res.data.data;
}

export async function listProjectCloudCredentials(projectId: string): Promise<CloudCredential[]> {
  const res = await api.get<APIResponse<CloudCredential[]>>(
    `/projects/${projectId}/cloud-credentials`,
  );
  return res.data.data;
}

export async function getProjectCloudCredential(
  projectId: string,
  credentialId: string,
): Promise<CloudCredential> {
  const res = await api.get<APIResponse<CloudCredential>>(
    `/projects/${projectId}/cloud-credentials/${credentialId}`,
  );
  return res.data.data;
}

export async function createProjectCloudCredential(
  projectId: string,
  body: CloudCredentialWriteRequest,
): Promise<CloudCredential> {
  const res = await api.post<APIResponse<CloudCredential>>(
    `/projects/${projectId}/cloud-credentials`,
    body,
  );
  return res.data.data;
}

export async function updateProjectCloudCredential(
  projectId: string,
  credentialId: string,
  body: Partial<CloudCredentialWriteRequest>,
): Promise<CloudCredential> {
  const res = await api.put<APIResponse<CloudCredential>>(
    `/projects/${projectId}/cloud-credentials/${credentialId}`,
    body,
  );
  return res.data.data;
}

export async function deleteProjectCloudCredential(
  projectId: string,
  credentialId: string,
): Promise<void> {
  await api.delete(`/projects/${projectId}/cloud-credentials/${credentialId}`);
}

export async function testProjectCloudCredential(
  projectId: string,
  credentialId: string,
): Promise<CloudCredentialTestResult> {
  const res = await api.post<APIResponse<CloudCredentialTestResult>>(
    `/projects/${projectId}/cloud-credentials/${credentialId}/test`,
  );
  return res.data.data;
}

// ============================================================
// Cluster templates
// ============================================================

export async function listClusterTemplates(params?: {
  search?: string;
  page?: number;
  pageSize?: number;
}): Promise<PaginatedResponse<ClusterTemplate>> {
  const res = await api.get<PaginatedResponse<ClusterTemplate>>('/cluster-templates', { params });
  return res.data;
}

export async function getClusterTemplate(id: string): Promise<ClusterTemplate> {
  const res = await api.get<APIResponse<ClusterTemplate>>(`/cluster-templates/${id}`);
  return res.data.data;
}

export async function createClusterTemplate(
  body: ClusterTemplateWriteRequest,
): Promise<ClusterTemplate> {
  const res = await api.post<APIResponse<ClusterTemplate>>('/cluster-templates', body);
  return res.data.data;
}

export async function updateClusterTemplate(
  id: string,
  body: Partial<ClusterTemplateWriteRequest>,
): Promise<ClusterTemplate> {
  const res = await api.put<APIResponse<ClusterTemplate>>(`/cluster-templates/${id}`, body);
  return res.data.data;
}

export async function deleteClusterTemplate(id: string): Promise<void> {
  await api.delete(`/cluster-templates/${id}`);
}

export async function getClusterTemplateBoundClusters(
  id: string,
): Promise<ClusterTemplateBoundCluster[]> {
  const res = await api.get<APIResponse<ClusterTemplateBoundCluster[]>>(
    `/cluster-templates/${id}/clusters`,
  );
  return res.data.data;
}
