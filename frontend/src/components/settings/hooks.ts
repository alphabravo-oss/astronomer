/**
 * React Query hooks for the Settings hub.
 *
 * These wrap `lib/api/settings.ts` with the same conventions every other
 * feature module uses: stable query-key factories, mutations that invalidate
 * the relevant lists, and `toast` calls for user-visible side-effects. They
 * live next to the settings pages rather than in the global `lib/hooks.ts`
 * so this phase doesn't touch the shared hooks module.
 */
'use client';

import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import * as api from '@/lib/api';
import { useAuthStore } from '@/lib/store';
import { isSuperuser as hasSuperuserAccess } from '@/lib/permissions';
import type {
  GroupMappingWriteRequest,
  QuotaPlanWriteRequest,
  SmtpConfig,
  SmtpTestRequest,
  WebhookWriteRequest,
} from '@/lib/api/settings';

// ============================================================
// Admin gating
// ============================================================

/**
 * Whether the current user is allowed to see admin-only settings pages.
 *
 * The backend marks bootstrap and platform administrators with `is_superuser`
 * while some older frontend/session shapes used `isSuperuser` or global role
 * labels. Use the shared permission helper so every admin-only settings page
 * accepts the same user shapes as the rest of the application.
 */
export function useIsSuperuser(): { isSuperuser: boolean; ready: boolean } {
  const user = useAuthStore((s) => s.user);
  if (!user) return { isSuperuser: false, ready: false };
  return {
    isSuperuser: hasSuperuserAccess(user),
    ready: true,
  };
}

// ============================================================
// Stable query keys
// ============================================================

export const settingsKeys = {
  all: ['settings'] as const,
  platform: ['settings', 'platform'] as const,
  smtp: ['settings', 'smtp'] as const,
  emails: (params?: Record<string, unknown>) =>
    ['settings', 'emails', params] as const,
  webhooks: ['settings', 'webhooks'] as const,
  webhook: (id: string) => ['settings', 'webhooks', id] as const,
  webhookDeliveries: (id: string, params?: Record<string, unknown>) =>
    ['settings', 'webhooks', id, 'deliveries', params] as const,
  quotas: ['settings', 'quotas'] as const,
  quota: (name: string) => ['settings', 'quotas', name] as const,
  quotaUsage: ['settings', 'quota-usage'] as const,
  groupMappings: ['settings', 'group-mappings'] as const,
  backupDrill: ['settings', 'backup-drill'] as const,
  backupDrillHistory: (params?: Record<string, unknown>) =>
    ['settings', 'backup-drill', 'history', params] as const,
  gitopsSources: ['settings', 'gitops-sources'] as const,
  gitopsSource: (id: string) => ['settings', 'gitops-sources', id] as const,
  gitopsClusters: (id: string) =>
    ['settings', 'gitops-sources', id, 'clusters'] as const,
};

// ============================================================
// Platform Settings
// ============================================================

export function usePlatformSettings() {
  return useQuery({
    queryKey: settingsKeys.platform,
    queryFn: () => api.listPlatformSettings(),
  });
}

export function useSavePlatformSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (updates: Record<string, unknown>) =>
      api.savePlatformSettingsBatch(updates),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.platform });
      toastSuccess('Platform settings saved');
    },
    onError: (err: Error) => {
      toastApiError('Failed to save settings', err);
    },
  });
}

// ============================================================
// SMTP
// ============================================================

export function useSmtpConfig() {
  return useQuery({
    queryKey: settingsKeys.smtp,
    queryFn: () => api.getSmtpConfig(),
  });
}

export function useUpdateSmtpConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<SmtpConfig>) => api.updateSmtpConfig(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.smtp });
      toastSuccess('SMTP configuration saved');
    },
    onError: (err: Error) => {
      toastApiError('Failed to save SMTP config', err);
    },
  });
}

export function useTestSmtp() {
  return useMutation({
    mutationFn: (body: SmtpTestRequest) => api.testSmtpConfig(body),
    onSuccess: (result) => {
      if (result.success) {
        toastSuccess(`Test email sent in ${result.durationMs}ms`);
      } else {
        toastApiError('Test failed', result.message);
      }
    },
    onError: (err: Error) => {
      toastApiError('SMTP test failed', err);
    },
  });
}

export function useSentEmails(params?: { page?: number; page_size?: number }) {
  return useQuery({
    queryKey: settingsKeys.emails(params),
    queryFn: () => api.listSentEmails(params),
  });
}

// ============================================================
// Webhooks
// ============================================================

export function useWebhooks() {
  return useQuery({
    queryKey: settingsKeys.webhooks,
    queryFn: () => api.listWebhooks(),
  });
}

export function useWebhook(id: string | undefined) {
  return useQuery({
    queryKey: settingsKeys.webhook(id ?? ''),
    queryFn: () => api.getWebhook(id as string),
    enabled: !!id,
  });
}

export function useCreateWebhook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: WebhookWriteRequest) => api.createWebhook(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.webhooks });
      toastSuccess('Webhook created');
    },
    onError: (err: Error) => {
      toastApiError('Failed to create webhook', err);
    },
  });
}

export function useUpdateWebhook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<WebhookWriteRequest> }) =>
      api.updateWebhook(id, body),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: settingsKeys.webhooks });
      qc.invalidateQueries({ queryKey: settingsKeys.webhook(vars.id) });
      toastSuccess('Webhook updated');
    },
    onError: (err: Error) => {
      toastApiError('Failed to update webhook', err);
    },
  });
}

export function useDeleteWebhook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteWebhook(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.webhooks });
      toastSuccess('Webhook deleted');
    },
    onError: (err: Error) => {
      toastApiError('Failed to delete webhook', err);
    },
  });
}

export function useTestWebhook() {
  return useMutation({
    mutationFn: (id: string) => api.testWebhook(id),
    onError: (err: Error) => {
      toastApiError('Webhook test failed', err);
    },
  });
}

export function useWebhookDeliveries(
  id: string | undefined,
  params?: { page?: number; page_size?: number },
) {
  return useQuery({
    queryKey: settingsKeys.webhookDeliveries(id ?? '', params),
    queryFn: () => api.listWebhookDeliveries(id as string, params),
    enabled: !!id,
  });
}

export function useRetryWebhookDelivery(webhookId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (deliveryId: string) =>
      api.retryWebhookDelivery(webhookId, deliveryId),
    onSuccess: () => {
      qc.invalidateQueries({
        queryKey: ['settings', 'webhooks', webhookId, 'deliveries'],
      });
      toastSuccess('Delivery re-queued');
    },
    onError: (err: Error) => {
      toastApiError('Retry failed', err);
    },
  });
}

// ============================================================
// Quotas
// ============================================================

export function useQuotaPlans() {
  return useQuery({
    queryKey: settingsKeys.quotas,
    queryFn: () => api.listQuotaPlans(),
  });
}

export function useQuotaPlan(name: string | undefined) {
  return useQuery({
    queryKey: settingsKeys.quota(name ?? ''),
    queryFn: () => api.getQuotaPlan(name as string),
    enabled: !!name,
  });
}

export function useCreateQuotaPlan() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: QuotaPlanWriteRequest) => api.createQuotaPlan(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.quotas });
      toastSuccess('Quota plan created');
    },
    onError: (err: Error) => {
      toastApiError('Failed to create plan', err);
    },
  });
}

export function useUpdateQuotaPlan() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, body }: { name: string; body: Partial<QuotaPlanWriteRequest> }) =>
      api.updateQuotaPlan(name, body),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: settingsKeys.quotas });
      qc.invalidateQueries({ queryKey: settingsKeys.quota(vars.name) });
      toastSuccess('Quota plan saved');
    },
    onError: (err: Error) => {
      toastApiError('Failed to save plan', err);
    },
  });
}

export function useDeleteQuotaPlan() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.deleteQuotaPlan(name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.quotas });
      toastSuccess('Quota plan deleted');
    },
    onError: (err: Error) => {
      toastApiError('Failed to delete plan', err);
    },
  });
}

export function useQuotaUsage() {
  return useQuery({
    queryKey: settingsKeys.quotaUsage,
    queryFn: () => api.getQuotaUsage(),
    // Usage shifts every time a project / cluster CRUD lands. Modest staleness
    // keeps the page snappy without hammering the backend.
    staleTime: 30 * 1000,
  });
}

// ============================================================
// Group Mappings
// ============================================================

export function useGroupMappings() {
  return useQuery({
    queryKey: settingsKeys.groupMappings,
    queryFn: () => api.listGroupMappings(),
  });
}

export function useCreateGroupMapping() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: GroupMappingWriteRequest) => api.createGroupMapping(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.groupMappings });
      toastSuccess('Group mapping created');
    },
    onError: (err: Error) => {
      toastApiError('Failed to create mapping', err);
    },
  });
}

export function useDeleteGroupMapping() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteGroupMapping(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.groupMappings });
      toastSuccess('Group mapping deleted');
    },
    onError: (err: Error) => {
      toastApiError('Failed to delete mapping', err);
    },
  });
}

export function useResyncUserGroups() {
  return useMutation({
    mutationFn: (userId: string) => api.resyncUserGroups(userId),
    onSuccess: (result) => {
      toastSuccess(`Synced ${result.synced} group(s)`);
    },
    onError: (err: Error) => {
      toastApiError('Resync failed', err);
    },
  });
}

// ============================================================
// Backup Drill
// ============================================================

export function useLatestBackupDrill() {
  return useQuery({
    queryKey: settingsKeys.backupDrill,
    queryFn: () => api.getLatestBackupDrill(),
  });
}

export function useBackupDrillHistory(params?: { page?: number; page_size?: number }) {
  return useQuery({
    queryKey: settingsKeys.backupDrillHistory(params),
    queryFn: () => api.listBackupDrillHistory(params),
  });
}

// ============================================================
// GitOps cluster registration (migration 060)
// ============================================================

export function useGitOpsSources() {
  return useQuery({
    queryKey: settingsKeys.gitopsSources,
    queryFn: () => api.listGitOpsSources(),
  });
}

export function useGitOpsSource(id: string | undefined) {
  return useQuery({
    queryKey: settingsKeys.gitopsSource(id ?? ''),
    queryFn: () => api.getGitOpsSource(id!),
    enabled: !!id,
  });
}

export function useGitOpsSourceClusters(id: string | undefined) {
  return useQuery({
    queryKey: settingsKeys.gitopsClusters(id ?? ''),
    queryFn: () => api.listGitOpsSourceClusters(id!),
    enabled: !!id,
  });
}

export function useCreateGitOpsSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: api.GitOpsSourceWriteRequest) =>
      api.createGitOpsSource(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSources });
      toastSuccess('GitOps source created');
    },
    onError: (err: Error) => {
      toastApiError('Create failed', err);
    },
  });
}

export function useUpdateGitOpsSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: string;
      body: Partial<api.GitOpsSourceWriteRequest>;
    }) => api.updateGitOpsSource(id, body),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSources });
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSource(vars.id) });
      toastSuccess('GitOps source updated');
    },
    onError: (err: Error) => {
      toastApiError('Update failed', err);
    },
  });
}

export function useDeleteGitOpsSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteGitOpsSource(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSources });
      toastSuccess('GitOps source deleted');
    },
    onError: (err: Error) => {
      toastApiError('Delete failed', err);
    },
  });
}

export function useSyncGitOpsSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.syncGitOpsSource(id),
    onSuccess: (_data, id) => {
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSource(id) });
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsClusters(id) });
      toastSuccess('Sync triggered');
    },
    onError: (err: Error) => {
      toastApiError('Sync failed', err);
    },
  });
}

export function usePreviewGitOpsSource() {
  return useMutation({
    mutationFn: (id: string) => api.previewGitOpsSource(id),
    onError: (err: Error) => {
      toastApiError('Preview failed', err);
    },
  });
}
