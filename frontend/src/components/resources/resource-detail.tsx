'use client';

import { useMemo, useState } from 'react';
import { useRouter } from '@/lib/navigation';
import { useK8sResource } from '@/lib/hooks';
import { usePermissionDecision } from '@/lib/permission-hooks';
import { YamlPanel } from '@/components/ui/yaml-view-dialog';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { formatRelativeTime, cn } from '@/lib/utils';
import { Loader2, ArrowLeft } from 'lucide-react';

interface ResourceDetailProps {
  clusterId: string;
  resourceType: string;
  namespace?: string;
  name: string;
  /** K8s API path to the single object (e.g. "api/v1/namespaces/default/pods/my-pod"). */
  k8sPath: string;
}

// ponytail: only the two GATE-A tabs. Events/Related are GATE-B.
const TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'yaml', label: 'YAML' },
] as const;
type TabId = (typeof TABS)[number]['id'];

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
  status?: { conditions?: Array<Record<string, unknown>> };
  data?: Record<string, string>;
}

export function ResourceDetail({ clusterId, resourceType, namespace, name, k8sPath }: ResourceDetailProps) {
  const router = useRouter();
  const [tab, setTab] = useState<TabId>('overview');

  // ponytail: detail page maps to the generic "clusters" permission resource, like the YAML dialogs.
  const scope = useMemo(() => ({ type: 'cluster' as const, id: clusterId }), [clusterId]);
  const read = usePermissionDecision('clusters', 'read', scope);
  const update = usePermissionDecision('clusters', 'update', scope);

  const { data: obj, isLoading, error } = useK8sResource(clusterId, k8sPath, read.allowed);

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
      <div className="flex items-start gap-4">
        <button
          onClick={() => router.back()}
          className="mt-1 p-1 rounded-md hover:bg-accent transition-colors text-muted-foreground hover:text-foreground"
          aria-label="Back"
        >
          <ArrowLeft className="h-5 w-5" />
        </button>
        <div className="flex-1 min-w-0">
          <h1 className="text-xl font-semibold text-foreground tracking-tight font-mono truncate">{name}</h1>
          <div className="mt-1 flex items-center gap-4 text-xs text-muted-foreground">
            <span>Kind: {kind}</span>
            {namespace && <span>Namespace: {namespace}</span>}
            {created && <span>Age: {formatRelativeTime(created)}</span>}
          </div>
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex gap-0 -mb-px">
          {TABS.map((t) => (
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
    </div>
  );
}

// ── Generic overview (no per-kind renderers — that's GATE-B) ──

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h2 className="mb-2 text-sm font-semibold text-foreground">{title}</h2>
      {children}
    </div>
  );
}

function KeyValueTable({ entries, mask }: { entries: Array<[string, string]>; mask?: boolean }) {
  if (entries.length === 0) {
    return <p className="text-xs text-muted-foreground">None</p>;
  }
  return (
    <Table>
      <TableBody>
        {entries.map(([k, v]) => (
          <TableRow key={k}>
            <TableCell className="font-mono text-xs text-muted-foreground align-top w-1/3">{k}</TableCell>
            <TableCell className="font-mono text-xs text-foreground break-all">
              {mask ? '••••••••' : v}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function ResourceOverview({ obj, resourceType }: { obj?: K8sObject; resourceType: string }) {
  const meta = obj?.metadata;
  if (!meta) {
    return <p className="text-sm text-muted-foreground">No data.</p>;
  }

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
  const conditions = obj?.status?.conditions ?? [];

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

      {conditions.length > 0 && (
        <Section title="Conditions">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Type</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead>Message</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {conditions.map((c, i) => (
                <TableRow key={String(c.type ?? i)}>
                  <TableCell className="text-xs font-medium">{String(c.type ?? '-')}</TableCell>
                  <TableCell className="text-xs">{String(c.status ?? '-')}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{String(c.reason ?? '-')}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{String(c.message ?? '-')}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Section>
      )}

      {dataEntries.length > 0 && (
        <Section title="Data">
          <KeyValueTable entries={dataEntries} mask={isSecret} />
        </Section>
      )}
    </div>
  );
}
