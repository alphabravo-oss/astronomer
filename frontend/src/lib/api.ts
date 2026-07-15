import axios, { AxiosError, AxiosInstance, InternalAxiosRequestConfig } from 'axios';
import type { APIResponse, PaginatedResponse } from '@/types';
import type { OpenAPIComponents } from '@/types/openapi.generated';
import { clearLegacyTokenStorage } from '@/lib/auth/session';
import { camelizeKeys } from '@/lib/camelize';
import { API_BASE, wsBase } from '@/lib/env';

// TEST-03: product code imports OpenAPI-generated schemas (not test-only).
export type AgentFleetItemOpenAPI = OpenAPIComponents['schemas']['AgentFleetItem'];

const API_BASE_URL = API_BASE;
const CSRF_COOKIE = 'astronomer_csrf';
const CSRF_HEADER = 'X-CSRF-Token';

function readCookie(name: string): string | null {
  if (typeof document === 'undefined') return null;
  const prefix = `${name}=`;
  const found = document.cookie
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix));
  return found ? decodeURIComponent(found.slice(prefix.length)) : null;
}

function isUnsafeMethod(method?: string): boolean {
  const normalized = (method || 'get').toUpperCase();
  return !['GET', 'HEAD', 'OPTIONS', 'TRACE'].includes(normalized);
}

function csrfHeaders(): Record<string, string> {
  const token = readCookie(CSRF_COOKIE);
  return token ? { [CSRF_HEADER]: token } : {};
}

/**
 * Configured Axios instance for all API communication
 */
const api: AxiosInstance = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30000,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
});

/**
 * Request interceptor - ensure trailing slash
 */
api.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    clearLegacyTokenStorage();
    if (isUnsafeMethod(config.method) && config.headers) {
      const token = readCookie(CSRF_COOKIE);
      if (token) {
        config.headers.set(CSRF_HEADER, token);
      }
    }
    // Ensure trailing slash for route compatibility. Skip k8s proxy paths,
    // where the suffix is an upstream Kubernetes API path.
    if (config.url && !config.url.endsWith('/') && !config.url.includes('?') && !config.url.includes('/k8s/')) {
      config.url += '/';
    }
    return config;
  },
  (error) => Promise.reject(error)
);

/**
 * Response interceptor - handle token refresh on 401, then redirect if still unauthorized
 */
let isRefreshing = false;
let failedQueue: Array<{ resolve: () => void; reject: (err: unknown) => void }> = [];

function processQueue(error: unknown, ok: boolean) {
  failedQueue.forEach((p) => {
    if (ok) p.resolve();
    else p.reject(error);
  });
  failedQueue = [];
}

api.interceptors.response.use(
  (response) => {
    // Don't camelize k8s proxy responses. The cluster k8s proxy ('/k8s/')
    // returns raw Kubernetes objects whose data keys (e.g. ConfigMap/Secret
    // 'database_url') are significant and must round-trip verbatim. Rewriting
    // them to camelCase silently corrupts snake_case keys when the user edits
    // YAML and PUTs it back, and the dry-run diff hides the change.
    if (response.config?.url?.includes('/k8s/')) {
      return response;
    }
    // Don't camelize binary / non-JSON payloads.
    if (response.data && typeof response.data === 'object') {
      response.data = camelizeKeys(response.data);
    }
    return response;
  },
  async (error: AxiosError<{ message?: string; code?: string }>) => {
    const originalRequest = error.config as InternalAxiosRequestConfig & { _retry?: boolean };

    if (error.response?.status === 401 && !originalRequest._retry && typeof window !== 'undefined') {
      // Skip refresh for auth endpoints themselves
      if (originalRequest.url?.includes('/auth/login') || originalRequest.url?.includes('/auth/refresh')) {
        return Promise.reject(error);
      }

      if (isRefreshing) {
        // Queue this request until refresh completes
        return new Promise<void>((resolve, reject) => {
          failedQueue.push({ resolve, reject });
        }).then(() => {
          return api(originalRequest);
        });
      }

      originalRequest._retry = true;
      isRefreshing = true;

      try {
        const res = await axios.post(
          `${API_BASE_URL}/auth/refresh/`,
          {},
          { headers: { 'Content-Type': 'application/json', ...csrfHeaders() }, withCredentials: true }
        );
        const data = res.data?.data || res.data;
        if (!data?.token) {
          throw new Error('Refresh did not return an access token');
        }

        processQueue(null, true);

        return api(originalRequest);
      } catch (refreshError) {
        processQueue(refreshError, false);
        clearLegacyTokenStorage();
        if (!window.location.pathname.startsWith('/auth')) {
          window.location.href = '/auth/login';
        }
        return Promise.reject(refreshError);
      } finally {
        isRefreshing = false;
      }
    }

    const message = error.response?.data?.message || error.message || 'An unexpected error occurred';
    return Promise.reject(new Error(message));
  }
);

export default api;

// ============================================================
// API Client Functions
// ============================================================

// --- Auth ---

export async function loginWithCredentials(email: string, password: string) {
  const res = await api.post<APIResponse<{ token: string; refresh: string; user: import('@/types').User }>>('/auth/login', {
    email,
    password,
  });
  return res.data.data;
}

export async function changeOwnPassword(currentPassword: string, newPassword: string) {
  const res = await api.post<{ detail: string; must_change_password?: boolean }>('/auth/change-password', {
    current_password: currentPassword,
    new_password: newPassword,
  });
  return res.data;
}

export async function refreshAccessToken(refreshToken: string) {
  const res = await api.post<APIResponse<{ token: string; refresh?: string }>>('/auth/refresh', {
    refresh: refreshToken,
  });
  return res.data.data;
}

export async function loginWithSSO(provider: string, code: string) {
  const res = await api.post<APIResponse<{ token: string; user: import('@/types').User }>>('/auth/sso/callback', {
    provider,
    code,
  });
  return res.data.data;
}

export async function getCurrentUser() {
  const res = await api.get<APIResponse<import('@/types').User>>('/auth/me');
  return res.data.data;
}

export type StreamTicketType = 'events' | 'registration' | 'logs' | 'exec' | 'shell';

export async function createStreamTicket(streamType: StreamTicketType, clusterId?: string) {
  const res = await api.post<APIResponse<{ ticket: string; expiresAt: string }>>('/streams/tickets', {
    stream_type: streamType,
    cluster_id: clusterId,
  });
  return res.data.data;
}

export type FeatureFlagKey =
  | 'feature.catalog'
  | 'feature.projects'
  | 'feature.monitoring'
  | 'feature.argocd'
  | 'feature.security'
  | 'feature.backups';

export type FeatureFlags = Partial<Record<FeatureFlagKey, boolean>>;

export async function getFeatureFlags(): Promise<FeatureFlags> {
  const res = await api.get<APIResponse<FeatureFlags>>('/settings/features');
  return res.data.data ?? {};
}

// --- Clusters ---

export async function getClusters(params?: {
  status?: string;
  provider?: string;
  environment?: string;
  search?: string;
  page?: number;
  pageSize?: number;
}) {
  const res = await api.get<PaginatedResponse<import('@/types').Cluster>>('/clusters', { params });
  return res.data;
}

export async function getAgentFleet(params?: { limit?: number; offset?: number }) {
  const res = await api.get<APIResponse<import('@/types').AgentFleetResponse>>('/agents/fleet', { params });
  return res.data.data;
}

export async function getAgentDiagnostics(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').AgentDiagnosticsResponse>>(
    `/agents/fleet/${clusterId}/diagnostics`,
  );
  return res.data.data;
}

export async function runAgentSelfTest(clusterId: string) {
  const res = await api.post<APIResponse<import('@/types').AgentSelfTestResponse>>(
    `/agents/fleet/${clusterId}/self-test`,
    {},
  );
  return res.data.data;
}

export async function downloadAgentDiagnosticsBundle(clusterId: string): Promise<Blob> {
  const res = await api.get<Blob>(`/agents/fleet/${clusterId}/diagnostics/bundle`, {
    responseType: 'blob',
  });
  return res.data;
}

export async function createAgentUpgradePlan(
  clusterId: string,
  data: import('@/types').AgentUpgradePlanRequest = {},
) {
  const res = await api.post<APIResponse<import('@/types').AgentUpgradePlanResponse>>(
    `/agents/fleet/${clusterId}/upgrade-plan`,
    data,
  );
  return res.data.data;
}

export async function createAgentUpgradeOperation(
  clusterId: string,
  data: import('@/types').AgentUpgradePlanRequest = {},
) {
  const res = await api.post<APIResponse<import('@/types').AgentUpgradeOperationResponse>>(
    `/agents/fleet/${clusterId}/upgrade`,
    data,
  );
  return res.data.data;
}

export async function getAgentOperations(clusterId: string, params?: { limit?: number; offset?: number }) {
  const res = await api.get<APIResponse<import('@/types').AgentLifecycleOperationsResponse>>(
    `/agents/fleet/${clusterId}/operations`,
    { params },
  );
  return res.data.data;
}

export async function getCluster(id: string) {
  const res = await api.get<APIResponse<import('@/types').Cluster>>(`/clusters/${id}`);
  return res.data.data;
}

export async function createCluster(data: import('@/types').ClusterRegistration) {
  const res = await api.post<APIResponse<import('@/types').Cluster>>('/clusters', data);
  return res.data.data;
}

export async function registerCluster(clusterId: string) {
  const res = await api.post<{ install_manifest: string; token: { token: string }}>(`/clusters/${clusterId}/register`);
  return res.data;
}

// ─── Cluster-registration wizard (sprint 22 / migration 078) ──────────
// Mirror of the backend's registration.Status / Step shapes.

export interface RegistrationStep {
  id: string;
  step_name: string;
  label: string;
  status: 'pending' | 'running' | 'success' | 'failed' | 'skipped';
  progress_pct: number;
  detail?: Record<string, unknown>;
  started_at?: string | null;
  completed_at?: string | null;
  error_message?: string;
  step_order: number;
  created_at: string;
}

export type RegistrationPhase =
  | 'created'
  | 'awaiting_agent'
  | 'connected'
  | 'provisioning'
  | 'ready'
  | 'failed';

export interface RegistrationStatus {
  cluster_id: string;
  phase: RegistrationPhase;
  install_baseline?: boolean | null;
  started_at?: string | null;
  completed_at?: string | null;
  steps: RegistrationStep[];
}

export async function getRegistrationStatus(clusterId: string): Promise<RegistrationStatus> {
  const res = await api.get<APIResponse<RegistrationStatus>>(`/clusters/${clusterId}/registration/status`);
  return res.data.data;
}

export async function setRegistrationOptions(clusterId: string, installBaseline: boolean): Promise<RegistrationStatus> {
  const res = await api.put<APIResponse<RegistrationStatus>>(
    `/clusters/${clusterId}/registration/options`,
    { install_baseline: installBaseline },
  );
  return res.data.data;
}

export async function confirmRegistration(clusterId: string): Promise<RegistrationStatus> {
  const res = await api.post<APIResponse<RegistrationStatus>>(`/clusters/${clusterId}/registration/confirm`);
  return res.data.data;
}

export async function retryRegistrationStep(clusterId: string, stepId: string): Promise<RegistrationStatus> {
  const res = await api.post<APIResponse<RegistrationStatus>>(
    `/clusters/${clusterId}/registration/retry/${stepId}`,
  );
  return res.data.data;
}

export async function cancelRegistration(clusterId: string): Promise<RegistrationStatus> {
  const res = await api.post<APIResponse<RegistrationStatus>>(`/clusters/${clusterId}/registration/cancel`);
  return res.data.data;
}

// Returns the agent install manifest as raw YAML text.
export async function getClusterManifest(clusterId: string): Promise<string> {
  const res = await api.get<string>(`/clusters/${clusterId}/manifest`, {
    responseType: 'text',
    transformResponse: (data: string) => data,
  });
  return typeof res.data === 'string' ? res.data : String(res.data);
}

// Like getClusterManifest, but also returns the registration token that
// was freshly minted to render it. The token comes back via response
// header (X-Astronomer-Registration-Token) so the wizard can build a
// Rancher-style `curl … | kubectl apply -f -` one-liner that points at
// the public /api/v1/register/<token> endpoint.
export async function getClusterManifestWithToken(clusterId: string): Promise<{ manifest: string; token: string }> {
  const res = await api.get<string>(`/clusters/${clusterId}/manifest`, {
    responseType: 'text',
    transformResponse: (data: string) => data,
  });
  const manifest = typeof res.data === 'string' ? res.data : String(res.data);
  // Axios normalises header names to lowercase.
  const token = String(res.headers?.['x-astronomer-registration-token'] ?? '');
  return { manifest, token };
}

export type RegistrationTLSMode = 'public_ca' | 'private_ca' | 'insecure';

// Pre-auth read of the operator-configured registration TLS posture.
// Drives which `curl …` variant the wizard renders by default.
export async function getRegistrationTLS(): Promise<{ mode: RegistrationTLSMode; caBundle: string }> {
  const res = await api.get<Record<string, unknown>>(`/settings/registration`);
  const body = res.data ?? {};
  const mode = String(body['registration.tls_mode'] ?? 'public_ca') as RegistrationTLSMode;
  const caBundle = String(body['registration.ca_bundle'] ?? '');
  return { mode, caBundle };
}

export async function updateCluster(id: string, data: Partial<import('@/types').ClusterRegistration>) {
  const res = await api.patch<APIResponse<import('@/types').Cluster>>(`/clusters/${id}`, data);
  return res.data.data;
}

export interface OwnershipTransferResult {
  id: string;
  managedBy: 'api' | 'ui' | 'crd' | 'system' | 'argocd';
  transferred: boolean;
}

export async function takeoverClusterOwnership(id: string) {
  const res = await api.post<APIResponse<OwnershipTransferResult>>(`/clusters/${id}/ownership/takeover`);
  return res.data.data;
}

export async function deleteCluster(id: string, opts?: { force?: boolean }) {
  await api.delete(`/clusters/${id}`, opts?.force ? { params: { force: true } } : undefined);
}

export async function getClusterConditions(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').ClusterCondition[]>>(`/clusters/${clusterId}/conditions`);
  return res.data.data;
}

// Sprint 086 — recent attempts by the cluster-condition remediation
// reconciler. Newest-first, capped at 50 server-side.
export async function getClusterConditionRemediation(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').ClusterConditionRemediationAttempt[]>>(
    `/clusters/${clusterId}/condition-remediation`,
  );
  return res.data.data;
}

export async function getClusterNodes(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').ClusterNode[]>>(`/clusters/${clusterId}/nodes`);
  return res.data.data;
}

export async function getNodeDetail(clusterId: string, nodeName: string) {
  const res = await api.get<APIResponse<import('@/types').NodeDetail>>(`/clusters/${clusterId}/nodes/${nodeName}`);
  return res.data.data;
}

export interface DrainNodeRequest {
  ignore_daemonsets?: boolean;
  delete_empty_dir_data?: boolean;
  grace_period_seconds?: number;
  dry_run?: boolean;
}

export interface DrainNodePodRef {
  namespace: string;
  name: string;
  reason?: string;
}

export interface DrainNodeResponse {
  node: string;
  status: 'dry_run' | 'draining' | 'drained' | 'blocked' | 'partial' | string;
  message: string;
  evicted: DrainNodePodRef[];
  skipped: DrainNodePodRef[];
  failed: DrainNodePodRef[];
  blockers?: string[];
}

export async function cordonNode(clusterId: string, nodeName: string) {
  const res = await api.post<APIResponse<{ node: string; status: string }>>(
    `/nodes/${encodeURIComponent(clusterId)}/${encodeURIComponent(nodeName)}/cordon`,
  );
  return res.data.data;
}

export async function uncordonNode(clusterId: string, nodeName: string) {
  const res = await api.post<APIResponse<{ node: string; status: string }>>(
    `/nodes/${encodeURIComponent(clusterId)}/${encodeURIComponent(nodeName)}/uncordon`,
  );
  return res.data.data;
}

export async function drainNode(clusterId: string, nodeName: string, data: DrainNodeRequest = {}) {
  const res = await api.post<APIResponse<DrainNodeResponse>>(`/nodes/${encodeURIComponent(clusterId)}/${encodeURIComponent(nodeName)}/drain`, data);
  return res.data.data;
}

export interface NodeKeyValueRequest {
  key: string;
  value: string;
}

export interface NodeKeyRequest {
  key: string;
}

export interface NodeTaintRequest {
  key: string;
  value?: string;
  effect: 'NoSchedule' | 'PreferNoSchedule' | 'NoExecute' | string;
}

export interface NodeTaintRemoveRequest {
  key: string;
  effect?: 'NoSchedule' | 'PreferNoSchedule' | 'NoExecute' | string;
}

function nodeActionPath(clusterId: string, nodeName: string, action: string) {
  return `/nodes/${encodeURIComponent(clusterId)}/${encodeURIComponent(nodeName)}/${action}`;
}

export async function setNodeLabel(clusterId: string, nodeName: string, data: NodeKeyValueRequest) {
  const res = await api.post<APIResponse<{ node: string; status: string; key: string; value: string }>>(
    nodeActionPath(clusterId, nodeName, 'labels'),
    data,
  );
  return res.data.data;
}

export async function removeNodeLabel(clusterId: string, nodeName: string, data: NodeKeyRequest) {
  const res = await api.post<APIResponse<{ node: string; status: string; key: string }>>(
    nodeActionPath(clusterId, nodeName, 'labels/remove'),
    data,
  );
  return res.data.data;
}

export async function setNodeAnnotation(clusterId: string, nodeName: string, data: NodeKeyValueRequest) {
  const res = await api.post<APIResponse<{ node: string; status: string; key: string; value: string }>>(
    nodeActionPath(clusterId, nodeName, 'annotations'),
    data,
  );
  return res.data.data;
}

export async function removeNodeAnnotation(clusterId: string, nodeName: string, data: NodeKeyRequest) {
  const res = await api.post<APIResponse<{ node: string; status: string; key: string }>>(
    nodeActionPath(clusterId, nodeName, 'annotations/remove'),
    data,
  );
  return res.data.data;
}

export async function addNodeTaint(clusterId: string, nodeName: string, data: NodeTaintRequest) {
  const res = await api.post<APIResponse<{ node: string; status: string; taint: NodeTaintRequest }>>(
    nodeActionPath(clusterId, nodeName, 'taints'),
    data,
  );
  return res.data.data;
}

export async function removeNodeTaint(clusterId: string, nodeName: string, data: NodeTaintRemoveRequest) {
  const res = await api.post<APIResponse<{ node: string; status: string; removed: boolean }>>(
    nodeActionPath(clusterId, nodeName, 'taints/remove'),
    data,
  );
  return res.data.data;
}

export async function getClusterNamespaces(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').Namespace[]>>(`/clusters/${clusterId}/namespaces`);
  return res.data.data;
}

export async function getClusterEvents(clusterId: string, params?: { type?: string; limit?: number }) {
  const res = await api.get<APIResponse<import('@/types').ClusterEvent[]>>(`/clusters/${clusterId}/events`, {
    params,
  });
  return res.data.data;
}

// --- Pods ---

export async function getClusterPods(clusterId: string, params?: { namespace?: string }) {
  const res = await api.get<APIResponse<import('@/types').Pod[]>>(`/clusters/${clusterId}/pods`, { params });
  return res.data.data;
}

export async function deletePod(clusterId: string, namespace: string, pod: string) {
  await api.delete(`/workloads/pods/${clusterId}/${namespace}/${pod}`);
}

// --- Workloads ---

export async function getWorkloads(
  clusterId: string,
  params?: { namespace?: string; kind?: string; search?: string; page?: number; pageSize?: number }
) {
  const res = await api.get<PaginatedResponse<import('@/types').Workload>>(`/clusters/${clusterId}/workloads`, {
    params,
  });
  return res.data;
}

export async function getWorkload(clusterId: string, kind: string, namespace: string, name: string) {
  const res = await api.get<APIResponse<import('@/types').Workload>>(
    `/clusters/${clusterId}/workloads/${kind}/${namespace}/${name}`
  );
  return res.data.data;
}

export async function scaleWorkload(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  replicas: number
) {
  const res = await api.patch<APIResponse<import('@/types').Workload>>(
    `/clusters/${clusterId}/workloads/${kind}/${namespace}/${name}/scale`,
    { replicas }
  );
  return res.data.data;
}

export async function restartWorkload(clusterId: string, kind: string, namespace: string, name: string) {
  const res = await api.post<APIResponse<void>>(
    `/clusters/${clusterId}/workloads/${kind}/${namespace}/${name}/restart`
  );
  return res.data;
}

export async function getWorkloadPods(clusterId: string, kind: string, namespace: string, name: string) {
  const res = await api.get<APIResponse<import('@/types').Pod[]>>(
    `/clusters/${clusterId}/workloads/${kind}/${namespace}/${name}/pods`
  );
  return res.data.data;
}

// --- Pod Logs ---

export async function getPodLogs(
  clusterId: string,
  namespace: string,
  pod: string,
  params?: {
    container?: string;
    tailLines?: number;
    sinceSeconds?: number;
    follow?: boolean;
  }
) {
  // Map to snake_case so the wire stays consistent with the rest of the
  // backend (which accepts both, but we standardize here).
  const query: Record<string, string | number | boolean> = {};
  if (params?.container) query.container = params.container;
  if (params?.tailLines && params.tailLines > 0) query.tail_lines = params.tailLines;
  if (params?.sinceSeconds && params.sinceSeconds > 0)
    query.since_seconds = params.sinceSeconds;
  if (params?.follow) query.follow = params.follow;
  const res = await api.get<APIResponse<import('@/types').PodLog[]>>(
    `/workloads/pods/${clusterId}/${namespace}/${pod}/logs`,
    { params: query }
  );
  return res.data.data;
}

/**
 * Create a streaming connection for pod logs via WebSocket.
 *
 * Auth: browser `new WebSocket(...)` cannot set custom headers, so the client
 * first requests a one-use stream ticket and passes only that short-lived
 * ticket in the URL.
 *
 * Options:
 *   - `follow` / `tailLines` / `sinceSeconds` are forwarded as query params
 *     and used to drive `kubectl logs --follow` on the agent side.
 *     `tailLines` and `sinceSeconds` are mutually exclusive in the UI
 *     (Rancher-style picker) — callers should pass one or the other.
 *   - `onError` is fired on connect failures (e.g. agent disconnected) and on
 *     structured `{"type":"error", ...}` frames returned by the backend.
 */
export function streamPodLogs(
  clusterId: string,
  namespace: string,
  pod: string,
  container: string,
  onMessage: (log: import('@/types').PodLog) => void,
  onError?: (error: { code?: string; message: string }) => void,
  opts?: { follow?: boolean; tailLines?: number; sinceSeconds?: number }
): () => void {
  const base = wsBase();

  const params = new URLSearchParams();
  if (opts?.follow) params.set('follow', 'true');
  if (opts?.tailLines && opts.tailLines > 0) params.set('tail_lines', String(opts.tailLines));
  if (opts?.sinceSeconds && opts.sinceSeconds > 0)
    params.set('since_seconds', String(opts.sinceSeconds));

  let closed = false;
  let ws: WebSocket | null = null;

  createStreamTicket('logs', clusterId)
    .then(({ ticket }) => {
      if (closed) return;
      params.set('ticket', ticket);
      const wsUrl =
        `${base}/logs/${clusterId}/${namespace}/${encodeURIComponent(pod)}/${encodeURIComponent(container)}/` +
        (params.toString() ? `?${params.toString()}` : '');
      ws = new WebSocket(wsUrl);

      ws.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data);
          // Backend sends a structured error frame for non-fatal problems
          // (e.g. agent disconnected) so the UI can surface them without
          // dropping the connection.
          if (data && data.type === 'error') {
            onError?.({ code: data.code, message: data.message || 'log stream error' });
            return;
          }
          onMessage(data);
        } catch {
          onMessage({ timestamp: new Date().toISOString(), message: event.data, container });
        }
      };

      ws.onerror = () => {
        if (!closed) {
          onError?.({ message: 'WebSocket connection error' });
        }
      };

      ws.onclose = (event) => {
        // Surface unexpected closes (server-side errors / agent gone). 1000 =
        // normal closure (cleanup on unmount), so don't toast on those.
        if (!closed && event.code !== 1000 && event.code !== 1005) {
          onError?.({
            code: String(event.code),
            message: event.reason || `log stream closed (code ${event.code})`,
          });
        }
      };
    })
    .catch((error: Error) => {
      if (!closed) onError?.({ message: error.message || 'Failed to create log stream ticket' });
    });

  return () => {
    closed = true;
    try {
      ws?.close(1000, 'client unmount');
    } catch {
      /* ignore */
    }
  };
}

// --- Metrics ---

export async function getClusterMetrics(clusterId: string, params?: { range?: string }) {
  const res = await api.get<APIResponse<import('@/types').MetricsData>>(`/clusters/${clusterId}/metrics`, { params });
  return res.data.data;
}

export async function getClusterMetricsSummary(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').MetricsSummary>>(`/clusters/${clusterId}/metrics/summary`);
  return res.data.data;
}

export async function getWorkloadMetrics(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  params?: { range?: string }
) {
  const res = await api.get<APIResponse<import('@/types').MetricsData>>(
    `/clusters/${clusterId}/workloads/${kind}/${namespace}/${name}/metrics`,
    { params }
  );
  return res.data.data;
}

// --- ArgoCD ---

export async function getArgoInstances(clusterId?: string) {
  const res = await api.get<APIResponse<import('@/types').ArgoInstance[]>>('/argocd/instances', {
    params: clusterId ? { clusterId } : undefined,
  });
  return res.data.data;
}

export async function getArgoApplications(params?: { clusterId?: string; project?: string; search?: string }) {
  const res = await api.get<APIResponse<import('@/types').ArgoApplication[]>>('/argocd/applications', { params });
  return res.data.data;
}

export interface ArgoCachedApplication {
  id: string;
  argocdInstanceId: string;
  name: string;
  project: string;
  repoUrl: string;
  path: string;
  targetRevision: string;
  destinationCluster: string;
  destinationNamespace: string;
  syncStatus: string;
  healthStatus: string;
  resourceCreatedCount?: number;
  resourceChangedCount?: number;
  resourcePrunedCount?: number;
  lastSynced?: string;
  createdAt?: string;
  updatedAt?: string;
}

export async function listArgoCachedApplications(params?: { instanceId?: string; limit?: number; offset?: number }) {
  const endpoint = params?.instanceId
    ? `/argocd/instances/${params.instanceId}/cached-applications`
    : '/argocd/applications';
  const res = await api.get<PaginatedResponse<ArgoCachedApplication>>(endpoint, {
    params: {
      limit: params?.limit,
      offset: params?.offset,
    },
  });
  return res.data.data ?? [];
}

export async function syncArgoApplication(instanceId: string, appName: string) {
  const res = await api.post<APIResponse<void>>(`/argocd/instances/${instanceId}/applications/${appName}/sync`);
  return res.data;
}

// --- RBAC ---

export async function getGlobalRoles() {
  const res = await api.get<APIResponse<import('@/types').GlobalRole[]>>('/rbac/global-roles');
  return res.data.data;
}

export async function getClusterRoles(clusterId?: string) {
  const res = await api.get<APIResponse<import('@/types').ClusterRole[]>>('/rbac/cluster-roles', {
    params: clusterId ? { clusterId } : undefined,
  });
  return res.data.data;
}

export async function getProjectRoles(projectId?: string) {
  const res = await api.get<APIResponse<import('@/types').ProjectRole[]>>('/rbac/project-roles', {
    params: projectId ? { projectId } : undefined,
  });
  return res.data.data;
}

export async function createGlobalRole(data: {
  name: string;
  displayName: string;
  description?: string;
  rules: import('@/types').PolicyRule[];
}) {
  const res = await api.post<APIResponse<import('@/types').GlobalRole>>('/rbac/global-roles', data);
  return res.data.data;
}

export async function createClusterRole(data: {
  name: string;
  displayName: string;
  description?: string;
  rules: import('@/types').PolicyRule[];
}) {
  const res = await api.post<APIResponse<import('@/types').ClusterRole>>('/rbac/cluster-roles', data);
  return res.data.data;
}

export async function createProjectRole(data: {
  name: string;
  displayName: string;
  description?: string;
  rules: import('@/types').PolicyRule[];
}) {
  const res = await api.post<APIResponse<import('@/types').ProjectRole>>('/rbac/project-roles', data);
  return res.data.data;
}

export async function getRoleBindings(params?: { roleType?: string; scope?: string }) {
  const res = await api.get<APIResponse<import('@/types').RoleBinding[]>>('/rbac/bindings', { params });
  return res.data.data;
}

export async function createRoleBinding(data: {
  roleName: string;
  roleType: string;
  subjects: import('@/types').RoleBindingSubject[];
  scope?: { clusterId?: string; projectId?: string };
}) {
  const res = await api.post<APIResponse<import('@/types').RoleBinding>>('/rbac/bindings', data);
  return res.data.data;
}

// --- Cluster role bindings (namespace-scoped authoring) ---
// These target the real backend endpoints (unlike the phantom /rbac/bindings
// helpers above). An empty `namespace` == cluster-wide.

export async function listClusterRoleBindings(params?: { cluster_id?: string }) {
  const res = await api.get<APIResponse<import('@/types').ClusterRoleBinding[]>>(
    '/rbac/cluster-role-bindings/',
    { params }
  );
  return res.data.data;
}

export async function createClusterRoleBinding(data: {
  user_id: string;
  role_id: string;
  cluster_id: string;
  namespace?: string;
}) {
  const res = await api.post<APIResponse<import('@/types').ClusterRoleBinding>>(
    '/rbac/cluster-role-bindings/',
    data
  );
  return res.data.data;
}

export async function deleteClusterRoleBinding(id: string) {
  await api.delete(`/rbac/cluster-role-bindings/${id}/`);
}

export interface EffectivePermissionParams {
  clusterId?: string;
  projectId?: string;
  namespace?: string;
}

function effectivePermissionQueryParams(params?: EffectivePermissionParams) {
  return {
    cluster_id: params?.clusterId || undefined,
    project_id: params?.projectId || undefined,
    namespace: params?.namespace || undefined,
  };
}

export async function getMyEffectivePermissions(params?: EffectivePermissionParams) {
  const res = await api.get<APIResponse<import('@/types').EffectivePermissionResponse>>('/rbac/my-permissions', {
    params: effectivePermissionQueryParams(params),
  });
  return res.data.data;
}

export async function getEffectivePermissionsForUser(userId: string, params?: EffectivePermissionParams) {
  const res = await api.get<APIResponse<import('@/types').EffectivePermissionResponse>>(
    `/rbac/effective-permissions/${userId}`,
    { params: effectivePermissionQueryParams(params) },
  );
  return res.data.data;
}

export async function previewPermissions(data: import('@/types').PermissionPreviewRequest) {
  const res = await api.post<APIResponse<import('@/types').PermissionPreviewResponse>>(
    '/rbac/permission-preview',
    data,
  );
  return res.data.data;
}

export async function getUsers(params?: { search?: string; page?: number; pageSize?: number }) {
  const res = await api.get<PaginatedResponse<import('@/types').User>>('/users', { params });
  return res.data;
}

// --- Settings ---

export interface GeneralSettings {
  platformName: string;
  agentHeartbeatInterval: number;
  defaultSessionTimeout: number;
  enableAuditLogging: boolean;
  metricsCollection: boolean;
}

export async function getGeneralSettings() {
  const res = await api.get<APIResponse<GeneralSettings>>('/settings/general');
  return res.data.data;
}

export async function saveGeneralSettings(data: GeneralSettings) {
  const res = await api.put<APIResponse<GeneralSettings>>('/settings/general', data);
  return res.data.data;
}

export async function getSSOProviders() {
  const res = await api.get<APIResponse<import('@/types').SSOProvider[]>>('/settings/sso');
  return res.data.data;
}

export async function createSSOProvider(data: {
  type: 'github' | 'google' | 'oidc';
  name: string;
  enabled: boolean;
  config: {
    clientId?: string;
    clientSecret?: string;
    metadataUrl?: string;
    allowedOrganizations?: string;
    autoCreateUsers?: boolean;
  };
}) {
  const res = await api.post<APIResponse<import('@/types').SSOProvider>>('/settings/sso', data);
  return res.data.data;
}

export async function getAPITokens() {
  const res = await api.get<APIResponse<import('@/types').APIToken[]>>('/settings/tokens');
  return res.data.data;
}

export async function createAPIToken(data: { name: string; description?: string; expiresInDays?: number }) {
  const res = await api.post<APIResponse<import('@/types').APIToken & { token: string }>>('/settings/tokens', data);
  return res.data.data;
}

export async function deleteAPIToken(id: string) {
  await api.delete(`/settings/tokens/${id}`);
}

export type AuditLogQueryParams = {
  page?: number;
  pageSize?: number;
  limit?: number;
  offset?: number;
  actor?: string;
  action?: string;
  user?: string;
  user_id?: string;
  target?: string;
  resource_type?: string;
  resource_id?: string;
  resource_name?: string;
  cluster_id?: string;
  project_id?: string;
  result?: 'success' | 'failure' | 'error' | string;
  status_code?: number;
  source?: string;
  correlation_id?: string;
  request_id?: string;
  from?: string;
  to?: string;
  // action_class: "mutation" | "read" | "auth" | "system" — migration 063.
  action_class?: string;
};

function auditLogRequestParams(params?: AuditLogQueryParams): Record<string, string | number> | undefined {
  if (!params) return undefined;
  const out: Record<string, string | number> = {};
  const limit = params.limit ?? params.pageSize;
  const page = params.page ?? 1;
  const offset = params.offset ?? (limit ? Math.max(0, page - 1) * limit : undefined);
  if (limit != null) out.limit = limit;
  if (offset != null) out.offset = offset;
  const entries: Array<[string, string | number | undefined]> = [
    ['actor', params.actor || params.user],
    ['user_id', params.user_id],
    ['action', params.action],
    ['action_class', params.action_class],
    ['target', params.target],
    ['resource_type', params.resource_type],
    ['resource_id', params.resource_id],
    ['resource_name', params.resource_name],
    ['cluster_id', params.cluster_id],
    ['project_id', params.project_id],
    ['result', params.result],
    ['status_code', params.status_code],
    ['source', params.source],
    ['correlation_id', params.correlation_id],
    ['request_id', params.request_id],
    ['from', params.from],
    ['to', params.to],
  ];
  for (const [key, value] of entries) {
    if (value !== undefined && value !== '') out[key] = value;
  }
  return out;
}

export async function getAuditLogs(params?: AuditLogQueryParams) {
  const res = await api.get<PaginatedResponse<import('@/types').AuditLogEntry>>('/audit', {
    params: auditLogRequestParams(params),
  });
  return res.data;
}

export function getAuditLogExportURL(params?: AuditLogQueryParams) {
  const requestParams = auditLogRequestParams(params) || {};
  requestParams.format = 'csv';
  const search = new URLSearchParams();
  Object.entries(requestParams).forEach(([key, value]) => search.set(key, String(value)));
  return `${API_BASE_URL}/audit/export/?${search.toString()}`;
}

// --- Activity Feed ---

export async function getActivityFeed(params?: { limit?: number }) {
  const res = await api.get<APIResponse<import('@/types').ActivityEvent[]>>('/activity', { params });
  return res.data.data;
}

// --- Alerting ---

export async function getAlertRules() {
  const res = await api.get<APIResponse<import('@/types').AlertRule[]>>('/alerting/rules');
  return res.data.data;
}

export async function createAlertRule(data: Partial<import('@/types').AlertRule>) {
  const res = await api.post<APIResponse<import('@/types').AlertRule>>('/alerting/rules', data);
  return res.data.data;
}

export async function updateAlertRule(id: string, data: Partial<import('@/types').AlertRule>) {
  const res = await api.put<APIResponse<import('@/types').AlertRule>>(`/alerting/rules/${id}`, data);
  return res.data.data;
}

export async function deleteAlertRule(id: string) {
  await api.delete(`/alerting/rules/${id}`);
}

export async function getAlertEvents(params?: Record<string, string>) {
  const res = await api.get<APIResponse<import('@/types').AlertEvent[]>>('/alerting/events', { params });
  return res.data.data;
}

export async function acknowledgeAlert(id: string) {
  const res = await api.post<APIResponse<import('@/types').AlertEvent>>(`/alerting/events/${id}/acknowledge`);
  return res.data.data;
}

export async function resolveAlert(id: string) {
  const res = await api.post<APIResponse<import('@/types').AlertEvent>>(`/alerting/events/${id}/resolve`);
  return res.data.data;
}

export async function getNotificationChannels() {
  const res = await api.get<APIResponse<import('@/types').NotificationChannel[]>>('/alerting/channels');
  return res.data.data;
}

export async function createNotificationChannel(data: Partial<import('@/types').NotificationChannel>) {
  const res = await api.post<APIResponse<import('@/types').NotificationChannel>>('/alerting/channels', data);
  return res.data.data;
}

export async function updateNotificationChannel(id: string, data: Partial<import('@/types').NotificationChannel>) {
  const res = await api.put<APIResponse<import('@/types').NotificationChannel>>(`/alerting/channels/${id}`, data);
  return res.data.data;
}

export async function deleteNotificationChannel(id: string) {
  await api.delete(`/alerting/channels/${id}`);
}

export async function testNotificationChannel(id: string) {
  const res = await api.post<APIResponse<{ success: boolean; message: string }>>(`/alerting/channels/${id}/test`);
  return res.data.data;
}

export async function getAlertSilences() {
  const res = await api.get<APIResponse<import('@/types').AlertSilence[]>>('/alerting/silences');
  return res.data.data;
}

export async function createAlertSilence(data: Partial<import('@/types').AlertSilence>) {
  const res = await api.post<APIResponse<import('@/types').AlertSilence>>('/alerting/silences', data);
  return res.data.data;
}

export async function deleteAlertSilence(id: string) {
  await api.delete(`/alerting/silences/${id}`);
}

// --- Anomaly baselines (sprint 072, read-only) ---

export async function getAnomalyBaselines(params?: { clusterId?: string; limit?: number; offset?: number }) {
  const res = await api.get<APIResponse<import('@/types').AnomalyBaseline[]>>('/anomaly-baselines', { params });
  return res.data.data;
}

export async function getAnomalyBaseline(id: string) {
  const res = await api.get<APIResponse<import('@/types').AnomalyBaseline>>(`/anomaly-baselines/${id}`);
  return res.data.data;
}

// --- Logging ---

export async function getLoggingOutputs() {
  const res = await api.get<APIResponse<import('@/types').LoggingOutput[]>>('/logging/outputs');
  return res.data.data;
}

export async function createLoggingOutput(data: Partial<import('@/types').LoggingOutput>) {
  const res = await api.post<APIResponse<import('@/types').LoggingOutput>>('/logging/outputs', data);
  return res.data.data;
}

export async function updateLoggingOutput(id: string, data: Partial<import('@/types').LoggingOutput>) {
  const res = await api.put<APIResponse<import('@/types').LoggingOutput>>(`/logging/outputs/${id}`, data);
  return res.data.data;
}

export async function deleteLoggingOutput(id: string) {
  await api.delete(`/logging/outputs/${id}`);
}

export async function testLoggingOutput(id: string) {
  const res = await api.post<APIResponse<{ success: boolean; message: string }>>(`/logging/outputs/${id}/test`);
  return res.data.data;
}

export async function getLoggingPipelines() {
  const res = await api.get<APIResponse<import('@/types').LoggingPipeline[]>>('/logging/pipelines');
  return res.data.data;
}

export async function createLoggingPipeline(data: Partial<import('@/types').LoggingPipeline>) {
  const res = await api.post<APIResponse<import('@/types').LoggingPipeline>>('/logging/pipelines', data);
  return res.data.data;
}

export async function updateLoggingPipeline(id: string, data: Partial<import('@/types').LoggingPipeline>) {
  const res = await api.put<APIResponse<import('@/types').LoggingPipeline>>(`/logging/pipelines/${id}`, data);
  return res.data.data;
}

export async function deleteLoggingPipeline(id: string) {
  await api.delete(`/logging/pipelines/${id}`);
}

// --- Logging Operations (controller-backed reconciler) ---
//
// These mirror the argocd /operations endpoints. The list handler returns the
// same double-wrapped envelope (`{data: {data: [...], limit, offset}}`) so we
// unwrap with the same helper as `listArgoOperations`.

export async function getLoggingOperations(params?: {
  status?: string;
  target_type?: string;
  limit?: number;
  offset?: number;
}) {
  const res = await api.get<APIResponse<{ data: import('@/types').LoggingOperation[]; limit?: number; offset?: number }>>(
    '/logging/operations',
    { params },
  );
  const inner = unwrapEnvelope<{ data?: import('@/types').LoggingOperation[] }>(res.data);
  return inner?.data ?? [];
}

export async function getLoggingOperation(id: string) {
  const res = await api.get<APIResponse<import('@/types').LoggingOperation>>(`/logging/operations/${id}`);
  return res.data.data;
}

export async function retryLoggingOperation(id: string) {
  const res = await api.post<APIResponse<import('@/types').LoggingOperation>>(`/logging/operations/${id}/retry`);
  return res.data.data;
}

// --- Storage ---

export async function getPersistentVolumes(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').PersistentVolume[]>>(
    `/clusters/${clusterId}/resources/persistentvolumes`
  );
  return res.data.data;
}

export async function getPersistentVolumeClaims(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').PersistentVolumeClaim[]>>(
    `/clusters/${clusterId}/resources/persistentvolumeclaims`
  );
  return res.data.data;
}

export async function getStorageClasses(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').StorageClass[]>>(
    `/clusters/${clusterId}/resources/storageclasses`
  );
  return res.data.data;
}

export async function createPersistentVolumeClaim(clusterId: string, data: Partial<import('@/types').PersistentVolumeClaim>) {
  const res = await api.post<APIResponse<import('@/types').PersistentVolumeClaim>>(
    `/clusters/${clusterId}/resources/persistentvolumeclaims`,
    data
  );
  return res.data.data;
}

export async function deletePersistentVolumeClaim(clusterId: string, namespace: string, name: string) {
  await api.delete(`/clusters/${clusterId}/resources/persistentvolumeclaims/${namespace}/${name}`);
}

export async function deletePersistentVolume(clusterId: string, name: string) {
  await api.delete(`/clusters/${clusterId}/resources/persistentvolumes/${name}`);
}

// --- Networking ---

export async function getServices(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').K8sService[]>>(
    `/clusters/${clusterId}/resources/services`
  );
  return res.data.data;
}

export async function createService(clusterId: string, data: Partial<import('@/types').K8sService>) {
  const res = await api.post<APIResponse<import('@/types').K8sService>>(
    `/clusters/${clusterId}/resources/services`,
    data
  );
  return res.data.data;
}

export async function deleteService(clusterId: string, namespace: string, name: string) {
  await api.delete(`/clusters/${clusterId}/resources/services/${namespace}/${name}`);
}

export async function getIngresses(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').Ingress[]>>(
    `/clusters/${clusterId}/resources/ingresses`
  );
  return res.data.data;
}

export async function createIngress(clusterId: string, data: Partial<import('@/types').Ingress>) {
  const res = await api.post<APIResponse<import('@/types').Ingress>>(
    `/clusters/${clusterId}/resources/ingresses`,
    data
  );
  return res.data.data;
}

export async function deleteIngress(clusterId: string, namespace: string, name: string) {
  await api.delete(`/clusters/${clusterId}/resources/ingresses/${namespace}/${name}`);
}

export async function getNetworkPolicies(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').NetworkPolicy[]>>(
    `/clusters/${clusterId}/resources/networkpolicies`
  );
  return res.data.data;
}

export async function createNetworkPolicy(clusterId: string, data: Partial<import('@/types').NetworkPolicy>) {
  const res = await api.post<APIResponse<import('@/types').NetworkPolicy>>(
    `/clusters/${clusterId}/resources/networkpolicies`,
    data
  );
  return res.data.data;
}

export async function deleteNetworkPolicy(clusterId: string, namespace: string, name: string) {
  await api.delete(`/clusters/${clusterId}/resources/networkpolicies/${namespace}/${name}`);
}

// --- Gateway API ---
//
// List endpoints go through the structured backend route which flattens the
// upstream Kubernetes object into a UI-friendly row shape (see
// internal/handler/resources.go). Deletes and YAML edits use the generic K8s
// proxy via useK8sDelete / useK8sApplyYaml in callers — no per-resource
// delete helpers needed here.

export async function getGateways(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').Gateway[]>>(
    `/clusters/${clusterId}/resources/gateways`,
  );
  return res.data.data;
}

export async function getHTTPRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').HTTPRoute[]>>(
    `/clusters/${clusterId}/resources/httproutes`,
  );
  return res.data.data;
}

export async function getGatewayClasses(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').GatewayClass[]>>(
    `/clusters/${clusterId}/resources/gatewayclasses`,
  );
  return res.data.data;
}

export async function getGRPCRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').GRPCRoute[]>>(
    `/clusters/${clusterId}/resources/grpcroutes`,
  );
  return res.data.data;
}

export async function getTLSRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').TLSRoute[]>>(
    `/clusters/${clusterId}/resources/tlsroutes`,
  );
  return res.data.data;
}

export async function getTCPRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').TCPRoute[]>>(
    `/clusters/${clusterId}/resources/tcproutes`,
  );
  return res.data.data;
}

export async function getUDPRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').UDPRoute[]>>(
    `/clusters/${clusterId}/resources/udproutes`,
  );
  return res.data.data;
}

export async function getReferenceGrants(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').ReferenceGrant[]>>(
    `/clusters/${clusterId}/resources/referencegrants`,
  );
  return res.data.data;
}

// --- Projects ---

export async function getProjects(params?: { search?: string; page?: number; pageSize?: number }) {
  const res = await api.get<PaginatedResponse<import('@/types').Project>>('/projects', { params });
  return res.data;
}

export async function getProject(id: string) {
  const res = await api.get<APIResponse<import('@/types').Project>>(`/projects/${id}`);
  return res.data.data;
}

export async function createProject(data: Partial<import('@/types').Project>) {
  const res = await api.post<APIResponse<import('@/types').Project>>('/projects', data);
  return res.data.data;
}

export async function updateProject(id: string, data: Partial<import('@/types').Project>) {
  const res = await api.put<APIResponse<import('@/types').Project>>(`/projects/${id}`, data);
  return res.data.data;
}

export async function takeoverProjectOwnership(id: string) {
  const res = await api.post<APIResponse<OwnershipTransferResult>>(`/projects/${id}/ownership/takeover`);
  return res.data.data;
}

export async function deleteProject(id: string) {
  await api.delete(`/projects/${id}`);
}

// --- User Management ---

export async function createUser(data: {
  username: string;
  email: string;
  displayName: string;
  password: string;
  globalRoles: string[];
}) {
  const res = await api.post<APIResponse<import('@/types').User>>('/users', data);
  return res.data.data;
}

export async function updateUser(id: string, data: Partial<import('@/types').User>) {
  const res = await api.put<APIResponse<import('@/types').User>>(`/users/${id}`, data);
  return res.data.data;
}

export async function deleteUser(id: string) {
  await api.delete(`/users/${id}`);
}

export async function resetUserPassword(id: string) {
  const res = await api.post<APIResponse<{ temporaryPassword: string }>>(`/users/${id}/reset-password`);
  return res.data.data;
}

// --- Catalog / Helm ---

export async function getHelmRepositories() {
  const res = await api.get<APIResponse<import('@/types').HelmRepository[]>>('/catalog/repositories');
  return res.data.data;
}

export async function createHelmRepository(data: {
  name: string;
  url: string;
  repoType: import('@/types').HelmRepoType;
  description?: string;
  username?: string;
  password?: string;
}) {
  const res = await api.post<APIResponse<import('@/types').HelmRepository>>('/catalog/repositories', data);
  return res.data.data;
}

export async function syncHelmRepository(id: string) {
  const res = await api.post<APIResponse<import('@/types').HelmRepository>>(`/catalog/repositories/${id}/sync`);
  return res.data.data;
}

export async function deleteHelmRepository(id: string) {
  await api.delete(`/catalog/repositories/${id}`);
}

export async function getHelmCharts(params?: { repository?: string; category?: string; search?: string }) {
  const res = await api.get<APIResponse<import('@/types').HelmChart[]>>('/catalog/charts', { params });
  return res.data.data;
}

export async function getHelmChartVersions(chartId: string) {
  const res = await api.get<APIResponse<import('@/types').HelmChartVersion[]>>(`/catalog/charts/${chartId}/versions`);
  return res.data.data;
}

export async function getInstalledCharts(params?: { cluster?: string }) {
  const res = await api.get<APIResponse<import('@/types').InstalledChart[]>>('/catalog/installed', { params });
  return res.data.data;
}

export async function installHelmChart(data: {
  cluster_id: string;
  chart_version_id: string;
  release_name: string;
  namespace: string;
  values_override?: string;
}) {
  const res = await api.post<APIResponse<import('@/types').InstalledChart>>('/catalog/installed', data);
  return res.data.data;
}

export async function upgradeInstalledChart(id: string, data: {
  chart_version_id: string;
  values_override?: string;
}) {
  const res = await api.put<APIResponse<import('@/types').InstalledChart>>(`/catalog/installed/${id}/upgrade`, data);
  return res.data.data;
}

export async function uninstallChart(id: string) {
  await api.delete(`/catalog/installed/${id}`);
}

export async function rollbackChart(id: string, revision: number) {
  const res = await api.post<APIResponse<import('@/types').InstalledChart>>(`/catalog/installed/${id}/rollback`, { revision });
  return res.data.data;
}

// --- Catalog ratings + recommendations (migration 055) ---

// Shape of one rating row as returned by the chart_ratings handler.
// Kept inline (not in @/types) until the broader catalog typing pass
// lands — the route surface is small and the consumer count is one.
export interface ChartRating {
  id: string;
  chart_id: string;
  installation_id?: string;
  user_id: string;
  stars: number;
  note: string;
  created_at: string;
  updated_at: string;
}

export interface ChartRatingAggregate {
  rating_count: number;
  avg_stars: number;
  bayesian_score: number;
  histogram: [number, number, number, number, number];
}

// Server returns one row per (other) chart in both the popular and
// similar surfaces. ChartScore mirrors the Go ChartScore struct on
// the handler side; weight is omitted on the popular endpoint.
export interface ChartScore {
  chart_id: string;
  rating_count: number;
  avg_stars: number;
  bayesian_score: number;
  weight?: number;
}

export async function rateChart(chartId: string, payload: {
  stars: number;
  installation_id?: string;
  note?: string;
}) {
  const res = await api.post<APIResponse<ChartRating>>(`/charts/${chartId}/ratings/`, payload);
  return res.data.data;
}

export async function getChartRatings(chartId: string, params?: { limit?: number; offset?: number }) {
  const res = await api.get<APIResponse<ChartRating[]>>(`/charts/${chartId}/ratings/`, { params });
  return res.data.data;
}

export async function getChartRatingAggregate(chartId: string) {
  const res = await api.get<APIResponse<ChartRatingAggregate>>(`/charts/${chartId}/ratings/aggregate/`);
  return res.data.data;
}

export async function getMyChartRating(chartId: string) {
  // Returns null on 404 (no rating yet) rather than throwing — the
  // detail page treats "no rating" as a normal UI state, not an
  // error.
  try {
    const res = await api.get<APIResponse<ChartRating>>(`/charts/${chartId}/ratings/mine/`);
    return res.data.data;
  } catch (e: any) {
    if (e?.response?.status === 404) {
      return null;
    }
    throw e;
  }
}

export async function updateChartRating(chartId: string, ratingId: string, payload: {
  stars: number;
  note?: string;
}) {
  const res = await api.put<APIResponse<ChartRating>>(`/charts/${chartId}/ratings/${ratingId}/`, payload);
  return res.data.data;
}

export async function deleteChartRating(chartId: string, ratingId: string) {
  await api.delete(`/charts/${chartId}/ratings/${ratingId}/`);
}

export async function getPopularCharts(limit = 6) {
  const res = await api.get<APIResponse<ChartScore[]>>('/catalog/recommendations/popular/', { params: { limit } });
  return res.data.data;
}

export async function getSimilarCharts(chartId: string, limit = 5) {
  const res = await api.get<APIResponse<ChartScore[]>>(`/catalog/recommendations/similar/${chartId}/`, { params: { limit } });
  return res.data.data;
}

// --- Backups ---

export async function getBackupStorageConfigs() {
  const res = await api.get<APIResponse<import('@/types').BackupStorageConfig[]>>('/backups/storage');
  return res.data.data;
}

export async function createBackupStorageConfig(data: {
  name: string;
  storageType: import('@/types').BackupStorageType;
  bucket: string;
  prefix?: string;
  region?: string;
  endpointUrl?: string;
  accessKey?: string;
  secretKey?: string;
  serviceAccountJson?: string;
  connectionString?: string;
}) {
  const res = await api.post<APIResponse<import('@/types').BackupStorageConfig>>('/backups/storage', data);
  return res.data.data;
}

export async function testBackupStorage(id: string) {
  const res = await api.post<APIResponse<{ success: boolean; message: string }>>(`/backups/storage/${id}/test`);
  return res.data.data;
}

export async function deleteBackupStorageConfig(id: string) {
  await api.delete(`/backups/storage/${id}`);
}

export async function getBackups() {
  const res = await api.get<APIResponse<import('@/types').Backup[]>>('/backups');
  return res.data.data;
}

export async function createBackup(data: {
  name: string;
  storage_id: string;
  backup_type: import('@/types').BackupType;
}) {
  const res = await api.post<APIResponse<import('@/types').Backup>>('/backups', data);
  return res.data.data;
}

export async function restoreFromBackup(id: string) {
  const res = await api.post<APIResponse<{ message: string }>>(`/backups/${id}/restore`);
  return res.data.data;
}

export async function deleteBackup(id: string) {
  await api.delete(`/backups/${id}`);
}

export async function getBackupSchedules() {
  const res = await api.get<APIResponse<import('@/types').BackupSchedule[]>>('/backups/schedules');
  return res.data.data;
}

export async function createBackupSchedule(data: {
  name: string;
  storage_id: string;
  backup_type: import('@/types').BackupType;
  cron_expression: string;
  retention_count: number;
  enabled?: boolean;
}) {
  const res = await api.post<APIResponse<import('@/types').BackupSchedule>>('/backups/schedules', data);
  return res.data.data;
}

export async function updateBackupSchedule(id: string, data: Partial<{
  name: string;
  storage_id: string;
  backup_type: import('@/types').BackupType;
  cron_expression: string;
  retention_count: number;
  enabled: boolean;
}>) {
  const res = await api.put<APIResponse<import('@/types').BackupSchedule>>(`/backups/schedules/${id}`, data);
  return res.data.data;
}

export async function deleteBackupSchedule(id: string) {
  await api.delete(`/backups/schedules/${id}`);
}

// --- Security ---

export async function getPodSecurityTemplates() {
  const res = await api.get<APIResponse<import('@/types').PodSecurityTemplate[]>>('/security/templates');
  return res.data.data;
}

export async function createPodSecurityTemplate(data: Partial<import('@/types').PodSecurityTemplate>) {
  const res = await api.post<APIResponse<import('@/types').PodSecurityTemplate>>('/security/templates', data);
  return res.data.data;
}

export async function updatePodSecurityTemplate(id: string, data: Partial<import('@/types').PodSecurityTemplate>) {
  const res = await api.put<APIResponse<import('@/types').PodSecurityTemplate>>(`/security/templates/${id}`, data);
  return res.data.data;
}

export async function deletePodSecurityTemplate(id: string) {
  await api.delete(`/security/templates/${id}`);
}

export async function getClusterSecurityPolicies() {
  const res = await api.get<APIResponse<import('@/types').ClusterSecurityPolicy[]>>('/security/policies');
  return res.data.data;
}

export async function assignSecurityPolicy(data: { cluster_id: string; template_id: string }) {
  const res = await api.post<APIResponse<import('@/types').ClusterSecurityPolicy>>('/security/policies', data);
  return res.data.data;
}

export async function applySecurityPolicy(id: string) {
  const res = await api.post<APIResponse<import('@/types').ClusterSecurityPolicy>>(`/security/policies/${id}/apply`);
  return res.data.data;
}

export async function removeSecurityPolicy(id: string) {
  await api.delete(`/security/policies/${id}`);
}

export async function getSecurityScans(params?: { cluster?: string; scan_type?: string }) {
  const res = await api.get<APIResponse<import('@/types').SecurityScanResult[]>>('/security/scans', { params });
  return res.data.data;
}

export async function triggerSecurityScan(data: { cluster_id: string; scan_type: import('@/types').SecurityScanType }) {
  const res = await api.post<APIResponse<import('@/types').SecurityScanResult>>('/security/scans', data);
  return res.data.data;
}

// --- Generic K8s Resources ---

export async function getGenericResources(clusterId: string, resourceType: string) {
  const res = await api.get<APIResponse<import('@/types').GenericK8sResource[]>>(
    `/clusters/${clusterId}/resources/generic/${resourceType}`
  );
  return res.data.data;
}

// --- Cross-cluster Resource Search ---

// SearchableResourceType matches the keys in searchResourceDefs on the
// backend (internal/handler/resources_search.go). When adding a new type
// keep both lists in sync.
export type SearchableResourceType =
  | 'pods'
  | 'services'
  | 'configmaps'
  | 'secrets'
  | 'namespaces'
  | 'nodes'
  | 'persistentvolumeclaims'
  | 'deployments'
  | 'statefulsets'
  | 'daemonsets'
  | 'jobs'
  | 'cronjobs'
  | 'ingresses';

export interface SearchResourcesParams {
  type: SearchableResourceType;
  namespace?: string;
  label?: string;
  field?: string;
  name?: string;
  limit?: number;
}

export interface SearchResultRow extends Record<string, unknown> {
  cluster_id: string;
  cluster_name: string;
  clusterId: string;
  clusterName: string;
  name?: string;
  namespace?: string;
  status?: string;
  type?: string;
  age?: string;
}

export interface SearchClusterError {
  cluster_id: string;
  cluster_name: string;
  error: string;
}

export interface SearchResourcesResponse {
  results: SearchResultRow[];
  errors: SearchClusterError[];
  // The backend emits these as snake_case but the axios interceptor
  // camelizes incoming keys, so consumers should reference the camelCase
  // forms. Keeping both here mirrors the wire format for completeness.
  clusters_queried?: number;
  clusters_failed?: number;
  clustersQueried: number;
  clustersFailed: number;
  type: string;
}

export async function searchResources(params: SearchResourcesParams) {
  const query: Record<string, string> = { type: params.type };
  if (params.namespace) query.namespace = params.namespace;
  if (params.label) query.label = params.label;
  if (params.field) query.field = params.field;
  if (params.name) query.name = params.name;
  if (params.limit) query.limit = String(params.limit);
  const res = await api.get<APIResponse<SearchResourcesResponse>>('/resources/search', {
    params: query,
  });
  return res.data.data;
}

// --- Kubeconfig ---

export async function generateKubeconfig(clusterId: string) {
  const res = await api.post(`/clusters/${clusterId}/generate-kubeconfig/`, null, {
    responseType: 'blob',
  });
  return res.data;
}

// --- Cluster Tools ---

export async function getTools() {
  const res = await api.get<APIResponse<import('@/types').ClusterTool[]>>('/tools');
  return res.data.data;
}

export async function getTool(slug: string) {
  const res = await api.get<APIResponse<import('@/types').ClusterTool>>(`/tools/${slug}`);
  return res.data.data;
}

export async function getClusterToolsStatus(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').ClusterToolStatus[]>>(`/clusters/${clusterId}/tools/status`);
  return res.data.data;
}

export async function previewToolInstall(slug: string, data: { cluster_id: string; preset: string }) {
  const res = await api.post<APIResponse<import('@/types').ToolPreviewResponse>>(`/tools/${slug}/preview`, data);
  return res.data.data;
}

export async function installTool(slug: string, data: { cluster_id: string; preset: string; values_override?: string }) {
  const res = await api.post<APIResponse<import('@/types').ToolOperation>>(`/tools/${slug}/install`, data);
  return res.data.data;
}

export async function getToolOperation(operationId: string) {
  const res = await api.get<APIResponse<import('@/types').ToolOperation>>(`/tools/operations/${operationId}/`);
  return res.data.data;
}

export async function upgradeTool(slug: string, data: { cluster_id: string; preset?: string; values_override?: string }) {
  const res = await api.put<APIResponse<unknown>>(`/tools/${slug}/upgrade`, data);
  return res.data.data;
}

export async function uninstallTool(slug: string, data: { cluster_id: string }) {
  const res = await api.delete<APIResponse<unknown>>(`/tools/${slug}/uninstall`, { data });
  return res.data.data;
}

export async function adoptTool(slug: string, data: { cluster_id: string; release_name: string }) {
  const res = await api.post<APIResponse<unknown>>(`/tools/${slug}/adopt`, data);
  return res.data.data;
}

// ============================================================
// K8s Proxy Functions
// ============================================================

/**
 * Generic K8s proxy: sends any HTTP method through the backend proxy
 * to the K8s API via the agent tunnel.
 *
 * URL pattern: /clusters/{clusterId}/k8s/{k8sPath}
 */
export async function k8sProxy(
  clusterId: string,
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE',
  k8sPath: string,
  body?: unknown,
  headers?: Record<string, string>,
) {
  const url = `/clusters/${clusterId}/k8s/${k8sPath}`;
  const res = await api.request({
    url,
    method,
    data: body,
    headers,
    timeout: 60000,
  });
  return res.data;
}

export async function k8sGet(clusterId: string, path: string) {
  return k8sProxy(clusterId, 'GET', path);
}

export async function k8sCreate(clusterId: string, path: string, body: unknown) {
  return k8sProxy(clusterId, 'POST', path, body);
}

export async function k8sUpdate(clusterId: string, path: string, body: unknown) {
  return k8sProxy(clusterId, 'PUT', path, body);
}

export async function k8sDryRunYaml(clusterId: string, path: string, yamlStr: string) {
  const yaml = await import('js-yaml');
  const body = yaml.load(yamlStr);
  const dryRunPath = appendK8sQuery(path, {
    dryRun: 'All',
    fieldManager: 'astronomer',
    fieldValidation: 'Warn',
  });
  return k8sProxy(clusterId, 'PATCH', dryRunPath, body, {
    'Content-Type': 'application/apply-patch+yaml',
  });
}

export async function k8sPatch(
  clusterId: string,
  path: string,
  body: unknown,
  patchType: 'strategic-merge' | 'merge' | 'json' = 'strategic-merge',
) {
  const contentTypeMap = {
    'strategic-merge': 'application/strategic-merge-patch+json',
    merge: 'application/merge-patch+json',
    json: 'application/json-patch+json',
  };
  return k8sProxy(clusterId, 'PATCH', path, body, {
    'Content-Type': contentTypeMap[patchType],
  });
}

export async function k8sDelete(clusterId: string, path: string) {
  return k8sProxy(clusterId, 'DELETE', path);
}

function appendK8sQuery(path: string, params: Record<string, string>): string {
  const [base, rawQuery = ''] = path.split('?', 2);
  const search = new URLSearchParams(rawQuery);
  for (const [key, value] of Object.entries(params)) {
    search.set(key, value);
  }
  const query = search.toString();
  return query ? `${base}?${query}` : base;
}

/**
 * Fetch a K8s resource as YAML text.
 * The backend proxy returns JSON; we convert client-side.
 */
export async function k8sGetYaml(clusterId: string, path: string): Promise<string> {
  const data = await k8sGet(clusterId, path);
  // Dynamic import to keep bundle size down for non-YAML users
  const yaml = await import('js-yaml');
  // Strip managedFields for readability
  if (data?.metadata?.managedFields) {
    delete data.metadata.managedFields;
  }
  return yaml.dump(data, { lineWidth: -1, noRefs: true });
}

/**
 * Apply a YAML manifest to a K8s resource via server-side apply (DIR-01).
 * Converts YAML → JSON client-side, then PATCH with apply-patch+yaml and
 * fieldManager=astronomer (force) so concurrent edits get ownership semantics.
 */
export async function k8sApplyYaml(clusterId: string, path: string, yamlStr: string) {
  const yaml = await import('js-yaml');
  const body = yaml.load(yamlStr);
  const applyPath = appendK8sQuery(path, {
    fieldManager: 'astronomer',
    force: 'true',
  });
  return k8sProxy(clusterId, 'PATCH', applyPath, body, {
    'Content-Type': 'application/apply-patch+yaml',
  });
}

// === Phase B1: ArgoCD lifecycle ===
//
// Functions that hit the 17 backend endpoints shipped in Phase B1. Names
// are kept distinct from the legacy helpers above (which only know about
// the cached cross-instance app list and the by-name sync). The Go server
// returns `data.data` on the lifecycle endpoints that wrap a single
// resource, but pass-through endpoints (live applications, manifests,
// projects list, applicationsets list) return raw upstream JSON and are
// typed as `unknown` so callers can narrow.

import type {
  ArgoInstanceB1,
  ArgoLiveApplication,
  ArgoCreateApplicationRequest,
  ArgoSyncOptions,
  ArgoAppHistoryEntry,
  ArgoManifests,
  ArgoProject,
  ArgoCreateProjectRequest,
  ArgoApplicationSet,
  ArgoCreateApplicationSetRequest,
  ArgoManagedCluster,
  ArgoManagedClusterRegisterRequest,
  ArgoOrphanReport,
  ArgoRepository,
  ArgoRepositoryCreate,
  ArgoOperation,
} from '@/types';

// --- Instances (live wrappers around already-existing helpers) ---

export async function getArgoInstanceB1(id: string) {
  const res = await api.get<APIResponse<ArgoInstanceB1>>(`/argocd/instances/${id}`);
  // Backend wraps single instances directly, not under data.data.
  // Older callers used `data.data`, but this endpoint emits the
  // bare object. We accept either shape via the optional cast below.
  const payload = (res.data as unknown) as ArgoInstanceB1 & { data?: ArgoInstanceB1 };
  return payload.data ?? payload;
}

export async function getArgoInstanceHealth(id: string) {
  // The /health/ endpoint returns 200 { data: { is_healthy: true } } or 502
  // with the same envelope and is_healthy:false. We surface both via a
  // non-throwing wrapper so the overview tab can render the unhealthy state
  // without raising a toast.
  try {
    const res = await api.get<APIResponse<{ isHealthy: boolean }>>(`/argocd/instances/${id}/health/`);
    return res.data.data;
  } catch {
    return { isHealthy: false };
  }
}

// unwrapEnvelope returns the inner value of a `{data: T}` server envelope, or
// the value itself if it's already bare. Used by the list endpoints below
// because the Go handler wraps all responses through RespondJSON, but a few
// older callers still tolerate bare shapes from tests.
function unwrapEnvelope<T>(value: unknown): T {
  if (value && typeof value === 'object' && !Array.isArray(value) && 'data' in (value as Record<string, unknown>)) {
    return (value as { data: T }).data;
  }
  return value as T;
}

// --- Applications (B1 CRUD against upstream) ---

export async function listArgoApplicationsLive(instanceId: string) {
  // The handler returns the upstream `{items:[...]}` (Kubernetes-style list)
  // wrapped in our standard `{data: ...}` envelope. Tolerate both wrapped
  // and bare shapes so this function survives an envelope refactor.
  const res = await api.get<APIResponse<{ items?: ArgoLiveApplication[] }> | { items?: ArgoLiveApplication[] } | ArgoLiveApplication[]>(
    `/argocd/instances/${instanceId}/applications`,
  );
  const inner = unwrapEnvelope<{ items?: ArgoLiveApplication[] } | ArgoLiveApplication[]>(res.data);
  if (Array.isArray(inner)) return inner;
  return inner?.items ?? [];
}

export async function createArgoApplication(instanceId: string, body: ArgoCreateApplicationRequest) {
  const res = await api.post<ArgoLiveApplication>(
    `/argocd/instances/${instanceId}/applications`,
    body,
  );
  return res.data;
}

export async function patchArgoApplicationByName(
  instanceId: string,
  name: string,
  patch: Record<string, unknown>,
) {
  // The PATCH endpoint accepts a JSON merge body which the backend
  // re-wraps for the upstream's `{name, patch, patchType:merge}` envelope.
  const res = await api.patch<ArgoLiveApplication>(
    `/argocd/instances/${instanceId}/applications/${encodeURIComponent(name)}`,
    patch,
  );
  return res.data;
}

export async function deleteArgoApplicationByName(
  instanceId: string,
  name: string,
  cascade = true,
) {
  await api.delete(
    `/argocd/instances/${instanceId}/applications/${encodeURIComponent(name)}?cascade=${cascade ? 'true' : 'false'}`,
  );
}

export async function syncArgoApplicationById(appId: string, opts: ArgoSyncOptions = {}) {
  // Backend body uses snake_case `dry_run`; revision/prune are passed as-is.
  const body: Record<string, unknown> = {};
  if (opts.revision) body.revision = opts.revision;
  if (opts.prune) body.prune = true;
  if (opts.dryRun) body.dry_run = true;
  if (opts.reason) body.reason = opts.reason;
  if (opts.syncWindowOverride) body.sync_window_override = true;
  const res = await api.post<ArgoOperation>(`/argocd/applications/${appId}/sync`, body);
  return res.data;
}

export async function refreshArgoApplicationById(appId: string, hard = false) {
  const url = `/argocd/applications/${appId}/refresh${hard ? '?hard=true' : ''}`;
  const res = await api.post<unknown>(url);
  return res.data;
}

export async function getArgoAppHistory(appId: string) {
  // Upstream returns either `{revisions:[...]}` or a bare array depending on
  // version. We coerce to a flat array.
  const res = await api.get<{ revisions?: ArgoAppHistoryEntry[] } | ArgoAppHistoryEntry[]>(
    `/argocd/applications/${appId}/history`,
  );
  if (Array.isArray(res.data)) return res.data;
  return res.data?.revisions ?? [];
}

export async function getArgoAppManifests(appId: string) {
  const res = await api.get<ArgoManifests>(`/argocd/applications/${appId}/manifests`);
  return res.data;
}

// --- AppProjects ---

export async function listArgoProjects(instanceId: string) {
  const res = await api.get<APIResponse<{ items?: ArgoProject[] }> | { items?: ArgoProject[] } | ArgoProject[]>(
    `/argocd/instances/${instanceId}/projects`,
  );
  const inner = unwrapEnvelope<{ items?: ArgoProject[] } | ArgoProject[]>(res.data);
  if (Array.isArray(inner)) return inner;
  return inner?.items ?? [];
}

export async function createArgoProject(instanceId: string, body: ArgoCreateProjectRequest) {
  const res = await api.post<ArgoProject>(`/argocd/instances/${instanceId}/projects`, body);
  return res.data;
}

export async function patchArgoProject(
  instanceId: string,
  name: string,
  patch: Record<string, unknown>,
) {
  const res = await api.patch<ArgoProject>(
    `/argocd/instances/${instanceId}/projects/${encodeURIComponent(name)}`,
    patch,
  );
  return res.data;
}

export async function deleteArgoProject(instanceId: string, name: string) {
  await api.delete(
    `/argocd/instances/${instanceId}/projects/${encodeURIComponent(name)}`,
  );
}

// --- ApplicationSets ---

export async function listArgoApplicationSets(instanceId: string) {
  const res = await api.get<APIResponse<{ items?: ArgoApplicationSet[] }> | { items?: ArgoApplicationSet[] } | ArgoApplicationSet[]>(
    `/argocd/instances/${instanceId}/applicationsets`,
  );
  const inner = unwrapEnvelope<{ items?: ArgoApplicationSet[] } | ArgoApplicationSet[]>(res.data);
  if (Array.isArray(inner)) return inner;
  return inner?.items ?? [];
}

export async function createArgoApplicationSet(
  instanceId: string,
  body: ArgoCreateApplicationSetRequest,
) {
  const res = await api.post<ArgoApplicationSet>(
    `/argocd/instances/${instanceId}/applicationsets`,
    body,
  );
  return res.data;
}

export async function deleteArgoApplicationSet(instanceId: string, name: string) {
  await api.delete(
    `/argocd/instances/${instanceId}/applicationsets/${encodeURIComponent(name)}`,
  );
}

// --- Managed clusters (Astronomer clusters registered into upstream ArgoCD) ---

export async function listArgoManagedClusters(instanceId: string) {
  const res = await api.get<APIResponse<ArgoManagedCluster[]> | ArgoManagedCluster[]>(
    `/argocd/instances/${instanceId}/clusters`,
  );
  return unwrapEnvelope<ArgoManagedCluster[]>(res.data) ?? [];
}

export async function getArgoOrphanReport(instanceId: string) {
  const res = await api.get<APIResponse<ArgoOrphanReport> | ArgoOrphanReport>(
    `/argocd/instances/${instanceId}/orphan-report`,
  );
  return unwrapEnvelope<ArgoOrphanReport>(res.data);
}

export async function registerArgoManagedCluster(
  instanceId: string,
  clusterId: string,
  body: ArgoManagedClusterRegisterRequest,
) {
  const res = await api.post<{ clusterId: string; argocdInstanceId: string; server: string }>(
    `/argocd/instances/${instanceId}/clusters/${clusterId}/register`,
    body,
  );
  return res.data;
}

export async function unregisterArgoManagedCluster(instanceId: string, clusterId: string) {
  await api.delete(
    `/argocd/instances/${instanceId}/clusters/${clusterId}/register`,
  );
}

export async function refreshArgoManagedClusterLabels(instanceId: string, clusterId: string) {
  const res = await api.post<APIResponse<ArgoManagedCluster> | ArgoManagedCluster>(
    `/argocd/instances/${instanceId}/clusters/${clusterId}/refresh-labels`,
  );
  return unwrapEnvelope<ArgoManagedCluster>(res.data);
}

export async function getArgoClusterOwnership(clusterId: string) {
  const res = await api.get<APIResponse<import('@/types').ArgoClusterOwnershipResponse>>(
    `/argocd/clusters/${clusterId}/ownership`,
  );
  return unwrapEnvelope<import('@/types').ArgoClusterOwnershipResponse>(res.data);
}

export async function setArgoClusterOwnershipDecision(
  clusterId: string,
  componentSlug: string,
  body: import('@/types').ArgoBaselineOwnershipDecisionRequest,
) {
  const res = await api.post<APIResponse<import('@/types').ArgoBaselineOwnershipDecision>>(
    `/argocd/clusters/${clusterId}/ownership/${componentSlug}/decision`,
    body,
  );
  return res.data.data;
}

// --- Repositories ---

export async function listArgoRepos(instanceId: string) {
  const res = await api.get<APIResponse<ArgoRepository[]> | ArgoRepository[]>(
    `/argocd/instances/${instanceId}/repos`,
  );
  return unwrapEnvelope<ArgoRepository[]>(res.data) ?? [];
}

export async function createArgoRepo(instanceId: string, body: ArgoRepositoryCreate) {
  const res = await api.post<ArgoRepository>(`/argocd/instances/${instanceId}/repos`, body);
  return res.data;
}

export async function deleteArgoRepo(instanceId: string, repoURL: string) {
  // The DELETE endpoint takes the repo URL as a query parameter to avoid
  // path-encoding pain on URLs that contain slashes.
  await api.delete(
    `/argocd/instances/${instanceId}/repos?repo=${encodeURIComponent(repoURL)}`,
  );
}

export async function testArgoRepo(instanceId: string, body: ArgoRepositoryCreate) {
  const res = await api.post<ArgoRepository>(
    `/argocd/instances/${instanceId}/repos/test`,
    body,
  );
  return res.data;
}

// --- Operations + reconciler ---

export async function listArgoOperations(params?: {
  targetType?: string;
  targetKey?: string;
  status?: string;
  limit?: number;
  offset?: number;
}) {
  // Server response is double-wrapped: the handler returns its own
  // `{data: [...], limit, offset}` pagination shape, then RespondJSON wraps
  // it once more in `{data: ...}`. Unwrap the outer envelope first, then
  // peel the inner `.data` to get the flat array.
  const res = await api.get<APIResponse<{ data: ArgoOperation[]; limit?: number; offset?: number }>>(
    '/argocd/operations',
    { params },
  );
  const inner = unwrapEnvelope<{ data?: ArgoOperation[] }>(res.data);
  return inner?.data ?? [];
}

export async function getArgoOperation(id: string) {
  const res = await api.get<ArgoOperation>(`/argocd/operations/${id}`);
  return res.data;
}

export async function listArgoApplicationsDB(params?: { limit?: number; offset?: number }) {
  // The DB-backed paginated list — used for the operations tab to map
  // app IDs back to names without re-fetching upstream.
  const res = await api.get<PaginatedResponse<{ id: string; name: string; argocdInstanceId: string }>>(
    '/argocd/applications',
    { params },
  );
  return res.data;
}

// ============================================================
// === Phase B4: Dex ===
// ============================================================
//
// Thin wrappers around /api/v1/auth/dex/*. The handler's responses are
// envelope-wrapped (`{ "data": ... }`) by RespondJSON, so we unwrap with
// `res.data.data` like the rest of this file. Outbound bodies for endpoints
// whose Go struct uses snake_case (settings, register-as-sso) are typed as
// snake_case and sent verbatim — the request interceptor does not rewrite
// outbound payloads.

import type {
  DexConnector,
  DexConnectorTypeSpec,
  DexConnectorWriteRequest,
  DexSettings,
  DexSettingsWriteRequest,
  DexApplyResponse,
  DexRegisterAsSSORequest,
  DexRegisterAsSSOResponse,
} from '@/types';

export async function getDexConnectorTypes() {
  const res = await api.get<APIResponse<DexConnectorTypeSpec[]>>('/auth/dex/connector-types');
  return res.data.data;
}

export async function getDexConnectors() {
  const res = await api.get<APIResponse<DexConnector[]>>('/auth/dex/connectors');
  return res.data.data;
}

export async function getDexConnector(id: string) {
  const res = await api.get<APIResponse<DexConnector>>(`/auth/dex/connectors/${id}`);
  return res.data.data;
}

export async function createDexConnector(data: DexConnectorWriteRequest) {
  // Backend expects snake_case for `display_name`. Build the body explicitly so
  // the contract is documented at the call site rather than implied by the type.
  const body = {
    type: data.type,
    name: data.name,
    display_name: data.displayName,
    config: data.config,
    enabled: data.enabled,
  };
  const res = await api.post<APIResponse<DexConnector>>('/auth/dex/connectors', body);
  return res.data.data;
}

export async function updateDexConnector(id: string, data: Partial<DexConnectorWriteRequest>) {
  const body: Record<string, unknown> = {};
  if (data.type !== undefined) body.type = data.type;
  if (data.name !== undefined) body.name = data.name;
  if (data.displayName !== undefined) body.display_name = data.displayName;
  if (data.config !== undefined) body.config = data.config;
  if (data.enabled !== undefined) body.enabled = data.enabled;
  const res = await api.patch<APIResponse<DexConnector>>(`/auth/dex/connectors/${id}`, body);
  return res.data.data;
}

export async function deleteDexConnector(id: string) {
  await api.delete(`/auth/dex/connectors/${id}`);
}

export async function getDexSettings() {
  const res = await api.get<APIResponse<DexSettings>>('/auth/dex/settings');
  return res.data.data;
}

export async function updateDexSettings(data: DexSettingsWriteRequest) {
  const res = await api.put<APIResponse<DexSettings>>('/auth/dex/settings', data);
  return res.data.data;
}

export async function applyDexConfig() {
  const res = await api.post<APIResponse<DexApplyResponse>>('/auth/dex/apply', {});
  return res.data.data;
}

export async function registerDexAsSSO(data: DexRegisterAsSSORequest) {
  const res = await api.post<APIResponse<DexRegisterAsSSOResponse>>('/auth/dex/register-as-sso', data);
  return res.data.data;
}

// === Phase B5: CIS scans ===
//
// CIS scans run via the cis-operator on the target cluster. The backend
// reflects upstream `ClusterScanReport` JSONB into our `security_scan_results`
// table. Three endpoints are split out from the legacy security routes:
//
//  - GET    /security/profiles/?cluster_id=…   — list scan profiles for a cluster
//  - GET    /security/scans/                   — paginated list (light shape)
//  - POST   /security/scans/                   — kick off a new scan
//  - GET    /security/scans/{id}/              — full scan with findings array
//  - GET    /security/scans/{id}/report.csv    — flattened CSV download

import type {
  CISProfilesResponse,
  CISScanDetail,
  CISScanListItem,
  CISScanCreatePayload,
} from '@/types';

/** List CIS profiles installed on the target cluster (or static fallback). */
export async function getCISProfiles(clusterId: string) {
  const res = await api.get<APIResponse<CISProfilesResponse>>('/security/profiles', {
    params: { cluster_id: clusterId },
  });
  return res.data.data;
}

/** Paginated list of historical CIS scans across all clusters. */
export async function getCISScans(params?: {
  page?: number;
  pageSize?: number;
  limit?: number;
  offset?: number;
}) {
  const res = await api.get<PaginatedResponse<CISScanListItem>>('/security/scans', { params });
  return res.data;
}

/** Full scan with parsed `findings`. Used by the detail page + polling loop. */
export async function getCISScan(id: string) {
  const res = await api.get<APIResponse<CISScanDetail>>(`/security/scans/${id}`);
  // The full-scan endpoint returns the raw object (not envelope) but the
  // axios interceptor doesn't care — we treat both shapes uniformly because
  // RespondJSON for this endpoint emits a flat object. Guard either way.
  const data = (res.data as unknown as APIResponse<CISScanDetail>).data
    ?? (res.data as unknown as CISScanDetail);
  return data as CISScanDetail;
}

/**
 * Trigger a new CIS scan. Profile is optional; the backend resolves it from
 * `cluster.distribution` when omitted. Returns the just-created `running`
 * row so callers can immediately route to its detail page.
 */
export async function createCISScan(payload: CISScanCreatePayload) {
  const res = await api.post<APIResponse<CISScanDetail>>('/security/scans', payload);
  // Same envelope-vs-flat handling as getCISScan.
  const data = (res.data as unknown as APIResponse<CISScanDetail>).data
    ?? (res.data as unknown as CISScanDetail);
  return data as CISScanDetail;
}

/** URL for the CSV export — used in `<a href download>` rather than fetch. */
export function cisScanReportCSVUrl(id: string): string {
  // The interceptor appends a trailing slash via the request hook only on
  // axios-issued URLs. We're building this for an `<a>`, so do it ourselves
  // to keep the same trailing-slash convention.
  return `${API_BASE_URL}/security/scans/${id}/report.csv`;
}

// === Phase B2: Velero backups ===
//
// Replacement client for the Velero-backed endpoints under
// `/api/v1/backups/`. The legacy `getBackupStorageConfigs / createBackup /
// ...` helpers higher up this file remain intact for any Phase B1 callers;
// new code should import the `b2*` symbols below. The handler emits
// snake_case JSON (`internal/handler/backups.go`); the response interceptor
// rewrites incoming keys to camelCase, so read shapes are camelCase but
// request bodies stay snake_case to match the Go `json:` tags exactly.

import type {
  BackupRestore,
  BackupRun,
  BackupScheduleRow,
  BackupStorageLocation,
  CreateBackupStorageRequest,
  CreateRestoreRequestB2,
  CreateScheduleRequestB2,
  TestStorageResult,
} from '@/types';

/** List storage locations (paginated). The backend returns a
 *  `{ data, total, page, page_size, total_pages }` envelope which we
 *  hand back as-is — same convention every other list endpoint uses. */
export async function b2ListStorageLocations(params?: {
  cluster_id?: string;
  page?: number;
  page_size?: number;
}) {
  const res = await api.get<PaginatedResponse<BackupStorageLocation>>(
    '/backups/storage',
    { params },
  );
  return res.data;
}

export async function b2GetStorageLocation(id: string): Promise<BackupStorageLocation> {
  const res = await api.get<APIResponse<BackupStorageLocation>>(`/backups/storage/${id}`);
  return res.data.data ?? (res.data as unknown as BackupStorageLocation);
}

export async function b2CreateStorageLocation(
  body: CreateBackupStorageRequest,
): Promise<BackupStorageLocation> {
  const res = await api.post<APIResponse<BackupStorageLocation>>('/backups/storage', body);
  return res.data.data ?? (res.data as unknown as BackupStorageLocation);
}

export async function b2UpdateStorageLocation(
  id: string,
  body: Partial<CreateBackupStorageRequest>,
): Promise<BackupStorageLocation> {
  const res = await api.put<APIResponse<BackupStorageLocation>>(
    `/backups/storage/${id}`,
    body,
  );
  return res.data.data ?? (res.data as unknown as BackupStorageLocation);
}

export async function b2DeleteStorageLocation(id: string): Promise<void> {
  await api.delete(`/backups/storage/${id}`);
}

/** Real SigV4 connection probe. Returns `{ success, message }` regardless of
 *  HTTP status — the backend never throws for an unreachable bucket. */
export async function b2TestStorageLocation(id: string): Promise<TestStorageResult> {
  const res = await api.post<APIResponse<TestStorageResult>>(`/backups/storage/${id}/test`);
  return res.data.data ?? (res.data as unknown as TestStorageResult);
}

// --- Schedules ---

export async function b2ListSchedules(params?: {
  cluster_id?: string;
  page?: number;
  page_size?: number;
}) {
  const res = await api.get<PaginatedResponse<BackupScheduleRow>>(
    '/backups/schedules',
    { params },
  );
  return res.data;
}

export async function b2GetSchedule(id: string): Promise<BackupScheduleRow> {
  const res = await api.get<APIResponse<BackupScheduleRow>>(`/backups/schedules/${id}`);
  return res.data.data ?? (res.data as unknown as BackupScheduleRow);
}

export async function b2CreateSchedule(
  body: CreateScheduleRequestB2,
): Promise<BackupScheduleRow> {
  const res = await api.post<APIResponse<BackupScheduleRow>>('/backups/schedules', body);
  return res.data.data ?? (res.data as unknown as BackupScheduleRow);
}

export async function b2UpdateSchedule(
  id: string,
  body: Partial<CreateScheduleRequestB2>,
): Promise<BackupScheduleRow> {
  const res = await api.put<APIResponse<BackupScheduleRow>>(
    `/backups/schedules/${id}`,
    body,
  );
  return res.data.data ?? (res.data as unknown as BackupScheduleRow);
}

export async function b2DeleteSchedule(id: string): Promise<void> {
  await api.delete(`/backups/schedules/${id}`);
}

/** Manual one-off trigger. The backend creates a Backup CR derived from the
 *  schedule's namespace selectors and returns the new run row. */
export async function b2TriggerScheduleNow(id: string): Promise<BackupRun> {
  const res = await api.post<APIResponse<BackupRun>>(
    `/backups/schedules/${id}/trigger-now`,
  );
  return res.data.data ?? (res.data as unknown as BackupRun);
}

// --- Runs (backups themselves) ---

export async function b2ListRuns(params?: {
  cluster_id?: string;
  schedule_id?: string;
  status?: string;
  page?: number;
  page_size?: number;
}) {
  const res = await api.get<PaginatedResponse<BackupRun>>('/backups', { params });
  return res.data;
}

export async function b2GetRun(id: string): Promise<BackupRun> {
  const res = await api.get<APIResponse<BackupRun>>(`/backups/${id}`);
  return res.data.data ?? (res.data as unknown as BackupRun);
}

// --- Restores ---

export async function b2ListRestores(params?: { page?: number; page_size?: number }) {
  const res = await api.get<PaginatedResponse<BackupRestore>>('/backups/restores', {
    params,
  });
  return res.data;
}

export async function b2GetRestore(id: string): Promise<BackupRestore> {
  const res = await api.get<APIResponse<BackupRestore>>(`/backups/restores/${id}`);
  return res.data.data ?? (res.data as unknown as BackupRestore);
}

/** Create a restore from a backup. Maps to `POST /backups/{backup_id}/restore`
 *  — the path-scoped form is what the Go handler accepts; we strip the id out
 *  of the payload before posting. */
export async function b2CreateRestore(
  body: CreateRestoreRequestB2,
): Promise<BackupRestore> {
  const { backup_id, ...payload } = body;
  const res = await api.post<APIResponse<BackupRestore>>(
    `/backups/${backup_id}/restore`,
    payload,
  );
  return res.data.data ?? (res.data as unknown as BackupRestore);
}

// ============================================================
// Settings hub (platform, smtp, webhooks, quotas, group mappings,
// compliance, backup drill) — see lib/api/settings.ts.
// ============================================================
export * from './api/settings';

// ============================================================
// Project detail tabs (policy, cloud credentials, effective
// quota) and the top-level cluster-templates surface — see
// lib/api/project-detail.ts. Consumers can also import directly
// from '@/lib/api/project-detail' to skip the re-export hop.
// ============================================================
export * from './api/project-detail';

// ============================================================
// Account security (TOTP, password reset, admin user actions,
// logout-with-redirect) — see lib/api/account-security.ts.
// Consumers can also import directly from
// '@/lib/api/account-security' to skip the re-export hop.
// ============================================================
export * from './api/account-security';

// ============================================================
// Admin security diagnostics (F-05): key-status + shell-session
// audit views. See lib/api/admin-security.ts.
// ============================================================
export * from './api/admin-security';

// ============================================================
// SIEM forwarders (F-05): external syslog / Splunk HEC / NDJSON
// destinations + per-forwarder status. See lib/api/siem-forwarders.ts.
// ============================================================
export * from './api/siem-forwarders';

// ============================================================
// SCIM provisioning tokens (F-05): mint / list / revoke. See
// lib/api/scim-tokens.ts.
// ============================================================
export * from './api/scim-tokens';

// ============================================================
// Alertmanager-style inhibition rules (P-03). See
// lib/api/alerting-inhibitions.ts.
// ============================================================
export * from './api/alerting-inhibitions';

// ============================================================
// Gatekeeper / OPA constraint authoring (P-04). See
// lib/api/gatekeeper-constraints.ts.
// ============================================================
export * from './api/gatekeeper-constraints';

// ============================================================
// Cluster groups (migration 066) — operator-defined folder
// hierarchy over clusters. See lib/api/cluster-groups.ts.
// ============================================================
export * from './api/cluster-groups';

// ============================================================
// UI extensions — manifest validation and registry controls.
// ============================================================
export * from './api/extensions';

// ============================================================
// Fleet operations (DIR-01) — bulk fanout of tool/template/token
// operations across the cluster fleet. See lib/api/fleet-operations.ts.
// ============================================================
export * from './api/fleet-operations';
