'use client';

import { useMemo, useState } from 'react';
import { useRouter } from '@/lib/navigation';
import { Link } from '@/lib/link';
import { useK8sResource, useWorkloadPods } from '@/lib/hooks';
import { useClusterResourcePermission } from '@/lib/permission-hooks';
import { YamlPanel } from '@/components/ui/yaml-view-dialog';
import { PodLogsViewer } from '@/components/workloads/pod-logs-viewer';
import { PodTerminal } from '@/components/workloads/pod-terminal';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { detailHref, KIND_TO_RESOURCE_TYPE } from '@/lib/k8s-paths';
import { ResourceActions } from '@/components/workloads/resource-actions';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { Pod } from '@/types';
import { Loader2, ArrowLeft } from 'lucide-react';

interface ResourceDetailProps {
  clusterId: string;
  resourceType: string;
  namespace?: string;
  name: string;
  /** K8s API path to the single object (e.g. "api/v1/namespaces/default/pods/my-pod"). */
  k8sPath: string;
  /** RBAC resource to gate on, when it differs from the canonical mapping of
   * resourceType (custom resources gate on 'custom_resources', not 'clusters'). */
  permissionResource?: string;
}

// ponytail: Logs/Exec are pod-only; we append them to this base set when
// resourceType === 'pods' rather than building a per-kind tab registry.
const BASE_TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'yaml', label: 'YAML' },
  { id: 'events', label: 'Events' },
  { id: 'related', label: 'Related' },
] as const;
type TabId = 'overview' | 'yaml' | 'conditions' | 'events' | 'related' | 'logs' | 'exec';

// k8s container spec/status (camelCase keys have no underscores, so they
// survive the api client's snake->camel transform untouched).
interface ContainerSpec {
  name?: string;
  image?: string;
}
interface ContainerStatus {
  name?: string;
  image?: string;
  ready?: boolean;
  restartCount?: number;
  state?: Record<string, { reason?: string } | undefined>;
}

interface K8sObject {
  kind?: string;
  metadata?: {
    name?: string;
    namespace?: string;
    creationTimestamp?: string;
    uid?: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
    ownerReferences?: Array<{ kind: string; name: string; uid?: string }>;
  };
  spec?: {
    nodeName?: string;
    containers?: ContainerSpec[];
    // Workloads (Deployment/StatefulSet/ReplicaSet)
    replicas?: number;
    paused?: boolean; // Deployment rollout
    suspend?: boolean; // CronJob
    // Service
    type?: string;
    clusterIP?: string;
    selector?: Record<string, string>;
    ports?: Array<{ name?: string; port?: number; targetPort?: number | string; protocol?: string; nodePort?: number }>;
    // Ingress
    ingressClassName?: string;
    rules?: Array<{ host?: string }>;
    tls?: Array<{ hosts?: string[]; secretName?: string }>;
    // PVC
    storageClassName?: string;
    volumeName?: string;
    resources?: { requests?: { storage?: string } };
  };
  status?: {
    conditions?: Array<Record<string, unknown>>;
    phase?: string;
    podIP?: string;
    containerStatuses?: ContainerStatus[];
    // PVC
    capacity?: { storage?: string };
  };
  data?: Record<string, string>;
}

export function ResourceDetail({ clusterId, resourceType, namespace, name, k8sPath, permissionResource }: ResourceDetailProps) {
  const router = useRouter();
  const [tab, setTab] = useState<TabId>('overview');

  // Gate on the SAME canonical permission resource the list rows use, so the
  // detail view doesn't fall through to the generic 'clusters' verb (GATE-A bug).
  // Custom resources pass permissionResource='custom_resources' explicitly since
  // their plural has no canonical mapping. Server-side RBAC stays the real gate.
  const read = useClusterResourcePermission(clusterId, resourceType, 'read', permissionResource);
  const update = useClusterResourcePermission(clusterId, resourceType, 'update', permissionResource);
  // ponytail: always call these (rules-of-hooks); only consulted for pods.
  const logsPerm = useClusterResourcePermission(clusterId, resourceType, 'logs', permissionResource);
  const execPerm = useClusterResourcePermission(clusterId, resourceType, 'exec', permissionResource);

  const isPod = resourceType === 'pods';
  const { data: obj, isLoading, error } = useK8sResource(clusterId, k8sPath, read.allowed);
  const conditions = (obj as K8sObject | undefined)?.status?.conditions ?? [];
  const tabs = useMemo(() => {
    const out: Array<{ id: TabId; label: string }> = [...BASE_TABS];
    // Conditions tab (Rancher-style) when the object reports any — after YAML.
    if (conditions.length > 0) out.splice(2, 0, { id: 'conditions', label: 'Conditions' });
    if (isPod) {
      if (logsPerm.allowed) out.push({ id: 'logs', label: 'Logs' });
      if (execPerm.allowed) out.push({ id: 'exec', label: 'Exec' });
    }
    return out;
  }, [isPod, logsPerm.allowed, execPerm.allowed, conditions.length]);

  if (!read.allowed) {
    return (
      <div className="flex items-center justify-center py-24 text-sm text-muted-foreground">
        {read.disabledReason || read.reason}
      </div>
    );
  }

  const o = obj as K8sObject | undefined;
  const kind = o?.kind || resourceType;
  const created = o?.metadata?.creationTimestamp;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-start gap-4">
        <button
          onClick={() => router.back()}
          className="mt-1 p-1 rounded-md hover:bg-accent transition-colors text-muted-foreground hover:text-foreground"
          aria-label="Back"
        >
          <ArrowLeft className="h-5 w-5" />
        </button>
        {/* min-w-[10rem] (not min-w-0) so on narrow viewports the actions wrap
            below instead of squeezing the title to zero width. */}
        <div className="flex-1 min-w-[10rem]">
          <h1 className="text-xl font-semibold text-foreground tracking-tight font-mono truncate">{name}</h1>
          <div className="mt-1 flex items-center gap-4 text-xs text-muted-foreground">
            <span>Kind: {kind}</span>
            {namespace && <span>Namespace: {namespace}</span>}
            {created && <span>Age: {formatRelativeTime(created)}</span>}
          </div>
        </div>
        {/* Management actions (Delete everywhere; Scale/Restart for workloads,
            Pause for Deployments, Suspend for CronJobs), once the object and
            its real Kind have loaded. */}
        {o?.kind && (
          <ResourceActions
            clusterId={clusterId}
            kind={o.kind}
            namespace={namespace}
            name={name}
            replicas={o.spec?.replicas}
            paused={o.kind === 'Deployment' ? (o.spec?.paused ?? false) : undefined}
            suspended={o.kind === 'CronJob' ? (o.spec?.suspend ?? false) : undefined}
            jobTemplate={o.kind === 'CronJob' ? asRecord(o.spec).jobTemplate as Record<string, unknown> : undefined}
            k8sPath={k8sPath}
            permissionResource={permissionResource}
            onDeleted={() => router.back()}
          />
        )}
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex gap-0 -mb-px">
          {tabs.map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={cn(
                'px-4 py-2 text-sm font-medium border-b-2 transition-colors',
                tab === t.id
                  ? 'border-foreground text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground hover:border-muted-foreground/30'
              )}
            >
              {t.label}
            </button>
          ))}
        </nav>
      </div>

      {tab === 'overview' && (
        isLoading ? (
          <div className="flex items-center justify-center py-24">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : error ? (
          <div className="py-24 text-center text-sm text-status-error">
            Failed to load resource: {(error as Error).message}
          </div>
        ) : (
          <ResourceOverview obj={o} resourceType={resourceType} />
        )
      )}

      {tab === 'yaml' && (
        <div className="h-[70vh] rounded-lg border border-border overflow-hidden">
          <YamlPanel clusterId={clusterId} k8sPath={k8sPath} allowEdit={update.allowed} />
        </div>
      )}

      {tab === 'conditions' && <ConditionsTab conditions={conditions} />}

      {tab === 'events' && (
        <ResourceEvents clusterId={clusterId} namespace={namespace} name={name} kind={kind} />
      )}

      {tab === 'related' && (
        <RelatedResources clusterId={clusterId} namespace={namespace} name={name} kind={kind} obj={o} />
      )}

      {/* Pod-only tabs. ponytail: reuse PodLogsViewer/PodTerminal as-is by
          synthesizing the minimal Pod shape they need from the raw k8s object. */}
      {tab === 'logs' && isPod && namespace && (
        <PodLogsViewer
          clusterId={clusterId}
          namespace={namespace}
          pods={[podForViewer(o, namespace)]}
          selectedPod={name}
          onPodChange={() => { /* single pod here; selector is a no-op */ }}
        />
      )}

      {tab === 'exec' && isPod && namespace && (
        <div className="h-[70vh]">
          <PodTerminal
            clusterId={clusterId}
            namespace={namespace}
            pod={name}
            container={podContainerNames(o)[0] ?? ''}
            containers={podContainerNames(o)}
          />
        </div>
      )}
    </div>
  );
}

// ── Pod helpers (plan C1) ──

function podContainerNames(obj?: K8sObject): string[] {
  return (obj?.spec?.containers ?? []).map((c) => c.name ?? '').filter(Boolean);
}

/**
 * Build the minimal `Pod` shape PodLogsViewer/PodTerminal consume from the raw
 * k8s object. ponytail: we only populate the fields those components touch
 * (name, namespace, containers) — not the full app Pod model.
 */
function podForViewer(obj: K8sObject | undefined, namespace: string): Pod {
  const containers = (obj?.spec?.containers ?? []).map((c) => ({
    name: c.name ?? '',
    image: c.image ?? '',
    status: 'running' as const,
    ready: true,
    restartCount: 0,
  }));
  return {
    name: obj?.metadata?.name ?? '',
    namespace,
    clusterId: '',
    phase: (obj?.status?.phase ?? 'Unknown') as Pod['phase'],
    status: obj?.status?.phase ?? 'Unknown',
    ready: '',
    restarts: 0,
    node: obj?.spec?.nodeName ?? '',
    ip: obj?.status?.podIP ?? '',
    containers,
    conditions: [],
    createdAt: obj?.metadata?.creationTimestamp ?? '',
    age: '',
  };
}

// ── Generic overview (no per-kind renderers — that's a later gate) ──

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  // Match the app's standard surface (rounded-lg border bg-card) so detail
  // views read as carded panels like the rest of the dashboard, not bare lists.
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      <h2 className="border-b border-border px-4 py-2.5 text-sm font-semibold text-foreground">{title}</h2>
      <div className="px-4 py-3">{children}</div>
    </div>
  );
}

function KeyValueTable({ entries, mask }: { entries: Array<[string, string]>; mask?: boolean }) {
  if (entries.length === 0) {
    return <p className="text-xs text-muted-foreground">None</p>;
  }
  // Definition grid (dl/dt/dd, not a bare HTML table): even rows with subtle
  // dividers so key/value pairs read cleanly inside the section cards.
  return (
    <dl className="divide-y divide-border/60 text-xs">
      {entries.map(([k, v]) => (
        <div key={k} className="grid grid-cols-[minmax(0,12rem)_1fr] gap-4 py-1.5 first:pt-0 last:pb-0">
          <dt className="font-mono text-muted-foreground break-all">{k}</dt>
          <dd className="font-mono text-foreground break-all">{mask ? '••••••••' : v}</dd>
        </div>
      ))}
    </dl>
  );
}

// Exported for unit testing the kind-specific branches without mounting the
// full detail shell (which pulls in PodTerminal/wterm). Pure presentational.
export function ResourceOverview({ obj, resourceType }: { obj?: K8sObject; resourceType: string }) {
  const meta = obj?.metadata;
  if (!meta) {
    return <p className="text-sm text-muted-foreground">No data.</p>;
  }

  // ponytail: small per-kind branches keyed by resourceType for the few
  // highest-value kinds; everything else falls through to the generic view.
  // Each branch renders its tailored section ABOVE the shared GenericOverview.
  const kindSpecific = (() => {
    switch (resourceType) {
      case 'pods': return <PodOverview obj={obj!} />;
      case 'services': return <ServiceOverview obj={obj!} />;
      case 'configmaps': return <ConfigMapOverview obj={obj!} />;
      case 'ingresses': return <IngressOverview obj={obj!} />;
      case 'persistentvolumeclaims': return <PVCOverview obj={obj!} />;
      case 'deployments':
      case 'statefulsets':
      case 'daemonsets': return <WorkloadOverview obj={obj!} />;
      case 'replicasets': return <ReplicaSetOverview obj={obj!} />;
      case 'jobs': return <JobOverview obj={obj!} />;
      case 'cronjobs': return <CronJobOverview obj={obj!} />;
      case 'hpa': return <HPAOverview obj={obj!} />;
      case 'secrets': return <SecretOverview obj={obj!} />;
      case 'persistentvolumes': return <PVOverview obj={obj!} />;
      case 'networkpolicies': return <NetworkPolicyOverview obj={obj!} />;
      case 'storageclasses': return <StorageClassOverview obj={obj!} />;
      case 'k8s-clusterroles':
      case 'k8s-roles': return <RoleOverview obj={obj!} />;
      case 'k8s-clusterrolebindings':
      case 'k8s-rolebindings': return <RoleBindingOverview obj={obj!} />;
      case 'serviceaccounts': return <ServiceAccountOverview obj={obj!} />;
      case 'poddisruptionbudgets': return <PDBOverview obj={obj!} />;
      case 'resourcequotas': return <ResourceQuotaOverview obj={obj!} />;
      case 'crds': return <CRDOverview obj={obj!} />;
      case 'gateways': return <GatewayOverview obj={obj!} />;
      case 'gatewayclasses': return <GatewayClassOverview obj={obj!} />;
      case 'httproutes':
      case 'grpcroutes':
      case 'tlsroutes':
      case 'tcproutes':
      case 'udproutes': return <RouteOverview obj={obj!} />;
      default: return null;
    }
  })();

  if (kindSpecific) {
    return (
      <div className="space-y-6">
        {kindSpecific}
        {/* Tailored overview already summarises status; skip the generic dump. */}
        <GenericOverview obj={obj} resourceType={resourceType} showStatus={false} />
      </div>
    );
  }

  return <GenericOverview obj={obj} resourceType={resourceType} showStatus />;
}

function GenericOverview({ obj, resourceType, showStatus }: { obj?: K8sObject; resourceType: string; showStatus?: boolean }) {
  const meta = obj?.metadata;
  if (!meta) {
    return <p className="text-sm text-muted-foreground">No data.</p>;
  }

  // Top-level spec scalars — surfaces .spec for arbitrary CRs / unmapped kinds
  // that have no tailored overview. Skip nested objects/arrays and the fields
  // that kind overviews / actions already handle (replicas/paused/suspend).
  const SPEC_SKIP = new Set(['replicas', 'paused', 'suspend']);
  const specEntries = showStatus
    ? (Object.entries(asRecord(obj?.spec))
        .filter(([k, v]) => !SPEC_SKIP.has(k) && (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean'))
        .map(([k, v]) => [k, String(v)] as [string, string]))
    : [];

  // Top-level status scalars — surfaces .status for arbitrary CRs / unmapped
  // kinds that have no tailored overview. Conditions live in their own tab.
  const statusEntries = showStatus
    ? (Object.entries(asRecord(obj?.status))
        .filter(([k, v]) => k !== 'conditions' && (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean'))
        .map(([k, v]) => [k, String(v)] as [string, string]))
    : [];

  const metadataEntries: Array<[string, string]> = [];
  if (meta.name) metadataEntries.push(['name', meta.name]);
  if (meta.namespace) metadataEntries.push(['namespace', meta.namespace]);
  if (meta.uid) metadataEntries.push(['uid', meta.uid]);
  if (meta.creationTimestamp) {
    metadataEntries.push(['created', meta.creationTimestamp]);
    metadataEntries.push(['age', formatRelativeTime(meta.creationTimestamp)]);
  }

  const labels = Object.entries(meta.labels ?? {}) as Array<[string, string]>;
  const annotations = Object.entries(meta.annotations ?? {}) as Array<[string, string]>;
  const owners = meta.ownerReferences ?? [];

  // ponytail: mask secret 'data' values; only secrets carries this.
  const isSecret = resourceType === 'secrets';
  const dataEntries = obj?.data ? (Object.entries(obj.data) as Array<[string, string]>) : [];

  return (
    <div className="space-y-6">
      <Section title="Metadata">
        <KeyValueTable entries={metadataEntries} />
      </Section>

      <Section title="Labels">
        <KeyValueTable entries={labels} />
      </Section>

      <Section title="Annotations">
        <KeyValueTable entries={annotations} />
      </Section>

      {owners.length > 0 && (
        <Section title="Owner References">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Kind</TableHead>
                <TableHead>Name</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {owners.map((ref) => (
                <TableRow key={ref.uid || `${ref.kind}/${ref.name}`}>
                  <TableCell className="text-xs">{ref.kind}</TableCell>
                  <TableCell className="font-mono text-xs">{ref.name}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Section>
      )}

      {/* Conditions moved to a dedicated tab (ConditionsTab) — Rancher-style. */}

      {specEntries.length > 0 && (
        <Section title="Spec"><KeyValueTable entries={specEntries} /></Section>
      )}

      {statusEntries.length > 0 && (
        <Section title="Status"><KeyValueTable entries={statusEntries} /></Section>
      )}

      {dataEntries.length > 0 && (
        <Section title="Data">
          <KeyValueTable entries={dataEntries} mask={isSecret} />
        </Section>
      )}
    </div>
  );
}

// ── Kind-specific overviews (plan A4 / C1) ──
//
// ponytail: only the few highest-value kinds get tailored sections; everything
// else uses GenericOverview. No per-kind framework — just small components.

function PodOverview({ obj }: { obj: K8sObject }) {
  const spec = obj.spec ?? {};
  const status = obj.status ?? {};
  const statuses = status.containerStatuses ?? [];
  const totalRestarts = statuses.reduce((sum, c) => sum + (c.restartCount ?? 0), 0);

  // Merge spec containers (image) with status containers (ready/restarts/state).
  const byName = new Map<string, ContainerStatus>();
  for (const c of statuses) if (c.name) byName.set(c.name, c);
  const rows = (spec.containers ?? []).map((c) => {
    const st = byName.get(c.name ?? '');
    return {
      name: c.name ?? '-',
      image: c.image ?? st?.image ?? '-',
      ready: st?.ready ?? false,
      restarts: st?.restartCount ?? 0,
      state: st?.state ? (Object.keys(st.state)[0] ?? 'unknown') : 'unknown',
    };
  });

  const summary: Array<[string, string]> = [];
  if (status.phase) summary.push(['phase', status.phase]);
  if (spec.nodeName) summary.push(['node', spec.nodeName]);
  if (status.podIP) summary.push(['podIP', status.podIP]);
  summary.push(['restarts', String(totalRestarts)]);

  return (
    <>
      <Section title="Pod">
        <KeyValueTable entries={summary} />
      </Section>
      <Section title="Containers">
        {rows.length === 0 ? (
          <p className="text-xs text-muted-foreground">None</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Image</TableHead>
                <TableHead className="text-center">Ready</TableHead>
                <TableHead className="text-center">Restarts</TableHead>
                <TableHead>State</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((c) => (
                <TableRow key={c.name}>
                  <TableCell className="font-mono text-xs">{c.name}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground break-all">{c.image}</TableCell>
                  <TableCell className="text-xs text-center">{c.ready ? 'Yes' : 'No'}</TableCell>
                  <TableCell className="text-xs tabular-nums text-center">{c.restarts}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{c.state}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>
    </>
  );
}

function ServiceOverview({ obj }: { obj: K8sObject }) {
  const spec = obj.spec ?? {};
  const summary: Array<[string, string]> = [];
  if (spec.type) summary.push(['type', spec.type]);
  if (spec.clusterIP) summary.push(['clusterIP', spec.clusterIP]);
  const selector = Object.entries(spec.selector ?? {}) as Array<[string, string]>;
  const ports = spec.ports ?? [];

  return (
    <>
      <Section title="Service">
        <KeyValueTable entries={summary} />
      </Section>
      {ports.length > 0 && (
        <Section title="Ports">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Port</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Protocol</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ports.map((p, i) => (
                <TableRow key={p.name || i}>
                  <TableCell className="text-xs">{p.name || '-'}</TableCell>
                  <TableCell className="text-xs tabular-nums">{p.port ?? '-'}</TableCell>
                  <TableCell className="text-xs tabular-nums">{String(p.targetPort ?? '-')}</TableCell>
                  <TableCell className="text-xs">{p.protocol || '-'}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Section>
      )}
      <Section title="Selector">
        <KeyValueTable entries={selector} />
      </Section>
    </>
  );
}

function ConfigMapOverview({ obj }: { obj: K8sObject }) {
  const keys = Object.keys(obj.data ?? {});
  return (
    <Section title="Keys">
      {keys.length === 0 ? (
        <p className="text-xs text-muted-foreground">No keys.</p>
      ) : (
        <ul className="space-y-1">
          {keys.map((k) => (
            <li key={k} className="font-mono text-xs text-foreground">{k}</li>
          ))}
        </ul>
      )}
    </Section>
  );
}

function IngressOverview({ obj }: { obj: K8sObject }) {
  const spec = obj.spec ?? {};
  const summary: Array<[string, string]> = [];
  if (spec.ingressClassName) summary.push(['class', spec.ingressClassName]);
  const hosts = (spec.rules ?? []).map((r) => r.host).filter(Boolean) as string[];
  const tlsHosts = (spec.tls ?? []).flatMap((t) => t.hosts ?? []);

  return (
    <>
      <Section title="Ingress">
        <KeyValueTable entries={summary} />
      </Section>
      <Section title="Hosts">
        {hosts.length === 0 ? (
          <p className="text-xs text-muted-foreground">None</p>
        ) : (
          <ul className="space-y-1">
            {hosts.map((h) => <li key={h} className="font-mono text-xs text-foreground">{h}</li>)}
          </ul>
        )}
      </Section>
      {tlsHosts.length > 0 && (
        <Section title="TLS">
          <ul className="space-y-1">
            {tlsHosts.map((h) => <li key={h} className="font-mono text-xs text-foreground">{h}</li>)}
          </ul>
        </Section>
      )}
    </>
  );
}

function PVCOverview({ obj }: { obj: K8sObject }) {
  const spec = obj.spec ?? {};
  const status = obj.status ?? {};
  const summary: Array<[string, string]> = [];
  if (status.phase) summary.push(['status', status.phase]);
  const capacity = status.capacity?.storage ?? spec.resources?.requests?.storage;
  if (capacity) summary.push(['capacity', capacity]);
  if (spec.storageClassName) summary.push(['storageClass', spec.storageClassName]);
  if (spec.volumeName) summary.push(['volume', spec.volumeName]);

  return (
    <Section title="PersistentVolumeClaim">
      <KeyValueTable entries={summary} />
    </Section>
  );
}

// ── Kind-specific overviews for workload/batch/autoscaling kinds (Rancher
// parity). These previously fell to the bare GenericOverview. They read the
// varied spec/status shapes through a loose record view rather than widening
// the shared K8sObject type with a dozen one-off fields. ──

function asRecord(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' ? (v as Record<string, unknown>) : {};
}
function num(v: unknown, fallback = '-'): string {
  return v == null ? fallback : String(v);
}
function rel(v: unknown): string | null {
  return typeof v === 'string' && v ? formatRelativeTime(v) : null;
}

function ConditionsTab({ conditions }: { conditions: Array<Record<string, unknown>> }) {
  if (conditions.length === 0) {
    return <p className="py-12 text-center text-sm text-muted-foreground">No conditions reported.</p>;
  }
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Type</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Reason</TableHead>
            <TableHead>Message</TableHead>
            <TableHead>Last Transition</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {conditions.map((c, i) => {
            const status = String(c.status ?? '-');
            return (
              <TableRow key={String(c.type ?? i)}>
                <TableCell className="text-xs font-medium">{String(c.type ?? '-')}</TableCell>
                <TableCell className={cn('text-xs font-medium',
                  status === 'True' ? 'text-status-success' : status === 'False' ? 'text-status-warning' : 'text-muted-foreground')}>
                  {status}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{String(c.reason ?? '-')}</TableCell>
                <TableCell className="text-xs text-muted-foreground break-words">{String(c.message ?? '-')}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{rel(c.lastTransitionTime) ?? '-'}</TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}

function WorkloadOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  // Defensive across Deployment/StatefulSet (spec.replicas + status.*Replicas)
  // and DaemonSet (status.desiredNumberScheduled / numberReady).
  const ready = status.readyReplicas ?? status.numberReady;
  const desired = spec.replicas ?? status.desiredNumberScheduled ?? status.replicas;
  const summary: Array<[string, string]> = [
    ['ready', `${num(ready, '0')}/${num(desired, '0')}`],
  ];
  const updated = status.updatedReplicas ?? status.updatedNumberScheduled;
  const available = status.availableReplicas ?? status.numberAvailable;
  if (updated != null) summary.push(['updated', num(updated)]);
  if (available != null) summary.push(['available', num(available)]);
  const strategy = asRecord(spec.strategy).type ?? asRecord(spec.updateStrategy).type;
  if (strategy) summary.push(['strategy', String(strategy)]);

  const selector = Object.entries(asRecord(asRecord(spec.selector).matchLabels)) as Array<[string, string]>;
  const templateSpec = asRecord(asRecord(spec.template).spec);
  const containers = Array.isArray(templateSpec.containers) ? (templateSpec.containers as Array<Record<string, unknown>>) : [];
  const images = containers.map((c) => String(c.image ?? '')).filter(Boolean);

  return (
    <>
      <Section title="Workload"><KeyValueTable entries={summary} /></Section>
      {images.length > 0 && (
        <Section title="Images">
          <ul className="space-y-1">
            {images.map((img) => <li key={img} className="font-mono text-xs text-foreground break-all">{img}</li>)}
          </ul>
        </Section>
      )}
      <Section title="Selector"><KeyValueTable entries={selector} /></Section>
    </>
  );
}

function ReplicaSetOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const selector = Object.entries(asRecord(asRecord(spec.selector).matchLabels)) as Array<[string, string]>;
  const ready = num(status.readyReplicas, '0');
  const desired = num(spec.replicas ?? status.replicas, '0');
  const summary: Array<[string, string]> = [
    ['ready', `${ready}/${desired}`],
    ['available', num(status.availableReplicas, '0')],
  ];
  return (
    <>
      <Section title="ReplicaSet"><KeyValueTable entries={summary} /></Section>
      <Section title="Selector"><KeyValueTable entries={selector} /></Section>
    </>
  );
}

function JobOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const summary: Array<[string, string]> = [
    ['completions', `${num(status.succeeded, '0')}/${num(spec.completions, '1')}`],
  ];
  if (spec.parallelism != null) summary.push(['parallelism', num(spec.parallelism)]);
  if (spec.backoffLimit != null) summary.push(['backoffLimit', num(spec.backoffLimit)]);
  if (status.active != null) summary.push(['active', num(status.active)]);
  if (status.failed != null) summary.push(['failed', num(status.failed)]);
  if (spec.suspend != null) summary.push(['suspended', spec.suspend ? 'Yes' : 'No']);
  const started = rel(status.startTime);
  const completed = rel(status.completionTime);
  if (started) summary.push(['started', started]);
  if (completed) summary.push(['completed', completed]);
  return <Section title="Job"><KeyValueTable entries={summary} /></Section>;
}

function CronJobOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const summary: Array<[string, string]> = [];
  if (spec.schedule) summary.push(['schedule', String(spec.schedule)]);
  if (spec.timeZone) summary.push(['timeZone', String(spec.timeZone)]);
  summary.push(['suspended', spec.suspend ? 'Yes' : 'No']);
  if (spec.concurrencyPolicy) summary.push(['concurrency', String(spec.concurrencyPolicy)]);
  const active = Array.isArray(status.active) ? status.active.length : 0;
  summary.push(['active jobs', String(active)]);
  const lastSchedule = rel(status.lastScheduleTime);
  const lastSuccess = rel(status.lastSuccessfulTime);
  if (lastSchedule) summary.push(['last scheduled', lastSchedule]);
  if (lastSuccess) summary.push(['last successful', lastSuccess]);
  return <Section title="CronJob"><KeyValueTable entries={summary} /></Section>;
}

function HPAOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const target = asRecord(spec.scaleTargetRef);
  const summary: Array<[string, string]> = [];
  if (target.kind || target.name) summary.push(['target', `${num(target.kind, '')} ${num(target.name, '')}`.trim()]);
  summary.push(['min / max', `${num(spec.minReplicas, '1')} / ${num(spec.maxReplicas)}`]);
  summary.push(['replicas', `${num(status.currentReplicas, '0')} → ${num(status.desiredReplicas, '0')}`]);
  const lastScale = rel(status.lastScaleTime);
  if (lastScale) summary.push(['last scaled', lastScale]);

  // Resource metrics: pair each spec target with its live current value from
  // status.currentMetrics so the row reads "current X% / target Y%".
  const current = new Map<string, string>();
  (Array.isArray(status.currentMetrics) ? status.currentMetrics : []).forEach((m) => {
    const r = asRecord(asRecord(m).resource);
    const c = asRecord(r.current);
    const name = num(r.name, '');
    const val = c.averageUtilization != null ? `${c.averageUtilization}%` : num(c.averageValue ?? c.value, '');
    if (name && val) current.set(name, val);
  });
  const metrics = (Array.isArray(spec.metrics) ? spec.metrics : []).map((m) => {
    const r = asRecord(asRecord(m).resource);
    const t = asRecord(r.target);
    const name = num(r.name, '');
    const target = t.averageUtilization != null ? `${t.averageUtilization}%` : num(t.averageValue ?? t.value, '');
    if (!name || !target) return null;
    const cur = current.get(name);
    return [name, cur ? `${cur} / target ${target}` : `target ${target}`] as [string, string];
  }).filter(Boolean) as Array<[string, string]>;

  return (
    <>
      <Section title="HorizontalPodAutoscaler"><KeyValueTable entries={summary} /></Section>
      {metrics.length > 0 && (
        <Section title="Metrics"><KeyValueTable entries={metrics} /></Section>
      )}
    </>
  );
}

function SecretOverview({ obj }: { obj: K8sObject }) {
  const type = asRecord(obj).type;
  const keys = Object.keys(obj.data ?? {});
  const summary: Array<[string, string]> = [];
  if (type) summary.push(['type', String(type)]);
  summary.push(['keys', String(keys.length)]);
  return (
    <>
      <Section title="Secret"><KeyValueTable entries={summary} /></Section>
      {keys.length > 0 && (
        <Section title="Keys">
          <ul className="space-y-1">
            {keys.map((k) => <li key={k} className="font-mono text-xs text-foreground">{k}</li>)}
          </ul>
        </Section>
      )}
    </>
  );
}

function PVOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const claim = asRecord(spec.claimRef);
  const summary: Array<[string, string]> = [];
  if (status.phase) summary.push(['status', String(status.phase)]);
  const cap = asRecord(spec.capacity).storage;
  if (cap) summary.push(['capacity', String(cap)]);
  if (Array.isArray(spec.accessModes)) summary.push(['accessModes', (spec.accessModes as string[]).join(', ')]);
  if (spec.persistentVolumeReclaimPolicy) summary.push(['reclaimPolicy', String(spec.persistentVolumeReclaimPolicy)]);
  if (spec.storageClassName) summary.push(['storageClass', String(spec.storageClassName)]);
  if (claim.name) summary.push(['claim', `${claim.namespace ? `${claim.namespace}/` : ''}${claim.name}`]);
  return <Section title="PersistentVolume"><KeyValueTable entries={summary} /></Section>;
}

function NetworkPolicyOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const summary: Array<[string, string]> = [];
  if (Array.isArray(spec.policyTypes)) summary.push(['policyTypes', (spec.policyTypes as string[]).join(', ')]);
  summary.push(['ingress rules', String(Array.isArray(spec.ingress) ? spec.ingress.length : 0)]);
  summary.push(['egress rules', String(Array.isArray(spec.egress) ? spec.egress.length : 0)]);
  const podSelector = Object.entries(asRecord(asRecord(spec.podSelector).matchLabels)) as Array<[string, string]>;
  return (
    <>
      <Section title="NetworkPolicy"><KeyValueTable entries={summary} /></Section>
      <Section title="Pod Selector"><KeyValueTable entries={podSelector} /></Section>
    </>
  );
}

function StorageClassOverview({ obj }: { obj: K8sObject }) {
  const o = asRecord(obj); // StorageClass fields are top-level, not under spec.
  const summary: Array<[string, string]> = [];
  if (o.provisioner) summary.push(['provisioner', String(o.provisioner)]);
  if (o.reclaimPolicy) summary.push(['reclaimPolicy', String(o.reclaimPolicy)]);
  if (o.volumeBindingMode) summary.push(['volumeBindingMode', String(o.volumeBindingMode)]);
  if (o.allowVolumeExpansion != null) summary.push(['allowVolumeExpansion', o.allowVolumeExpansion ? 'Yes' : 'No']);
  const params = Object.entries(asRecord(o.parameters)) as Array<[string, string]>;
  return (
    <>
      <Section title="StorageClass"><KeyValueTable entries={summary} /></Section>
      {params.length > 0 && <Section title="Parameters"><KeyValueTable entries={params} /></Section>}
    </>
  );
}

function RoleOverview({ obj }: { obj: K8sObject }) {
  const rules = (Array.isArray(asRecord(obj).rules) ? (asRecord(obj).rules as Array<Record<string, unknown>>) : []);
  const join = (v: unknown, blankAs?: string) =>
    (Array.isArray(v) ? v : []).map((x) => (x === '' && blankAs ? blankAs : String(x))).join(', ') || '-';
  return (
    <Section title="Rules">
      {rules.length === 0 ? (
        <p className="text-xs text-muted-foreground">No rules.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow><TableHead>API Groups</TableHead><TableHead>Resources</TableHead><TableHead>Verbs</TableHead></TableRow>
          </TableHeader>
          <TableBody>
            {rules.map((r, i) => (
              <TableRow key={i}>
                <TableCell className="font-mono text-xs">{join(r.apiGroups, 'core')}</TableCell>
                <TableCell className="font-mono text-xs">{join(r.resources)}</TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">{join(r.verbs)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Section>
  );
}

function RoleBindingOverview({ obj }: { obj: K8sObject }) {
  const o = asRecord(obj);
  const roleRef = asRecord(o.roleRef);
  const subjects = (Array.isArray(o.subjects) ? (o.subjects as Array<Record<string, unknown>>) : []);
  return (
    <>
      <Section title="Role Reference">
        <KeyValueTable entries={[['kind', String(roleRef.kind ?? '-')], ['name', String(roleRef.name ?? '-')]]} />
      </Section>
      <Section title="Subjects">
        {subjects.length === 0 ? (
          <p className="text-xs text-muted-foreground">None</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow><TableHead>Kind</TableHead><TableHead>Name</TableHead><TableHead>Namespace</TableHead></TableRow>
            </TableHeader>
            <TableBody>
              {subjects.map((s, i) => (
                <TableRow key={`${String(s.name ?? '')}-${i}`}>
                  <TableCell className="text-xs">{String(s.kind ?? '-')}</TableCell>
                  <TableCell className="font-mono text-xs">{String(s.name ?? '-')}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{String(s.namespace ?? '-')}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>
    </>
  );
}

function ServiceAccountOverview({ obj }: { obj: K8sObject }) {
  const o = asRecord(obj);
  const secrets = (Array.isArray(o.secrets) ? (o.secrets as Array<Record<string, unknown>>) : []);
  const summary: Array<[string, string]> = [
    ['automountToken', o.automountServiceAccountToken === false ? 'No' : 'Yes'],
    ['secrets', String(secrets.length)],
  ];
  return (
    <>
      <Section title="ServiceAccount"><KeyValueTable entries={summary} /></Section>
      {secrets.length > 0 && (
        <Section title="Secrets">
          <ul className="space-y-1">
            {secrets.map((s, i) => <li key={`${String(s.name ?? '')}-${i}`} className="font-mono text-xs text-foreground">{String(s.name ?? '-')}</li>)}
          </ul>
        </Section>
      )}
    </>
  );
}

function PDBOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const summary: Array<[string, string]> = [];
  if (spec.minAvailable != null) summary.push(['minAvailable', String(spec.minAvailable)]);
  if (spec.maxUnavailable != null) summary.push(['maxUnavailable', String(spec.maxUnavailable)]);
  summary.push(['healthy', `${num(status.currentHealthy, '0')}/${num(status.desiredHealthy, '0')}`]);
  if (status.disruptionsAllowed != null) summary.push(['disruptionsAllowed', String(status.disruptionsAllowed)]);
  return <Section title="PodDisruptionBudget"><KeyValueTable entries={summary} /></Section>;
}

function ResourceQuotaOverview({ obj }: { obj: K8sObject }) {
  const status = asRecord(obj.status);
  const hard = asRecord(Object.keys(asRecord(status.hard)).length ? status.hard : asRecord(obj.spec).hard);
  const used = asRecord(status.used);
  const rows = Object.keys(hard).map((k) => [k, `${num(used[k], '0')} / ${num(hard[k])}`] as [string, string]);
  return <Section title="Quota (used / hard)"><KeyValueTable entries={rows} /></Section>;
}

function CRDOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const names = asRecord(spec.names);
  const versions = (Array.isArray(spec.versions) ? (spec.versions as Array<Record<string, unknown>>) : []);
  const summary: Array<[string, string]> = [];
  if (spec.group) summary.push(['group', String(spec.group)]);
  if (spec.scope) summary.push(['scope', String(spec.scope)]);
  if (names.kind) summary.push(['kind', String(names.kind)]);
  if (names.plural) summary.push(['plural', String(names.plural)]);
  const vers = versions.map((v) => String(v.name ?? '')).filter(Boolean).join(', ');
  if (vers) summary.push(['versions', vers]);
  return <Section title="CustomResourceDefinition"><KeyValueTable entries={summary} /></Section>;
}

function GatewayOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const summary: Array<[string, string]> = [];
  if (spec.gatewayClassName) summary.push(['gatewayClass', String(spec.gatewayClassName)]);
  const addrs = (Array.isArray(status.addresses) ? status.addresses : [])
    .map((a) => String(asRecord(a).value ?? '')).filter(Boolean);
  if (addrs.length) summary.push(['addresses', addrs.join(', ')]);
  const listeners = (Array.isArray(spec.listeners) ? (spec.listeners as Array<Record<string, unknown>>) : []);
  return (
    <>
      <Section title="Gateway"><KeyValueTable entries={summary} /></Section>
      {listeners.length > 0 && (
        <Section title="Listeners">
          <Table>
            <TableHeader>
              <TableRow><TableHead>Name</TableHead><TableHead>Port</TableHead><TableHead>Protocol</TableHead><TableHead>Hostname</TableHead></TableRow>
            </TableHeader>
            <TableBody>
              {listeners.map((l, i) => (
                <TableRow key={String(l.name ?? i)}>
                  <TableCell className="text-xs font-medium">{String(l.name ?? '-')}</TableCell>
                  <TableCell className="text-xs tabular-nums">{String(l.port ?? '-')}</TableCell>
                  <TableCell className="text-xs">{String(l.protocol ?? '-')}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{String(l.hostname ?? '*')}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Section>
      )}
    </>
  );
}

function RouteOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const parents = (Array.isArray(spec.parentRefs) ? (spec.parentRefs as Array<Record<string, unknown>>) : [])
    .map((p) => {
      const r = asRecord(p);
      return `${r.namespace ? `${r.namespace}/` : ''}${String(r.name ?? '')}`.trim();
    })
    .filter(Boolean);
  const hostnames = (Array.isArray(spec.hostnames) ? (spec.hostnames as string[]) : []);
  const summary: Array<[string, string]> = [];
  if (parents.length) summary.push(['parents', parents.join(', ')]);
  summary.push(['rules', String(Array.isArray(spec.rules) ? spec.rules.length : 0)]);
  return (
    <>
      <Section title="Route"><KeyValueTable entries={summary} /></Section>
      {hostnames.length > 0 && (
        <Section title="Hostnames">
          <ul className="space-y-1">
            {hostnames.map((h) => <li key={h} className="font-mono text-xs text-foreground">{h}</li>)}
          </ul>
        </Section>
      )}
    </>
  );
}

function GatewayClassOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const summary: Array<[string, string]> = [];
  if (spec.controllerName) summary.push(['controller', String(spec.controllerName)]);
  if (spec.description) summary.push(['description', String(spec.description)]);
  return <Section title="GatewayClass"><KeyValueTable entries={summary} /></Section>;
}

// ── Events tab (plan D1) ──
//
// ponytail: fetch this object's events straight from the k8s proxy with a
// fieldSelector — no backend change. Namespaced objects scope to their ns;
// cluster-scoped objects query the cluster-wide /events feed.

interface K8sEvent {
  metadata?: { uid?: string };
  type?: string;
  reason?: string;
  message?: string;
  count?: number;
  lastTimestamp?: string;
  eventTime?: string;
  firstTimestamp?: string;
}

function eventsPath(namespace: string | undefined, name: string, kind: string): string {
  const selector = `involvedObject.name=${name},involvedObject.kind=${kind}`;
  const base = namespace
    ? `api/v1/namespaces/${namespace}/events`
    : 'api/v1/events';
  return `${base}?fieldSelector=${encodeURIComponent(selector)}`;
}

function ResourceEvents({ clusterId, namespace, name, kind }: {
  clusterId: string; namespace?: string; name: string; kind: string;
}) {
  const path = useMemo(() => eventsPath(namespace, name, kind), [namespace, name, kind]);
  const { data, isLoading, error } = useK8sResource(clusterId, path);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-24">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (error) {
    return (
      <div className="py-24 text-center text-sm text-status-error">
        Failed to load events: {(error as Error).message}
      </div>
    );
  }

  const items = ((data as { items?: K8sEvent[] } | undefined)?.items ?? []);
  if (items.length === 0) {
    return <p className="py-12 text-center text-sm text-muted-foreground">No events for this resource.</p>;
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Type</TableHead>
          <TableHead>Reason</TableHead>
          <TableHead>Message</TableHead>
          <TableHead className="text-center">Count</TableHead>
          <TableHead>Last Seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((e, i) => {
          const last = e.lastTimestamp || e.eventTime || e.firstTimestamp;
          return (
            <TableRow key={e.metadata?.uid || i}>
              <TableCell className={cn('text-xs font-medium', e.type === 'Warning' ? 'text-status-warning' : 'text-status-info')}>
                {e.type || '-'}
              </TableCell>
              <TableCell className="text-xs font-medium text-foreground">{e.reason || '-'}</TableCell>
              <TableCell className="text-xs text-muted-foreground">{e.message || '-'}</TableCell>
              <TableCell className="text-xs tabular-nums text-center">{e.count ?? 1}</TableCell>
              <TableCell className="text-xs text-muted-foreground">{last ? formatRelativeTime(last) : '-'}</TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}

// ── Related tab (plan D2) ──
//
// ponytail: deliberately NOT a general relationship engine. Just two relations:
//   - ownerReferences (up), as drill-down links;
//   - pods (down) for the workload kinds that already have useWorkloadPods.
// Everything else is out of scope (that's a later gate).


/** Workload kinds whose pods we can list via the existing hook. */
const WORKLOAD_POD_KINDS: Record<string, string> = {
  Deployment: 'deployment',
  StatefulSet: 'statefulset',
  DaemonSet: 'daemonset',
};

function RelatedResources({ clusterId, namespace, name, kind, obj }: {
  clusterId: string; namespace?: string; name: string; kind: string; obj?: K8sObject;
}) {
  const owners = obj?.metadata?.ownerReferences ?? [];

  // ponytail: always call the hook (rules-of-hooks); it self-disables when the
  // kind isn't a workload or args are missing.
  const workloadKind = WORKLOAD_POD_KINDS[kind] ?? '';
  const { data: pods } = useWorkloadPods(clusterId, workloadKind, namespace ?? '', name);
  const showPods = !!workloadKind && !!namespace;

  return (
    <div className="space-y-6">
      <Section title="Owned By">
        {owners.length === 0 ? (
          <p className="text-xs text-muted-foreground">No owner references.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Kind</TableHead>
                <TableHead>Name</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {owners.map((ref) => {
                const ownerType = KIND_TO_RESOURCE_TYPE[ref.kind];
                // ponytail: owners share the object's namespace (controller refs always do).
                return (
                  <TableRow key={ref.uid || `${ref.kind}/${ref.name}`}>
                    <TableCell className="text-xs">{ref.kind}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {ownerType ? (
                        <Link
                          href={detailHref(clusterId, ownerType, namespace, ref.name)}
                          className="text-foreground hover:underline"
                        >
                          {ref.name}
                        </Link>
                      ) : (
                        ref.name
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </Section>

      {showPods && (
        <Section title="Pods">
          {!pods || pods.length === 0 ? (
            <p className="text-xs text-muted-foreground">No pods.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-center">Restarts</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {pods.map((p) => (
                  <TableRow key={`${p.namespace}/${p.name}`}>
                    <TableCell className="font-mono text-xs">
                      <Link
                        href={detailHref(clusterId, 'pods', p.namespace, p.name)}
                        className="text-foreground hover:underline"
                      >
                        {p.name}
                      </Link>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">{p.status}</TableCell>
                    <TableCell className="text-xs tabular-nums text-center">{p.restarts}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Section>
      )}
    </div>
  );
}
