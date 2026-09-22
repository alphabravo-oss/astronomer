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
