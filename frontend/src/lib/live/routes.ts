/**
 * SSE invalidation map (D23): the single routing table from live-event
 * types to the React Query cache keys they refresh. The dispatcher
 * (`lib/live/dispatch.ts`) looks events up here and feeds every produced
 * key through the central paced invalidator.
 *
 * Rules:
 *  - Keys come ONLY from the `query-keys.ts` factory — never inline arrays
 *    (locked in by `routes.test.ts`).
 *  - `cluster.metrics` / `cluster.status_changed` list+detail rows are
 *    patched in place by `cluster-merger.ts` (`setQueryData`), NOT
 *    invalidated here — an invalidate per tick would queue a refetch that
 *    races the patch. Metrics ticks only invalidate the per-cluster metrics
 *    history/summary keys (bounded: one mounted chart per viewed cluster).
 *  - `audit.*` (prefix route): restricted users never receive audit events —
 *    they carry no `cluster_id`, so SEC-R07 drops them fail-closed. Their
 *    activity feed heals via `liveFallback` polling when the stream is down
 *    and the reconnect bulk invalidation when it reopens.
 */

import type { QueryKey } from '@tanstack/react-query';
import { queryKeys as qk } from '@/lib/query-keys';

/** Camelized event payload (envelope.ts camelizes centrally). */
export type LiveEventData = Record<string, unknown>;

/** Prefix key for the `audit.*` family (see module doc for RBAC caveat). */
export const AUDIT_PREFIX = 'audit.';

function clusterIdOf(d: LiveEventData): string | null {
  const v = d.clusterId;
  return typeof v === 'string' && v !== '' ? v : null;
}

/** Cluster lifecycle/liveness events: list rows + detail + agent fleet. */
function clusterLivenessRoute(d: LiveEventData): QueryKey[] {
  const cid = clusterIdOf(d);
  const keys: QueryKey[] = [qk.clusters.listAll, qk.agents.fleet];
  if (cid) keys.push(qk.clusters.detail(cid));
  return keys;
}

/**
 * `cluster.k8s_changed` routing, per `data.kind`. Kinds listed here map to
 * the precise keys their views read from; kinds with no row (e.g.
 * ServiceAccount, ResourceQuota) fall through to `defaultK8sRoute` — the
 * generic resource list for the kind. The P4.6 informer expansion (agent
 * metadata informers + discover-if-present CRDs) feeds the rows below.
 */
/** Velero CRDs: global backup pages + the per-cluster snapshots page. */
function veleroKindRoute(cid: string): QueryKey[] {
  return [
    qk.backups.all,
    qk.backups.b2All,
    qk.clusterPages.snapshots(cid),
    qk.clusterPages.snapshotSchedules(cid),
    qk.clusterPages.veleroStatus(cid),
  ];
}

export const K8S_KIND_ROUTES: Record<
  string,
  (clusterId: string, d: LiveEventData) => QueryKey[]
> = {
  // Pod payloads carry the pod's own kind/ns/name, not its owner workload,
  // so workload pods are covered by the per-cluster workloads prefix.
  Pod: (cid) => [qk.clusters.podsAll(cid), qk.workloads.byCluster(cid)],
  Deployment: (cid) => [
    qk.clusterPages.workloadKind(cid, 'deployments'),
    qk.workloads.byCluster(cid),
  ],
  StatefulSet: (cid) => [
    qk.clusterPages.workloadKind(cid, 'statefulsets'),
    qk.workloads.byCluster(cid),
  ],
  DaemonSet: (cid) => [
    qk.clusterPages.workloadKind(cid, 'daemonsets'),
    qk.workloads.byCluster(cid),
  ],
  // ReplicaSet churn is the rollout-progress signal for workload detail.
  ReplicaSet: (cid) => [
    qk.generic.resources(cid, 'replicasets'),
    qk.workloads.byCluster(cid),
  ],
  Service: (cid) => [qk.networking.services(cid)],
  Node: (cid) => [qk.clusters.nodes(cid)],
  Event: (cid) => [qk.clusters.eventsAll(cid)],
  ConfigMap: (cid) => [qk.generic.resources(cid, 'configmaps')],
  // Helm release storage churn also refreshes the installed-apps views —
  // the agent only forwards `helm.sh/release.v1` Secrets (P4.6 filter), so
  // every Secret frame IS a release change (P4.9).
  Secret: (cid) => [
    qk.generic.resources(cid, 'secrets'),
    qk.catalog.installedAll,
    qk.clusterPages.appsInstalled(cid),
  ],
  // ── P4.6 informer expansion (agent metadata informers) ──
  Namespace: (cid) => [qk.clusters.namespaces(cid)],
  Job: (cid) => [
    qk.clusterPages.workloadKind(cid, 'jobs'),
    qk.generic.resources(cid, 'jobs'),
    qk.workloads.byCluster(cid),
  ],
  CronJob: (cid) => [
    qk.clusterPages.workloadKind(cid, 'cronjobs'),
    qk.generic.resources(cid, 'cronjobs'),
    qk.workloads.byCluster(cid),
  ],
  Ingress: (cid) => [qk.networking.ingresses(cid)],
  NetworkPolicy: (cid) => [qk.networking.networkPolicies(cid)],
  PersistentVolume: (cid) => [qk.storage.pvs(cid)],
  PersistentVolumeClaim: (cid) => [qk.storage.pvcs(cid)],
  StorageClass: (cid) => [qk.storage.storageClasses(cid)],
  // Explorer path segment is 'hpa', not the pluralized kind.
  HorizontalPodAutoscaler: (cid) => [qk.generic.resources(cid, 'hpa')],
  // RBAC kinds render under 'k8s-'-prefixed explorer segments.
  Role: (cid) => [qk.generic.resources(cid, 'k8s-roles')],
  RoleBinding: (cid) => [qk.generic.resources(cid, 'k8s-rolebindings')],
  ClusterRole: (cid) => [qk.generic.resources(cid, 'k8s-clusterroles')],
  ClusterRoleBinding: (cid) => [qk.generic.resources(cid, 'k8s-clusterrolebindings')],
  // ── P4.6 CRD informers (discover-if-present) ──
  Backup: veleroKindRoute,
  Restore: veleroKindRoute,
  Schedule: veleroKindRoute,
  // Coarse by design, matching `argocd.changed` (see EVENT_ROUTES note).
  Application: () => [qk.argocd.all],
  ApplicationSet: () => [qk.argocd.all],
  VulnerabilityReport: (cid) => [
    qk.clusterPages.imageVulnsAll(cid),
    qk.clusterPages.vulnerabilitySummary(cid),
  ],
  // Gatekeeper constraints — the agent normalizes every
  // constraints.gatekeeper.sh resource to this stable kind.
  Constraint: (cid) => [qk.gatekeeperConstraints(cid)],
};

/**
 * Fallback for kinds without an explicit row: invalidate the generic
 * resource list the explorer renders for that kind.
 */
export function defaultK8sRoute(clusterId: string, d: LiveEventData): QueryKey[] {
  const kind = typeof d.kind === 'string' ? d.kind : '';
  if (!kind) return [];
  return [qk.generic.resources(clusterId, genericResourceType(kind))];
}

/** Kind → explorer resource-type segment (lowercase plural). */
function genericResourceType(kind: string): string {
  const lower = kind.toLowerCase();
  if (lower.endsWith('y')) return `${lower.slice(0, -1)}ies`;
  if (/(?:s|x|z|ch|sh)$/.test(lower)) return `${lower}es`;
  return `${lower}s`;
}

function k8sChangedRoute(d: LiveEventData): QueryKey[] {
  const cid = clusterIdOf(d);
  if (!cid) return [];
  const kind = typeof d.kind === 'string' ? d.kind : '';
  const route = K8S_KIND_ROUTES[kind];
  return route ? route(cid, d) : defaultK8sRoute(cid, d);
}

function registrationRoute(d: LiveEventData): QueryKey[] {
  const cid = clusterIdOf(d);
  return cid ? [qk.clusterPages.registrationStatus(cid)] : [];
}

function entityIdOf(d: LiveEventData): string | null {
  const v = d.id;
  return typeof v === 'string' && v !== '' ? v : null;
}

/**
 * Event type → query keys. Every type the backend publishes today has a row
 * (exhaustiveness locked by `routes.test.ts`); P4.5/P4.6/P4.9 grow the table
 * per domain as publishers land.
 */
export const EVENT_ROUTES: Record<string, (d: LiveEventData) => QueryKey[]> = {
  'cluster.connected': clusterLivenessRoute,
  'cluster.disconnected': clusterLivenessRoute,
  // Heartbeats also refresh the conditions surface (P4.9): the health-check
  // worker reconciles cluster conditions on the same signal that bumps
  // last_heartbeat, and there is no dedicated conditions event.
  'cluster.heartbeat': (d) => {
    const keys = clusterLivenessRoute(d);
    const cid = clusterIdOf(d);
    if (cid) keys.push(qk.clusters.conditions(cid), qk.clusters.conditionRemediation(cid));
    return keys;
  },
  // Merger-owned for list/detail rows: status is patched in place,
  // deliberately without an invalidate (see cluster-merger.ts). Conditions +
  // remediation history are separate queries written by the same status
  // reconciler, so transitions refresh them here (P4.9).
  'cluster.status_changed': (d) => {
    const cid = clusterIdOf(d);
    return cid ? [qk.clusters.conditions(cid), qk.clusters.conditionRemediation(cid)] : [];
  },
  // Merger patches list/detail percentages; only the metrics history +
  // summary for the ticking cluster refetch (paced).
  'cluster.metrics': (d) => {
    const cid = clusterIdOf(d);
    return cid ? [qk.clusters.metricsAll(cid)] : [];
  },
  'cluster.created': () => [qk.clusters.listAll],
  'cluster.updated': (d) => {
    const cid = clusterIdOf(d);
    const keys: QueryKey[] = [qk.clusters.listAll];
    if (cid) keys.push(qk.clusters.detail(cid));
    return keys;
  },
  'cluster.deleted': clusterLivenessRoute,
  'agent.reconnecting': clusterLivenessRoute,
  'agent.failed': clusterLivenessRoute,
  'cluster.k8s_changed': k8sChangedRoute,
  'cluster.registration.step': registrationRoute,
  'cluster.registration.phase': registrationRoute,
  // Heartbeat only — nothing to refresh.
  'sys.ping': () => [],
  // ── P4.5 domain publishers — metadata-only `<resource>.changed` events ──
  // Velero backups/restores/schedules (payload kind: backup|restore|schedule).
  // Both key families cover the legacy backups hooks and the B2 engine hooks.
  'backup.changed': () => [qk.backups.all, qk.backups.b2All],
  // Published per distinct target cluster; the deleted-cluster tombstone
  // window also heals through the clusters list here (see useDeleteCluster).
  'fleet_operation.changed': () => [qk.fleetOperations.all, qk.clusters.listAll],
  // Prefix covers every list variant + the detail rows.
  'logging_operation.changed': () => [qk.logging.operationsAll],
  'tool_operation.changed': (d) => {
    const keys: QueryKey[] = [];
    const id = entityIdOf(d);
    if (id) keys.push(qk.tools.operation(id));
    const cid = clusterIdOf(d);
    if (cid) keys.push(qk.tools.clusterStatus(cid));
    return keys;
  },
  // Prefix covers the paginated scan list variants + the detail rows.
  'cis_scan.changed': () => [qk.cis.scansAll],
  'image_scan.changed': (d) => {
    const cid = clusterIdOf(d);
    return cid
      ? [qk.clusterPages.imageVulnsAll(cid), qk.clusterPages.vulnerabilitySummary(cid)]
      : [];
  },
  // Coarse by design: the payload's scope (instance|operation|health|
  // ownership) doesn't map to per-instance keys without a lookup, and the
  // paced invalidator only refetches mounted argocd queries. The D8 trio
  // (manifests/history/orphan report) keeps its plain polls regardless.
  'argocd.changed': () => [qk.argocd.all],
  // Unscoped (superuser-only via the SEC-R07 fail-closed drop, D9);
  // payload id is the queue name.
  'admin_queue.changed': (d) => {
    const keys: QueryKey[] = [qk.adminOperations.queues];
    const queue = entityIdOf(d);
    if (queue) keys.push(qk.adminOperations.dlq(queue));
    return keys;
  },
  // Unscoped (superuser-only, D9); prefix covers list/detail/status.
  'siem_forwarder.changed': () => [qk.siemForwarders.all],
  'agent_fleet.changed': (d) => {
    const cid = clusterIdOf(d);
    const keys: QueryKey[] = [qk.agents.fleet];
    if (cid) keys.push(qk.agents.operations(cid));
    return keys;
  },
  'template_binding.changed': (d) => {
    const cid = clusterIdOf(d);
    return cid ? [qk.clusterPages.templateBinding(cid)] : [];
  },
  'registry.changed': (d) => {
    const cid = clusterIdOf(d);
    return cid ? [qk.clusterPages.registries(cid)] : [];
  },
  // Cluster snapshots/restores/schedules (payload kind discriminates; all
  // three views live on one page so they refresh together).
  'snapshot.changed': (d) => {
    const cid = clusterIdOf(d);
    return cid
      ? [
          qk.clusterPages.snapshots(cid),
          qk.clusterPages.snapshotSchedules(cid),
          qk.clusterPages.veleroStatus(cid),
        ]
      : [];
  },
  // ── P4.9 coverage completion ──
  // Payload kind discriminates rule|event|silence|baseline; unknown kinds
  // refresh the whole domain.
  'alerting.changed': (d) => {
    switch (d.kind) {
      case 'rule':
        return [qk.alerting.rules];
      case 'event':
        return [qk.alerting.eventsAll];
      case 'silence':
        return [qk.alerting.silences];
      case 'baseline':
        return [qk.anomalyBaselines.all];
      default:
        return [qk.alerting.all, qk.anomalyBaselines.all];
    }
  },
  'security_policy.changed': () => [qk.security.policies],
  // Prefix covers every params variant of the generic scans list.
  'security_scan.changed': () => [qk.security.scansAll],
  'network_access.changed': (d) => {
    const cid = clusterIdOf(d);
    return cid
      ? [
          qk.clusterPages.apiserverAllowlist(cid),
          qk.clusterPages.apiserverAllowlistSnapshots(cid),
        ]
      : [];
  },
  // Server-initiated Helm release changes; the agent's Helm-Secret informer
  // covers cluster-side churn via the Secret kind route above.
  'catalog_release.changed': (d) => {
    const keys: QueryKey[] = [qk.catalog.installedAll];
    const cid = clusterIdOf(d);
    if (cid) keys.push(qk.clusterPages.appsInstalled(cid));
    return keys;
  },
  // Prefix route (see module doc): any audit.<action> refreshes the
  // activity feed for principals allowed to receive audit events.
  [AUDIT_PREFIX]: () => [qk.activityAll],
};

/**
 * Route lookup used by the dispatcher: exact type first, then the `audit.`
 * prefix family. Unknown types route nowhere (their pages either subscribe
 * directly or poll via `liveFallback`).
 */
export function resolveEventRoute(
  type: string,
): ((d: LiveEventData) => QueryKey[]) | undefined {
  return (
    EVENT_ROUTES[type] ??
    (type.startsWith(AUDIT_PREFIX) ? EVENT_ROUTES[AUDIT_PREFIX] : undefined)
  );
}
