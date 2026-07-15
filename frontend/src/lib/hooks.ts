import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useRef, useState } from 'react';
import * as apiClient from './api';
import type {
  ClusterRegistration,
  PodLog,
  PolicyRule,
  RoleBindingSubject,
  User,
  AlertRule,
  NotificationChannel,
  AlertSilence,
  LoggingOutput,
  LoggingPipeline,
  LoggingOperation,
  PersistentVolumeClaim,
  Project,
} from '@/types';
import type { AuditLogQueryParams, GeneralSettings } from './api';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { liveFallback } from '@/lib/live/status-store';

// Query key factory lives in ./query-keys.ts (single source of truth).
// Imported here and re-exported so existing `import { queryKeys } from '@/lib/hooks'`
// call sites keep working.
import { queryKeys } from './query-keys';
export { queryKeys };

export function useFeatureFlags() {
  return useQuery({
    queryKey: queryKeys.featureFlags,
    queryFn: () => apiClient.getFeatureFlags(),
    staleTime: 30_000,
  });
}

// ============================================================
// Cluster Hooks
// ============================================================

export function useClusters(params?: {
  status?: string;
  provider?: string;
  environment?: string;
  search?: string;
  page?: number;
  pageSize?: number;
}) {
  return useQuery({
    queryKey: queryKeys.clusters.list(params),
    queryFn: () => apiClient.getClusters(params),
    // Poll only while the live bus is down; events drive freshness when open.
    refetchInterval: liveFallback(30000),
  });
}

export function useCluster(id: string) {
  return useQuery({
    queryKey: queryKeys.clusters.detail(id),
    queryFn: () => apiClient.getCluster(id),
    enabled: !!id,
    refetchInterval: liveFallback(15000),
  });
}

export function useClusterNodes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.nodes(clusterId),
    queryFn: () => apiClient.getClusterNodes(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30000),
  });
}

// The health-check worker reconciles conditions every 60s; while the stream
// is open the `cluster.status_changed` + heartbeat routes refresh this
// (P4.9) and the poll only backstops a dropped stream.
export function useClusterConditions(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.conditions(clusterId),
    queryFn: () => apiClient.getClusterConditions(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(60000),
  });
}

// Sprint 086 — remediation history feeds the "Last action" footer
// under the condition pills. Routed from the same status/heartbeat
// events as conditions (P4.9); the poll backstops a dropped stream.
export function useClusterConditionRemediation(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.conditionRemediation(clusterId),
    queryFn: () => apiClient.getClusterConditionRemediation(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30000),
  });
}

export function useNodeDetail(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: queryKeys.clusters.nodeDetail(clusterId, nodeName),
    queryFn: () => apiClient.getNodeDetail(clusterId, nodeName),
    enabled: !!clusterId && !!nodeName,
    refetchInterval: liveFallback(30000),
  });
}

export function useClusterNamespaces(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.namespaces(clusterId),
    queryFn: () => apiClient.getClusterNamespaces(clusterId),
    enabled: !!clusterId,
  });
}

export function useClusterEvents(clusterId: string, params?: { type?: string; limit?: number }) {
  return useQuery({
    queryKey: queryKeys.clusters.events(clusterId, params as Record<string, unknown> | undefined),
    queryFn: () => apiClient.getClusterEvents(clusterId, params),
    enabled: !!clusterId,
    refetchInterval: liveFallback(15000),
  });
}

export function useClusterPods(clusterId: string, params?: { namespace?: string }) {
  return useQuery({
    queryKey: queryKeys.clusters.pods(clusterId, params),
    queryFn: () => apiClient.getClusterPods(clusterId, params),
    enabled: !!clusterId,
    refetchInterval: liveFallback(15000),
  });
}

export function useCreateCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: ClusterRegistration) => apiClient.createCluster(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      toastSuccess('Cluster registration initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to register cluster', error);
    },
  });
}

export function useUpdateCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<ClusterRegistration> }) =>
      apiClient.updateCluster(id, data),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.detail(variables.id) });
      toastSuccess('Cluster updated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to update cluster', error);
    },
  });
}

export function useTakeoverClusterOwnership() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.takeoverClusterOwnership(id),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.detail(id) });
      toastSuccess('Cluster ownership transferred');
    },
    onError: (error: Error) => {
      toastApiError('Failed to transfer cluster ownership', error);
    },
  });
}

export function useDeleteCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (arg: string | { id: string; force?: boolean }) => {
      const { id, force } = typeof arg === 'string' ? { id: arg, force: false } : arg;
      return apiClient.deleteCluster(id, { force });
    },
    onSuccess: () => {
      // Decommission is async: DELETE returns 202 and the worker tombstones the
      // row when cleanup finishes (instant for a disconnected agent's record;
      // up to the grace window if it waits for an agent to reconnect). The row
      // STAYS in the list, flagged `decommissioning`, so the dashboard shows a
      // stable "Decommissioning" badge — no optimistic hide/re-show flicker. It
      // drops out on its own once tombstoned: `cluster.deleted` +
      // `fleet_operation.changed` events cover the tombstone window (P4.5),
      // so a single refresh here just makes the badge appear immediately.
      toastSuccess('Cluster decommissioning started');
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete cluster', error);
    },
  });
}

export function useDeletePod() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, namespace, name }: { clusterId: string; namespace: string; name: string }) =>
      apiClient.deletePod(clusterId, namespace, name),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.podsAll(variables.clusterId) });
      toastSuccess('Pod deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete pod', error);
    },
  });
}

// ============================================================
// Workload Hooks
// ============================================================

export function useWorkloads(
  clusterId: string,
  params?: { namespace?: string; kind?: string; search?: string; page?: number; pageSize?: number }
) {
  return useQuery({
    queryKey: queryKeys.workloads.list(clusterId, params),
    queryFn: () => apiClient.getWorkloads(clusterId, params),
    enabled: !!clusterId,
    refetchInterval: liveFallback(15000),
  });
}

export function useWorkload(clusterId: string, kind: string, namespace: string, name: string) {
  return useQuery({
    queryKey: queryKeys.workloads.detail(clusterId, kind, namespace, name),
    queryFn: () => apiClient.getWorkload(clusterId, kind, namespace, name),
    enabled: !!clusterId && !!kind && !!namespace && !!name,
    refetchInterval: liveFallback(10000),
  });
}

export function useWorkloadPods(clusterId: string, kind: string, namespace: string, name: string) {
  return useQuery({
    queryKey: queryKeys.workloads.pods(clusterId, kind, namespace, name),
    queryFn: () => apiClient.getWorkloadPods(clusterId, kind, namespace, name),
    enabled: !!clusterId && !!kind && !!namespace && !!name,
    refetchInterval: liveFallback(10000),
  });
}

export function useScaleWorkload() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      clusterId: string;
      kind: string;
      namespace: string;
      name: string;
      replicas: number;
    }) => apiClient.scaleWorkload(params.clusterId, params.kind, params.namespace, params.name, params.replicas),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.workloads.detail(
          variables.clusterId,
          variables.kind,
          variables.namespace,
          variables.name
        ),
      });
      toastSuccess(`Scaled to ${variables.replicas} replicas`);
    },
    onError: (error: Error) => {
      toastApiError('Failed to scale workload', error);
    },
  });
}

export function useRestartWorkload() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: {
      clusterId: string;
      kind: string;
      namespace: string;
      name: string;
    }) => apiClient.restartWorkload(params.clusterId, params.kind, params.namespace, params.name),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.workloads.detail(
          variables.clusterId,
          variables.kind,
          variables.namespace,
          variables.name
        ),
      });
      toastSuccess('Workload restart initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to restart workload', error);
    },
  });
}

// ============================================================
// Pod Logs Hook (with streaming)
// ============================================================

export type PodLogsStatus =
  | 'connecting'
  | 'streaming'
  | 'disconnected'
  | 'idle';

export function usePodLogs(
  clusterId: string,
  namespace: string,
  pod: string,
  params?: {
    container?: string;
    tailLines?: number;
    // sinceSeconds enables Rancher-style time-window queries. When set, it
    // takes precedence over tailLines (both can be passed, but the UI
    // picker chooses exactly one mode).
    sinceSeconds?: number;
    // noTail disables both the line-count and time-window limits ("All").
    // We need an explicit flag because `tailLines === undefined` already
    // means "use the default of 500"; without this we can't represent
    // "give me everything."
    noTail?: boolean;
    follow?: boolean;
  }
) {
  const [streamLogs, setStreamLogs] = useState<PodLog[]>([]);
  // Exposes WS connection state so consumers (e.g. the window-manager tab
  // strip) can show a pill without owning the WS lifecycle themselves.
  const [status, setStatus] = useState<PodLogsStatus>('idle');
  const cleanupRef = useRef<(() => void) | null>(null);
  // De-duplicate toasts: if the WS errors mid-stream we only want one toast
  // per (pod, container) selection rather than one per reconnect/dropped
  // frame.
  const errorShownRef = useRef(false);

  // Resolve the effective query params once so the initial fetch, the
  // streaming useEffect, and the queryKey all agree. The precedence is:
  //   noTail  -> no limit at all
  //   sinceSeconds set -> time-window mode (kubelet sinceSeconds)
  //   tailLines set -> line-count mode
  //   otherwise -> default 500 lines
  const effectiveTailLines = params?.noTail
    ? undefined
    : params?.sinceSeconds && params.sinceSeconds > 0
      ? undefined
      : params?.tailLines ?? 500;
  const effectiveSinceSeconds = params?.noTail
    ? undefined
    : params?.sinceSeconds && params.sinceSeconds > 0
      ? params.sinceSeconds
      : undefined;

  // Initial fetch — include tail/since in the queryKey so flipping modes
  // refetches instead of serving the previous mode's cache.
  const query = useQuery({
    queryKey: queryKeys.podLogsFetch(
      clusterId,
      namespace,
      pod,
      params?.container,
      effectiveTailLines ?? 'no-tail',
      effectiveSinceSeconds ?? 'no-since'
    ),
    queryFn: () =>
      apiClient.getPodLogs(clusterId, namespace, pod, {
        container: params?.container,
        tailLines: effectiveTailLines,
        sinceSeconds: effectiveSinceSeconds,
      }),
    enabled: !!clusterId && !!namespace && !!pod,
  });

  // Reset the streaming buffer when the pod or container changes. Without
  // this, switching pods would show the old pod's tail of stream lines
  // prepended to the new pod's REST fetch — a real correctness bug that
  // also leaked memory across switches.
  useEffect(() => {
    setStreamLogs([]);
    errorShownRef.current = false;
  }, [clusterId, namespace, pod, params?.container]);

  // Streaming
  useEffect(() => {
    if (!params?.follow || !clusterId || !namespace || !pod) {
      setStatus('idle');
      return;
    }

    setStatus('connecting');
    const cleanup = apiClient.streamPodLogs(
      clusterId,
      namespace,
      pod,
      params?.container || '',
      (log) => {
        setStatus('streaming');
        setStreamLogs((prev) => [...prev.slice(-2000), log]);
      },
      (err) => {
        setStatus('disconnected');
        // Surface the first error per stream so the user knows the live
        // tail dropped — but suppress duplicates so a flapping agent
        // doesn't spam the corner of the screen.
        if (!errorShownRef.current) {
          errorShownRef.current = true;
          toastApiError('Log stream', err);
        }
      },
      {
        follow: true,
        // The REST query above already returned the historical tail (up to
        // 500 lines / the since-window). Opening the follow stream with the
        // same tail/since would replay all of those lines again, duplicating
        // the whole buffer on every open. Ask the backend for NO backfill
        // (tail_lines=0) and only lines newer than ~now (since=1s) so the
        // stream contributes strictly NEW output on top of the REST fetch.
        tailLines: 0,
        sinceSeconds: 1,
      }
    );

    cleanupRef.current = cleanup;

    return () => {
      cleanup();
      cleanupRef.current = null;
      setStatus('idle');
    };
  }, [
    clusterId,
    namespace,
    pod,
    params?.container,
    params?.follow,
    effectiveTailLines,
    effectiveSinceSeconds,
  ]);

  // Always surface the streamed tail in `allLogs`, even after the caller
  // turned follow off. The previous behavior dropped streamLogs entirely
  // when follow=false, which made the live tail vanish the instant the
  // user clicked Pause — they thought the button had failed. "Pause"
  // should stop *accumulating* new lines (the streaming useEffect tears
  // down when follow is false) without losing what was already on
  // screen.
  const allLogs = [...(query.data || []), ...streamLogs];

  const stopStreaming = useCallback(() => {
    cleanupRef.current?.();
    cleanupRef.current = null;
  }, []);

  // Forward query fields explicitly rather than spreading the whole result —
  // spreading a TanStack Query result subscribes the consumer to every field
  // and defeats its fine-grained re-render tracking (@tanstack/query/no-rest-destructuring).
  return {
    data: allLogs,
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error,
    refetch: query.refetch,
    streamLogs,
    stopStreaming,
    status,
  };
}

// ============================================================
// Metrics Hooks
// ============================================================

export function useClusterMetrics(clusterId: string, range?: string) {
  return useQuery({
    queryKey: queryKeys.clusters.metrics(clusterId, range),
    queryFn: () => apiClient.getClusterMetrics(clusterId, { range }),
    enabled: !!clusterId,
    // While the stream is open, `cluster.metrics` ticks invalidate the
    // per-cluster metrics prefix (see lib/live/routes.ts).
    refetchInterval: liveFallback(60000),
  });
}

export function useClusterMetricsSummary(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.metricsSummary(clusterId),
    queryFn: () => apiClient.getClusterMetricsSummary(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30000),
  });
}

export function useWorkloadMetrics(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  range?: string
) {
  return useQuery({
    queryKey: queryKeys.workloads.metrics(clusterId, kind, namespace, name, range),
    queryFn: () => apiClient.getWorkloadMetrics(clusterId, kind, namespace, name, { range }),
    enabled: !!clusterId && !!kind && !!namespace && !!name,
    // No per-workload metrics event exists; while the stream is open this
    // refreshes on the cluster's Pod/workload `cluster.k8s_changed` churn
    // (routed through the per-cluster workloads prefix) and on stream
    // transitions.
    refetchInterval: liveFallback(60000),
  });
}

// ============================================================
// ArgoCD Hooks
// ============================================================

export function useArgoInstances(clusterId?: string) {
  return useQuery({
    queryKey: queryKeys.argocd.instances(clusterId),
    queryFn: () => apiClient.getArgoInstances(clusterId),
  });
}

export function useArgoApplications(params?: { clusterId?: string; project?: string; search?: string }) {
  return useQuery({
    queryKey: queryKeys.argocd.applications(params),
    queryFn: () => apiClient.getArgoApplications(params),
    refetchInterval: liveFallback(30000),
  });
}

export function useSyncArgoApp() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { instanceId: string; appName: string }) =>
      apiClient.syncArgoApplication(params.instanceId, params.appName),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.all });
      toastSuccess('Sync initiated');
    },
    onError: (error: Error) => {
      toastApiError('Sync failed', error);
    },
  });
}

// ============================================================
// RBAC Hooks
// ============================================================

export function useGlobalRoles() {
  return useQuery({
    queryKey: queryKeys.rbac.globalRoles,
    queryFn: () => apiClient.getGlobalRoles(),
  });
}

export function useClusterRoles(clusterId?: string) {
  return useQuery({
    queryKey: queryKeys.rbac.clusterRoles(clusterId),
    queryFn: () => apiClient.getClusterRoles(clusterId),
  });
}

export function useProjectRoles(projectId?: string) {
  return useQuery({
    queryKey: queryKeys.rbac.projectRoles(projectId),
    queryFn: () => apiClient.getProjectRoles(projectId),
  });
}

export function useRoleBindings(params?: { roleType?: string; scope?: string }) {
  return useQuery({
    queryKey: queryKeys.rbac.bindings(params),
    queryFn: () => apiClient.getRoleBindings(params),
  });
}

export function useMyEffectivePermissions(params?: apiClient.EffectivePermissionParams) {
  return useQuery({
    queryKey: queryKeys.rbac.myPermissions(params),
    queryFn: () => apiClient.getMyEffectivePermissions(params),
  });
}

export function useCreateRole() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      scope: 'global' | 'cluster' | 'project';
      name: string;
      displayName: string;
      description?: string;
      rules: PolicyRule[];
    }) => {
      const { scope, ...roleData } = data;
      switch (scope) {
        case 'global':
          return apiClient.createGlobalRole(roleData);
        case 'cluster':
          return apiClient.createClusterRole(roleData);
        case 'project':
          return apiClient.createProjectRole(roleData);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess('Role created successfully');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create role', error);
    },
  });
}

export function useCreateRoleBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      roleName: string;
      roleType: string;
      subjects: RoleBindingSubject[];
      scope?: { clusterId?: string; projectId?: string };
    }) => apiClient.createRoleBinding(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess('Role binding created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create role binding', error);
    },
  });
}

// --- Cluster role bindings (namespace-scoped authoring) ---

export function useClusterRoleBindings(params?: { cluster_id?: string }) {
  return useQuery({
    queryKey: queryKeys.rbac.clusterRoleBindings(params),
    queryFn: () => apiClient.listClusterRoleBindings(params),
  });
}

export function useCreateClusterRoleBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      user_id: string;
      role_id: string;
      cluster_id: string;
      namespace?: string;
    }) => apiClient.createClusterRoleBinding(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess('Cluster role binding created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create cluster role binding', error);
    },
  });
}

export function useDeleteClusterRoleBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteClusterRoleBinding(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess('Cluster role binding revoked');
    },
    onError: (error: Error) => {
      toastApiError('Failed to revoke cluster role binding', error);
    },
  });
}

// ============================================================
// User Hooks
// ============================================================

export function useCurrentUser() {
  return useQuery({
    queryKey: queryKeys.users.current,
    queryFn: () => apiClient.getCurrentUser(),
    retry: false,
    staleTime: 5 * 60 * 1000,
  });
}

export function useUsers(params?: { search?: string; page?: number; pageSize?: number }) {
  return useQuery({
    queryKey: queryKeys.users.list(params),
    queryFn: () => apiClient.getUsers(params),
  });
}

// ============================================================
// Settings Hooks
// ============================================================

export function useGeneralSettings() {
  return useQuery({
    queryKey: queryKeys.settings.general,
    queryFn: () => apiClient.getGeneralSettings(),
  });
}

export function useSaveGeneralSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: GeneralSettings) => apiClient.saveGeneralSettings(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.general });
      toastSuccess('Settings saved successfully');
    },
    onError: (error: Error) => {
      toastApiError('Failed to save settings', error);
    },
  });
}

export function useSSOProviders() {
  return useQuery({
    queryKey: queryKeys.settings.sso,
    queryFn: () => apiClient.getSSOProviders(),
  });
}

export function useCreateSSOProvider() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
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
    }) => apiClient.createSSOProvider(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.sso });
      toastSuccess('SSO provider created successfully');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create SSO provider', error);
    },
  });
}

export function useAPITokens() {
  return useQuery({
    queryKey: queryKeys.settings.tokens,
    queryFn: () => apiClient.getAPITokens(),
  });
}

export function useCreateAPIToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: { name: string; description?: string; expiresInDays?: number }) =>
      apiClient.createAPIToken(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.tokens });
      toastSuccess('API token created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create token', error);
    },
  });
}

export function useDeleteAPIToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteAPIToken(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.tokens });
      toastSuccess('API token deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete token', error);
    },
  });
}

export function useAuditLogs(params?: AuditLogQueryParams, options?: { enabled?: boolean }) {
  return useQuery({
    queryKey: queryKeys.settings.auditLogs(params),
    queryFn: () => apiClient.getAuditLogs(params),
    enabled: options?.enabled,
  });
}

// ============================================================
// Activity Feed Hook
// ============================================================

export function useActivityFeed(limit: number = 20) {
  return useQuery({
    queryKey: queryKeys.activity(limit),
    queryFn: () => apiClient.getActivityFeed({ limit }),
    // `audit.*` events refresh this while the stream is open. Restricted
    // users never receive them (no cluster_id → SEC-R07 fail-closed drop);
    // they heal via this fallback poll + the reconnect bulk invalidation.
    refetchInterval: liveFallback(30000),
  });
}

// ============================================================
// Alerting Hooks
// ============================================================

export function useAlertRules() {
  return useQuery({
    queryKey: queryKeys.alerting.rules,
    queryFn: () => apiClient.getAlertRules(),
    // `alerting.changed` (kind: rule) refreshes this while the stream is open.
    refetchInterval: liveFallback(30000),
  });
}

export function useCreateAlertRule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<AlertRule>) => apiClient.createAlertRule(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.rules });
      toastSuccess('Alert rule created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create alert rule', error);
    },
  });
}

export function useUpdateAlertRule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<AlertRule> }) =>
      apiClient.updateAlertRule(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.rules });
      toastSuccess('Alert rule updated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to update alert rule', error);
    },
  });
}

export function useDeleteAlertRule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteAlertRule(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.rules });
      toastSuccess('Alert rule deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete alert rule', error);
    },
  });
}

export function useAlertEvents(params?: Record<string, string>) {
  return useQuery({
    queryKey: queryKeys.alerting.events(params),
    queryFn: () => apiClient.getAlertEvents(params),
    // `alerting.changed` (kind: event) covers API-side ack/resolve AND the
    // worker evaluator's fire/resolve writes (Redis-attached runtime bus).
    refetchInterval: liveFallback(15000),
  });
}

export function useAcknowledgeAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.acknowledgeAlert(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.all });
      toastSuccess('Alert acknowledged');
    },
    onError: (error: Error) => {
      toastApiError('Failed to acknowledge alert', error);
    },
  });
}

export function useResolveAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.resolveAlert(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.all });
      toastSuccess('Alert resolved');
    },
    onError: (error: Error) => {
      toastApiError('Failed to resolve alert', error);
    },
  });
}

export function useNotificationChannels() {
  return useQuery({
    queryKey: queryKeys.alerting.channels,
    queryFn: () => apiClient.getNotificationChannels(),
  });
}

export function useCreateNotificationChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<NotificationChannel>) => apiClient.createNotificationChannel(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.channels });
      toastSuccess('Notification channel created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create notification channel', error);
    },
  });
}

export function useTestNotificationChannel() {
  return useMutation({
    mutationFn: (id: string) => apiClient.testNotificationChannel(id),
    onSuccess: (data) => {
      if (data.success) {
        toastSuccess('Test notification sent successfully');
      } else {
        toastApiError('Test failed', data.message);
      }
    },
    onError: (error: Error) => {
      toastApiError('Test failed', error);
    },
  });
}

export function useAlertSilences() {
  return useQuery({
    queryKey: queryKeys.alerting.silences,
    queryFn: () => apiClient.getAlertSilences(),
    // `alerting.changed` (kind: silence) refreshes this while the stream is open.
    refetchInterval: liveFallback(30000),
  });
}

export function useCreateAlertSilence() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<AlertSilence>) => apiClient.createAlertSilence(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.silences });
      toastSuccess('Silence created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create silence', error);
    },
  });
}

// ============================================================
// Anomaly Baseline Hooks (sprint 072, read-only)
// ============================================================

export function useAnomalyBaselines(params?: { clusterId?: string; limit?: number; offset?: number }) {
  return useQuery({
    queryKey: queryKeys.anomalyBaselines.list(params),
    queryFn: () => apiClient.getAnomalyBaselines(params),
    // The recompute worker publishes `alerting.changed` (kind: baseline)
    // once per cluster per 5m pass; the poll backstops a dropped stream.
    refetchInterval: liveFallback(30000),
  });
}

export function useAnomalyBaseline(id: string) {
  return useQuery({
    queryKey: queryKeys.anomalyBaselines.detail(id),
    queryFn: () => apiClient.getAnomalyBaseline(id),
    enabled: Boolean(id),
  });
}

// ============================================================
// Logging Hooks
// ============================================================

export function useLoggingOutputs() {
  return useQuery({
    queryKey: queryKeys.logging.outputs,
    queryFn: () => apiClient.getLoggingOutputs(),
  });
}

export function useCreateLoggingOutput() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<LoggingOutput>) => apiClient.createLoggingOutput(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.outputs });
      toastSuccess('Logging output created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create logging output', error);
    },
  });
}

export function useTestLoggingOutput() {
  return useMutation({
    mutationFn: (id: string) => apiClient.testLoggingOutput(id),
    onSuccess: (data) => {
      if (data.success) {
        toastSuccess('Test connection successful');
      } else {
        toastApiError('Test failed', data.message);
      }
    },
    onError: (error: Error) => {
      toastApiError('Test failed', error);
    },
  });
}

export function useLoggingPipelines() {
  return useQuery({
    queryKey: queryKeys.logging.pipelines,
    queryFn: () => apiClient.getLoggingPipelines(),
  });
}

export function useCreateLoggingPipeline() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<LoggingPipeline>) => apiClient.createLoggingPipeline(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.pipelines });
      toastSuccess('Logging pipeline created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create logging pipeline', error);
    },
  });
}

// --- Logging Operations (controller-backed reconciler) ---

export function useLoggingOperations(params?: {
  status?: string;
  target_type?: string;
  limit?: number;
  offset?: number;
}) {
  return useQuery<LoggingOperation[]>({
    queryKey: queryKeys.logging.operations(params),
    queryFn: () => apiClient.getLoggingOperations(params),
    // `logging_operation.changed` drives freshness while the stream is open;
    // poll so pending -> running -> completed transitions still appear when
    // it is down.
    refetchInterval: liveFallback(5000),
  });
}

export function useLoggingOperation(id: string) {
  return useQuery<LoggingOperation>({
    queryKey: queryKeys.logging.operation(id),
    queryFn: () => apiClient.getLoggingOperation(id),
    enabled: !!id,
    refetchInterval: liveFallback(5000),
  });
}

export function useRetryLoggingOperation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.retryLoggingOperation(id),
    onSuccess: () => {
      // Invalidate every cached list (parameterized keys) and the detail rows.
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.operationsAll });
      toastSuccess('Operation retry queued');
    },
    onError: (error: Error) => {
      toastApiError('Failed to retry operation', error);
    },
  });
}

// ============================================================
// Storage Hooks
// ============================================================

export function usePersistentVolumes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.storage.pvs(clusterId),
    queryFn: () => apiClient.getPersistentVolumes(clusterId),
    enabled: !!clusterId,
  });
}

export function usePersistentVolumeClaims(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.storage.pvcs(clusterId),
    queryFn: () => apiClient.getPersistentVolumeClaims(clusterId),
    enabled: !!clusterId,
  });
}

export function useStorageClasses(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.storage.storageClasses(clusterId),
    queryFn: () => apiClient.getStorageClasses(clusterId),
    enabled: !!clusterId,
  });
}

export function useCreatePVC() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, data }: { clusterId: string; data: Partial<PersistentVolumeClaim> }) =>
      apiClient.createPersistentVolumeClaim(clusterId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      toastSuccess('PVC created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create PVC', error);
    },
  });
}

export function useDeletePVC() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, namespace, name }: { clusterId: string; namespace: string; name: string }) =>
      apiClient.deletePersistentVolumeClaim(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      toastSuccess('PVC deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete PVC', error);
    },
  });
}

export function useDeletePV() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, name }: { clusterId: string; name: string }) =>
      apiClient.deletePersistentVolume(clusterId, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      toastSuccess('PV deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete PV', error);
    },
  });
}

// ============================================================
// Networking Hooks
// ============================================================

export function useServices(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.services(clusterId),
    queryFn: () => apiClient.getServices(clusterId),
    enabled: !!clusterId,
  });
}

export function useIngresses(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.ingresses(clusterId),
    queryFn: () => apiClient.getIngresses(clusterId),
    enabled: !!clusterId,
  });
}

export function useNetworkPolicies(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.networkPolicies(clusterId),
    queryFn: () => apiClient.getNetworkPolicies(clusterId),
    enabled: !!clusterId,
  });
}

export function useDeleteService() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, namespace, name }: { clusterId: string; namespace: string; name: string }) =>
      apiClient.deleteService(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      toastSuccess('Service deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete service', error);
    },
  });
}

export function useDeleteIngress() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, namespace, name }: { clusterId: string; namespace: string; name: string }) =>
      apiClient.deleteIngress(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      toastSuccess('Ingress deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete ingress', error);
    },
  });
}

export function useDeleteNetworkPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, namespace, name }: { clusterId: string; namespace: string; name: string }) =>
      apiClient.deleteNetworkPolicy(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      toastSuccess('Network policy deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete network policy', error);
    },
  });
}

// ============================================================
// Gateway API Hooks
// ============================================================
//
// Read hooks only. Deletes and YAML edits in callers go through the generic
// useK8sDelete / useK8sApplyYaml above (with k8sResourcePath from
// lib/k8s-paths.ts), so we don't need per-resource mutations here.

export function useGateways(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.gateways(clusterId),
    queryFn: () => apiClient.getGateways(clusterId),
    enabled: !!clusterId,
  });
}

export function useHTTPRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.httpRoutes(clusterId),
    queryFn: () => apiClient.getHTTPRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useGatewayClasses(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.gatewayClasses(clusterId),
    queryFn: () => apiClient.getGatewayClasses(clusterId),
    enabled: !!clusterId,
  });
}

export function useGRPCRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.grpcRoutes(clusterId),
    queryFn: () => apiClient.getGRPCRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useTLSRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.tlsRoutes(clusterId),
    queryFn: () => apiClient.getTLSRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useTCPRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.tcpRoutes(clusterId),
    queryFn: () => apiClient.getTCPRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useUDPRoutes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.udpRoutes(clusterId),
    queryFn: () => apiClient.getUDPRoutes(clusterId),
    enabled: !!clusterId,
  });
}

export function useReferenceGrants(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.networking.referenceGrants(clusterId),
    queryFn: () => apiClient.getReferenceGrants(clusterId),
    enabled: !!clusterId,
  });
}

// ============================================================
// Project Hooks
// ============================================================

export function useProjects(params?: { search?: string; page?: number; pageSize?: number }) {
  return useQuery({
    queryKey: queryKeys.projects.list(params),
    queryFn: () => apiClient.getProjects(params),
  });
}

export function useProject(id: string) {
  return useQuery({
    queryKey: queryKeys.projects.detail(id),
    queryFn: () => apiClient.getProject(id),
    enabled: !!id,
  });
}

export function useCreateProject() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<Project>) => apiClient.createProject(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all });
      toastSuccess('Project created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create project', error);
    },
  });
}

export function useDeleteProject() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteProject(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all });
      toastSuccess('Project deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete project', error);
    },
  });
}

export function useTakeoverProjectOwnership() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.takeoverProjectOwnership(id),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.detail(id) });
      toastSuccess('Project ownership transferred');
    },
    onError: (error: Error) => {
      toastApiError('Failed to transfer project ownership', error);
    },
  });
}

// ============================================================
// User Management Hooks
// ============================================================

export function useCreateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      username: string;
      email: string;
      displayName: string;
      password: string;
      globalRoles: string[];
    }) => apiClient.createUser(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.users.all });
      toastSuccess('User created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create user', error);
    },
  });
}

export function useUpdateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<User> }) =>
      apiClient.updateUser(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.users.all });
      toastSuccess('User updated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to update user', error);
    },
  });
}

export function useDeleteUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteUser(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.users.all });
      toastSuccess('User deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete user', error);
    },
  });
}

export function useResetUserPassword() {
  return useMutation({
    mutationFn: (id: string) => apiClient.resetUserPassword(id),
    onSuccess: () => {
      toastSuccess('Password reset email sent');
    },
    onError: (error: Error) => {
      toastApiError('Failed to reset password', error);
    },
  });
}

// ============================================================
// Catalog / Helm Hooks
// ============================================================

export function useHelmRepositories() {
  return useQuery({
    queryKey: queryKeys.catalog.repositories,
    queryFn: () => apiClient.getHelmRepositories(),
  });
}

export function useCreateHelmRepository() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      name: string;
      url: string;
      repoType: import('@/types').HelmRepoType;
      description?: string;
      username?: string;
      password?: string;
    }) => apiClient.createHelmRepository(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess('Repository added');
    },
    onError: (error: Error) => {
      toastApiError('Failed to add repository', error);
    },
  });
}

export function useSyncHelmRepository() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.syncHelmRepository(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess('Repository sync initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to sync repository', error);
    },
  });
}

export function useDeleteHelmRepository() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteHelmRepository(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess('Repository deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete repository', error);
    },
  });
}

export function useHelmCharts(params?: { repository?: string; category?: string; search?: string }) {
  return useQuery({
    queryKey: queryKeys.catalog.charts(params),
    queryFn: () => apiClient.getHelmCharts(params),
  });
}

export function useHelmChartVersions(chartId: string) {
  return useQuery({
    queryKey: queryKeys.catalog.chartVersions(chartId),
    queryFn: () => apiClient.getHelmChartVersions(chartId),
    enabled: !!chartId,
  });
}

export function useInstalledCharts(params?: { cluster?: string }) {
  return useQuery({
    queryKey: queryKeys.catalog.installed(params),
    queryFn: () => apiClient.getInstalledCharts(params),
    // `catalog_release.changed` (server writes) + the Helm-Secret k8s route
    // (cluster-side churn) refresh this while the stream is open.
    refetchInterval: liveFallback(30000),
  });
}

export function useInstallHelmChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      cluster_id: string;
      chart_version_id: string;
      release_name: string;
      namespace: string;
      values_override?: string;
    }) => apiClient.installHelmChart(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess('Chart installation initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to install chart', error);
    },
  });
}

export function useUpgradeInstalledChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: { chart_version_id: string; values_override?: string } }) =>
      apiClient.upgradeInstalledChart(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess('Chart upgrade initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to upgrade chart', error);
    },
  });
}

export function useUninstallChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.uninstallChart(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess('Chart uninstalled');
    },
    onError: (error: Error) => {
      toastApiError('Failed to uninstall chart', error);
    },
  });
}

export function useRollbackChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, revision }: { id: string; revision: number }) =>
      apiClient.rollbackChart(id, revision),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.catalog.all });
      toastSuccess('Chart rollback initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to rollback chart', error);
    },
  });
}

// ============================================================
// Backup Hooks
// ============================================================

export function useBackupStorageConfigs() {
  return useQuery({
    queryKey: queryKeys.backups.storage,
    queryFn: () => apiClient.getBackupStorageConfigs(),
  });
}

export function useCreateBackupStorageConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
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
    }) => apiClient.createBackupStorageConfig(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backups.all });
      toastSuccess('Storage configuration created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create storage config', error);
    },
  });
}

export function useTestBackupStorage() {
  return useMutation({
    mutationFn: (id: string) => apiClient.testBackupStorage(id),
    onSuccess: (data) => {
      if (data.success) {
        toastSuccess('Storage connection test successful');
      } else {
        toastApiError('Storage test failed', data.message);
      }
    },
    onError: (error: Error) => {
      toastApiError('Storage test failed', error);
    },
  });
}

export function useDeleteBackupStorageConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteBackupStorageConfig(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backups.all });
      toastSuccess('Storage configuration deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete storage config', error);
    },
  });
}

export function useBackups() {
  return useQuery({
    queryKey: queryKeys.backups.list,
    queryFn: () => apiClient.getBackups(),
    refetchInterval: liveFallback(15000),
  });
}

export function useCreateBackup() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      name: string;
      storage_id: string;
      backup_type: import('@/types').BackupType;
    }) => apiClient.createBackup(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backups.all });
      toastSuccess('Backup initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create backup', error);
    },
  });
}

export function useRestoreFromBackup() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.restoreFromBackup(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backups.all });
      toastSuccess('Restore initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to restore backup', error);
    },
  });
}

export function useDeleteBackup() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteBackup(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backups.all });
      toastSuccess('Backup deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete backup', error);
    },
  });
}

export function useBackupSchedules() {
  return useQuery({
    queryKey: queryKeys.backups.schedules,
    queryFn: () => apiClient.getBackupSchedules(),
  });
}

export function useCreateBackupSchedule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      name: string;
      storage_id: string;
      backup_type: import('@/types').BackupType;
      cron_expression: string;
      retention_count: number;
      enabled?: boolean;
    }) => apiClient.createBackupSchedule(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backups.all });
      toastSuccess('Backup schedule created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create schedule', error);
    },
  });
}

export function useUpdateBackupSchedule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<{
      name: string;
      storage_id: string;
      backup_type: import('@/types').BackupType;
      cron_expression: string;
      retention_count: number;
      enabled: boolean;
    }> }) => apiClient.updateBackupSchedule(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backups.all });
      toastSuccess('Backup schedule updated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to update schedule', error);
    },
  });
}

export function useDeleteBackupSchedule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteBackupSchedule(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.backups.all });
      toastSuccess('Backup schedule deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete schedule', error);
    },
  });
}

// ============================================================
// Security Hooks
// ============================================================

export function usePodSecurityTemplates() {
  return useQuery({
    queryKey: queryKeys.security.templates,
    queryFn: () => apiClient.getPodSecurityTemplates(),
  });
}

export function useCreatePodSecurityTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<import('@/types').PodSecurityTemplate>) =>
      apiClient.createPodSecurityTemplate(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess('PSA template created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create template', error);
    },
  });
}

export function useUpdatePodSecurityTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<import('@/types').PodSecurityTemplate> }) =>
      apiClient.updatePodSecurityTemplate(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess('PSA template updated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to update template', error);
    },
  });
}

export function useDeletePodSecurityTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deletePodSecurityTemplate(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess('PSA template deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete template', error);
    },
  });
}

export function useClusterSecurityPolicies() {
  return useQuery({
    queryKey: queryKeys.security.policies,
    queryFn: () => apiClient.getClusterSecurityPolicies(),
    // `security_policy.changed` refreshes this while the stream is open.
    refetchInterval: liveFallback(30000),
  });
}

export function useAssignSecurityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: { cluster_id: string; template_id: string }) =>
      apiClient.assignSecurityPolicy(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess('Security policy assigned');
    },
    onError: (error: Error) => {
      toastApiError('Failed to assign policy', error);
    },
  });
}

export function useApplySecurityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.applySecurityPolicy(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess('Security policy applied to cluster');
    },
    onError: (error: Error) => {
      toastApiError('Failed to apply policy', error);
    },
  });
}

export function useRemoveSecurityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.removeSecurityPolicy(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess('Security policy removed');
    },
    onError: (error: Error) => {
      toastApiError('Failed to remove policy', error);
    },
  });
}

export function useSecurityScans(params?: { cluster?: string; scan_type?: string }) {
  return useQuery({
    queryKey: queryKeys.security.scans(params),
    queryFn: () => apiClient.getSecurityScans(params),
    // `security_scan.changed` fires on every scan write (trigger, ingest,
    // failure) alongside `cis_scan.changed` — same table, two read surfaces.
    refetchInterval: liveFallback(15000),
  });
}

export function useTriggerSecurityScan() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: { cluster_id: string; scan_type: import('@/types').SecurityScanType }) =>
      apiClient.triggerSecurityScan(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.security.all });
      toastSuccess('Security scan initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to start scan', error);
    },
  });
}

// ============================================================
// Cluster Tools Hooks
// ============================================================

export function useTools() {
  return useQuery({
    queryKey: queryKeys.tools.list(),
    queryFn: () => apiClient.getTools(),
    staleTime: 60000,
  });
}

export function useClusterToolsStatus(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.tools.clusterStatus(clusterId),
    queryFn: () => apiClient.getClusterToolsStatus(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30000),
  });
}

const TOOL_OP_TERMINAL = ['completed', 'failed', 'superseded'];

// Polls a single tool operation (+ its events) every 2s while in-flight, then
// stops once it reaches a terminal state. Drives the install-progress drawer.
export function useToolOperation(operationId: string | null) {
  return useQuery({
    queryKey: queryKeys.tools.operation(operationId || ''),
    queryFn: () => apiClient.getToolOperation(operationId as string),
    enabled: !!operationId,
    // Terminal-wrap (P4.5): stop entirely once terminal; while in-flight,
    // `tool_operation.changed` drives freshness and the 2s poll is only the
    // stream-down fallback (belt-and-braces for ops in flight during a drop).
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status && TOOL_OP_TERMINAL.includes(status) ? false : liveFallback(2000)();
    },
  });
}

export function useInstallTool() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ slug, ...data }: { slug: string; cluster_id: string; preset: string; values_override?: string }) =>
      apiClient.installTool(slug, data),
    onSuccess: (_, { cluster_id }) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.tools.clusterStatus(cluster_id) });
      toastSuccess('Tool installation initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to install tool', error);
    },
  });
}

export function useUninstallTool() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ slug, cluster_id }: { slug: string; cluster_id: string }) =>
      apiClient.uninstallTool(slug, { cluster_id }),
    onSuccess: (_, { cluster_id }) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.tools.clusterStatus(cluster_id) });
      toastSuccess('Tool uninstall initiated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to uninstall tool', error);
    },
  });
}

export function useAdoptTool() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ slug, ...data }: { slug: string; cluster_id: string; release_name: string }) =>
      apiClient.adoptTool(slug, data),
    onSuccess: (_, { cluster_id }) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.tools.clusterStatus(cluster_id) });
      toastSuccess('Tool adopted successfully');
    },
    onError: (error: Error) => {
      toastApiError('Failed to adopt tool', error);
    },
  });
}

// ============================================================
// Generic K8s Resource Hook
// ============================================================

export function useGenericResources(clusterId: string, resourceType: string) {
  return useQuery({
    queryKey: queryKeys.generic.resources(clusterId, resourceType),
    queryFn: () => apiClient.getGenericResources(clusterId, resourceType),
    enabled: !!clusterId && !!resourceType,
    refetchInterval: liveFallback(30000),
  });
}

// ============================================================
// Kubeconfig Hook
// ============================================================

export function useGenerateKubeconfig() {
  return useMutation({
    mutationFn: (clusterId: string) => apiClient.generateKubeconfig(clusterId),
    onSuccess: () => {
      toastSuccess('Kubeconfig downloaded');
    },
    onError: (error: Error) => {
      toastApiError('Failed to generate kubeconfig', error);
    },
  });
}

// ============================================================
// K8s Proxy Hooks
// ============================================================

export const k8sQueryKeys = {
  yaml: (clusterId: string, path: string) => ['k8s', clusterId, 'yaml', path] as const,
  resource: (clusterId: string, path: string) => ['k8s', clusterId, 'resource', path] as const,
};

export function useK8sGetYaml(clusterId: string, path: string, enabled = true) {
  return useQuery({
    queryKey: k8sQueryKeys.yaml(clusterId, path),
    queryFn: () => apiClient.k8sGetYaml(clusterId, path),
    enabled: !!clusterId && !!path && enabled,
    staleTime: 0,
    gcTime: 0,
  });
}

// Single object as JSON (mirrors useK8sGetYaml). ponytail: reuse existing k8sQueryKeys.resource.
export function useK8sResource(clusterId: string, path: string, enabled = true) {
  return useQuery({
    queryKey: k8sQueryKeys.resource(clusterId, path),
    queryFn: () => apiClient.k8sGet(clusterId, path),
    enabled: !!clusterId && !!path && enabled,
    staleTime: 0,
    gcTime: 0,
  });
}

export function useK8sDelete() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, path }: { clusterId: string; path: string }) =>
      apiClient.k8sDelete(clusterId, path),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.workloads.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
      toastSuccess('Resource deleted');
    },
    onError: (error: Error) => {
      toastApiError('Failed to delete resource', error);
    },
  });
}

export function useK8sApplyYaml() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, path, yaml }: { clusterId: string; path: string; yaml: string }) =>
      apiClient.k8sApplyYaml(clusterId, path, yaml),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.workloads.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
      toastSuccess('Resource updated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to update resource', error);
    },
  });
}

export function useK8sDryRunYaml() {
  return useMutation({
    mutationFn: ({ clusterId, path, yaml }: { clusterId: string; path: string; yaml: string }) =>
      apiClient.k8sDryRunYaml(clusterId, path, yaml),
    onSuccess: () => {
      toastSuccess('Dry run passed');
    },
    onError: (error: Error) => {
      toastApiError('Dry run failed', error);
    },
  });
}

export function useK8sCreate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, path, body }: { clusterId: string; path: string; body: unknown }) =>
      apiClient.k8sCreate(clusterId, path, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.workloads.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
      toastSuccess('Resource created');
    },
    onError: (error: Error) => {
      toastApiError('Failed to create resource', error);
    },
  });
}

export function useK8sPatch() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      clusterId,
      path,
      body,
      patchType = 'strategic-merge',
    }: {
      clusterId: string;
      path: string;
      body: unknown;
      patchType?: 'strategic-merge' | 'merge' | 'json';
    }) => apiClient.k8sPatch(clusterId, path, body, patchType),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
      toastSuccess('Resource updated');
    },
    onError: (error: Error) => {
      toastApiError('Failed to patch resource', error);
    },
  });
}

// ============================================================
// Phase B5: CIS Scan Hooks
// ============================================================

const cisQueryKeys = {
  profiles: (clusterId: string) => ['cis', 'profiles', clusterId] as const,
  scans: (params?: Record<string, unknown>) => ['cis', 'scans', params] as const,
  scan: (id: string) => ['cis', 'scans', 'detail', id] as const,
};

/** Profiles installed on a cluster (or static fallback). */
export function useCISProfiles(clusterId: string | undefined) {
  return useQuery({
    queryKey: cisQueryKeys.profiles(clusterId ?? ''),
    queryFn: () => apiClient.getCISProfiles(clusterId!),
    enabled: !!clusterId,
    staleTime: 60_000,
  });
}

/** Paginated scan history. */
export function useCISScans(params?: { page?: number; pageSize?: number; limit?: number; offset?: number }) {
  return useQuery({
    queryKey: cisQueryKeys.scans(params),
    queryFn: () => apiClient.getCISScans(params),
    refetchInterval: liveFallback(30_000),
  });
}

/**
 * One scan with findings. Polls every 10s while the row is non-terminal so
 * the detail page reflects ingest progress without manual refresh.
 */
export function useCISScan(id: string | undefined) {
  return useQuery({
    queryKey: cisQueryKeys.scan(id ?? ''),
    queryFn: () => apiClient.getCISScan(id!),
    enabled: !!id,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      // Backend writes 'running' on insert and flips to 'completed' / 'failed'
      // when the report ingester finishes. Terminal-wrap (P4.5): stop once
      // terminal; in-flight rows rely on `cis_scan.changed` while the stream
      // is open and this poll only as the stream-down fallback.
      if (status === 'completed' || status === 'failed') return false;
      return liveFallback(10_000)();
    },
  });
}

/** Kick off a scan and route to its detail page. */
export function useCreateCISScan() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (payload: import('@/types').CISScanCreatePayload) => apiClient.createCISScan(payload),
    onSuccess: (scan) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.cis.scansAll });
      queryClient.setQueryData(cisQueryKeys.scan(scan.id), scan);
      toastSuccess('CIS scan queued');
    },
    onError: (error: Error) => {
      toastApiError('Failed to start scan', error);
    },
  });
}
