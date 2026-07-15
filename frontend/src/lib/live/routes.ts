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
 * the precise keys their views read from; kinds with no row (including the
 * P4.6 informer expansion + CRDs until their rows land) fall through to
 * `defaultK8sRoute` — the generic resource list for the kind.
 */
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
  Secret: (cid) => [qk.generic.resources(cid, 'secrets')],
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

/**
 * Event type → query keys. Every type the backend publishes today has a row
 * (exhaustiveness locked by `routes.test.ts`); P4.5/P4.6/P4.9 grow the table
 * per domain as publishers land.
 */
export const EVENT_ROUTES: Record<string, (d: LiveEventData) => QueryKey[]> = {
  'cluster.connected': clusterLivenessRoute,
  'cluster.disconnected': clusterLivenessRoute,
  'cluster.heartbeat': clusterLivenessRoute,
  // Merger-owned: list/detail status is patched in place, deliberately
  // without an invalidate (see cluster-merger.ts).
  'cluster.status_changed': () => [],
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
