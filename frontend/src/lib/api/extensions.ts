import api from '@/lib/api';
import type { APIResponse } from '@/types';

export interface ExtensionCSP {
  scriptSrc?: string[];
  connectSrc?: string[];
  frameSrc?: string[];
  imageSrc?: string[];
}

export interface ExtensionManifest {
  apiVersion: string;
  name: string;
  displayName?: string;
  version: string;
  compatibleAstronomer: string;
  entry: string;
  permissions: string[];
  backendApiScopes?: string[];
  csp?: ExtensionCSP;
  extensionPoints: {
    sidebar?: Array<{ label: string; path: string }>;
    widgets?: Array<{ id: string; title: string }>;
    clusterTabs?: Array<{ label: string; component: string }>;
    settings?: Array<{ label: string; component: string }>;
  };
}

// ============================================================
// §Schema — runtime render additions (Tier 1 declarative / Tier 2 bundle)
// ============================================================
// These mirror the Go structs in internal/handler/extensions.go. They are the
// browser-visible projection: the /mounts/ endpoint never leaks upstream paths,
// so DataSourceRef here exposes only id + shape (NOT proxy/path/rbac/query).

export type ExtensionPointKind = 'sidebar' | 'dashboardWidget' | 'clusterTab' | 'settingsPage';

export type DeclarativeKind = 'table' | 'chart' | 'stat' | 'form';
export type FieldFormat =
  | 'text'
  | 'number'
  | 'bytes'
  | 'datetime'
  | 'duration'
  | 'badge'
  | 'currency';
export type DataShape = 'list' | 'object' | 'series';

export interface FieldBinding {
  path: string;
  label: string;
  format?: FieldFormat;
}

export interface ChartSpec {
  type: 'line' | 'bar' | 'area';
  x: string;
  y: string[];
}

export interface StatSpec {
  value: FieldBinding;
  delta?: FieldBinding;
  label: string;
}

export interface FormInput {
  name: string;
  label: string;
  type: 'text' | 'number' | 'select' | 'toggle';
  options?: string[];
  maxLength?: number;
  required: boolean;
}

export interface FormSpec {
  submit: string;
  inputs: FormInput[];
  submitLabel: string;
}

export interface DeclarativeWidget {
  kind: DeclarativeKind;
  dataSource: string;
  fields?: FieldBinding[];
  chart?: ChartSpec;
  form?: FormSpec;
  stat?: StatSpec;
  emptyText?: string;
}

export interface BundleDescriptor {
  url: string;
  sha256: string;
  integrity: string;
  // signature is intentionally NOT exposed to the browser — it is a server-side
  // install-time concern. The /mounts/ projection only ships render-time fields.
  entry: string;
  sandboxOrigin: string;
  component: string;
  csp?: ExtensionCSP;
  // Browser sees ds ids + shapes only (the handshake allowlist), never paths.
  dataSources?: ExtensionDataSourceMeta[];
}

export interface ExtensionRender {
  declarative?: DeclarativeWidget;
  bundle?: BundleDescriptor;
}

// Render-only metadata about a data source: id + shape, no upstream path/rbac.
export interface ExtensionDataSourceMeta {
  id: string;
  shape: DataShape;
}

// One enabled mount as projected by GET /extensions/mounts/.
export interface ExtensionMount {
  extension: string;
  displayName: string;
  point: ExtensionPointKind;
  pointId: string;
  // 1 = declarative (Tier 1), 2 = signed-bundle iframe (Tier 2).
  tier: 1 | 2;
  render: ExtensionRender;
  dataSources?: ExtensionDataSourceMeta[];
  // Sidebar/settings entries carry their display label + (sidebar) host route.
  label?: string;
  path?: string;
}

export interface ExtensionMountsResponse {
  sidebar: ExtensionMount[];
  dashboardWidgets: ExtensionMount[];
  clusterTabs: ExtensionMount[];
  settings: ExtensionMount[];
}

// Route context supplied by the host page to the data proxy / bridge.
export interface ExtensionContext {
  clusterId?: string | null;
  projectId?: string | null;
  namespace?: string | null;
}

export interface ExtensionDataRequest {
  context?: ExtensionContext;
  pathParams?: Record<string, string>;
  query?: Record<string, string>;
  body?: unknown;
}

export interface ExtensionDataResponse<T = unknown> {
  data: T;
  shape: DataShape;
  meta: {
    dataSourceId: string;
    rows?: number;
    rbacScope?: string;
    cached?: boolean;
    ttlSeconds?: number;
    truncated?: boolean;
  };
}

// §BridgeProtocol — opaque, single-use, <=60s scoped ticket the Tier-2 iframe
// sends as X-Extension-Ticket. The SDK never receives the session JWT.
export interface ExtensionBridgeToken {
  token: string;
  dataSource: string;
  expiresAt: string;
  scope: string;
}

export interface ExtensionFinding {
  field?: string;
  severity: 'error' | 'warning' | string;
  message: string;
}

export interface ExtensionValidationResponse {
  valid: boolean;
  compatibilityStatus: 'compatible' | 'incompatible' | 'unknown' | string;
  checksum: string;
  manifest: ExtensionManifest;
  warnings: ExtensionFinding[];
  errors: ExtensionFinding[];
}

export interface ExtensionRecord {
  id: string;
  name: string;
  displayName: string;
  version: string;
  source: string;
  checksum: string;
  enabled: boolean;
  compatibilityStatus: 'compatible' | 'incompatible' | 'unknown' | string;
  manifest: ExtensionManifest;
  installedAt: string;
  updatedAt: string;
}

export interface ExtensionListResponse {
  items: ExtensionRecord[];
  sampleManifest: ExtensionManifest;
}

export async function listExtensions(): Promise<ExtensionListResponse> {
  const res = await api.get<APIResponse<ExtensionListResponse>>('/extensions/');
  return res.data.data;
}

export async function getSampleExtensionManifest(): Promise<ExtensionManifest> {
  const res = await api.get<APIResponse<ExtensionManifest>>('/extensions/sample-manifest/');
  return res.data.data;
}

export async function validateExtensionManifest(
  manifest: ExtensionManifest,
): Promise<ExtensionValidationResponse> {
  const res = await api.post<APIResponse<ExtensionValidationResponse>>('/extensions/validate/', {
    manifest,
  });
  return res.data.data;
}

export async function installExtension(
  manifest: ExtensionManifest,
  opts?: { source?: string; enable?: boolean },
): Promise<ExtensionRecord> {
  const res = await api.post<APIResponse<ExtensionRecord>>('/extensions/', {
    manifest,
    source: opts?.source,
    enable: opts?.enable ?? false,
  });
  return res.data.data;
}

export async function enableExtension(name: string): Promise<ExtensionRecord> {
  const res = await api.post<APIResponse<ExtensionRecord>>(
    `/extensions/${encodeURIComponent(name)}/enable/`,
  );
  return res.data.data;
}

export async function disableExtension(name: string): Promise<ExtensionRecord> {
  const res = await api.post<APIResponse<ExtensionRecord>>(
    `/extensions/${encodeURIComponent(name)}/disable/`,
  );
  return res.data.data;
}

// ============================================================
// §HostMounts runtime client
// ============================================================

// GET /extensions/mounts/ — viewer-readable, render-only projection of every
// enabled+compatible (and, for Tier 2, bundle_verified) extension mount. The
// server normalizes the four buckets; we backfill missing buckets to empty
// arrays so callers never have to null-check.
export async function getExtensionMounts(): Promise<ExtensionMountsResponse> {
  const res = await api.get<APIResponse<Partial<ExtensionMountsResponse>>>('/extensions/mounts/');
  const data = res.data.data ?? {};
  return {
    sidebar: data.sidebar ?? [],
    dashboardWidgets: data.dashboardWidgets ?? [],
    clusterTabs: data.clusterTabs ?? [],
    settings: data.settings ?? [],
  };
}

// POST /extensions/{name}/data/{dataSourceId}/ — Tier-1 data proxy. The browser
// names a dataSource id, never a URL; the server re-derives the upstream and
// re-runs RBAC against the caller's own bindings on every call.
export async function fetchExtensionData<T = unknown>(
  name: string,
  dataSourceId: string,
  req: ExtensionDataRequest = {},
): Promise<ExtensionDataResponse<T>> {
  const res = await api.post<APIResponse<ExtensionDataResponse<T>>>(
    `/extensions/${encodeURIComponent(name)}/data/${encodeURIComponent(dataSourceId)}/`,
    req,
  );
  return res.data.data;
}

// POST /extensions/{name}/token/ — §BridgeProtocol ticket issuance backing
// ext/token.request. The host issues an opaque, single-use, <=60s ticket ONLY
// when the dataSource is in the handshake allowlist and CheckPermission passes
// for the current user. The Tier-2 iframe then sends it as X-Extension-Ticket.
export async function requestExtensionBridgeToken(
  name: string,
  dataSourceId: string,
  context?: ExtensionContext,
): Promise<ExtensionBridgeToken> {
  const res = await api.post<APIResponse<ExtensionBridgeToken>>(
    `/extensions/${encodeURIComponent(name)}/token/`,
    { dataSource: dataSourceId, context },
  );
  return res.data.data;
}
