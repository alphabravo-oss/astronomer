/**
 * The monitoring-stack families, described as data.
 *
 * The lifecycle SCREENS are one component (stack-lifecycle-panel.tsx) driven by
 * the specs below, for the same reason the backend collapsed its two shared
 * families into `sharedStackLifecycle` in 7ad9321: the families differ only in
 * which fields they carry, and every copy of a lifecycle screen is a copy of
 * the RBAC gating, the in-flight handling and the uninstall confirmation, one
 * of which will eventually be dropped.
 *
 * Everything here is pure — no React, no network — so the form round-trip
 * (status → form values → request body) is unit-testable on its own.
 *
 * FIELD NAMES ARE REQUEST-BODY KEYS AND ARE camelCase. This surface is the
 * repo's exception to the snake_case request-body convention; see the header of
 * src/lib/api/monitoring-stack.ts. `buildStackBody` emits `field.name`
 * verbatim, so renaming a field here silently changes the wire payload.
 */
import type {
  MonitoringStackRequestBody,
  MonitoringStackStatusBase,
  SharedGrafanaStatus,
} from '@/lib/api/monitoring-stack';

/**
 * `tristate` is a boolean the request can also leave UNSET — an empty form value
 * means "omit this key from the body", which is how the backend's `*bool`
 * pointers are asked to apply their own policy. It exists because the status
 * endpoints do not project these fields back (see the note on
 * SERVER_BLIND_FIELDS), so a plain checkbox could only ever replay a guess.
 */
export type StackFieldKind =
  | 'text'
  | 'number'
  | 'boolean'
  | 'tristate'
  | 'cluster'
  | 'storageConfig';

export interface StackField {
  /** Request-body key, camelCase. Also the status-response key we seed from. */
  name: string;
  label: string;
  kind: StackFieldKind;
  help?: string;
  placeholder?: string;
  /** `tristate` only: what the empty option means, e.g. "Use platform default". */
  unsetLabel?: string;
  /** Blocks submission when empty. The backend 400s on these. */
  required?: boolean;
  /**
   * Changing this field cannot be an in-place Helm upgrade — the backend
   * answers 409 replace_required and the operator must Replace (uninstall +
   * reinstall). Marked in the form so the consequence is visible BEFORE the
   * request, not only in the conflict banner afterwards.
   */
  replaceTrigger?: boolean;
}

export interface StackFamilySpec {
  /** Matches MonitoringStackTarget['kind']. */
  key: 'cluster' | 'thanos' | 'alertmanager' | 'grafana' | 'loki';
  title: string;
  description: string;
  /**
   * Spelled into the uninstall confirmation so the dialog names what is
   * destroyed rather than saying "are you sure".
   */
  destroys: string;
  fields: StackField[];
  /**
   * The backend's own defaults, mirrored here so a not-yet-installed stack
   * shows what it is about to get instead of a grid of empty boxes.
   *
   * They also matter on UPGRADE. The replace-required checks compare the
   * persisted value against the decoded request field, and the request is
   * decoded before most defaults are applied — so an upgrade that omits a field
   * the operator never touched reads as a CHANGE and 409s. Seeding the form
   * from status and posting every field back is what keeps that from happening;
   * these defaults are the seed of last resort when there is no status yet.
   */
  defaults: Record<string, string>;
}

/**
 * Form state. Everything is a string (checkboxes hold 'true'/'false') so a
 * partially-typed number or a cleared field has one unambiguous representation
 * and `buildStackBody` owns the coercion in one place.
 */
export type StackFormValues = Record<string, string>;

/**
 * FIELDS NO STATUS ENDPOINT RETURNS — the form is blind to their current value
 * and must therefore never replay one.
 *
 *   per-cluster    chartVersion, scrapeInterval, enableGrafana,
 *                  enableAlertmanager, autoRollbackOnFailure
 *                  (GetStackStatus builds its map at
 *                  internal/handler/monitoring_stack_cluster.go:325-341 and
 *                  emits none of them)
 *   shared Thanos  autoRollbackOnFailure
 *   shared Alertm. autoRollbackOnFailure
 *                  (both statusFields projections —
 *                  internal/handler/monitoring_stack_shared.go:363-378 and
 *                  :418-431 — omit it, although the payload builders DO persist
 *                  it into metadata at :631 and :689)
 *
 * Seeding them from `defaults` and posting them on every Upgrade is a real
 * behaviour change, not a cosmetic gap:
 *
 *   - chartVersion reaches Helm as the release's chart version
 *     (internal/handler/monitoring.go:493), so an operator running a newer chart
 *     is pinned back to 61.3.2 by any Upgrade launched from this screen.
 *   - enableGrafana / enableAlertmanager come back ON after being unticked.
 *   - autoRollbackOnFailure defeats the platform policy outright:
 *     resolveAutoRollbackPolicy (internal/handler/monitoring_operations.go:731-734)
 *     checks `if override != nil` BEFORE consulting
 *     operationPolicies.defaultAutoRollbackOnFailure, so an admin who set that
 *     to false across all clusters (to leave failed releases in place for forensics)
 *     finds this UI rolling back anyway.
 *
 * None of the five are replace triggers (clusterMonitoringReplaceRequired,
 * internal/handler/monitoring_stack_cluster.go:490-516, keys off namespace,
 * release name, object storage and storage class/size only), so nothing 409s to
 * warn the operator either.
 *
 * The fix here is to make them EXPRESS UNSET: the text fields carry no default
 * so an untouched box stays empty and buildStackBody drops it, and the booleans
 * are `tristate` so "" means "omit the key". Follow-up owed on the backend —
 * add these five to the cluster statusFields and autoRollbackOnFailure to all
 * three — after which they can be seeded honestly and this list shrinks.
 */
export const SERVER_BLIND_FIELDS: readonly string[] = [
  'chartVersion',
  'scrapeInterval',
  'enableGrafana',
  'enableAlertmanager',
  'autoRollbackOnFailure',
];

/** Copy for the unset option on the two tri-state shapes we render. */
const PLATFORM_DEFAULT = 'Use platform default';
const CHART_DEFAULT = 'Use chart default (enabled)';

// ─────────────────────────────────────────────────────────────────────
// Per-cluster kube-prometheus-stack
// ─────────────────────────────────────────────────────────────────────

export const CLUSTER_STACK_FAMILY: StackFamilySpec = {
  key: 'cluster',
  title: 'Cluster monitoring stack',
  description:
    'kube-prometheus-stack on this cluster — Prometheus, optionally Grafana and Alertmanager, with a Thanos sidecar shipping blocks to shared object storage. Cluster Grafana is this Prometheus (15d) and survives an Astronomer outage; fleet Grafana is the lobby.',
  destroys:
    'the Helm release, its Prometheus StatefulSet and the PersistentVolumeClaims holding this cluster’s local metrics',
  fields: [
    {
      name: 'namespace',
      label: 'Namespace',
      kind: 'text',
      placeholder: 'monitoring',
      replaceTrigger: true,
      help: 'Namespace the release is installed into.',
    },
    {
      name: 'releaseName',
      label: 'Release name',
      kind: 'text',
      placeholder: 'prometheus',
      replaceTrigger: true,
      help: 'Helm release name.',
    },
    {
      name: 'chartVersion',
      label: 'Chart version',
      kind: 'text',
      placeholder: '61.3.2',
      help: 'Left empty the backend keeps its own default. The status endpoint does not report the installed chart version for this family, so this box is never pre-filled — typing a value pins the release to it.',
    },
    {
      name: 'retention',
      label: 'Local retention',
      kind: 'text',
      placeholder: '15d',
      help: 'How long Prometheus keeps blocks locally before Thanos takes over.',
    },
    {
      name: 'scrapeInterval',
      label: 'Scrape interval',
      kind: 'text',
      placeholder: '30s',
      help: 'Not reported by the status endpoint; left empty the backend applies 30s.',
    },
    {
      name: 'storageClass',
      label: 'Storage class',
      kind: 'text',
      placeholder: 'default',
      replaceTrigger: true,
    },
    {
      name: 'storageSize',
      label: 'Storage size',
      kind: 'text',
      placeholder: '50Gi',
      // clusterMonitoringReplaceRequired treats a storage-size change as
      // needing a reinstall (monitoring_stack_cluster.go:512-514), same as the
      // shared Alertmanager family already declares.
      replaceTrigger: true,
    },
    {
      name: 'storageConfigId',
      label: 'Object storage',
      kind: 'storageConfig',
      replaceTrigger: true,
      help: 'Backup storage config used for the Thanos sidecar’s objstore secret. When shared Thanos is healthy this is pre-filled (Use shared Thanos bucket).',
    },
    {
      name: 'enableGrafana',
      label: 'Grafana',
      kind: 'tristate',
      unsetLabel: 'Use backend default',
      help: 'Cluster Grafana talks to this Prometheus (15d) and survives an Astronomer outage. Omitted: enabled, except new (not_configured) stacks default off when fleet Grafana is healthy.',
    },
    {
      name: 'enableAlertmanager',
      label: 'Alertmanager',
      kind: 'tristate',
      unsetLabel: CHART_DEFAULT,
    },
    { name: 'thanosSidecarEnabled', label: 'Thanos sidecar', kind: 'boolean' },
    {
      name: 'autoRollbackOnFailure',
      label: 'Roll back on failure',
      kind: 'tristate',
      unsetLabel: PLATFORM_DEFAULT,
      help: 'Ask the reconciler to roll the release back to its previous revision if the install fails its health checks. Left unset, the platform-wide operationPolicies.defaultAutoRollbackOnFailure applies.',
    },
  ],
  defaults: {
    namespace: 'monitoring',
    releaseName: 'prometheus',
    retention: '15d',
    storageClass: 'default',
    storageSize: '50Gi',
    storageConfigId: '',
    thanosSidecarEnabled: 'true',
    // chartVersion / scrapeInterval / enableGrafana / enableAlertmanager /
    // autoRollbackOnFailure are deliberately ABSENT — see SERVER_BLIND_FIELDS.
    // Their placeholders and unset options show the backend's default without
    // the form claiming to know the current one.
  },
};

// ─────────────────────────────────────────────────────────────────────
// Shared Thanos
// ─────────────────────────────────────────────────────────────────────

export const SHARED_THANOS_FAMILY: StackFamilySpec = {
  key: 'thanos',
  title: 'Shared Thanos',
  description:
    'The deployment-wide long-term metrics tier: query, query-frontend, store gateway and compactor, reading the blocks every cluster stack ships to object storage.',
  destroys:
    'the Thanos Helm release on the management cluster. Every cluster’s long-term metrics and the platform’s Thanos query endpoint go away with it; the blocks in object storage are NOT deleted',
  fields: [
    {
      name: 'managementClusterId',
      label: 'Management cluster',
      kind: 'cluster',
      required: true,
      help: 'Cluster the shared Thanos release runs on.',
    },
    {
      name: 'storageConfigId',
      label: 'Object storage',
      kind: 'storageConfig',
      required: true,
      replaceTrigger: true,
      help: 'Bucket Thanos reads blocks from. The backend renders it into an objstore secret.',
    },
    {
      name: 'namespace',
      label: 'Namespace',
      kind: 'text',
      placeholder: 'monitoring',
      replaceTrigger: true,
    },
    {
      name: 'releaseName',
      label: 'Release name',
      kind: 'text',
      placeholder: 'thanos',
      replaceTrigger: true,
    },
    { name: 'chartVersion', label: 'Chart version', kind: 'text', placeholder: '1.23.0' },
    { name: 'queryReplicas', label: 'Query replicas', kind: 'number', placeholder: '2' },
    {
      name: 'storeGatewayReplicas',
      label: 'Store gateway replicas',
      kind: 'number',
      placeholder: '1',
    },
    { name: 'compactorReplicas', label: 'Compactor replicas', kind: 'number', placeholder: '1' },
    {
      name: 'autoRollbackOnFailure',
      label: 'Roll back on failure',
      kind: 'tristate',
      unsetLabel: PLATFORM_DEFAULT,
    },
  ],
  defaults: {
    managementClusterId: '',
    storageConfigId: '',
    namespace: 'monitoring',
    releaseName: 'thanos',
    chartVersion: '1.23.0',
    queryReplicas: '2',
    storeGatewayReplicas: '1',
    compactorReplicas: '1',
    // autoRollbackOnFailure absent — see SERVER_BLIND_FIELDS.
  },
};

// ─────────────────────────────────────────────────────────────────────
// Shared Alertmanager
// ─────────────────────────────────────────────────────────────────────

export const SHARED_ALERTMANAGER_FAMILY: StackFamilySpec = {
  key: 'alertmanager',
  title: 'Shared Alertmanager',
  description:
    'The deployment-wide alert router. Platform alert rules and notification channels deliver through this release.',
  destroys:
    'the Alertmanager Helm release on the management cluster and its notification silences. Platform alerts stop being delivered until it is reinstalled',
  fields: [
    {
      name: 'managementClusterId',
      label: 'Management cluster',
      kind: 'cluster',
      required: true,
      help: 'Cluster the shared Alertmanager release runs on.',
    },
    {
      name: 'namespace',
      label: 'Namespace',
      kind: 'text',
      placeholder: 'monitoring',
      replaceTrigger: true,
    },
    {
      name: 'releaseName',
      label: 'Release name',
      kind: 'text',
      placeholder: 'astronomer-alertmanager',
      replaceTrigger: true,
    },
    { name: 'chartVersion', label: 'Chart version', kind: 'text', placeholder: '1.18.0' },
    { name: 'replicas', label: 'Replicas', kind: 'number', placeholder: '1' },
    {
      name: 'storageClass',
      label: 'Storage class',
      kind: 'text',
      placeholder: 'default',
      replaceTrigger: true,
    },
    {
      name: 'storageSize',
      label: 'Storage size',
      kind: 'text',
      placeholder: '2Gi',
      replaceTrigger: true,
    },
    {
      name: 'autoRollbackOnFailure',
      label: 'Roll back on failure',
      kind: 'tristate',
      unsetLabel: PLATFORM_DEFAULT,
    },
  ],
  defaults: {
    managementClusterId: '',
    namespace: 'monitoring',
    releaseName: 'astronomer-alertmanager',
    chartVersion: '1.18.0',
    replicas: '1',
    storageClass: '',
    storageSize: '2Gi',
    // autoRollbackOnFailure absent — see SERVER_BLIND_FIELDS.
  },
};

// ─────────────────────────────────────────────────────────────────────
// Shared Grafana (ticket bounce + grafana-proxy on grafana.<host>)
// ─────────────────────────────────────────────────────────────────────

export const SHARED_GRAFANA_FAMILY: StackFamilySpec = {
  key: 'grafana',
  title: 'Shared Grafana',
  description:
    'Fleet Grafana on grafana.<platform-host> via grafana-proxy (ticket bounce, Explore-lock). Datasources are shared Thanos (when installed) and an optional BYO Loki URL. Open is shown only when authMode is proxy.',
  destroys:
    'the Grafana Helm release on the management cluster, grafana-proxy, and its provisioned dashboard/datasource ConfigMaps. Per-cluster Grafana is not touched',
  fields: [
    {
      name: 'managementClusterId',
      label: 'Management cluster',
      kind: 'cluster',
      required: true,
      help: 'Cluster the shared Grafana release runs on.',
    },
    {
      name: 'namespace',
      label: 'Namespace',
      kind: 'text',
      placeholder: 'monitoring',
      replaceTrigger: true,
    },
    {
      name: 'releaseName',
      label: 'Release name',
      kind: 'text',
      placeholder: 'astronomer-grafana',
      replaceTrigger: true,
    },
    { name: 'chartVersion', label: 'Chart version', kind: 'text', placeholder: '8.12.1' },
    { name: 'replicas', label: 'Replicas', kind: 'number', placeholder: '1' },
    {
      name: 'storageClass',
      label: 'Storage class',
      kind: 'text',
      placeholder: 'default',
      replaceTrigger: true,
      help: 'Used only when a PVC is requested below.',
    },
    {
      name: 'storageSize',
      label: 'Storage size',
      kind: 'text',
      placeholder: '1Gi',
      replaceTrigger: true,
      help: 'Optional 1Gi PVC for stars and prefs. Leave empty to stay stateless. Dashboards and datasources stay sidecar ConfigMaps.',
    },
    {
      name: 'ingressHost',
      label: 'Grafana host',
      kind: 'text',
      placeholder: 'grafana.example.com',
      help: 'Defaults to grafana.<Astronomer ServerURL host>. Never taken from the Astronomer chart ingress.host.',
    },
    {
      name: 'logDatasourceUrl',
      label: 'BYO Loki URL',
      kind: 'text',
      placeholder: 'http://loki.example:3100',
      help: 'Optional Grafana-owned Loki datasource. Astronomer Loki is a later family.',
    },
    {
      name: 'autoRollbackOnFailure',
      label: 'Roll back on failure',
      kind: 'tristate',
      unsetLabel: PLATFORM_DEFAULT,
    },
  ],
  defaults: {
    managementClusterId: '',
    namespace: 'monitoring',
    releaseName: 'astronomer-grafana',
    chartVersion: '8.12.1',
    replicas: '1',
    storageClass: '',
    storageSize: '',
    ingressHost: '',
    logDatasourceUrl: '',
  },
};

// ─────────────────────────────────────────────────────────────────────
// Shared Loki (sizer-gated ClusterIP warehouse; no Ingress until tokens)
// ─────────────────────────────────────────────────────────────────────

export const SHARED_LOKI_FAMILY: StackFamilySpec = {
  key: 'loki',
  title: 'Shared Loki',
  description:
    'Optional Astronomer log warehouse on the management cluster. Install is refused unless the sizer passes. Gateway and loki-auth stay ClusterIP until ingest tokens exist. Object storage uses the same backup-storage config as Thanos, with prefix join(prefix, "loki").',
  destroys:
    'the Loki Helm release and its WAL disks. Index and chunks in object storage (computed Loki prefix) are NOT deleted',
  fields: [
    {
      name: 'managementClusterId',
      label: 'Management cluster',
      kind: 'cluster',
      required: true,
      help: 'Cluster the shared Loki release runs on.',
    },
    {
      name: 'storageConfigId',
      label: 'Object storage',
      kind: 'storageConfig',
      required: true,
      replaceTrigger: true,
      help: 'Same backup-storage config as Thanos. Loki writes under join(prefix, "loki"), never Thanos objstore.yml.',
    },
    {
      name: 'ingestHostname',
      label: 'Ingest hostname',
      kind: 'text',
      required: true,
      placeholder: 'loki-ingest.example.com',
      help: 'Required and explicit. Never derived from the Astronomer ingress host. Ingress is not created until tokens exist.',
    },
    {
      name: 'namespace',
      label: 'Namespace',
      kind: 'text',
      placeholder: 'monitoring',
      replaceTrigger: true,
    },
    {
      name: 'releaseName',
      label: 'Release name',
      kind: 'text',
      placeholder: 'astronomer-loki',
      replaceTrigger: true,
    },
    { name: 'chartVersion', label: 'Chart version', kind: 'text', placeholder: '6.27.0' },
    {
      name: 'storageClass',
      label: 'Storage class',
      kind: 'text',
      placeholder: 'default',
      replaceTrigger: true,
      help: 'RWO class for WAL disks.',
    },
    {
      name: 'walStorageSize',
      label: 'WAL size',
      kind: 'text',
      placeholder: '10Gi',
      replaceTrigger: true,
    },
    {
      name: 'mode',
      label: 'Mode',
      kind: 'text',
      placeholder: 'singleBinary',
      replaceTrigger: true,
      help: 'Empty = sizer pick. May only narrow (singleBinary when SimpleScalable was selected). Mode change replaces the release (WAL lost, bucket kept).',
    },
    { name: 'retention', label: 'Retention', kind: 'text', placeholder: '14d' },
    {
      name: 'skipDiskCheck',
      label: 'Skip WAL disk check',
      kind: 'tristate',
      unsetLabel: 'Probe WAL on install/replace',
    },
    {
      name: 'autoRollbackOnFailure',
      label: 'Roll back on failure',
      kind: 'tristate',
      unsetLabel: PLATFORM_DEFAULT,
    },
  ],
  defaults: {
    managementClusterId: '',
    storageConfigId: '',
    ingestHostname: '',
    namespace: 'monitoring',
    releaseName: 'astronomer-loki',
    chartVersion: '6.27.0',
    storageClass: 'default',
    walStorageSize: '10Gi',
    mode: '',
    retention: '14d',
  },
};

/** Public Grafana URL only when the proxy + ticket bounce are installed. */
export function fleetGrafanaOpenURL(
  status?: Pick<SharedGrafanaStatus, 'status' | 'authMode' | 'grafanaHost' | 'ingressHost'> | null,
): string | null {
  if (!status || status.authMode !== 'proxy') return null;
  if (!stackIsInstalled(status)) return null;
  const raw = (status.grafanaHost || status.ingressHost || '').trim();
  if (!raw) return null;
  const host = raw.replace(/^https?:\/\//, '').replace(/\/+$/, '');
  if (!host) return null;
  return `https://${host}/`;
}

/** Fleet Grafana with this cluster pre-selected. Null unless the Open button exists. */
export function fleetGrafanaClusterURL(
  status: Pick<SharedGrafanaStatus, 'status' | 'authMode' | 'grafanaHost' | 'ingressHost'> | null | undefined,
  clusterId: string,
): string | null {
  const base = fleetGrafanaOpenURL(status);
  if (!base || !clusterId) return null;
  return `${base}?var-cluster=${encodeURIComponent(clusterId)}`;
}

// ─────────────────────────────────────────────────────────────────────
// Status interpretation
// ─────────────────────────────────────────────────────────────────────

/**
 * The two recorded states that mean "there is no release": a stack that was
 * never configured, and one that has been uninstalled. This is the same test
 * the backend applies before deciding whether a change needs a replace
 * (clusterMonitoringReplaceRequired / sharedThanosReplaceRequired), so the
 * Install-vs-Upgrade split in the UI matches what the API will accept.
 */
export const ABSENT_STACK_STATUSES: readonly string[] = ['not_configured', 'uninstalled'];

export function stackIsInstalled(status: MonitoringStackStatusBase | undefined | null): boolean {
  if (!status?.status) return false;
  return !ABSENT_STACK_STATUSES.includes(status.status);
}

/** Recorded lifecycle status → a StatusBadge tone from lib/utils statusBgColor. */
export function stackStatusTone(status: string | undefined): string {
  switch (status) {
    case 'healthy':
    case 'configured':
    case 'reinstalled':
      return 'healthy';
    case 'installing':
    case 'updating':
      return 'progressing';
    case 'drifted':
      return 'drifted';
    case 'uninstalled':
    case 'not_configured':
    case undefined:
      return 'unknown';
    default:
      return 'unknown';
  }
}

export function stackStatusLabel(status: string | undefined): string {
  switch (status) {
    case 'not_configured':
      return 'Not installed';
    case 'uninstalled':
      return 'Uninstalled';
    case 'installing':
      return 'Installing';
    case 'updating':
      return 'Updating';
    case 'reinstalled':
      return 'Reinstalled';
    case 'configured':
      return 'Configured';
    case 'healthy':
      return 'Healthy';
    case 'drifted':
      return 'Drifted';
    default:
      return status ? status.replace(/_/g, ' ') : 'Unknown';
  }
}

// ─────────────────────────────────────────────────────────────────────
// Form round-trip
// ─────────────────────────────────────────────────────────────────────

function stringifyStatusValue(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'number') return Number.isFinite(value) ? String(value) : null;
  if (typeof value === 'string') return value;
  return null;
}

/**
 * Seed the form from the recorded desired state, falling back to the backend's
 * defaults. Seeding from status is not a nicety: see the note on
 * StackFamilySpec.defaults — an upgrade posts every field back, and a field
 * that silently reverted to a default would read to the backend as a change and
 * be rejected with replace_required.
 */
export function seedStackValues(
  spec: StackFamilySpec,
  status?: MonitoringStackStatusBase | null,
): StackFormValues {
  const source = (status ?? {}) as Record<string, unknown>;
  const values: StackFormValues = {};
  for (const field of spec.fields) {
    const fromStatus = stackIsInstalled(status) ? stringifyStatusValue(source[field.name]) : null;
    values[field.name] = fromStatus ?? spec.defaults[field.name] ?? '';
  }
  return values;
}

/** Fields whose value differs from what the recorded state would replay. */
export function replaceTriggeringChanges(
  spec: StackFamilySpec,
  values: StackFormValues,
  status?: MonitoringStackStatusBase | null,
): string[] {
  if (!stackIsInstalled(status)) return [];
  const seeded = seedStackValues(spec, status);
  return spec.fields
    .filter((field) => field.replaceTrigger && (values[field.name] ?? '') !== (seeded[field.name] ?? ''))
    .map((field) => field.label);
}

/** Required fields with no value — the submit blockers. */
export function missingRequiredFields(
  spec: StackFamilySpec,
  values: StackFormValues,
): StackField[] {
  return spec.fields.filter((field) => field.required && !(values[field.name] ?? '').trim());
}

/**
 * Form values → request body.
 *
 * Empty is OMITTED, for every kind. That is the request's only way to say
 * "unset", and the backend's fields are built for it: strings fall back to the
 * handler's own default rather than writing a blank namespace, and the `*bool`
 * pointers fall back to policy (`autoRollbackOnFailure`) or to the backend
 * default (`enableGrafana` is chart-enabled except new stacks when fleet
 * Grafana is healthy; `enableAlertmanager` stays chart-enabled).
 *
 * `boolean` fields are still always sent, because those ARE round-tripped
 * through status (thanosSidecarEnabled) and so the form genuinely knows what
 * the operator chose. `tristate` fields are not — sending a guess for them
 * overrides a platform policy the UI cannot see. See SERVER_BLIND_FIELDS.
 */
export function buildStackBody(
  spec: StackFamilySpec,
  values: StackFormValues,
): MonitoringStackRequestBody {
  const body: Record<string, unknown> = {};
  for (const field of spec.fields) {
    const raw = values[field.name];
    if (field.kind === 'boolean') {
      body[field.name] = raw === 'true';
      continue;
    }
    const trimmed = (raw ?? '').trim();
    if (!trimmed) continue;
    if (field.kind === 'tristate') {
      body[field.name] = trimmed === 'true';
      continue;
    }
    if (field.kind === 'number') {
      const parsed = Number(trimmed);
      if (Number.isFinite(parsed)) body[field.name] = parsed;
      continue;
    }
    body[field.name] = trimmed;
  }
  return body as MonitoringStackRequestBody;
}
