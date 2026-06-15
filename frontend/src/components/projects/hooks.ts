/**
 * React-Query hooks for the project-detail tabs and the top-level
 * cluster-templates surface.
 *
 * Kept next to the project-detail UI (rather than the central
 * `lib/hooks.ts`) so the feature owns its query keys and invalidation
 * rules — the existing settings/auth feature does the same with
 * `components/auth/hooks.ts`.
 *
 * Each hook follows the established astronomer convention: react-query
 * cache key in a stable factory, toast on success/error for mutations,
 * invalidate the relevant query on success.
 */
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import * as api from '@/lib/api/project-detail';
import { queryKeys } from '@/lib/hooks';
import type {
  ProjectPolicyPatch,
  CloudCredentialWriteRequest,
  ClusterTemplateWriteRequest,
} from '@/lib/api/project-detail';

// ============================================================
// Query keys
// ============================================================

export const projectDetailKeys = {
  policy: (projectId: string) => ['projects', 'detail', projectId, 'policy'] as const,
  quotaUsage: (projectId: string) => ['projects', 'detail', projectId, 'quota-usage'] as const,
  effectiveQuota: (projectId: string) => ['projects', 'detail', projectId, 'quota'] as const,
  cloudCredentials: (projectId: string) =>
    ['projects', 'detail', projectId, 'cloud-credentials'] as const,
  cloudCredential: (projectId: string, credentialId: string) =>
    ['projects', 'detail', projectId, 'cloud-credentials', credentialId] as const,
  cloudCredentialProviders: ['cloud-credentials', 'providers'] as const,
  // BYO catalogs (migration 061).
  catalogs: (projectId: string) => ['projects', 'detail', projectId, 'catalogs'] as const,
  catalogCharts: (projectId: string, catalogId: string) =>
    ['projects', 'detail', projectId, 'catalogs', catalogId, 'charts'] as const,
};

export const clusterTemplateKeys = {
  all: ['cluster-templates'] as const,
  list: (params?: Record<string, unknown>) => ['cluster-templates', 'list', params] as const,
  detail: (id: string) => ['cluster-templates', 'detail', id] as const,
  boundClusters: (id: string) => ['cluster-templates', 'detail', id, 'clusters'] as const,
};

// ============================================================
// Project policy
// ============================================================

export function useProjectPolicy(projectId: string) {
  return useQuery({
    queryKey: projectDetailKeys.policy(projectId),
    queryFn: () => api.getProjectPolicy(projectId),
    enabled: !!projectId,
  });
}

export function useUpdateProjectPolicy(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (patch: ProjectPolicyPatch) => api.updateProjectPolicy(projectId, patch),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: projectDetailKeys.policy(projectId) });
      // The project resource itself embeds resourceQuota; invalidate so the
      // list page reflects the new caps when the user navigates back.
      qc.invalidateQueries({ queryKey: queryKeys.projects.detail(projectId) });
      qc.invalidateQueries({ queryKey: queryKeys.projects.all });
      toastSuccess('Project policy updated');
    },
    onError: (err: Error) => {
      toastApiError('Failed to update policy', err);
    },
  });
}

export function useProjectQuotaUsage(projectId: string) {
  return useQuery({
    queryKey: projectDetailKeys.quotaUsage(projectId),
    queryFn: () => api.getProjectQuotaUsage(projectId),
    enabled: !!projectId,
    // Quota.status.used ticks with workload pressure; tighten the stale time
    // so the policy page stays approximately live without a manual refresh.
    refetchInterval: 30 * 1000,
    staleTime: 15 * 1000,
  });
}

// ============================================================
// Project effective quota
// ============================================================

export function useProjectEffectiveQuota(projectId: string) {
  return useQuery({
    queryKey: projectDetailKeys.effectiveQuota(projectId),
    queryFn: () => api.getProjectEffectiveQuota(projectId),
    enabled: !!projectId,
  });
}

// ============================================================
// Cloud credentials
// ============================================================

export function useCloudCredentialProviders() {
  return useQuery({
    queryKey: projectDetailKeys.cloudCredentialProviders,
    queryFn: () => api.listCloudCredentialProviders(),
    // The provider registry is process-static on the backend; cache hard.
    staleTime: 5 * 60 * 1000,
  });
}

export function useProjectCloudCredentials(projectId: string) {
  return useQuery({
    queryKey: projectDetailKeys.cloudCredentials(projectId),
    queryFn: () => api.listProjectCloudCredentials(projectId),
    enabled: !!projectId,
  });
}

export function useProjectCloudCredential(projectId: string, credentialId: string | undefined) {
  return useQuery({
    queryKey: projectDetailKeys.cloudCredential(projectId, credentialId || ''),
    queryFn: () => api.getProjectCloudCredential(projectId, credentialId as string),
    enabled: !!projectId && !!credentialId,
  });
}

export function useCreateCloudCredential(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CloudCredentialWriteRequest) =>
      api.createProjectCloudCredential(projectId, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: projectDetailKeys.cloudCredentials(projectId) });
      toastSuccess('Cloud credential created');
    },
    onError: (err: Error) => {
      toastApiError('Failed to create credential', err);
    },
  });
}

export function useUpdateCloudCredential(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      credentialId,
      body,
    }: {
      credentialId: string;
      body: Partial<CloudCredentialWriteRequest>;
    }) => api.updateProjectCloudCredential(projectId, credentialId, body),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: projectDetailKeys.cloudCredentials(projectId) });
      qc.invalidateQueries({
        queryKey: projectDetailKeys.cloudCredential(projectId, vars.credentialId),
      });
      toastSuccess('Cloud credential updated');
    },
    onError: (err: Error) => {
      toastApiError('Failed to update credential', err);
    },
  });
}

export function useDeleteCloudCredential(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (credentialId: string) =>
      api.deleteProjectCloudCredential(projectId, credentialId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: projectDetailKeys.cloudCredentials(projectId) });
      toastSuccess('Cloud credential deleted');
    },
    onError: (err: Error) => {
      toastApiError('Failed to delete credential', err);
    },
  });
}

export function useTestCloudCredential(projectId: string) {
  // Note: no toast wrapper — the caller renders pass/fail inline with the
  // returned `{ok, message}` so it stays next to the Test button.
  return useMutation({
    mutationFn: (credentialId: string) =>
      api.testProjectCloudCredential(projectId, credentialId),
  });
}

// ============================================================
// Cluster templates
// ============================================================

export function useClusterTemplates(params?: {
  search?: string;
  page?: number;
  pageSize?: number;
}) {
  return useQuery({
    queryKey: clusterTemplateKeys.list(params),
    queryFn: () => api.listClusterTemplates(params),
  });
}

export function useClusterTemplate(id: string | undefined) {
  return useQuery({
    queryKey: clusterTemplateKeys.detail(id || ''),
    queryFn: () => api.getClusterTemplate(id as string),
    enabled: !!id,
  });
}

export function useClusterTemplateBoundClusters(id: string | undefined) {
  return useQuery({
    queryKey: clusterTemplateKeys.boundClusters(id || ''),
    queryFn: () => api.getClusterTemplateBoundClusters(id as string),
    enabled: !!id,
  });
}

export function useCreateClusterTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ClusterTemplateWriteRequest) => api.createClusterTemplate(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: clusterTemplateKeys.all });
      toastSuccess('Cluster template created');
    },
    onError: (err: Error) => {
      toastApiError('Failed to create template', err);
    },
  });
}

export function useUpdateClusterTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<ClusterTemplateWriteRequest> }) =>
      api.updateClusterTemplate(id, body),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: clusterTemplateKeys.all });
      qc.invalidateQueries({ queryKey: clusterTemplateKeys.detail(vars.id) });
      toastSuccess('Cluster template updated');
    },
    onError: (err: Error) => {
      toastApiError('Failed to update template', err);
    },
  });
}

export function useDeleteClusterTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteClusterTemplate(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: clusterTemplateKeys.all });
      toastSuccess('Cluster template deleted');
    },
    onError: (err: Error) => {
      toastApiError('Failed to delete template', err);
    },
  });
}

// ============================================================
// Permission helpers
// ============================================================

/** Shape of the current user we care about for permission gating. */
type RoleHolder = { globalRoles?: string[] } | null | undefined;

/**
 * Roughly model the backend's RBAC: admin/superadmin can do anything,
 * else the user needs to hold a role granting the named scope. We don't
 * yet have a richer client-side policy evaluator, so this is a best-effort
 * check — the server is still the source of truth and any mutation that
 * the UI failed to gate will surface as a 403 toast.
 */
export function hasPermission(user: RoleHolder, role: string): boolean {
  if (!user) return false;
  const roles = user.globalRoles || [];
  if (roles.includes('admin') || roles.includes('superadmin')) return true;
  return roles.includes(role);
}

export function canEditProject(user: RoleHolder): boolean {
  return hasPermission(user, 'projects:update');
}

export function canReadClusterTemplates(user: RoleHolder): boolean {
  return hasPermission(user, 'cluster_templates:read') || hasPermission(user, 'cluster_templates:write');
}

export function canWriteClusterTemplates(user: RoleHolder): boolean {
  return hasPermission(user, 'cluster_templates:write');
}

// ============================================================
// Project catalogs (BYO Helm — migration 061)
// ============================================================

export function useProjectCatalogs(projectId: string) {
  return useQuery({
    queryKey: projectDetailKeys.catalogs(projectId),
    queryFn: () => api.listProjectCatalogs(projectId),
    enabled: !!projectId,
  });
}

export function useProjectCatalogCharts(projectId: string, catalogId: string | undefined) {
  return useQuery({
    queryKey: projectDetailKeys.catalogCharts(projectId, catalogId || ''),
    queryFn: () => api.listProjectCatalogCharts(projectId, catalogId as string),
    enabled: !!projectId && !!catalogId,
  });
}

export function useCreateProjectCatalog(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: api.CreateProjectCatalogRequest) =>
      api.createProjectCatalog(projectId, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: projectDetailKeys.catalogs(projectId) });
      toastSuccess('Catalog created');
    },
    onError: (err: Error) => {
      toastApiError('Failed to create catalog', err);
    },
  });
}

export function useSubscribeProjectCatalog(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (catalogId: string) => api.subscribeProjectCatalog(projectId, catalogId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: projectDetailKeys.catalogs(projectId) });
      toastSuccess('Subscribed to catalog');
    },
    onError: (err: Error) => {
      toastApiError('Failed to subscribe', err);
    },
  });
}

export function useDeleteProjectCatalog(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (catalogId: string) => api.deleteProjectCatalog(projectId, catalogId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: projectDetailKeys.catalogs(projectId) });
      toastSuccess('Catalog unsubscribed');
    },
    onError: (err: Error) => {
      toastApiError('Failed to unsubscribe', err);
    },
  });
}
