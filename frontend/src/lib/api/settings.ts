/**
 * Settings hub API client — sprints 9–14.
 *
 * Covers the new admin surfaces: platform settings, SMTP, webhooks, quota
 * plans, SSO group mappings, compliance exports, and backup-restore drills.
 *
 * Conventions match the rest of `lib/api.ts`:
 *   - Reads come back camelCased (the axios response interceptor handles it).
 *   - Writes send the snake_case keys the Go handlers declare in their
 *     `json:"..."` tags.
 *   - Single-object endpoints return `{ data: T }`; lists return the standard
 *     paginated envelope `{ data, total, page, page_size, total_pages }`.
 *
 * All endpoints under `/api/v1/admin/*` require a JWT with the `admin` role
 * (or `is_superuser` claim). The axios instance already stamps the bearer
 * header; admin gating in the UI is layered on top via `useIsSuperuser()`.
 */
import api from '@/lib/api';
import type { APIResponse, PaginatedResponse } from '@/types';

// ============================================================
// Types — Platform Settings
// ============================================================

/**
 * Settings are stored as flat dotted keys (e.g. `branding.primary_color`).
 * The API exposes both the flat list and a grouped view; we keep both
 * representations available since the form renders the grouped view but
 * batch-saves via individual `PUT /settings/{key}/` calls.
 */
export interface PlatformSetting {
  key: string;
  value: unknown;
  valueType: 'string' | 'int' | 'bool' | 'json';
  group: string;
  description?: string;
  updatedAt: string;
  updatedBy?: string;
}

export interface BannerColor {
  bg: 'info' | 'success' | 'warning' | 'error';
}

export interface PlatformSettingsGrouped {
  branding: {
    logoUrl: string;
    productName: string;
    primaryColor: string;
    supportUrl: string;
    copyright: string;
  };
  banners: {
    loginBannerText: string;
    globalBannerText: string;
    globalBannerColor: BannerColor['bg'];
  };
  features: {
    catalog: boolean;
    projects: boolean;
    monitoring: boolean;
    argocd: boolean;
    security: boolean;
    backups: boolean;
  };
  tokens: {
    defaultTtlSeconds: number;
    maxTtlSeconds: number;
  };
  telemetry: {
    enabled: boolean;
    endpoint: string;
  };
}

// ============================================================
// Types — SMTP
// ============================================================

export type SmtpAuth = 'plain' | 'login' | 'cram-md5' | 'none';
export type SmtpEncryption = 'starttls' | 'tls' | 'none';

export interface SmtpConfig {
  host: string;
  port: number;
  username: string;
  /**
   * On reads the backend returns a sentinel ("__redacted__") rather than the
   * stored secret. On writes, sending the same sentinel preserves the
   * existing password; sending any other value rotates it.
   */
  password: string;
  fromAddress: string;
  fromName: string;
  authMechanism: SmtpAuth;
  encryption: SmtpEncryption;
  requireTls: boolean;
  timeoutSeconds: number;
  updatedAt?: string;
}

export const SMTP_REDACTED_SENTINEL = '__redacted__';

export interface SmtpTestRequest {
  to: string;
}

export interface SmtpTestResult {
  success: boolean;
  message: string;
  durationMs: number;
}

export type EmailStatus = 'queued' | 'sending' | 'sent' | 'failed' | 'bounced';

export interface SentEmail {
  id: string;
  to: string;
  subject: string;
  template: string;
  status: EmailStatus;
  attempts: number;
  lastError?: string;
  sentAt?: string;
  createdAt: string;
}

// ============================================================
// Types — Webhooks
// ============================================================

export type WebhookTemplate = 'slack' | 'pagerduty' | 'generic';

export interface WebhookFilter {
  /** Event types to dispatch — e.g. `cluster.healthy`, `backup.failed`. */
  events: string[];
  /** Optional severity gate (`info`, `warning`, `critical`). */
  minSeverity?: 'info' | 'warning' | 'critical';
}

export interface WebhookSubscription {
  id: string;
  name: string;
  url: string;
  template: WebhookTemplate;
  /** Shared HMAC secret. Redacted on reads, sentinel-preserved on writes. */
  secret: string;
  enabled: boolean;
  filters: WebhookFilter;
  lastDeliveryStatus?: 'success' | 'failed';
  lastDeliveryAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface WebhookWriteRequest {
  name: string;
  url: string;
  template: WebhookTemplate;
  secret?: string;
  enabled: boolean;
  filters: WebhookFilter;
}

export type WebhookDeliveryStatus = 'pending' | 'success' | 'failed' | 'retrying';

export interface WebhookDelivery {
  id: string;
  subscriptionId: string;
  eventType: string;
  status: WebhookDeliveryStatus;
  responseCode?: number;
  responseBody?: string;
  errorMessage?: string;
  attempts: number;
  durationMs?: number;
  deliveredAt?: string;
  createdAt: string;
}

export interface WebhookTestResult {
  success: boolean;
  responseCode?: number;
  responseBody?: string;
  durationMs: number;
  errorMessage?: string;
}

// ============================================================
// Types — Quotas
// ============================================================

export type QuotaEnforcement = 'soft' | 'hard' | 'disabled';

export interface QuotaPlan {
  name: string;
  displayName: string;
  description?: string;
  enforcement: QuotaEnforcement;
  maxProjects: number;
  maxClusters: number;
  maxNamespaces: number;
  maxUsers: number;
  maxStorageGb: number;
  maxCpuCores: number;
  maxMemoryGb: number;
  maxBackupsPerDay: number;
  maxApiTokens: number;
  createdAt: string;
  updatedAt: string;
}

export interface QuotaPlanWriteRequest {
  name: string;
  display_name: string;
  description?: string;
  enforcement: QuotaEnforcement;
  max_projects: number;
  max_clusters: number;
  max_namespaces: number;
  max_users: number;
  max_storage_gb: number;
  max_cpu_cores: number;
  max_memory_gb: number;
  max_backups_per_day: number;
  max_api_tokens: number;
}

export interface QuotaUsageRow {
  planName: string;
  scope: 'global' | 'project' | 'cluster';
  scopeId?: string;
  scopeName?: string;
  /** Map of `max_*` field → current usage. */
  usage: Record<string, number>;
  /** Map of `max_*` field → percent of cap (0-100). */
  utilization: Record<string, number>;
}

export interface QuotaUsageSummary {
  rows: QuotaUsageRow[];
  fleetTotals: Record<string, number>;
  /** Entities at >80% of any cap. */
  topOffenders: QuotaUsageRow[];
}

// ============================================================
// Types — Group Mappings
// ============================================================

export type GroupScope = 'global' | 'cluster' | 'project';

export interface GroupMapping {
  id: string;
  /** Empty / "any" matches any connector. */
  connector: string;
  groupName: string;
  scope: GroupScope;
  role: string;
  /** Cluster UUID when scope=cluster; project name when scope=project. */
  target?: string;
  targetDisplay?: string;
  createdAt: string;
  createdBy: string;
}

export interface GroupMappingWriteRequest {
  connector: string;
  group_name: string;
  scope: GroupScope;
  role: string;
  target?: string;
}

// ============================================================
// Types — Compliance
// ============================================================

export interface ComplianceExportSummary {
  id: string;
  from: string;
  to: string;
  /** Set when the export is large enough to require background work. */
  status?: 'pending' | 'running' | 'ready' | 'failed';
  progress?: number;
  sizeBytes?: number;
  downloadUrl?: string;
  errorMessage?: string;
  createdAt: string;
  completedAt?: string;
}

// ============================================================
// Types — Backup Drill
// ============================================================

export type BackupDrillStatus = 'success' | 'failure' | 'partial' | 'running';

export interface BackupDrillResult {
  id: string;
  status: BackupDrillStatus;
  schemaVersion: string;
  startedAt: string;
  completedAt?: string;
  durationSeconds?: number;
  ageSeconds: number;
  backupId?: string;
  restoredObjects?: number;
  errorMessage?: string;
}

// ============================================================
// Platform Settings — API funcs
// ============================================================

/** Flat list of every key. Useful when we need everything in one shot. */
export async function listPlatformSettings(): Promise<PlatformSetting[]> {
  const res = await api.get<APIResponse<PlatformSetting[]>>('/admin/settings');
  return res.data.data ?? (res.data as unknown as PlatformSetting[]);
}

export async function getPlatformSetting(key: string): Promise<PlatformSetting> {
  const res = await api.get<APIResponse<PlatformSetting>>(`/admin/settings/${encodeURIComponent(key)}`);
  return res.data.data ?? (res.data as unknown as PlatformSetting);
}

/**
 * Upsert a single setting. The backend treats PUT as idempotent — the form
 * batches one PUT per dirty field rather than sending a single grouped blob,
 * which mirrors what the Go handler accepts.
 */
export async function putPlatformSetting(key: string, value: unknown): Promise<PlatformSetting> {
  const res = await api.put<APIResponse<PlatformSetting>>(
    `/admin/settings/${encodeURIComponent(key)}`,
    { value },
  );
  return res.data.data ?? (res.data as unknown as PlatformSetting);
}

export async function deletePlatformSetting(key: string): Promise<void> {
  await api.delete(`/admin/settings/${encodeURIComponent(key)}`);
}

/**
 * Save a batch of settings, one PUT per key. Returns once all writes resolve
 * (or rejects on the first failure — the caller is expected to surface a
 * toast).
 */
export async function savePlatformSettingsBatch(
  updates: Record<string, unknown>,
): Promise<void> {
  await Promise.all(
    Object.entries(updates).map(([key, value]) => putPlatformSetting(key, value)),
  );
}

// ============================================================
// SMTP — API funcs
// ============================================================

export async function getSmtpConfig(): Promise<SmtpConfig> {
  const res = await api.get<APIResponse<SmtpConfig>>('/admin/smtp');
  return res.data.data ?? (res.data as unknown as SmtpConfig);
}

export async function updateSmtpConfig(body: Partial<SmtpConfig>): Promise<SmtpConfig> {
  // Strip the redaction sentinel from the password unless the operator
  // typed a new value — the backend will refuse the sentinel as a literal.
  const payload = { ...body };
  if (payload.password === SMTP_REDACTED_SENTINEL) {
    delete payload.password;
  }
  const res = await api.put<APIResponse<SmtpConfig>>('/admin/smtp', payload);
  return res.data.data ?? (res.data as unknown as SmtpConfig);
}

export async function testSmtpConfig(body: SmtpTestRequest): Promise<SmtpTestResult> {
  const res = await api.post<APIResponse<SmtpTestResult>>('/admin/smtp/test', body);
  return res.data.data ?? (res.data as unknown as SmtpTestResult);
}

export async function listSentEmails(params?: {
  page?: number;
  page_size?: number;
  status?: EmailStatus;
}) {
  const res = await api.get<PaginatedResponse<SentEmail>>('/admin/emails', { params });
  return res.data;
}

// ============================================================
// Webhooks — API funcs
// ============================================================

export async function listWebhooks(): Promise<WebhookSubscription[]> {
  const res = await api.get<APIResponse<WebhookSubscription[]>>('/admin/webhooks');
  return res.data.data ?? (res.data as unknown as WebhookSubscription[]);
}

export async function getWebhook(id: string): Promise<WebhookSubscription> {
  const res = await api.get<APIResponse<WebhookSubscription>>(`/admin/webhooks/${id}`);
  return res.data.data ?? (res.data as unknown as WebhookSubscription);
}

export async function createWebhook(body: WebhookWriteRequest): Promise<WebhookSubscription> {
  const res = await api.post<APIResponse<WebhookSubscription>>('/admin/webhooks', body);
  return res.data.data ?? (res.data as unknown as WebhookSubscription);
}

export async function updateWebhook(
  id: string,
  body: Partial<WebhookWriteRequest>,
): Promise<WebhookSubscription> {
  const res = await api.put<APIResponse<WebhookSubscription>>(`/admin/webhooks/${id}`, body);
  return res.data.data ?? (res.data as unknown as WebhookSubscription);
}

export async function deleteWebhook(id: string): Promise<void> {
  await api.delete(`/admin/webhooks/${id}`);
}

export async function testWebhook(id: string): Promise<WebhookTestResult> {
  const res = await api.post<APIResponse<WebhookTestResult>>(`/admin/webhooks/${id}/test`);
  return res.data.data ?? (res.data as unknown as WebhookTestResult);
}

export async function listWebhookDeliveries(
  id: string,
  params?: { page?: number; page_size?: number },
) {
  const res = await api.get<PaginatedResponse<WebhookDelivery>>(
    `/admin/webhooks/${id}/deliveries`,
    { params },
  );
  return res.data;
}

export async function retryWebhookDelivery(
  webhookId: string,
  deliveryId: string,
): Promise<WebhookDelivery> {
  const res = await api.post<APIResponse<WebhookDelivery>>(
    `/admin/webhooks/${webhookId}/deliveries/${deliveryId}/retry`,
  );
  return res.data.data ?? (res.data as unknown as WebhookDelivery);
}

// ============================================================
// Quota Plans — API funcs
// ============================================================

export async function listQuotaPlans(): Promise<QuotaPlan[]> {
  const res = await api.get<APIResponse<QuotaPlan[]>>('/admin/quota-plans');
  return res.data.data ?? (res.data as unknown as QuotaPlan[]);
}

export async function getQuotaPlan(name: string): Promise<QuotaPlan> {
  const res = await api.get<APIResponse<QuotaPlan>>(`/admin/quota-plans/${encodeURIComponent(name)}`);
  return res.data.data ?? (res.data as unknown as QuotaPlan);
}

export async function createQuotaPlan(body: QuotaPlanWriteRequest): Promise<QuotaPlan> {
  const res = await api.post<APIResponse<QuotaPlan>>('/admin/quota-plans', body);
  return res.data.data ?? (res.data as unknown as QuotaPlan);
}

export async function updateQuotaPlan(
  name: string,
  body: Partial<QuotaPlanWriteRequest>,
): Promise<QuotaPlan> {
  const res = await api.put<APIResponse<QuotaPlan>>(
    `/admin/quota-plans/${encodeURIComponent(name)}`,
    body,
  );
  return res.data.data ?? (res.data as unknown as QuotaPlan);
}

export async function deleteQuotaPlan(name: string): Promise<void> {
  await api.delete(`/admin/quota-plans/${encodeURIComponent(name)}`);
}

export async function getQuotaUsage(): Promise<QuotaUsageSummary> {
  const res = await api.get<APIResponse<QuotaUsageSummary>>('/admin/quota-usage');
  return res.data.data ?? (res.data as unknown as QuotaUsageSummary);
}

// ============================================================
// Group Mappings — API funcs
// ============================================================

export async function listGroupMappings(): Promise<GroupMapping[]> {
  const res = await api.get<APIResponse<GroupMapping[]>>('/admin/group-mappings');
  return res.data.data ?? (res.data as unknown as GroupMapping[]);
}

export async function createGroupMapping(
  body: GroupMappingWriteRequest,
): Promise<GroupMapping> {
  const res = await api.post<APIResponse<GroupMapping>>('/admin/group-mappings', body);
  return res.data.data ?? (res.data as unknown as GroupMapping);
}

export async function deleteGroupMapping(id: string): Promise<void> {
  await api.delete(`/admin/group-mappings/${id}`);
}

export async function resyncUserGroups(userId: string): Promise<{ synced: number }> {
  const res = await api.post<APIResponse<{ synced: number }>>(
    `/admin/users/${userId}/resync-groups`,
  );
  return res.data.data ?? (res.data as unknown as { synced: number });
}

// ============================================================
// Compliance — API funcs
// ============================================================

/**
 * Trigger or fetch a compliance export.
 *
 * Small ranges (< 30 days, < ~100MB) return the ZIP body directly with a
 * 200 status. Larger ranges return 202 with an `ExportSummary` payload
 * pointing at a background job — callers should poll
 * `getComplianceExport(id)` until `status === 'ready'`.
 */
export async function requestComplianceExport(params: {
  from: string;
  to: string;
}): Promise<{ kind: 'blob'; blob: Blob; filename: string } | { kind: 'job'; job: ComplianceExportSummary }> {
  const res = await api.get('/admin/compliance/export', {
    params,
    responseType: 'blob',
    // 202 still resolves; we inspect the headers/status to decide.
    validateStatus: (s) => s >= 200 && s < 300,
  });
  if (res.status === 202) {
    // axios still gave us a Blob — parse it back into JSON.
    const text = await (res.data as Blob).text();
    const job = JSON.parse(text) as { data?: ComplianceExportSummary } | ComplianceExportSummary;
    const summary = (job as { data?: ComplianceExportSummary }).data ?? (job as ComplianceExportSummary);
    return { kind: 'job', job: summary };
  }
  const disposition = (res.headers as Record<string, string>)?.['content-disposition'] || '';
  const match = /filename="([^"]+)"/.exec(disposition);
  const filename = match?.[1] || `compliance-${params.from}_${params.to}.zip`;
  return { kind: 'blob', blob: res.data as Blob, filename };
}

export async function getComplianceExport(id: string): Promise<ComplianceExportSummary> {
  const res = await api.get<APIResponse<ComplianceExportSummary>>(
    `/admin/compliance/exports/${id}`,
  );
  return res.data.data ?? (res.data as unknown as ComplianceExportSummary);
}

// ============================================================
// Backup Drill — API funcs
// ============================================================

export async function getLatestBackupDrill(): Promise<BackupDrillResult | null> {
  try {
    const res = await api.get<APIResponse<BackupDrillResult>>('/admin/backup-drill');
    return res.data.data ?? (res.data as unknown as BackupDrillResult);
  } catch (err) {
    // 404 = no drill has run yet; surface as null instead of throwing.
    const status = (err as { response?: { status?: number } })?.response?.status;
    if (status === 404) return null;
    throw err;
  }
}

export async function listBackupDrillHistory(params?: {
  page?: number;
  page_size?: number;
}) {
  const res = await api.get<PaginatedResponse<BackupDrillResult>>(
    '/admin/backup-drill/history',
    { params },
  );
  return res.data;
}

// ============================================================
// Notification Templates (migration 059)
// ============================================================

export interface NotificationTemplateVariable {
  name: string;
  description: string;
  required: boolean;
  example: string;
}

export interface NotificationTemplateListItem {
  key: string;
  channel: 'email' | 'webhook';
  description: string;
  bodyFormat: string;
  hasOverride: boolean;
  enabled: boolean;
  updatedAt?: string;
}

export interface NotificationTemplateDetail {
  key: string;
  channel: 'email' | 'webhook';
  description: string;
  bodyFormat: string;
  defaultSubject: string;
  defaultBody: string;
  subject: string;
  body: string;
  hasOverride: boolean;
  enabled: boolean;
  updatedAt?: string;
  updatedBy?: string;
  variables: NotificationTemplateVariable[];
}

export interface NotificationTemplateUpsertBody {
  subject?: string;
  body: string;
  body_format?: string;
  enabled?: boolean;
}

export interface NotificationTemplatePreviewBody {
  subject?: string;
  body?: string;
  body_format?: string;
  variables: Record<string, unknown>;
}

export interface NotificationTemplatePreviewResult {
  subject: string;
  body: string;
}

export async function listNotificationTemplates(): Promise<NotificationTemplateListItem[]> {
  const res = await api.get<APIResponse<{ items: NotificationTemplateListItem[]; total: number }>>(
    '/admin/notification-templates/',
  );
  const data = res.data.data ?? (res.data as unknown as { items: NotificationTemplateListItem[] });
  return data.items ?? [];
}

export async function getNotificationTemplate(key: string): Promise<NotificationTemplateDetail> {
  const res = await api.get<APIResponse<NotificationTemplateDetail>>(
    `/admin/notification-templates/${encodeURIComponent(key)}/`,
  );
  return res.data.data ?? (res.data as unknown as NotificationTemplateDetail);
}

export async function updateNotificationTemplate(
  key: string,
  body: NotificationTemplateUpsertBody,
): Promise<NotificationTemplateDetail> {
  const res = await api.put<APIResponse<NotificationTemplateDetail>>(
    `/admin/notification-templates/${encodeURIComponent(key)}/`,
    body,
  );
  return res.data.data ?? (res.data as unknown as NotificationTemplateDetail);
}

export async function resetNotificationTemplate(key: string): Promise<void> {
  await api.delete(`/admin/notification-templates/${encodeURIComponent(key)}/`);
}

export async function previewNotificationTemplate(
  key: string,
  body: NotificationTemplatePreviewBody,
): Promise<NotificationTemplatePreviewResult> {
  const res = await api.post<APIResponse<NotificationTemplatePreviewResult>>(
    `/admin/notification-templates/${encodeURIComponent(key)}/preview/`,
    body,
  );
  return res.data.data ?? (res.data as unknown as NotificationTemplatePreviewResult);
}

export async function getNotificationTemplateVariables(
  key: string,
): Promise<NotificationTemplateVariable[]> {
  const res = await api.get<APIResponse<{ variables: NotificationTemplateVariable[] }>>(
    `/admin/notification-templates/${encodeURIComponent(key)}/variables/`,
  );
  const data =
    res.data.data ?? (res.data as unknown as { variables: NotificationTemplateVariable[] });
  return data.variables ?? [];
}

// ============================================================
// Types — GitOps cluster registration (migration 060)
// ============================================================

export type GitOpsAuthMode = 'none' | 'https_token' | 'ssh_key';
export type GitOpsSyncMode = 'manual' | 'interval';
export type GitOpsOnDelete = 'log' | 'tombstone' | 'decommission';

/** Sentinel echoed by the backend when an auth blob is configured. PUT
 *  the sentinel back to "keep existing"; substitute a real value to
 *  rotate. */
export const GITOPS_AUTH_SENTINEL = '<encrypted>';

export interface GitOpsSource {
  id: string;
  name: string;
  repo_url: string;
  branch: string;
  path_prefix: string;
  auth_mode: GitOpsAuthMode;
  auth: string;
  auth_configured: boolean;
  sync_mode: GitOpsSyncMode;
  sync_interval_seconds: number;
  on_delete: GitOpsOnDelete;
  last_synced_at?: string;
  last_synced_sha?: string;
  last_error?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface GitOpsSourceWriteRequest {
  name: string;
  repo_url: string;
  branch?: string;
  path_prefix?: string;
  auth_mode?: GitOpsAuthMode;
  auth?: string;
  sync_mode?: GitOpsSyncMode;
  sync_interval_seconds?: number;
  on_delete?: GitOpsOnDelete;
  enabled?: boolean;
}

export interface GitOpsManagedCluster {
  cluster_id: string;
  cluster_name?: string;
  display_name?: string;
  repo_path: string;
  last_yaml_sha: string;
  last_applied_at: string;
  status: 'active' | 'tombstoned';
  tombstoned_at?: string;
}

export interface GitOpsApplyPreview {
  cluster_name: string;
  cluster_id?: string;
  repo_path: string;
  created: boolean;
  updated: boolean;
  no_op: boolean;
  template_bound: boolean;
  registries: string[];
  tool_presets: string[];
  project?: string;
  restored_active?: boolean;
}

export interface GitOpsPreviewResult {
  head_sha: string;
  source_id: string;
  source_name: string;
  applies: GitOpsApplyPreview[];
  would_miss: string[];
  would_restore: string[];
  on_delete_policy: GitOpsOnDelete;
}

// ============================================================
// GitOps — API funcs
// ============================================================

export async function listGitOpsSources(): Promise<GitOpsSource[]> {
  const res = await api.get<APIResponse<{ sources: GitOpsSource[] }>>(
    '/admin/gitops-sources',
  );
  return res.data.data?.sources ?? [];
}

export async function getGitOpsSource(id: string): Promise<GitOpsSource> {
  const res = await api.get<APIResponse<GitOpsSource>>(
    `/admin/gitops-sources/${id}`,
  );
  return res.data.data ?? (res.data as unknown as GitOpsSource);
}

export async function createGitOpsSource(
  body: GitOpsSourceWriteRequest,
): Promise<GitOpsSource> {
  const res = await api.post<APIResponse<GitOpsSource>>(
    '/admin/gitops-sources',
    body,
  );
  return res.data.data ?? (res.data as unknown as GitOpsSource);
}

export async function updateGitOpsSource(
  id: string,
  body: Partial<GitOpsSourceWriteRequest>,
): Promise<GitOpsSource> {
  const res = await api.put<APIResponse<GitOpsSource>>(
    `/admin/gitops-sources/${id}`,
    body,
  );
  return res.data.data ?? (res.data as unknown as GitOpsSource);
}

export async function deleteGitOpsSource(id: string): Promise<void> {
  await api.delete(`/admin/gitops-sources/${id}`);
}

export async function syncGitOpsSource(id: string): Promise<void> {
  await api.post(`/admin/gitops-sources/${id}/sync`);
}

export async function previewGitOpsSource(
  id: string,
): Promise<GitOpsPreviewResult> {
  const res = await api.get<APIResponse<GitOpsPreviewResult>>(
    `/admin/gitops-sources/${id}/preview`,
  );
  return res.data.data ?? (res.data as unknown as GitOpsPreviewResult);
}

export async function listGitOpsSourceClusters(
  id: string,
): Promise<GitOpsManagedCluster[]> {
  const res = await api.get<APIResponse<{ clusters: GitOpsManagedCluster[] }>>(
    `/admin/gitops-sources/${id}/clusters`,
  );
  return res.data.data?.clusters ?? [];
}

// ---------------------------------------------------------------
// Read-audit policies (migration 063).
// ---------------------------------------------------------------

export interface ReadAuditPolicy {
  id: string;
  name: string;
  description: string;
  path_pattern: string;
  verbs: string;
  sample_rate: number;
  enabled: boolean;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface ReadAuditPolicyCreateBody {
  name: string;
  description?: string;
  path_pattern: string;
  verbs?: string;
  sample_rate?: number;
  enabled?: boolean;
}

export interface ReadAuditPolicyUpdateBody {
  description?: string;
  path_pattern?: string;
  verbs?: string;
  sample_rate?: number;
  enabled?: boolean;
}

export async function listReadAuditPolicies(): Promise<ReadAuditPolicy[]> {
  const res = await api.get<APIResponse<{ items: ReadAuditPolicy[]; total: number }>>(
    '/admin/read-audit-policies/',
  );
  const data = res.data.data ?? (res.data as unknown as { items: ReadAuditPolicy[] });
  return data.items ?? [];
}

export async function getReadAuditPolicy(id: string): Promise<ReadAuditPolicy> {
  const res = await api.get<APIResponse<ReadAuditPolicy>>(`/admin/read-audit-policies/${id}/`);
  return res.data.data ?? (res.data as unknown as ReadAuditPolicy);
}

export async function createReadAuditPolicy(
  body: ReadAuditPolicyCreateBody,
): Promise<ReadAuditPolicy> {
  const res = await api.post<APIResponse<ReadAuditPolicy>>('/admin/read-audit-policies/', body);
  return res.data.data ?? (res.data as unknown as ReadAuditPolicy);
}

export async function updateReadAuditPolicy(
  id: string,
  body: ReadAuditPolicyUpdateBody,
): Promise<ReadAuditPolicy> {
  const res = await api.put<APIResponse<ReadAuditPolicy>>(
    `/admin/read-audit-policies/${id}/`,
    body,
  );
  return res.data.data ?? (res.data as unknown as ReadAuditPolicy);
}

export async function deleteReadAuditPolicy(id: string): Promise<void> {
  await api.delete(`/admin/read-audit-policies/${id}/`);
}
