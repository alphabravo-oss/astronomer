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
import { toast } from 'sonner';
import * as api from '@/lib/api';
import { useAuthStore } from '@/lib/store';
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
 * The User type currently exposes `globalRoles: string[]`; presence of the
 * `admin` or `superuser` role gates the page. The backend already returns
 * 403 for non-admins, so this is purely a UX nicety to avoid the empty
 * loading-then-error flash.
 */
export function useIsSuperuser(): { isSuperuser: boolean; ready: boolean } {
  const user = useAuthStore((s) => s.user);
  if (!user) return { isSuperuser: false, ready: false };
  const roles = user.globalRoles ?? [];
  return {
    isSuperuser: roles.includes('admin') || roles.includes('superuser'),
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
      toast.success('Platform settings saved');
    },
    onError: (err: Error) => {
      toast.error(`Failed to save settings: ${err.message}`);
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
      toast.success('SMTP configuration saved');
    },
    onError: (err: Error) => {
      toast.error(`Failed to save SMTP config: ${err.message}`);
    },
  });
}

export function useTestSmtp() {
  return useMutation({
    mutationFn: (body: SmtpTestRequest) => api.testSmtpConfig(body),
    onSuccess: (result) => {
      if (result.success) {
        toast.success(`Test email sent in ${result.durationMs}ms`);
      } else {
        toast.error(`Test failed: ${result.message}`);
      }
    },
    onError: (err: Error) => {
      toast.error(`SMTP test failed: ${err.message}`);
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
      toast.success('Webhook created');
    },
    onError: (err: Error) => {
      toast.error(`Failed to create webhook: ${err.message}`);
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
      toast.success('Webhook updated');
    },
    onError: (err: Error) => {
      toast.error(`Failed to update webhook: ${err.message}`);
    },
  });
}

export function useDeleteWebhook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteWebhook(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.webhooks });
      toast.success('Webhook deleted');
    },
    onError: (err: Error) => {
      toast.error(`Failed to delete webhook: ${err.message}`);
    },
  });
}

export function useTestWebhook() {
  return useMutation({
    mutationFn: (id: string) => api.testWebhook(id),
    onError: (err: Error) => {
      toast.error(`Webhook test failed: ${err.message}`);
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
      toast.success('Delivery re-queued');
    },
    onError: (err: Error) => {
      toast.error(`Retry failed: ${err.message}`);
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
      toast.success('Quota plan created');
    },
    onError: (err: Error) => {
      toast.error(`Failed to create plan: ${err.message}`);
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
      toast.success('Quota plan saved');
    },
    onError: (err: Error) => {
      toast.error(`Failed to save plan: ${err.message}`);
    },
  });
}

export function useDeleteQuotaPlan() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.deleteQuotaPlan(name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.quotas });
      toast.success('Quota plan deleted');
    },
    onError: (err: Error) => {
      toast.error(`Failed to delete plan: ${err.message}`);
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
      toast.success('Group mapping created');
    },
    onError: (err: Error) => {
      toast.error(`Failed to create mapping: ${err.message}`);
    },
  });
}

export function useDeleteGroupMapping() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteGroupMapping(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.groupMappings });
      toast.success('Group mapping deleted');
    },
    onError: (err: Error) => {
      toast.error(`Failed to delete mapping: ${err.message}`);
    },
  });
}

export function useResyncUserGroups() {
  return useMutation({
    mutationFn: (userId: string) => api.resyncUserGroups(userId),
    onSuccess: (result) => {
      toast.success(`Synced ${result.synced} group(s)`);
    },
    onError: (err: Error) => {
      toast.error(`Resync failed: ${err.message}`);
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
