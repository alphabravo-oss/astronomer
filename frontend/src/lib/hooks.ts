import { useQuery, useMutation, useQueryClient, type QueryKey } from '@tanstack/react-query';
import { useCallback, useEffect, useRef, useState } from 'react';
import * as apiClient from './api';
import type {
  Cluster,
  ClusterRegistration,
  Workload,
  Pod,
  PodLog,
  MetricsData,
  MetricsSummary,
  ArgoInstance,
  ArgoApplication,
  GlobalRole,
  ClusterRole,
  ProjectRole,
  PolicyRule,
  RoleBinding,
  RoleBindingSubject,
  User,
  ClusterNode,
  ClusterEvent,
  Namespace,
  SSOProvider,
  APIToken,
  AuditLogEntry,
  ActivityEvent,
  AlertRule,
  AlertEvent,
  NotificationChannel,
  AlertSilence,
  LoggingOutput,
  LoggingPipeline,
  LoggingOperation,
  PersistentVolume,
  PersistentVolumeClaim,
  StorageClass,
  K8sService,
  Ingress,
  NetworkPolicy,
  Project,
} from '@/types';
import type { GeneralSettings } from './api';
import { toast } from 'sonner';

// ============================================================
// Query Key Factories
// ============================================================

export const queryKeys = {
  clusters: {
    all: ['clusters'] as const,
    list: (params?: Record<string, unknown>) => ['clusters', 'list', params] as const,
    detail: (id: string) => ['clusters', 'detail', id] as const,
    nodes: (id: string) => ['clusters', id, 'nodes'] as const,
    nodeDetail: (id: string, nodeName: string) => ['clusters', id, 'nodes', nodeName] as const,
    namespaces: (id: string) => ['clusters', id, 'namespaces'] as const,
    events: (id: string) => ['clusters', id, 'events'] as const,
    pods: (id: string) => ['clusters', id, 'pods'] as const,
    metrics: (id: string, range?: string) => ['clusters', id, 'metrics', range] as const,
    metricsSummary: (id: string) => ['clusters', id, 'metrics', 'summary'] as const,
  },
  workloads: {
    list: (clusterId: string, params?: Record<string, unknown>) =>
      ['workloads', clusterId, 'list', params] as const,
    detail: (clusterId: string, kind: string, ns: string, name: string) =>
      ['workloads', clusterId, kind, ns, name] as const,
    pods: (clusterId: string, kind: string, ns: string, name: string) =>
      ['workloads', clusterId, kind, ns, name, 'pods'] as const,
    metrics: (clusterId: string, kind: string, ns: string, name: string, range?: string) =>
      ['workloads', clusterId, kind, ns, name, 'metrics', range] as const,
  },
  podLogs: (clusterId: string, ns: string, pod: string, container?: string) =>
    ['pod-logs', clusterId, ns, pod, container] as const,
  argocd: {
    instances: (clusterId?: string) => ['argocd', 'instances', clusterId] as const,
    applications: (params?: Record<string, unknown>) => ['argocd', 'applications', params] as const,
  },
  rbac: {
    globalRoles: ['rbac', 'global-roles'] as const,
    clusterRoles: (clusterId?: string) => ['rbac', 'cluster-roles', clusterId] as const,
    projectRoles: (projectId?: string) => ['rbac', 'project-roles', projectId] as const,
    bindings: (params?: Record<string, unknown>) => ['rbac', 'bindings', params] as const,
  },
  users: {
    current: ['users', 'current'] as const,
    list: (params?: Record<string, unknown>) => ['users', 'list', params] as const,
  },
  settings: {
    general: ['settings', 'general'] as const,
    sso: ['settings', 'sso'] as const,
    tokens: ['settings', 'tokens'] as const,
    auditLogs: (params?: Record<string, unknown>) => ['settings', 'audit-logs', params] as const,
  },
  activity: (limit?: number) => ['activity', limit] as const,
  alerting: {
    rules: ['alerting', 'rules'] as const,
    events: (params?: Record<string, unknown>) => ['alerting', 'events', params] as const,
    channels: ['alerting', 'channels'] as const,
    silences: ['alerting', 'silences'] as const,
  },
  logging: {
    outputs: ['logging', 'outputs'] as const,
    pipelines: ['logging', 'pipelines'] as const,
    operations: (params?: Record<string, unknown>) => ['logging', 'operations', params] as const,
    operation: (id: string) => ['logging', 'operations', 'detail', id] as const,
  },
  storage: {
    pvs: (clusterId: string) => ['storage', clusterId, 'pvs'] as const,
    pvcs: (clusterId: string) => ['storage', clusterId, 'pvcs'] as const,
    storageClasses: (clusterId: string) => ['storage', clusterId, 'storageclasses'] as const,
  },
  networking: {
    services: (clusterId: string) => ['networking', clusterId, 'services'] as const,
    ingresses: (clusterId: string) => ['networking', clusterId, 'ingresses'] as const,
    networkPolicies: (clusterId: string) => ['networking', clusterId, 'networkpolicies'] as const,
    gateways: (clusterId: string) => ['networking', clusterId, 'gateways'] as const,
    httpRoutes: (clusterId: string) => ['networking', clusterId, 'httproutes'] as const,
    gatewayClasses: (clusterId: string) => ['networking', clusterId, 'gatewayclasses'] as const,
    grpcRoutes: (clusterId: string) => ['networking', clusterId, 'grpcroutes'] as const,
    tlsRoutes: (clusterId: string) => ['networking', clusterId, 'tlsroutes'] as const,
    tcpRoutes: (clusterId: string) => ['networking', clusterId, 'tcproutes'] as const,
    udpRoutes: (clusterId: string) => ['networking', clusterId, 'udproutes'] as const,
    referenceGrants: (clusterId: string) => ['networking', clusterId, 'referencegrants'] as const,
  },
  projects: {
    all: ['projects'] as const,
    list: (params?: Record<string, unknown>) => ['projects', 'list', params] as const,
    detail: (id: string) => ['projects', 'detail', id] as const,
  },
  catalog: {
    repositories: ['catalog', 'repositories'] as const,
    charts: (params?: Record<string, unknown>) => ['catalog', 'charts', params] as const,
    chartVersions: (chartId: string) => ['catalog', 'charts', chartId, 'versions'] as const,
    installed: (params?: Record<string, unknown>) => ['catalog', 'installed', params] as const,
  },
  backups: {
    list: ['backups', 'list'] as const,
    storage: ['backups', 'storage'] as const,
    schedules: ['backups', 'schedules'] as const,
  },
  security: {
    templates: ['security', 'templates'] as const,
    policies: ['security', 'policies'] as const,
    scans: (params?: Record<string, unknown>) => ['security', 'scans', params] as const,
  },
  tools: {
    all: ['tools'] as const,
    list: () => ['tools', 'list'] as const,
    detail: (slug: string) => ['tools', 'detail', slug] as const,
    clusterStatus: (clusterId: string) => ['tools', 'clusterStatus', clusterId] as const,
  },
  generic: {
    resources: (clusterId: string, resourceType: string) =>
      ['generic', clusterId, resourceType] as const,
  },
};

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
    refetchInterval: 30000,
  });
}

export function useCluster(id: string) {
  return useQuery({
    queryKey: queryKeys.clusters.detail(id),
    queryFn: () => apiClient.getCluster(id),
    enabled: !!id,
    refetchInterval: 15000,
  });
}

export function useClusterNodes(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.nodes(clusterId),
    queryFn: () => apiClient.getClusterNodes(clusterId),
    enabled: !!clusterId,
    refetchInterval: 30000,
  });
}

// The health-check worker reconciles conditions every 60s; polling at the
// same cadence keeps the UI fresh without piling on. Cluster page also
// gets invalidated on cluster.heartbeat events via useLiveQueryInvalidation.
export function useClusterConditions(clusterId: string) {
  return useQuery({
    queryKey: ['clusters', clusterId, 'conditions'] as const,
    queryFn: () => apiClient.getClusterConditions(clusterId),
    enabled: !!clusterId,
    refetchInterval: 60000,
  });
}

export function useNodeDetail(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: queryKeys.clusters.nodeDetail(clusterId, nodeName),
    queryFn: () => apiClient.getNodeDetail(clusterId, nodeName),
    enabled: !!clusterId && !!nodeName,
    refetchInterval: 30000,
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
    queryKey: queryKeys.clusters.events(clusterId),
    queryFn: () => apiClient.getClusterEvents(clusterId, params),
    enabled: !!clusterId,
    refetchInterval: 15000,
  });
}

export function useClusterPods(clusterId: string, params?: { namespace?: string }) {
  return useQuery({
    queryKey: queryKeys.clusters.pods(clusterId),
    queryFn: () => apiClient.getClusterPods(clusterId, params),
    enabled: !!clusterId,
    refetchInterval: 15000,
  });
}

export function useCreateCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: ClusterRegistration) => apiClient.createCluster(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      toast.success('Cluster registration initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to register cluster: ${error.message}`);
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
      toast.success('Cluster updated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to update cluster: ${error.message}`);
    },
  });
}

export function useDeleteCluster() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteCluster(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all });
      toast.success('Cluster deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete cluster: ${error.message}`);
    },
  });
}

export function useDeletePod() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, namespace, name }: { clusterId: string; namespace: string; name: string }) =>
      apiClient.deletePod(clusterId, namespace, name),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.pods(variables.clusterId) });
      toast.success('Pod deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete pod: ${error.message}`);
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
    refetchInterval: 15000,
  });
}

export function useWorkload(clusterId: string, kind: string, namespace: string, name: string) {
  return useQuery({
    queryKey: queryKeys.workloads.detail(clusterId, kind, namespace, name),
    queryFn: () => apiClient.getWorkload(clusterId, kind, namespace, name),
    enabled: !!clusterId && !!kind && !!namespace && !!name,
    refetchInterval: 10000,
  });
}

export function useWorkloadPods(clusterId: string, kind: string, namespace: string, name: string) {
  return useQuery({
    queryKey: queryKeys.workloads.pods(clusterId, kind, namespace, name),
    queryFn: () => apiClient.getWorkloadPods(clusterId, kind, namespace, name),
    enabled: !!clusterId && !!kind && !!namespace && !!name,
    refetchInterval: 10000,
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
      toast.success(`Scaled to ${variables.replicas} replicas`);
    },
    onError: (error: Error) => {
      toast.error(`Failed to scale workload: ${error.message}`);
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
      toast.success('Workload restart initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to restart workload: ${error.message}`);
    },
  });
}

// ============================================================
// Pod Logs Hook (with streaming)
// ============================================================

export function usePodLogs(
  clusterId: string,
  namespace: string,
  pod: string,
  params?: { container?: string; tailLines?: number; follow?: boolean }
) {
  const [streamLogs, setStreamLogs] = useState<PodLog[]>([]);
  const cleanupRef = useRef<(() => void) | null>(null);
  // De-duplicate toasts: if the WS errors mid-stream we only want one toast
  // per (pod, container) selection rather than one per reconnect/dropped
  // frame.
  const errorShownRef = useRef(false);

  // Initial fetch
  const query = useQuery({
    queryKey: queryKeys.podLogs(clusterId, namespace, pod, params?.container),
    queryFn: () =>
      apiClient.getPodLogs(clusterId, namespace, pod, {
        container: params?.container,
        tailLines: params?.tailLines || 500,
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
    if (!params?.follow || !clusterId || !namespace || !pod) return;

    const cleanup = apiClient.streamPodLogs(
      clusterId,
      namespace,
      pod,
      params?.container || '',
      (log) => {
        setStreamLogs((prev) => [...prev.slice(-2000), log]);
      },
      (err) => {
        // Surface the first error per stream so the user knows the live
        // tail dropped — but suppress duplicates so a flapping agent
        // doesn't spam the corner of the screen.
        if (!errorShownRef.current) {
          errorShownRef.current = true;
          toast.error(`Log stream: ${err.message}`);
        }
      },
      { follow: true, tailLines: params?.tailLines }
    );

    cleanupRef.current = cleanup;

    return () => {
      cleanup();
      cleanupRef.current = null;
    };
  }, [clusterId, namespace, pod, params?.container, params?.follow, params?.tailLines]);

  const allLogs = params?.follow
    ? [...(query.data || []), ...streamLogs]
    : query.data || [];

  const stopStreaming = useCallback(() => {
    cleanupRef.current?.();
    cleanupRef.current = null;
  }, []);

  return {
    ...query,
    data: allLogs,
    streamLogs,
    stopStreaming,
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
    refetchInterval: 60000,
  });
}

export function useClusterMetricsSummary(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.metricsSummary(clusterId),
    queryFn: () => apiClient.getClusterMetricsSummary(clusterId),
    enabled: !!clusterId,
    refetchInterval: 30000,
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
    refetchInterval: 60000,
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
    refetchInterval: 30000,
  });
}

export function useSyncArgoApp() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: { instanceId: string; appName: string }) =>
      apiClient.syncArgoApplication(params.instanceId, params.appName),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['argocd'] });
      toast.success('Sync initiated');
    },
    onError: (error: Error) => {
      toast.error(`Sync failed: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['rbac'] });
      toast.success('Role created successfully');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create role: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['rbac'] });
      toast.success('Role binding created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create role binding: ${error.message}`);
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
      toast.success('Settings saved successfully');
    },
    onError: (error: Error) => {
      toast.error(`Failed to save settings: ${error.message}`);
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
      toast.success('SSO provider created successfully');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create SSO provider: ${error.message}`);
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
      toast.success('API token created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create token: ${error.message}`);
    },
  });
}

export function useDeleteAPIToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteAPIToken(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.tokens });
      toast.success('API token deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete token: ${error.message}`);
    },
  });
}

export function useAuditLogs(params?: { page?: number; pageSize?: number; action?: string; user?: string }) {
  return useQuery({
    queryKey: queryKeys.settings.auditLogs(params),
    queryFn: () => apiClient.getAuditLogs(params),
  });
}

// ============================================================
// Activity Feed Hook
// ============================================================

export function useActivityFeed(limit: number = 20) {
  return useQuery({
    queryKey: queryKeys.activity(limit),
    queryFn: () => apiClient.getActivityFeed({ limit }),
    refetchInterval: 30000,
  });
}

// ============================================================
// Alerting Hooks
// ============================================================

export function useAlertRules() {
  return useQuery({
    queryKey: queryKeys.alerting.rules,
    queryFn: () => apiClient.getAlertRules(),
    refetchInterval: 30000,
  });
}

export function useCreateAlertRule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<AlertRule>) => apiClient.createAlertRule(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.rules });
      toast.success('Alert rule created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create alert rule: ${error.message}`);
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
      toast.success('Alert rule updated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to update alert rule: ${error.message}`);
    },
  });
}

export function useDeleteAlertRule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteAlertRule(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.rules });
      toast.success('Alert rule deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete alert rule: ${error.message}`);
    },
  });
}

export function useAlertEvents(params?: Record<string, string>) {
  return useQuery({
    queryKey: queryKeys.alerting.events(params),
    queryFn: () => apiClient.getAlertEvents(params),
    refetchInterval: 15000,
  });
}

export function useAcknowledgeAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.acknowledgeAlert(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['alerting'] });
      toast.success('Alert acknowledged');
    },
    onError: (error: Error) => {
      toast.error(`Failed to acknowledge alert: ${error.message}`);
    },
  });
}

export function useResolveAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.resolveAlert(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['alerting'] });
      toast.success('Alert resolved');
    },
    onError: (error: Error) => {
      toast.error(`Failed to resolve alert: ${error.message}`);
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
      toast.success('Notification channel created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create notification channel: ${error.message}`);
    },
  });
}

export function useTestNotificationChannel() {
  return useMutation({
    mutationFn: (id: string) => apiClient.testNotificationChannel(id),
    onSuccess: (data) => {
      if (data.success) {
        toast.success('Test notification sent successfully');
      } else {
        toast.error(`Test failed: ${data.message}`);
      }
    },
    onError: (error: Error) => {
      toast.error(`Test failed: ${error.message}`);
    },
  });
}

export function useAlertSilences() {
  return useQuery({
    queryKey: queryKeys.alerting.silences,
    queryFn: () => apiClient.getAlertSilences(),
    refetchInterval: 30000,
  });
}

export function useCreateAlertSilence() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<AlertSilence>) => apiClient.createAlertSilence(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.alerting.silences });
      toast.success('Silence created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create silence: ${error.message}`);
    },
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
      toast.success('Logging output created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create logging output: ${error.message}`);
    },
  });
}

export function useTestLoggingOutput() {
  return useMutation({
    mutationFn: (id: string) => apiClient.testLoggingOutput(id),
    onSuccess: (data) => {
      if (data.success) {
        toast.success('Test connection successful');
      } else {
        toast.error(`Test failed: ${data.message}`);
      }
    },
    onError: (error: Error) => {
      toast.error(`Test failed: ${error.message}`);
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
      toast.success('Logging pipeline created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create logging pipeline: ${error.message}`);
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
    // Poll so pending -> running -> completed transitions appear without a
    // manual refresh. Matches the argocd Operations tab cadence.
    refetchInterval: 5000,
  });
}

export function useLoggingOperation(id: string) {
  return useQuery<LoggingOperation>({
    queryKey: queryKeys.logging.operation(id),
    queryFn: () => apiClient.getLoggingOperation(id),
    enabled: !!id,
    refetchInterval: 5000,
  });
}

export function useRetryLoggingOperation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.retryLoggingOperation(id),
    onSuccess: () => {
      // Invalidate every cached list (parameterized keys) and the detail rows.
      queryClient.invalidateQueries({ queryKey: ['logging', 'operations'] });
      toast.success('Operation retry queued');
    },
    onError: (error: Error) => {
      toast.error(`Failed to retry operation: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['storage'] });
      toast.success('PVC created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create PVC: ${error.message}`);
    },
  });
}

export function useDeletePVC() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, namespace, name }: { clusterId: string; namespace: string; name: string }) =>
      apiClient.deletePersistentVolumeClaim(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['storage'] });
      toast.success('PVC deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete PVC: ${error.message}`);
    },
  });
}

export function useDeletePV() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, name }: { clusterId: string; name: string }) =>
      apiClient.deletePersistentVolume(clusterId, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['storage'] });
      toast.success('PV deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete PV: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['networking'] });
      toast.success('Service deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete service: ${error.message}`);
    },
  });
}

export function useDeleteIngress() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, namespace, name }: { clusterId: string; namespace: string; name: string }) =>
      apiClient.deleteIngress(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['networking'] });
      toast.success('Ingress deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete ingress: ${error.message}`);
    },
  });
}

export function useDeleteNetworkPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, namespace, name }: { clusterId: string; namespace: string; name: string }) =>
      apiClient.deleteNetworkPolicy(clusterId, namespace, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['networking'] });
      toast.success('Network policy deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete network policy: ${error.message}`);
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
      toast.success('Project created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create project: ${error.message}`);
    },
  });
}

export function useDeleteProject() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteProject(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all });
      toast.success('Project deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete project: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['users'] });
      toast.success('User created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create user: ${error.message}`);
    },
  });
}

export function useUpdateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<User> }) =>
      apiClient.updateUser(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] });
      toast.success('User updated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to update user: ${error.message}`);
    },
  });
}

export function useDeleteUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteUser(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] });
      toast.success('User deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete user: ${error.message}`);
    },
  });
}

export function useResetUserPassword() {
  return useMutation({
    mutationFn: (id: string) => apiClient.resetUserPassword(id),
    onSuccess: () => {
      toast.success('Password reset email sent');
    },
    onError: (error: Error) => {
      toast.error(`Failed to reset password: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['catalog'] });
      toast.success('Repository added');
    },
    onError: (error: Error) => {
      toast.error(`Failed to add repository: ${error.message}`);
    },
  });
}

export function useSyncHelmRepository() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.syncHelmRepository(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['catalog'] });
      toast.success('Repository sync initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to sync repository: ${error.message}`);
    },
  });
}

export function useDeleteHelmRepository() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteHelmRepository(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['catalog'] });
      toast.success('Repository deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete repository: ${error.message}`);
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
    refetchInterval: 30000,
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
      queryClient.invalidateQueries({ queryKey: ['catalog'] });
      toast.success('Chart installation initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to install chart: ${error.message}`);
    },
  });
}

export function useUpgradeInstalledChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: { chart_version_id: string; values_override?: string } }) =>
      apiClient.upgradeInstalledChart(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['catalog'] });
      toast.success('Chart upgrade initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to upgrade chart: ${error.message}`);
    },
  });
}

export function useUninstallChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.uninstallChart(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['catalog'] });
      toast.success('Chart uninstalled');
    },
    onError: (error: Error) => {
      toast.error(`Failed to uninstall chart: ${error.message}`);
    },
  });
}

export function useRollbackChart() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, revision }: { id: string; revision: number }) =>
      apiClient.rollbackChart(id, revision),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['catalog'] });
      toast.success('Chart rollback initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to rollback chart: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['backups'] });
      toast.success('Storage configuration created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create storage config: ${error.message}`);
    },
  });
}

export function useTestBackupStorage() {
  return useMutation({
    mutationFn: (id: string) => apiClient.testBackupStorage(id),
    onSuccess: (data) => {
      if (data.success) {
        toast.success('Storage connection test successful');
      } else {
        toast.error(`Storage test failed: ${data.message}`);
      }
    },
    onError: (error: Error) => {
      toast.error(`Storage test failed: ${error.message}`);
    },
  });
}

export function useDeleteBackupStorageConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteBackupStorageConfig(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups'] });
      toast.success('Storage configuration deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete storage config: ${error.message}`);
    },
  });
}

export function useBackups() {
  return useQuery({
    queryKey: queryKeys.backups.list,
    queryFn: () => apiClient.getBackups(),
    refetchInterval: 15000,
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
      queryClient.invalidateQueries({ queryKey: ['backups'] });
      toast.success('Backup initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create backup: ${error.message}`);
    },
  });
}

export function useRestoreFromBackup() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.restoreFromBackup(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups'] });
      toast.success('Restore initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to restore backup: ${error.message}`);
    },
  });
}

export function useDeleteBackup() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteBackup(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups'] });
      toast.success('Backup deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete backup: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['backups'] });
      toast.success('Backup schedule created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create schedule: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['backups'] });
      toast.success('Backup schedule updated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to update schedule: ${error.message}`);
    },
  });
}

export function useDeleteBackupSchedule() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteBackupSchedule(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups'] });
      toast.success('Backup schedule deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete schedule: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['security'] });
      toast.success('PSA template created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create template: ${error.message}`);
    },
  });
}

export function useUpdatePodSecurityTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<import('@/types').PodSecurityTemplate> }) =>
      apiClient.updatePodSecurityTemplate(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['security'] });
      toast.success('PSA template updated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to update template: ${error.message}`);
    },
  });
}

export function useDeletePodSecurityTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deletePodSecurityTemplate(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['security'] });
      toast.success('PSA template deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete template: ${error.message}`);
    },
  });
}

export function useClusterSecurityPolicies() {
  return useQuery({
    queryKey: queryKeys.security.policies,
    queryFn: () => apiClient.getClusterSecurityPolicies(),
    refetchInterval: 30000,
  });
}

export function useAssignSecurityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: { cluster_id: string; template_id: string }) =>
      apiClient.assignSecurityPolicy(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['security'] });
      toast.success('Security policy assigned');
    },
    onError: (error: Error) => {
      toast.error(`Failed to assign policy: ${error.message}`);
    },
  });
}

export function useApplySecurityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.applySecurityPolicy(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['security'] });
      toast.success('Security policy applied to cluster');
    },
    onError: (error: Error) => {
      toast.error(`Failed to apply policy: ${error.message}`);
    },
  });
}

export function useRemoveSecurityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.removeSecurityPolicy(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['security'] });
      toast.success('Security policy removed');
    },
    onError: (error: Error) => {
      toast.error(`Failed to remove policy: ${error.message}`);
    },
  });
}

export function useSecurityScans(params?: { cluster?: string; scan_type?: string }) {
  return useQuery({
    queryKey: queryKeys.security.scans(params),
    queryFn: () => apiClient.getSecurityScans(params),
    refetchInterval: 15000,
  });
}

export function useTriggerSecurityScan() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: { cluster_id: string; scan_type: import('@/types').SecurityScanType }) =>
      apiClient.triggerSecurityScan(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['security'] });
      toast.success('Security scan initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to start scan: ${error.message}`);
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
    refetchInterval: 30000,
  });
}

export function useInstallTool() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ slug, ...data }: { slug: string; cluster_id: string; preset: string; values_override?: string }) =>
      apiClient.installTool(slug, data),
    onSuccess: (_, { cluster_id }) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.tools.clusterStatus(cluster_id) });
      toast.success('Tool installation initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to install tool: ${error.message}`);
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
      toast.success('Tool uninstall initiated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to uninstall tool: ${error.message}`);
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
      toast.success('Tool adopted successfully');
    },
    onError: (error: Error) => {
      toast.error(`Failed to adopt tool: ${error.message}`);
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
    refetchInterval: 30000,
  });
}

// ============================================================
// Kubeconfig Hook
// ============================================================

export function useGenerateKubeconfig() {
  return useMutation({
    mutationFn: (clusterId: string) => apiClient.generateKubeconfig(clusterId),
    onSuccess: () => {
      toast.success('Kubeconfig downloaded');
    },
    onError: (error: Error) => {
      toast.error(`Failed to generate kubeconfig: ${error.message}`);
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

export function useK8sDelete() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, path }: { clusterId: string; path: string }) =>
      apiClient.k8sDelete(clusterId, path),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['k8s'] });
      queryClient.invalidateQueries({ queryKey: ['clusters'] });
      queryClient.invalidateQueries({ queryKey: ['networking'] });
      queryClient.invalidateQueries({ queryKey: ['storage'] });
      queryClient.invalidateQueries({ queryKey: ['workloads'] });
      queryClient.invalidateQueries({ queryKey: ['generic'] });
      toast.success('Resource deleted');
    },
    onError: (error: Error) => {
      toast.error(`Failed to delete resource: ${error.message}`);
    },
  });
}

export function useK8sApplyYaml() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, path, yaml }: { clusterId: string; path: string; yaml: string }) =>
      apiClient.k8sApplyYaml(clusterId, path, yaml),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['k8s'] });
      queryClient.invalidateQueries({ queryKey: ['clusters'] });
      queryClient.invalidateQueries({ queryKey: ['networking'] });
      queryClient.invalidateQueries({ queryKey: ['storage'] });
      queryClient.invalidateQueries({ queryKey: ['workloads'] });
      queryClient.invalidateQueries({ queryKey: ['generic'] });
      toast.success('Resource updated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to update resource: ${error.message}`);
    },
  });
}

export function useK8sCreate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ clusterId, path, body }: { clusterId: string; path: string; body: unknown }) =>
      apiClient.k8sCreate(clusterId, path, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['k8s'] });
      queryClient.invalidateQueries({ queryKey: ['clusters'] });
      queryClient.invalidateQueries({ queryKey: ['networking'] });
      queryClient.invalidateQueries({ queryKey: ['storage'] });
      queryClient.invalidateQueries({ queryKey: ['workloads'] });
      queryClient.invalidateQueries({ queryKey: ['generic'] });
      toast.success('Resource created');
    },
    onError: (error: Error) => {
      toast.error(`Failed to create resource: ${error.message}`);
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
      queryClient.invalidateQueries({ queryKey: ['k8s'] });
      queryClient.invalidateQueries({ queryKey: ['clusters'] });
      queryClient.invalidateQueries({ queryKey: ['generic'] });
      toast.success('Resource updated');
    },
    onError: (error: Error) => {
      toast.error(`Failed to patch resource: ${error.message}`);
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
    refetchInterval: 30_000,
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
      // when the report ingester finishes. Anything not in that terminal set
      // means we should keep polling.
      if (status === 'completed' || status === 'failed') return false;
      return 10_000;
    },
  });
}

/** Kick off a scan and route to its detail page. */
export function useCreateCISScan() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (payload: import('@/types').CISScanCreatePayload) => apiClient.createCISScan(payload),
    onSuccess: (scan) => {
      queryClient.invalidateQueries({ queryKey: ['cis', 'scans'] });
      queryClient.setQueryData(cisQueryKeys.scan(scan.id), scan);
      toast.success('CIS scan queued');
    },
    onError: (error: Error) => {
      toast.error(`Failed to start scan: ${error.message}`);
    },
  });
}
