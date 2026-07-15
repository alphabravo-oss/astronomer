/**
 * Exhaustiveness lock for the SSE invalidation map (D23):
 *  - every event type the backend publishes today has an EVENT_ROUTES row;
 *  - every key any route emits is built from the `query-keys.ts` factory
 *    (the expected values below are constructed from the factory — an
 *    ad-hoc inline key in routes.ts can never match them).
 * Grows with each publisher task (P4.5/P4.6/P4.9).
 */

import type { QueryKey } from '@tanstack/react-query';
import { queryKeys as qk } from '@/lib/query-keys';
import {
  AUDIT_PREFIX,
  defaultK8sRoute,
  EVENT_ROUTES,
  K8S_KIND_ROUTES,
  resolveEventRoute,
} from './routes';

const CID = 'c-1';
const EID = 'e-1';

/**
 * Wire types published today — mirrors the Type constants in
 * `internal/events/bus.go` that reach the SSE stream (the P4.5/P4.9 domain
 * publishers add their `.changed` types to this list as they land).
 */
const WIRE_TYPES = [
  'cluster.connected',
  'cluster.disconnected',
  'cluster.heartbeat',
  'cluster.status_changed',
  'cluster.metrics',
  'cluster.created',
  'cluster.updated',
  'cluster.deleted',
  'agent.reconnecting',
  'agent.failed',
  'cluster.k8s_changed',
  'cluster.registration.step',
  'cluster.registration.phase',
  'sys.ping',
  // P4.5 domain publishers (internal/events/bus.go `.changed` constants).
  'backup.changed',
  'fleet_operation.changed',
  'logging_operation.changed',
  'tool_operation.changed',
  'cis_scan.changed',
  'image_scan.changed',
  'argocd.changed',
  'admin_queue.changed',
  'siem_forwarder.changed',
  'agent_fleet.changed',
  'template_binding.changed',
  'registry.changed',
  'snapshot.changed',
] as const;

const livenessKeys = [qk.clusters.listAll, qk.agents.fleet, qk.clusters.detail(CID)];

/** Expected keys per event type, built exclusively from the factory. */
const EVENT_CASES: Record<string, QueryKey[]> = {
  'cluster.connected': livenessKeys,
  'cluster.disconnected': livenessKeys,
  'cluster.heartbeat': livenessKeys,
  // Merger-owned tick types: status is patched in place, metrics patches
  // rows and only invalidates the per-cluster metrics prefix.
  'cluster.status_changed': [],
  'cluster.metrics': [qk.clusters.metricsAll(CID)],
  'cluster.created': [qk.clusters.listAll],
  'cluster.updated': [qk.clusters.listAll, qk.clusters.detail(CID)],
  'cluster.deleted': livenessKeys,
  'agent.reconnecting': livenessKeys,
  'agent.failed': livenessKeys,
  'cluster.k8s_changed': [qk.clusters.podsAll(CID), qk.workloads.byCluster(CID)], // kind: Pod fixture
  'cluster.registration.step': [qk.clusterPages.registrationStatus(CID)],
  'cluster.registration.phase': [qk.clusterPages.registrationStatus(CID)],
  'sys.ping': [],
  [AUDIT_PREFIX]: [qk.activityAll],
  // P4.5 domain publishers — fixture carries clusterId + id.
  'backup.changed': [qk.backups.all, qk.backups.b2All],
  'fleet_operation.changed': [qk.fleetOperations.all, qk.clusters.listAll],
  'logging_operation.changed': [qk.logging.operationsAll],
  'tool_operation.changed': [qk.tools.operation(EID), qk.tools.clusterStatus(CID)],
  'cis_scan.changed': [qk.cis.scansAll],
  'image_scan.changed': [qk.clusterPages.imageVulnsAll(CID), qk.clusterPages.vulnerabilitySummary(CID)],
  'argocd.changed': [qk.argocd.all],
  'admin_queue.changed': [qk.adminOperations.queues, qk.adminOperations.dlq(EID)],
  'siem_forwarder.changed': [qk.siemForwarders.all],
  'agent_fleet.changed': [qk.agents.fleet, qk.agents.operations(CID)],
  'template_binding.changed': [qk.clusterPages.templateBinding(CID)],
  'registry.changed': [qk.clusterPages.registries(CID)],
  'snapshot.changed': [
    qk.clusterPages.snapshots(CID),
    qk.clusterPages.snapshotSchedules(CID),
    qk.clusterPages.veleroStatus(CID),
  ],
};

/** Velero CRD kinds share one expected-key set (Backup/Restore/Schedule). */
const veleroKeys = [
  qk.backups.all,
  qk.backups.b2All,
  qk.clusterPages.snapshots(CID),
  qk.clusterPages.snapshotSchedules(CID),
  qk.clusterPages.veleroStatus(CID),
];

/** Expected keys per `cluster.k8s_changed` kind, factory-built. */
const KIND_CASES: Record<string, QueryKey[]> = {
  Pod: [qk.clusters.podsAll(CID), qk.workloads.byCluster(CID)],
  Deployment: [qk.clusterPages.workloadKind(CID, 'deployments'), qk.workloads.byCluster(CID)],
  StatefulSet: [qk.clusterPages.workloadKind(CID, 'statefulsets'), qk.workloads.byCluster(CID)],
  DaemonSet: [qk.clusterPages.workloadKind(CID, 'daemonsets'), qk.workloads.byCluster(CID)],
  ReplicaSet: [qk.generic.resources(CID, 'replicasets'), qk.workloads.byCluster(CID)],
  Service: [qk.networking.services(CID)],
  Node: [qk.clusters.nodes(CID)],
  Event: [qk.clusters.eventsAll(CID)],
  ConfigMap: [qk.generic.resources(CID, 'configmaps')],
  Secret: [qk.generic.resources(CID, 'secrets')],
  // P4.6 informer expansion (agent metadata informers).
  Namespace: [qk.clusters.namespaces(CID)],
  Job: [
    qk.clusterPages.workloadKind(CID, 'jobs'),
    qk.generic.resources(CID, 'jobs'),
    qk.workloads.byCluster(CID),
  ],
  CronJob: [
    qk.clusterPages.workloadKind(CID, 'cronjobs'),
    qk.generic.resources(CID, 'cronjobs'),
    qk.workloads.byCluster(CID),
  ],
  Ingress: [qk.networking.ingresses(CID)],
  NetworkPolicy: [qk.networking.networkPolicies(CID)],
  PersistentVolume: [qk.storage.pvs(CID)],
  PersistentVolumeClaim: [qk.storage.pvcs(CID)],
  StorageClass: [qk.storage.storageClasses(CID)],
  HorizontalPodAutoscaler: [qk.generic.resources(CID, 'hpa')],
  Role: [qk.generic.resources(CID, 'k8s-roles')],
  RoleBinding: [qk.generic.resources(CID, 'k8s-rolebindings')],
  ClusterRole: [qk.generic.resources(CID, 'k8s-clusterroles')],
  ClusterRoleBinding: [qk.generic.resources(CID, 'k8s-clusterrolebindings')],
  // P4.6 CRD informers (discover-if-present).
  Backup: veleroKeys,
  Restore: veleroKeys,
  Schedule: veleroKeys,
  Application: [qk.argocd.all],
  ApplicationSet: [qk.argocd.all],
  VulnerabilityReport: [
    qk.clusterPages.imageVulnsAll(CID),
    qk.clusterPages.vulnerabilitySummary(CID),
  ],
  Constraint: [qk.gatekeeperConstraints(CID)],
};

const fixture = (kind?: string) => ({ clusterId: CID, id: EID, kind, namespace: 'ns', name: 'x' });

describe('EVENT_ROUTES', () => {
  it('covers every published wire type (exhaustiveness)', () => {
    for (const type of WIRE_TYPES) {
      expect(EVENT_ROUTES[type], `missing EVENT_ROUTES entry for ${type}`).toBeTypeOf('function');
    }
  });

  it('has no route without an expected-keys row in this test', () => {
    // Forces this lock file to grow with the table: a new EVENT_ROUTES row
    // fails here until its expected factory keys are spelled out above.
    expect(Object.keys(EVENT_ROUTES).sort()).toEqual(Object.keys(EVENT_CASES).sort());
  });

  it('every route emits only factory-built keys', () => {
    for (const [type, expected] of Object.entries(EVENT_CASES)) {
      const got = EVENT_ROUTES[type](fixture(type === 'cluster.k8s_changed' ? 'Pod' : undefined));
      expect(got, `keys for ${type}`).toEqual(expected);
    }
  });

  it('routes audit.<action> types through the prefix row', () => {
    const route = resolveEventRoute('audit.user.login');
    expect(route).toBe(EVENT_ROUTES[AUDIT_PREFIX]);
    expect(route!(fixture())).toEqual([qk.activityAll]);
  });

  it('routes unknown types nowhere', () => {
    expect(resolveEventRoute('mystery.changed')).toBeUndefined();
  });

  it('drops k8s_changed frames without a cluster id', () => {
    expect(EVENT_ROUTES['cluster.k8s_changed']({ kind: 'Pod' })).toEqual([]);
  });
});

describe('K8S_KIND_ROUTES', () => {
  it('maps every informer kind to its factory keys', () => {
    expect(Object.keys(K8S_KIND_ROUTES).sort()).toEqual(Object.keys(KIND_CASES).sort());
    for (const [kind, expected] of Object.entries(KIND_CASES)) {
      expect(K8S_KIND_ROUTES[kind](CID, fixture(kind)), `keys for kind ${kind}`).toEqual(expected);
    }
  });

  it('defaults unmapped kinds to the generic resource list', () => {
    expect(defaultK8sRoute(CID, fixture('ServiceAccount'))).toEqual([
      qk.generic.resources(CID, 'serviceaccounts'),
    ]);
    expect(defaultK8sRoute(CID, fixture('ResourceQuota'))).toEqual([
      qk.generic.resources(CID, 'resourcequotas'),
    ]);
    expect(defaultK8sRoute(CID, fixture('PodDisruptionBudget'))).toEqual([
      qk.generic.resources(CID, 'poddisruptionbudgets'),
    ]);
    expect(defaultK8sRoute(CID, fixture())).toEqual([]);
  });
});
