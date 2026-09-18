/**
 * React Query hooks for the Settings hub.
 *
 * These wrap `lib/api/settings.ts` with the same conventions every other
 * feature module uses: stable query-key factories, mutations that invalidate
 * the relevant lists, and `toast` calls for user-visible side-effects. They
 * live next to the settings pages rather than in the global `lib/hooks.ts`
 * so this phase doesn't touch the shared hooks module.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import {
  listPlatformSettings,
  savePlatformSettingsBatch,
} from "@/lib/api/platform-settings";
import {
  getSmtpConfig,
  updateSmtpConfig,
  testSmtpConfig,
  listSentEmails,
} from "@/lib/api/settings-email";
import {
  listWebhooks,
  getWebhook,
  createWebhook,
  updateWebhook,
  deleteWebhook,
  testWebhook,
  listWebhookDeliveries,
  retryWebhookDelivery,
} from "@/lib/api/settings-webhooks";
import {
  listQuotaPlans,
  getQuotaPlan,
  createQuotaPlan,
  updateQuotaPlan,
  deleteQuotaPlan,
  getQuotaUsage,
} from "@/lib/api/quotas";
import {
  listGroupMappings,
  createGroupMapping,
  deleteGroupMapping,
} from "@/lib/api/settings-group-mappings";
import {
  getLatestBackupDrill,
  listBackupDrillHistory,
  getManagementBackupStatus,
  createManagementBackupDestination,
  updateManagementBackupDestination,
  deleteManagementBackupDestination,
  testManagementBackupDestination,
  getManagementBackupOperation,
  runManagementBackupDestination,
} from "@/lib/api/settings-backup-drill";
import {
  listNotificationTemplates,
  getNotificationTemplate,
} from "@/lib/api/settings-notification-templates";
import type { ManagementBackupDestinationWrite } from "@/lib/api/settings-backup-drill";
import {
  listGitOpsSources,
  getGitOpsSource,
  listGitOpsSourceClusters,
  createGitOpsSource,
  updateGitOpsSource,
  deleteGitOpsSource,
  syncGitOpsSource,
  previewGitOpsSource,
} from "@/lib/api/gitops";
import type { GitOpsSourceWriteRequest } from "@/lib/api/gitops";
import { queryKeys } from "@/lib/query-keys";
import { useAuthStore } from "@/lib/store";
import { isSuperuser as hasSuperuserAccess } from "@/lib/permissions";
import type {
  GroupMappingWriteRequest,
  QuotaPlanWriteRequest,
  SmtpConfig,
  SmtpTestRequest,
  WebhookWriteRequest,
} from "@/lib/api/settings";
import { useOperationMutation } from "@/lib/hooks/operation-mutation";

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
  all: ["settings"] as const,
  platform: ["settings", "platform"] as const,
  smtp: ["settings", "smtp"] as const,
  emails: (params?: Record<string, unknown>) =>
    ["settings", "emails", params] as const,
  webhooks: ["settings", "webhooks"] as const,
  webhook: (id: string) => ["settings", "webhooks", id] as const,
  webhookDeliveries: (id: string, params?: Record<string, unknown>) =>
    ["settings", "webhooks", id, "deliveries", params] as const,
  quotas: ["settings", "quotas"] as const,
  quota: (name: string) => ["settings", "quotas", name] as const,
  quotaUsage: ["settings", "quota-usage"] as const,
  groupMappings: ["settings", "group-mappings"] as const,
  backupDrill: ["settings", "backup-drill"] as const,
  backupDrillHistory: (params?: Record<string, unknown>) =>
    ["settings", "backup-drill", "history", params] as const,
  managementBackup: ["settings", "management-backup"] as const,
  notificationTemplates: ["settings", "notification-templates"] as const,
  notificationTemplate: (key: string) =>
    ["settings", "notification-templates", key] as const,
  gitopsSources: ["settings", "gitops-sources"] as const,
  gitopsSource: (id: string) => ["settings", "gitops-sources", id] as const,
  gitopsClusters: (id: string) =>
    ["settings", "gitops-sources", id, "clusters"] as const,
};

// ============================================================
// Platform Settings
// ============================================================

export function usePlatformSettings() {
  return useQuery({
    queryKey: settingsKeys.platform,
    queryFn: () => listPlatformSettings(),
  });
}

export function useSavePlatformSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (updates: Record<string, unknown>) =>
      savePlatformSettingsBatch(updates),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.platform });
      qc.invalidateQueries({ queryKey: queryKeys.featureFlags });
      toastSuccess("Platform settings saved");
    },
    onError: (err: Error) => {
      toastApiError("Failed to save settings", err);
    },
  });
}

// ============================================================
// SMTP
// ============================================================

export function useSmtpConfig() {
  return useQuery({
    queryKey: settingsKeys.smtp,
    queryFn: () => getSmtpConfig(),
  });
}

export function useUpdateSmtpConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<SmtpConfig>) => updateSmtpConfig(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.smtp });
      toastSuccess("SMTP configuration saved");
    },
    onError: (err: Error) => {
      toastApiError("Failed to save SMTP config", err);
    },
  });
}

export function useTestSmtp() {
  return useMutation({
    mutationFn: (body: SmtpTestRequest) => testSmtpConfig(body),
    onSuccess: (result) => {
      if (result.success) {
        toastSuccess(`Test email sent in ${result.durationMs}ms`);
      } else {
        toastApiError("Test failed", result.message);
      }
    },
    onError: (err: Error) => {
      toastApiError("SMTP test failed", err);
    },
  });
}

export function useSentEmails(params?: { page?: number; page_size?: number }) {
  return useQuery({
    queryKey: settingsKeys.emails(params),
    queryFn: ({ signal }) => listSentEmails({ ...params, signal }),
  });
}

// ============================================================
// Webhooks
// ============================================================

export function useWebhooks() {
  return useQuery({
    queryKey: settingsKeys.webhooks,
    queryFn: ({ signal }) => listWebhooks({ signal }),
  });
}

export function useWebhook(id: string | undefined) {
  return useQuery({
    queryKey: settingsKeys.webhook(id ?? ""),
    queryFn: ({ signal }) => getWebhook(id as string, { signal }),
    enabled: !!id,
  });
}

export function useCreateWebhook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: WebhookWriteRequest) => createWebhook(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.webhooks });
      toastSuccess("Webhook created");
    },
    onError: (err: Error) => {
      toastApiError("Failed to create webhook", err);
    },
  });
}

export function useUpdateWebhook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: string;
      body: Partial<WebhookWriteRequest>;
    }) => updateWebhook(id, body),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: settingsKeys.webhooks });
      qc.invalidateQueries({ queryKey: settingsKeys.webhook(vars.id) });
      toastSuccess("Webhook updated");
    },
    onError: (err: Error) => {
      toastApiError("Failed to update webhook", err);
    },
  });
}

export function useDeleteWebhook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteWebhook(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.webhooks });
      toastSuccess("Webhook deleted");
    },
    onError: (err: Error) => {
      toastApiError("Failed to delete webhook", err);
    },
  });
}

export function useTestWebhook() {
  return useMutation({
    mutationFn: (id: string) => testWebhook(id),
    onError: (err: Error) => {
      toastApiError("Webhook test failed", err);
    },
  });
}

export function useWebhookDeliveries(
  id: string | undefined,
  params?: { page?: number; page_size?: number },
) {
  return useQuery({
    queryKey: settingsKeys.webhookDeliveries(id ?? "", params),
    queryFn: ({ signal }) =>
      listWebhookDeliveries(id as string, params, { signal }),
    enabled: !!id,
  });
}

export function useRetryWebhookDelivery(webhookId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (deliveryId: string) =>
      retryWebhookDelivery(webhookId, deliveryId),
    onSuccess: () => {
      qc.invalidateQueries({
        queryKey: queryKeys.settings.webhookDeliveries(webhookId),
      });
      toastSuccess("Delivery re-queued");
    },
    onError: (err: Error) => {
      toastApiError("Retry failed", err);
    },
  });
}

// ============================================================
// Quotas
// ============================================================

export function useQuotaPlans() {
  return useQuery({
    queryKey: settingsKeys.quotas,
    queryFn: () => listQuotaPlans(),
  });
}

export function useQuotaPlan(name: string | undefined) {
  return useQuery({
    queryKey: settingsKeys.quota(name ?? ""),
    queryFn: () => getQuotaPlan(name as string),
    enabled: !!name,
  });
}

export function useCreateQuotaPlan() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: QuotaPlanWriteRequest) => createQuotaPlan(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.quotas });
      toastSuccess("Quota plan created");
    },
    onError: (err: Error) => {
      toastApiError("Failed to create plan", err);
    },
  });
}

export function useUpdateQuotaPlan() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      name,
      body,
    }: {
      name: string;
      body: QuotaPlanWriteRequest;
    }) => updateQuotaPlan(name, body),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: settingsKeys.quotas });
      qc.invalidateQueries({ queryKey: settingsKeys.quota(vars.name) });
      toastSuccess("Quota plan saved");
    },
    onError: (err: Error) => {
      toastApiError("Failed to save plan", err);
    },
  });
}

export function useDeleteQuotaPlan() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => deleteQuotaPlan(name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.quotas });
      toastSuccess("Quota plan deleted");
    },
    onError: (err: Error) => {
      toastApiError("Failed to delete plan", err);
    },
  });
}

export function useQuotaUsage() {
  return useQuery({
    queryKey: settingsKeys.quotaUsage,
    queryFn: () => getQuotaUsage(),
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
    queryFn: () => listGroupMappings(),
  });
}

export function useCreateGroupMapping() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: GroupMappingWriteRequest) => createGroupMapping(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.groupMappings });
      toastSuccess("Group mapping created");
    },
    onError: (err: Error) => {
      toastApiError("Failed to create mapping", err);
    },
  });
}

export function useDeleteGroupMapping() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteGroupMapping(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.groupMappings });
      toastSuccess("Group mapping deleted");
    },
    onError: (err: Error) => {
      toastApiError("Failed to delete mapping", err);
    },
  });
}

// ============================================================
// Backup Drill
// ============================================================

export function useLatestBackupDrill() {
  return useQuery({
    queryKey: settingsKeys.backupDrill,
    queryFn: ({ signal }) => getLatestBackupDrill({ signal }),
  });
}

export function useBackupDrillHistory(params?: {
  page?: number;
  page_size?: number;
}) {
  return useQuery({
    queryKey: settingsKeys.backupDrillHistory(params),
    queryFn: ({ signal }) => listBackupDrillHistory(params, { signal }),
  });
}

export function useManagementBackupStatus() {
  return useQuery({
    queryKey: settingsKeys.managementBackup,
    queryFn: ({ signal }) => getManagementBackupStatus({ signal }),
    refetchInterval: (query) =>
      query.state.data?.destinations.some((destination) => {
        const status = destination.reconcileStatus?.toLowerCase();
        return (
          status === "pending" ||
          status === "running" ||
          status === "retrying" ||
          (destination.desiredGeneration ?? 0) >
            (destination.appliedGeneration ?? 0)
        );
      })
        ? 2_000
        : false,
  });
}

export function useNotificationTemplates() {
  return useQuery({
    queryKey: settingsKeys.notificationTemplates,
    queryFn: ({ signal }) => listNotificationTemplates({ signal }),
  });
}

export function useNotificationTemplate(key: string) {
  return useQuery({
    queryKey: settingsKeys.notificationTemplate(key),
    queryFn: ({ signal }) => getNotificationTemplate(key, { signal }),
    enabled: !!key,
  });
}

export function useCreateManagementBackupDestination() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ManagementBackupDestinationWrite) =>
      createManagementBackupDestination(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.managementBackup });
      toastSuccess("Backup destination saved; reconciliation queued");
    },
    onError: (err: Error) => toastApiError("Failed to save destination", err),
  });
}

export function useUpdateManagementBackupDestination() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: string;
      body: ManagementBackupDestinationWrite;
    }) => updateManagementBackupDestination(id, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.managementBackup });
      toastSuccess("Backup destination update queued");
    },
    onError: (err: Error) => toastApiError("Failed to update destination", err),
  });
}

export function useDeleteManagementBackupDestination() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteManagementBackupDestination(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.managementBackup });
      toastSuccess("Backup destination removal queued");
    },
    onError: (err: Error) => toastApiError("Failed to remove destination", err),
  });
}

export function useTestManagementBackupDestination() {
  return useOperationMutation({
    keyPrefix: "management-backup-test",
    submit: (id: string, context) =>
      testManagementBackupDestination(id, context),
    read: getManagementBackupOperation,
    mutation: {
      onSuccess: () => toastSuccess("Backup destination connection verified"),
      onError: (err: Error) => toastApiError("Connection test failed", err),
    },
  });
}

export function useRunManagementBackupDestination() {
  const qc = useQueryClient();
  return useOperationMutation({
    keyPrefix: "management-backup-run",
    submit: (id: string, context) =>
      runManagementBackupDestination(id, context),
    read: getManagementBackupOperation,
    mutation: {
      onSuccess: () => {
        qc.invalidateQueries({ queryKey: settingsKeys.managementBackup });
        toastSuccess(managementBackupSubmittedMessage);
      },
      onError: (err: Error) => toastApiError("Failed to start backup", err),
    },
  });
}

export const managementBackupSubmittedMessage =
  "Backup run accepted; track execution in backup history";

// ============================================================
// GitOps cluster registration (migration 060)
// ============================================================

export function useGitOpsSources() {
  return useQuery({
    queryKey: settingsKeys.gitopsSources,
    queryFn: () => listGitOpsSources(),
  });
}

export function useGitOpsSource(id: string | undefined) {
  return useQuery({
    queryKey: settingsKeys.gitopsSource(id ?? ""),
    queryFn: () => getGitOpsSource(id!),
    enabled: !!id,
  });
}

export function useGitOpsSourceClusters(id: string | undefined) {
  return useQuery({
    queryKey: settingsKeys.gitopsClusters(id ?? ""),
    queryFn: () => listGitOpsSourceClusters(id!),
    enabled: !!id,
  });
}

export function useCreateGitOpsSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: GitOpsSourceWriteRequest) => createGitOpsSource(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSources });
      toastSuccess("GitOps source created");
    },
    onError: (err: Error) => {
      toastApiError("Create failed", err);
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
      body: Partial<GitOpsSourceWriteRequest>;
    }) => updateGitOpsSource(id, body),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSources });
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSource(vars.id) });
      toastSuccess("GitOps source updated");
    },
    onError: (err: Error) => {
      toastApiError("Update failed", err);
    },
  });
}

export function useDeleteGitOpsSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteGitOpsSource(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSources });
      toastSuccess("GitOps source deleted");
    },
    onError: (err: Error) => {
      toastApiError("Delete failed", err);
    },
  });
}

export function useSyncGitOpsSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => syncGitOpsSource(id),
    onSuccess: (_data, id) => {
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsSource(id) });
      qc.invalidateQueries({ queryKey: settingsKeys.gitopsClusters(id) });
      toastSuccess("Sync triggered");
    },
    onError: (err: Error) => {
      toastApiError("Sync failed", err);
    },
  });
}

export function usePreviewGitOpsSource() {
  return useMutation({
    mutationFn: (id: string) => previewGitOpsSource(id),
    onError: (err: Error) => {
      toastApiError("Preview failed", err);
    },
  });
}
