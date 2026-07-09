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

function unwrapData<T>(value: T | APIResponse<T> | null | undefined): T | undefined {
  if (value && typeof value === 'object' && 'data' in value) {
    return (value as APIResponse<T>).data;
  }
  return value ?? undefined;
}

interface ItemsEnvelope<T> {
  items?: T[];
  total?: number;
  limit?: number;
  offset?: number;
}

function toPaginatedResponse<T>(
  envelope: ItemsEnvelope<T> | PaginatedResponse<T> | T[] | undefined,
  params?: { page?: number; page_size?: number },
): PaginatedResponse<T> {
  if (Array.isArray(envelope)) {
    const pageSize = params?.page_size ?? envelope.length;
    return {
      data: envelope,
      total: envelope.length,
      count: envelope.length,
      next: null,
      previous: null,
      page: params?.page ?? 1,
      pageSize,
      totalPages: pageSize > 0 ? Math.max(1, Math.ceil(envelope.length / pageSize)) : 1,
    };
  }

  const data = Array.isArray((envelope as PaginatedResponse<T> | undefined)?.data)
    ? (envelope as PaginatedResponse<T>).data
    : ((envelope as ItemsEnvelope<T> | undefined)?.items ?? []);
  const total = (envelope as ItemsEnvelope<T> | undefined)?.total ?? data.length;
  const limit = (envelope as ItemsEnvelope<T> | undefined)?.limit
    ?? (envelope as PaginatedResponse<T> | undefined)?.pageSize
    ?? params?.page_size
    ?? data.length
    ?? 0;
  const offset = (envelope as ItemsEnvelope<T> | undefined)?.offset ?? 0;
  const page = (envelope as PaginatedResponse<T> | undefined)?.page
    ?? params?.page
    ?? (limit > 0 ? Math.floor(offset / limit) + 1 : 1);
  const totalPages = (envelope as PaginatedResponse<T> | undefined)?.totalPages
    ?? (limit > 0 ? Math.max(1, Math.ceil(total / limit)) : 1);

  return {
    data,
    total,
    count: (envelope as PaginatedResponse<T> | undefined)?.count ?? total,
    next: (envelope as PaginatedResponse<T> | undefined)?.next ?? null,
    previous: (envelope as PaginatedResponse<T> | undefined)?.previous ?? null,
    page,
    pageSize: limit,
    totalPages,
  };
}

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
  /** Browser/API JWT access-token absolute lifetime (session.timeout_minutes). */
  session: {
    timeoutMinutes: number;
  };
  telemetry: {
    enabled: boolean;
    endpoint: string;
  };
  // Registration TLS posture — controls which `curl …` variant the
  // cluster-registration wizard renders. Three modes mirror Rancher:
  // public_ca (curl -sfL), private_ca (--cacert), insecure (--insecure).
  registration: {
    tlsMode: 'public_ca' | 'private_ca' | 'insecure';
    caBundle: string;
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
  scope: 'global' | 'project' | 'cluster' | 'user';
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

interface QuotaUsageWireOffender {
  projectId?: string;
  project_id?: string;
  projectName?: string;
  project_name?: string;
  userId?: string;
  user_id?: string;
  username?: string;
  quotaPlan?: string;
  quota_plan?: string;
  limit?: string;
  current?: number;
  maximum?: number;
  usagePct?: number;
  usage_pct?: number;
}

interface QuotaUsageWire {
  rows?: QuotaUsageRow[];
  fleetTotals?: Record<string, number>;
  fleet_totals?: Record<string, number>;
  topOffenders?: QuotaUsageRow[];
  top_offenders?: QuotaUsageRow[];
  global?: {
    totalClusters?: number;
    total_clusters?: number;
    maxTotalClusters?: number;
    max_total_clusters?: number;
    totalUsers?: number;
    total_users?: number;
    maxTotalUsers?: number;
    max_total_users?: number;
  };
  projectOffenders?: QuotaUsageWireOffender[];
  project_offenders?: QuotaUsageWireOffender[];
  userOffenders?: QuotaUsageWireOffender[];
  user_offenders?: QuotaUsageWireOffender[];
}

function quotaOffenderToRow(
  kind: 'project' | 'user',
  offender: QuotaUsageWireOffender,
): QuotaUsageRow {
  const limit = offender.limit || 'unknown';
  return {
    planName: offender.quotaPlan ?? offender.quota_plan ?? 'default',
    scope: kind,
    scopeId: kind === 'project'
      ? offender.projectId ?? offender.project_id
      : offender.userId ?? offender.user_id,
    scopeName: kind === 'project'
      ? offender.projectName ?? offender.project_name
      : offender.username,
    usage: { [limit]: offender.current ?? 0 },
    utilization: { [limit]: offender.usagePct ?? offender.usage_pct ?? 0 },
  };
}

function normalizeQuotaUsage(wire: QuotaUsageWire | undefined): QuotaUsageSummary {
  if (!wire) {
    return { rows: [], fleetTotals: {}, topOffenders: [] };
  }
  const projectRows = (wire.projectOffenders ?? wire.project_offenders ?? []).map((row) =>
    quotaOffenderToRow('project', row),
  );
  const userRows = (wire.userOffenders ?? wire.user_offenders ?? []).map((row) =>
    quotaOffenderToRow('user', row),
  );
  const topOffenders = wire.topOffenders ?? wire.top_offenders ?? [...projectRows, ...userRows];
  const rows = wire.rows ?? topOffenders;
  const global = wire.global ?? {};
  const fleetTotals = wire.fleetTotals ?? wire.fleet_totals ?? {
    max_clusters: global.totalClusters ?? global.total_clusters ?? 0,
    max_users: global.totalUsers ?? global.total_users ?? 0,
  };
  return { rows, fleetTotals, topOffenders };
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
  /** Connector UUID; empty/omitted = wildcard (any connector). */
  connector_id?: string;
  group_name: string;
  scope: GroupScope;
  /** Role UUID. */
  role_id: string;
  /** Cluster UUID, required when scope=cluster. */
  cluster_id?: string;
  /** Project UUID, required when scope=project. */
  project_id?: string;
}

// ============================================================
// Types — Compliance
// ============================================================

export interface ComplianceExportSummary {
  id: string;
  from: string;
  to: string;
  /** Forward-compatible status for future durable background exports. */
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
  const pageSize = params?.page_size ?? 25;
  const page = params?.page ?? 1;
  const res = await api.get<PaginatedResponse<SentEmail> | APIResponse<ItemsEnvelope<SentEmail>>>('/admin/emails', {
    params: {
      limit: pageSize,
      offset: Math.max(0, page - 1) * pageSize,
      status: params?.status,
    },
  });
  return toPaginatedResponse(unwrapData(res.data), { page, page_size: pageSize });
}

// ============================================================
// Webhooks — API funcs
// ============================================================

export async function listWebhooks(): Promise<WebhookSubscription[]> {
  const res = await api.get<APIResponse<WebhookSubscription[] | ItemsEnvelope<WebhookSubscription>>>('/admin/webhooks');
  const data = unwrapData(res.data);
  if (Array.isArray(data)) return data;
  return data?.items ?? [];
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
  const res = await api.get<APIResponse<QuotaUsageWire>>('/admin/quota-usage');
  return normalizeQuotaUsage(unwrapData(res.data));
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
 * The current backend streams the ZIP body directly with a 200 status.
 * The 202 branch remains for forward compatibility once durable background
 * export jobs exist.
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

export async function downloadComplianceExportBlob(downloadUrl: string): Promise<Blob> {
  const res = await fetch(downloadUrl);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.blob();
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

// ============================================================
// Compliance Baselines — sprint 17 (migration 064)
// ============================================================

/**
 * The four preset compliance profiles. Slugs are stable; new presets
 * append rows in future migrations without renaming the old ones.
 */
export type ComplianceBaselineSlug =
  | 'pci_dss_4_0'
  | 'hipaa'
  | 'fedramp_moderate'
  | 'soc2';

export interface ComplianceQuotaPlanSpec {
  name: string;
  enforcement: string;
  description?: string;
  max_clusters_per_project?: number;
  max_namespaces_per_project?: number;
  max_members_per_project?: number;
  max_projects_per_user?: number;
  max_tokens_per_user?: number;
  max_streams_per_user?: number;
  max_total_clusters?: number;
  max_total_users?: number;
}

export interface ComplianceMaintenanceWindowSpec {
  name: string;
  description?: string;
  days_of_week?: number[];
  start_hour: number;
  start_minute: number;
  duration_min: number;
}

export interface ComplianceAlertRuleSpec {
  name: string;
  rule_type: string;
  severity: string;
  cooldown_minutes?: number;
  configuration: Record<string, unknown>;
}

export interface ComplianceBaselineSpec {
  audit_retention_days: number;
  pss_profile?: string;
  totp_required?: boolean;
  required_smtp?: boolean;
  required_webhooks?: string[];
  quota_plans?: ComplianceQuotaPlanSpec[];
  maintenance_window_template?: ComplianceMaintenanceWindowSpec;
  alert_rules?: ComplianceAlertRuleSpec[];
  platform_settings?: Record<string, string>;
  read_audit_policies?: string[];
}

export interface ComplianceBaseline {
  id: string;
  slug: ComplianceBaselineSlug;
  name: string;
  description: string;
  version: string;
  enabled: boolean;
  active: boolean;
  spec: ComplianceBaselineSpec;
}

export interface ComplianceBaselineDiff {
  baseline_id: string;
  baseline_slug: ComplianceBaselineSlug;
  baseline_name: string;
  current: Record<string, unknown>;
  target: Record<string, unknown>;
  changes: string[];
}

export interface ComplianceBaselineApplication {
  id: string;
  baseline_id: string;
  baseline_slug: ComplianceBaselineSlug;
  baseline_name: string;
  applied_by?: string;
  applied_at: string;
  status: 'applied' | 'reverted';
  reverted_at?: string;
  reverted_by?: string;
  notes: string;
  previous_state?: unknown;
}

/** GET /admin/compliance-baselines/ */
export async function listComplianceBaselines(): Promise<ComplianceBaseline[]> {
  const res = await api.get<APIResponse<ComplianceBaseline[]>>('/admin/compliance-baselines');
  return res.data.data ?? (res.data as unknown as ComplianceBaseline[]);
}

/** GET /admin/compliance-baselines/{id}/ */
export async function getComplianceBaseline(id: string): Promise<ComplianceBaseline> {
  const res = await api.get<APIResponse<ComplianceBaseline>>(`/admin/compliance-baselines/${id}`);
  return res.data.data ?? (res.data as unknown as ComplianceBaseline);
}

/** GET /admin/compliance-baselines/{id}/diff/ */
export async function getComplianceBaselineDiff(id: string): Promise<ComplianceBaselineDiff> {
  const res = await api.get<APIResponse<ComplianceBaselineDiff>>(
    `/admin/compliance-baselines/${id}/diff`,
  );
  return res.data.data ?? (res.data as unknown as ComplianceBaselineDiff);
}

/** POST /admin/compliance-baselines/{id}/apply/ */
export async function applyComplianceBaseline(
  id: string,
  notes?: string,
): Promise<{ application_id: string; baseline_id: string; slug: string }> {
  const res = await api.post<APIResponse<{ application_id: string; baseline_id: string; slug: string }>>(
    `/admin/compliance-baselines/${id}/apply`,
    { notes: notes || '' },
  );
  return res.data.data ?? (res.data as unknown as { application_id: string; baseline_id: string; slug: string });
}

/** GET /admin/compliance-baselines/active/ */
export async function getActiveComplianceBaseline(): Promise<{
  active: ComplianceBaselineApplication | null;
}> {
  const res = await api.get<APIResponse<{ active: ComplianceBaselineApplication | null }>>(
    '/admin/compliance-baselines/active',
  );
  return res.data.data ?? (res.data as unknown as { active: ComplianceBaselineApplication | null });
}

/** GET /admin/compliance-baseline-applications/ */
export async function listComplianceBaselineApplications(): Promise<ComplianceBaselineApplication[]> {
  const res = await api.get<APIResponse<ComplianceBaselineApplication[]>>(
    '/admin/compliance-baseline-applications',
  );
  return res.data.data ?? (res.data as unknown as ComplianceBaselineApplication[]);
}

/** POST /admin/compliance-baseline-applications/{id}/revert/ */
export async function revertComplianceBaselineApplication(
  id: string,
): Promise<{ application_id: string; status: string }> {
  const res = await api.post<APIResponse<{ application_id: string; status: string }>>(
    `/admin/compliance-baseline-applications/${id}/revert`,
  );
  return res.data.data ?? (res.data as unknown as { application_id: string; status: string });
}

// ────────────────────────────────────────────────────────────────────────
// Network policy templates (migration 068)
// ────────────────────────────────────────────────────────────────────────

export interface NetworkPolicyTemplate {
  id: string;
  slug: string;
  name: string;
  description: string;
  kind: 'builtin' | 'custom';
  spec_template: string;
  enabled: boolean;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface NetworkPolicyApplication {
  id: string;
  template_id: string;
  template_slug?: string;
  cluster_id: string;
  namespace: string;
  policy_name: string;
  status: 'pending' | 'applied' | 'failed' | 'drifting';
  last_applied_at?: string;
  last_error?: string;
  applied_by?: string;
  created_at: string;
  updated_at: string;
}

export interface NetworkPolicyTemplateWriteRequest {
  slug?: string;
  name: string;
  description?: string;
  spec_template: string;
  enabled?: boolean;
  clone_from?: string;
}

export interface ApplyNetworkPolicyRequest {
  template_id: string;
  namespace?: string;
  namespaces?: string[];
}

/** GET /admin/network-policy-templates/ */
export async function listNetworkPolicyTemplates(): Promise<NetworkPolicyTemplate[]> {
  const res = await api.get<PaginatedResponse<NetworkPolicyTemplate>>('/admin/network-policy-templates');
  return res.data.data ?? (res.data as unknown as NetworkPolicyTemplate[]);
}

/** GET /admin/network-policy-templates/{id}/ */
export async function getNetworkPolicyTemplate(id: string): Promise<NetworkPolicyTemplate> {
  const res = await api.get<APIResponse<NetworkPolicyTemplate>>(`/admin/network-policy-templates/${id}`);
  return res.data.data ?? (res.data as unknown as NetworkPolicyTemplate);
}

/** POST /admin/network-policy-templates/ */
export async function createNetworkPolicyTemplate(
  body: NetworkPolicyTemplateWriteRequest,
): Promise<NetworkPolicyTemplate> {
  const res = await api.post<APIResponse<NetworkPolicyTemplate>>('/admin/network-policy-templates', body);
  return res.data.data ?? (res.data as unknown as NetworkPolicyTemplate);
}

/** PUT /admin/network-policy-templates/{id}/ */
export async function updateNetworkPolicyTemplate(
  id: string,
  body: NetworkPolicyTemplateWriteRequest,
): Promise<NetworkPolicyTemplate> {
  const res = await api.put<APIResponse<NetworkPolicyTemplate>>(`/admin/network-policy-templates/${id}`, body);
  return res.data.data ?? (res.data as unknown as NetworkPolicyTemplate);
}

/** DELETE /admin/network-policy-templates/{id}/ */
export async function deleteNetworkPolicyTemplate(id: string): Promise<void> {
  await api.delete(`/admin/network-policy-templates/${id}`);
}

/** GET /clusters/{cluster_id}/network-policies/applications/ */
export async function listNetworkPolicyApplications(
  clusterID: string,
): Promise<NetworkPolicyApplication[]> {
  const res = await api.get<APIResponse<NetworkPolicyApplication[]>>(
    `/clusters/${clusterID}/network-policies/applications`,
  );
  return res.data.data ?? (res.data as unknown as NetworkPolicyApplication[]);
}

/** POST /clusters/{cluster_id}/network-policies/applications/ */
export async function applyNetworkPolicy(
  clusterID: string,
  body: ApplyNetworkPolicyRequest,
): Promise<NetworkPolicyApplication[]> {
  const res = await api.post<APIResponse<NetworkPolicyApplication[]>>(
    `/clusters/${clusterID}/network-policies/applications`,
    body,
  );
  return res.data.data ?? (res.data as unknown as NetworkPolicyApplication[]);
}

/** DELETE /clusters/{cluster_id}/network-policies/applications/{id}/ */
export async function deleteNetworkPolicyApplication(
  clusterID: string,
  applicationID: string,
): Promise<void> {
  await api.delete(`/clusters/${clusterID}/network-policies/applications/${applicationID}`);
}

/** POST /clusters/{cluster_id}/network-policies/applications/{id}/reapply/ */
export async function reapplyNetworkPolicyApplication(
  clusterID: string,
  applicationID: string,
): Promise<NetworkPolicyApplication> {
  const res = await api.post<APIResponse<NetworkPolicyApplication>>(
    `/clusters/${clusterID}/network-policies/applications/${applicationID}/reapply`,
  );
  return res.data.data ?? (res.data as unknown as NetworkPolicyApplication);
}
