import {
  getExtensions,
  getExtensionsMounts,
  getExtensionsSampleManifest,
  postExtensions,
  postExtensionsByNameDataByDataSourceId,
  postExtensionsByNameDisable,
  postExtensionsByNameEnable,
  postExtensionsByNameToken,
  postExtensionsValidate,
} from "@/lib/api/generated/client";
import type {
  ExtensionCSP as ExtensionCSPWire,
  ExtensionManifest as ExtensionManifestWire,
  ExtensionMount as ExtensionMountWire,
  ExtensionRecord as ExtensionRecordWire,
  ExtensionValidation as ExtensionValidationWire,
} from "@/types/openapi.generated";

export interface ExtensionManifest {
  apiVersion: "extensions.astronomer.io/v1alpha1";
  name: string;
  displayName?: string;
  version: string;
  compatibleAstronomer: string;
  entry: string;
  permissions: string[];
  backendApiScopes?: string[];
  csp?: ExtensionCSPWire;
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

export type ExtensionPointKind =
  "sidebar" | "dashboardWidget" | "clusterTab" | "settingsPage";

export type DeclarativeKind = "table" | "chart" | "stat" | "form";
export type FieldFormat =
  "text" | "number" | "bytes" | "datetime" | "duration" | "badge" | "currency";
export type DataShape = "list" | "object" | "series";

export interface FieldBinding {
  path: string;
  label: string;
  format?: FieldFormat;
}

export interface ChartSpec {
  type: "line" | "bar" | "area";
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
  type: "text" | "number" | "select" | "toggle";
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
  csp?: ExtensionCSPWire;
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
  severity: "error" | "warning" | string;
  message: string;
}

export interface ExtensionValidationResponse {
  valid: boolean;
  compatibilityStatus: "compatible" | "incompatible" | "unknown" | string;
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
  compatibilityStatus: "compatible" | "incompatible" | "unknown" | string;
  manifest: ExtensionManifest;
  installedAt: string;
  updatedAt: string;
}

export interface ExtensionListResponse {
  items: ExtensionRecord[];
  sampleManifest: ExtensionManifest;
}

export interface ExtensionRequestOptions {
  signal?: AbortSignal;
}

function requireExtensionManifest(
  wire: ExtensionManifestWire | undefined,
): ExtensionManifest {
  if (
    !wire?.name ||
    !wire.version ||
    !wire.compatibleAstronomer ||
    !wire.entry ||
    !wire.extensionPoints
  ) {
    throw new Error("Extension manifest response is incomplete");
  }
  return {
    apiVersion: wire.apiVersion,
    name: wire.name,
    displayName: wire.displayName,
    version: wire.version,
    compatibleAstronomer: wire.compatibleAstronomer,
    entry: wire.entry,
    permissions: wire.permissions,
    backendApiScopes: wire.backendApiScopes,
    csp: wire.csp,
    extensionPoints:
      wire.extensionPoints as ExtensionManifest["extensionPoints"],
  };
}

function toExtensionManifestWire(
  manifest: ExtensionManifest,
): ExtensionManifestWire {
  if (manifest.apiVersion !== "extensions.astronomer.io/v1alpha1") {
    throw new Error(`Unsupported extension apiVersion: ${manifest.apiVersion}`);
  }
  return {
    ...manifest,
    apiVersion: manifest.apiVersion,
    extensionPoints: manifest.extensionPoints,
  };
}

function mapExtensionRecord(
  wire: ExtensionRecordWire | undefined,
): ExtensionRecord {
  if (
    !wire?.id ||
    !wire.name ||
    !wire.version ||
    !wire.checksum ||
    !wire.manifest ||
    !wire.installed_at ||
    !wire.updated_at
  ) {
    throw new Error("Extension record response is incomplete");
  }
  return {
    id: wire.id,
    name: wire.name,
    displayName: wire.display_name ?? wire.name,
    version: wire.version,
    source: wire.source ?? "",
    checksum: wire.checksum,
    enabled: wire.enabled ?? false,
    compatibilityStatus: wire.compatibility_status ?? "unknown",
    manifest: requireExtensionManifest(wire.manifest),
    installedAt: wire.installed_at,
    updatedAt: wire.updated_at,
  };
}

function mapFinding(value: Record<string, unknown>): ExtensionFinding {
  return {
    field: typeof value.field === "string" ? value.field : undefined,
    severity: typeof value.severity === "string" ? value.severity : "warning",
    message: typeof value.message === "string" ? value.message : "",
  };
}

function mapExtensionValidation(
  wire: ExtensionValidationWire | undefined,
): ExtensionValidationResponse {
  if (!wire?.manifest || typeof wire.valid !== "boolean") {
    throw new Error("Extension validation response is incomplete");
  }
  return {
    valid: wire.valid,
    compatibilityStatus: wire.compatibility_status ?? "unknown",
    checksum: wire.checksum ?? "",
    manifest: requireExtensionManifest(wire.manifest),
    warnings: (wire.warnings ?? []).map(mapFinding),
    errors: (wire.errors ?? []).map(mapFinding),
  };
}

function mapExtensionMount(wire: ExtensionMountWire): ExtensionMount {
  if (
    !wire.extension ||
    !wire.point ||
    !wire.pointId ||
    (wire.tier !== 1 && wire.tier !== 2)
  ) {
    throw new Error("Extension mount response is incomplete");
  }
  const render = (wire.render ?? {}) as ExtensionRender;
  return {
    extension: wire.extension,
    displayName: wire.displayName ?? wire.extension,
    point: wire.point,
    pointId: wire.pointId,
    tier: wire.tier,
    render,
    dataSources: (wire.dataSources ?? []).flatMap((source) =>
      source.id && source.shape
        ? [{ id: source.id, shape: source.shape as DataShape }]
        : [],
    ),
    label: wire.title,
    path: wire.point === "sidebar" ? wire.pointId : undefined,
  };
}

export async function listExtensions(
  options: ExtensionRequestOptions = {},
): Promise<ExtensionListResponse> {
  const response = await getExtensions({ signal: options.signal });
  return {
    items: (response.data?.items ?? []).map(mapExtensionRecord),
    sampleManifest: requireExtensionManifest(response.data?.sample_manifest),
  };
}

export async function getSampleExtensionManifest(
  options: ExtensionRequestOptions = {},
): Promise<ExtensionManifest> {
  const response = await getExtensionsSampleManifest({
    signal: options.signal,
  });
  return requireExtensionManifest(response.data);
}

export async function validateExtensionManifest(
  manifest: ExtensionManifest,
  options: ExtensionRequestOptions = {},
): Promise<ExtensionValidationResponse> {
  const response = await postExtensionsValidate({
    body: { manifest: toExtensionManifestWire(manifest) },
    signal: options.signal,
  });
  return mapExtensionValidation(response.data);
}

export async function installExtension(
  manifest: ExtensionManifest,
  opts?: { source?: string; enable?: boolean },
  options: ExtensionRequestOptions = {},
): Promise<ExtensionRecord> {
  const response = await postExtensions({
    body: {
      manifest: toExtensionManifestWire(manifest),
      source: opts?.source,
      enable: opts?.enable ?? false,
    },
    signal: options.signal,
  });
  return mapExtensionRecord(response.data);
}

export async function enableExtension(
  name: string,
  options: ExtensionRequestOptions = {},
): Promise<ExtensionRecord> {
  const response = await postExtensionsByNameEnable({
    path: { name },
    signal: options.signal,
  });
  return mapExtensionRecord(response.data);
}

export async function disableExtension(
  name: string,
  options: ExtensionRequestOptions = {},
): Promise<ExtensionRecord> {
  const response = await postExtensionsByNameDisable({
    path: { name },
    signal: options.signal,
  });
  return mapExtensionRecord(response.data);
}

// ============================================================
// §HostMounts runtime client
// ============================================================

// GET /extensions/mounts/ — viewer-readable, render-only projection of every
// enabled+compatible (and, for Tier 2, bundle_verified) extension mount. The
// server normalizes the four buckets; we backfill missing buckets to empty
// arrays so callers never have to null-check.
export async function getExtensionMounts(
  options: ExtensionRequestOptions = {},
): Promise<ExtensionMountsResponse> {
  const response = await getExtensionsMounts({ signal: options.signal });
  const data = response.data ?? {};
  return {
    sidebar: (data.sidebar ?? []).map(mapExtensionMount),
    dashboardWidgets: (data.dashboardWidgets ?? []).map(mapExtensionMount),
    clusterTabs: (data.clusterTabs ?? []).map(mapExtensionMount),
    settings: (data.settings ?? []).map(mapExtensionMount),
  };
}

// POST /extensions/{name}/data/{dataSourceId}/ — Tier-1 data proxy. The browser
// names a dataSource id, never a URL; the server re-derives the upstream and
// re-runs RBAC against the caller's own bindings on every call.
export async function fetchExtensionData<T = unknown>(
  name: string,
  dataSourceId: string,
  req: ExtensionDataRequest = {},
  options: ExtensionRequestOptions = {},
): Promise<ExtensionDataResponse<T>> {
  const response = await postExtensionsByNameDataByDataSourceId({
    path: { name, dataSourceId },
    body: {
      context: req.context
        ? {
            clusterId: req.context.clusterId ?? undefined,
            projectId: req.context.projectId ?? undefined,
            namespace: req.context.namespace ?? undefined,
          }
        : undefined,
      pathParams: req.pathParams,
      query: req.query,
      body: req.body as Record<string, unknown> | undefined,
    },
    signal: options.signal,
  });
  const wire = response.data;
  if (!wire?.shape) throw new Error("Extension data response is incomplete");
  const meta = wire.meta ?? {};
  return {
    data: wire.data as T,
    shape: wire.shape,
    meta: {
      dataSourceId:
        typeof meta.dataSourceId === "string"
          ? meta.dataSourceId
          : dataSourceId,
      rows: typeof meta.rows === "number" ? meta.rows : undefined,
      rbacScope:
        typeof meta.rbacScope === "string" ? meta.rbacScope : undefined,
      cached: typeof meta.cached === "boolean" ? meta.cached : undefined,
      ttlSeconds:
        typeof meta.ttlSeconds === "number" ? meta.ttlSeconds : undefined,
      truncated:
        typeof meta.truncated === "boolean" ? meta.truncated : undefined,
    },
  };
}

// POST /extensions/{name}/token/ — §BridgeProtocol ticket issuance backing
// ext/token.request. The host issues an opaque, single-use, <=60s ticket ONLY
// when the dataSource is in the handshake allowlist and CheckPermission passes
// for the current user. The Tier-2 iframe then sends it as X-Extension-Ticket.
export async function requestExtensionBridgeToken(
  name: string,
  dataSourceId: string,
  context?: ExtensionContext,
  options: ExtensionRequestOptions = {},
): Promise<ExtensionBridgeToken> {
  const response = await postExtensionsByNameToken({
    path: { name },
    body: {
      dataSource: dataSourceId,
      context: context
        ? {
            clusterId: context.clusterId ?? undefined,
            projectId: context.projectId ?? undefined,
            namespace: context.namespace ?? undefined,
          }
        : undefined,
    },
    signal: options.signal,
  });
  const wire = response.data;
  if (!wire?.token || !wire.dataSource || !wire.expiresAt || !wire.scope) {
    throw new Error("Extension bridge token response is incomplete");
  }
  return {
    token: wire.token,
    dataSource: wire.dataSource,
    expiresAt: wire.expiresAt,
    scope: wire.scope,
  };
}
