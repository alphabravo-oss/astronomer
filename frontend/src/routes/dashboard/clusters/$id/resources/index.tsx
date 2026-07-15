import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Cluster Resources tab — sprint 069 CRD-mirror v2 read-only view.
 *
 * Renders five expandable cards over the mirrored_* tables:
 *   - Ingress classes (with the "default" badge)
 *   - Gateway classes (with the Accepted condition)
 *   - Network policies (full mirror, managed-by-astronomer badge where applicable)
 *   - Resource quotas (used/hard progress bars)
 *   - Limit ranges (per-type defaults / requests)
 *
 * Data comes from the management plane's mirrored_* tables — no
 * round-trip through kubectl on every render. The per-cluster CRD
 * agent keeps the rows fresh; the server's periodic prune (every 30m)
 * drops rows whose agent stopped reporting, so the UI never has to
 * second-guess whether a row is still in the cluster.
 */

import { useState } from 'react';
import { useParams } from '@/lib/navigation';
import { useQuery } from '@tanstack/react-query';
import {
  ChevronDown,
  ChevronRight,
  CircleCheck,
  CircleHelp,
  CircleX,
  Layers,
  Network,
  Shield,
  Slash,
  SquareStack,
} from 'lucide-react';

import {
  listMirroredGatewayClasses,
  listMirroredIngressClasses,
  listMirroredLimitRanges,
  listMirroredNetworkPolicies,
  listMirroredResourceQuotas,
  type MirroredGatewayClass,
  type MirroredIngressClass,
  type MirroredLimitRange,
  type MirroredNetworkPolicy,
  type MirroredResourceQuota,
} from '@/lib/api/cluster-detail';
import { queryKeys } from '@/lib/hooks';

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

function fmtRelative(iso?: string): string {
  if (!iso) return '—';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const delta = Date.now() - t;
  const mins = Math.floor(delta / 60_000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

// parseQuantity converts a Kubernetes-style quantity string to a number
// where possible (so we can compute used/hard ratios). Returns NaN for
// anything we don't recognise — the UI degrades to a numerator/
// denominator string when that happens.
function parseQuantity(v: string | number | undefined | null): number {
  if (v == null) return NaN;
  if (typeof v === 'number') return v;
  const m = /^(\d+(?:\.\d+)?)([a-zA-Z]*)$/.exec(v.trim());
  if (!m) return Number.NaN;
  const num = parseFloat(m[1]);
  const suffix = m[2];
  const mult: Record<string, number> = {
    '': 1,
    Ki: 1024,
    Mi: 1024 ** 2,
    Gi: 1024 ** 3,
    Ti: 1024 ** 4,
    K: 1e3,
    M: 1e6,
    G: 1e9,
    T: 1e12,
    m: 1e-3,
  };
  return num * (mult[suffix] ?? 1);
}

// ---------------------------------------------------------------------
// Section primitive
// ---------------------------------------------------------------------

function Section({
  title,
  icon,
  count,
  defaultOpen = true,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  count: number;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="rounded-lg border bg-card mb-4">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between p-4 text-left"
      >
        <div className="flex items-center gap-2">
          {icon}
          <h2 className="text-base font-semibold">{title}</h2>
          <span className="rounded-full bg-muted px-2 py-0.5 text-xs">{count}</span>
        </div>
        {open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
      </button>
      {open && <div className="border-t p-4">{children}</div>}
    </div>
  );
}

// ---------------------------------------------------------------------
// Per-kind tables
// ---------------------------------------------------------------------

function IngressClassesTable({ rows }: { rows: MirroredIngressClass[] }) {
  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">No IngressClasses installed.</p>;
  }
  return (
    <Table className="w-full text-sm">
      <TableHeader>
        <TableRow className="text-left text-xs uppercase text-muted-foreground">
          <TableHead className="py-2">Name</TableHead>
          <TableHead className="py-2">Controller</TableHead>
          <TableHead className="py-2">Default</TableHead>
          <TableHead className="py-2">Last seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r.name} className="border-t">
            <TableCell className="py-2 font-mono">{r.name}</TableCell>
            <TableCell className="py-2 font-mono text-xs">{r.controller || '—'}</TableCell>
            <TableCell className="py-2">
              {r.isDefault ? (
                <span className="rounded-full bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700">
                  default
                </span>
              ) : (
                <span className="text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell className="py-2 text-muted-foreground">{fmtRelative(r.lastSeenAt)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function AcceptedBadge({ status }: { status: string }) {
  if (status === 'True') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700">
        <CircleCheck className="h-3 w-3" /> Accepted
      </span>
    );
  }
  if (status === 'False') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-red-100 px-2 py-0.5 text-xs text-red-700">
        <CircleX className="h-3 w-3" /> Rejected
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-zinc-100 px-2 py-0.5 text-xs text-zinc-700">
      <CircleHelp className="h-3 w-3" /> Unknown
    </span>
  );
}

function GatewayClassesTable({ rows }: { rows: MirroredGatewayClass[] }) {
  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">No GatewayClasses installed.</p>;
  }
  return (
    <Table className="w-full text-sm">
      <TableHeader>
        <TableRow className="text-left text-xs uppercase text-muted-foreground">
          <TableHead className="py-2">Name</TableHead>
          <TableHead className="py-2">Controller</TableHead>
          <TableHead className="py-2">Accepted</TableHead>
          <TableHead className="py-2">Last seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r.name} className="border-t">
            <TableCell className="py-2 font-mono">{r.name}</TableCell>
            <TableCell className="py-2 font-mono text-xs">{r.controllerName || '—'}</TableCell>
            <TableCell className="py-2">
              <AcceptedBadge status={r.acceptedStatus} />
            </TableCell>
            <TableCell className="py-2 text-muted-foreground">{fmtRelative(r.lastSeenAt)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function NetworkPoliciesTable({ rows }: { rows: MirroredNetworkPolicy[] }) {
  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">No NetworkPolicies in this cluster.</p>;
  }
  return (
    <Table className="w-full text-sm">
      <TableHeader>
        <TableRow className="text-left text-xs uppercase text-muted-foreground">
          <TableHead className="py-2">Namespace</TableHead>
          <TableHead className="py-2">Name</TableHead>
          <TableHead className="py-2">Types</TableHead>
          <TableHead className="py-2">Owner</TableHead>
          <TableHead className="py-2">Last seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={`${r.namespace}/${r.name}`} className="border-t">
            <TableCell className="py-2 font-mono">{r.namespace}</TableCell>
            <TableCell className="py-2 font-mono">{r.name}</TableCell>
            <TableCell className="py-2">
              {(r.policyTypes ?? []).map((t) => (
                <span key={t} className="mr-1 rounded-full bg-zinc-100 px-2 py-0.5 text-xs">
                  {t}
                </span>
              ))}
            </TableCell>
            <TableCell className="py-2">
              {r.isManaged ? (
                <span className="rounded-full bg-violet-100 px-2 py-0.5 text-xs text-violet-700">
                  astronomer
                </span>
              ) : (
                <span className="rounded-full bg-zinc-100 px-2 py-0.5 text-xs text-zinc-600">
                  operator
                </span>
              )}
            </TableCell>
            <TableCell className="py-2 text-muted-foreground">{fmtRelative(r.lastSeenAt)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function QuotaProgressRow({
  label,
  hard,
  used,
}: {
  label: string;
  hard: string | undefined;
  used: string | undefined;
}) {
  const hardN = parseQuantity(hard);
  const usedN = parseQuantity(used);
  const pct =
    Number.isFinite(hardN) && Number.isFinite(usedN) && hardN > 0
      ? Math.min(100, Math.round((usedN / hardN) * 100))
      : null;
  const barColor = pct == null ? 'bg-zinc-400' : pct > 90 ? 'bg-red-500' : pct > 75 ? 'bg-amber-500' : 'bg-emerald-500';
  return (
    <div className="mb-2">
      <div className="flex items-center justify-between text-xs">
        <span className="font-mono">{label}</span>
        <span className="text-muted-foreground">
          {used ?? '0'} / {hard ?? '—'}
          {pct != null ? ` (${pct}%)` : ''}
        </span>
      </div>
      <div className="mt-1 h-1.5 w-full overflow-hidden rounded bg-muted">
        <div
          className={`h-full ${barColor}`}
          style={{ width: pct == null ? '0%' : `${pct}%` }}
        />
      </div>
    </div>
  );
}

function ResourceQuotasView({ rows }: { rows: MirroredResourceQuota[] }) {
  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">No ResourceQuotas in this cluster.</p>;
  }
  return (
    <div className="space-y-4">
      {rows.map((r) => {
        const hardEntries = Object.entries(r.hard ?? {});
        return (
          <div key={`${r.namespace}/${r.name}`} className="rounded border p-3">
            <div className="mb-2 flex items-center justify-between">
              <div>
                <span className="font-mono text-sm">{r.namespace}/{r.name}</span>
              </div>
              <span className="text-xs text-muted-foreground">{fmtRelative(r.lastSeenAt)}</span>
            </div>
            {hardEntries.length === 0 ? (
              <p className="text-xs text-muted-foreground">No hard limits set.</p>
            ) : (
              hardEntries.map(([k, v]) => (
                <QuotaProgressRow key={k} label={k} hard={v} used={r.used?.[k]} />
              ))
            )}
          </div>
        );
      })}
    </div>
  );
}

interface LimitRangeItem {
  type?: string;
  default?: Record<string, string>;
  defaultRequest?: Record<string, string>;
  max?: Record<string, string>;
  min?: Record<string, string>;
}

function LimitRangesTable({ rows }: { rows: MirroredLimitRange[] }) {
  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">No LimitRanges in this cluster.</p>;
  }
  return (
    <div className="space-y-3">
      {rows.map((r) => {
        const limits = (r.limits ?? []) as LimitRangeItem[];
        return (
          <div key={`${r.namespace}/${r.name}`} className="rounded border p-3">
            <div className="mb-2 flex items-center justify-between">
              <span className="font-mono text-sm">{r.namespace}/{r.name}</span>
              <span className="text-xs text-muted-foreground">{fmtRelative(r.lastSeenAt)}</span>
            </div>
            <Table className="w-full text-xs">
              <TableHeader>
                <TableRow className="text-left text-muted-foreground">
                  <TableHead className="py-1">Type</TableHead>
                  <TableHead className="py-1">Default</TableHead>
                  <TableHead className="py-1">DefaultRequest</TableHead>
                  <TableHead className="py-1">Min</TableHead>
                  <TableHead className="py-1">Max</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {limits.map((l, i) => (
                  <TableRow key={i} className="border-t">
                    <TableCell className="py-1 font-mono">{l.type ?? '—'}</TableCell>
                    <TableCell className="py-1 font-mono">{fmtMap(l.default)}</TableCell>
                    <TableCell className="py-1 font-mono">{fmtMap(l.defaultRequest)}</TableCell>
                    <TableCell className="py-1 font-mono">{fmtMap(l.min)}</TableCell>
                    <TableCell className="py-1 font-mono">{fmtMap(l.max)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        );
      })}
    </div>
  );
}

function fmtMap(m?: Record<string, string>): string {
  if (!m) return '—';
  const keys = Object.keys(m);
  if (keys.length === 0) return '—';
  return keys.map((k) => `${k}=${m[k]}`).join(', ');
}

// ---------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------

function ClusterResourcesPage() {
  const { id } = useParams<{ id: string }>();

  const ingressClassesQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredIngressClasses(id),
    queryFn: () => listMirroredIngressClasses(id),
  });
  const gatewayClassesQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredGatewayClasses(id),
    queryFn: () => listMirroredGatewayClasses(id),
  });
  const networkPoliciesQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredNetworkPolicies(id),
    queryFn: () => listMirroredNetworkPolicies(id),
  });
  const resourceQuotasQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredResourceQuotas(id),
    queryFn: () => listMirroredResourceQuotas(id),
  });
  const limitRangesQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredLimitRanges(id),
    queryFn: () => listMirroredLimitRanges(id),
  });

  return (
    <div className="p-6">
      <header className="mb-4">
        <h1 className="text-xl font-semibold">Cluster resources</h1>
        <p className="text-sm text-muted-foreground">
          A read-only view of the policy / routing / quota objects installed in this cluster.
          Data is mirrored from the cluster agent every ~10 minutes; rows you delete in the
          cluster disappear here within roughly an hour.
        </p>
      </header>

      <Section
        title="Ingress classes"
        icon={<Layers className="h-4 w-4" />}
        count={ingressClassesQ.data?.length ?? 0}
      >
        <IngressClassesTable rows={ingressClassesQ.data ?? []} />
      </Section>

      <Section
        title="Gateway classes"
        icon={<Network className="h-4 w-4" />}
        count={gatewayClassesQ.data?.length ?? 0}
      >
        <GatewayClassesTable rows={gatewayClassesQ.data ?? []} />
      </Section>

      <Section
        title="Network policies"
        icon={<Shield className="h-4 w-4" />}
        count={networkPoliciesQ.data?.length ?? 0}
      >
        <NetworkPoliciesTable rows={networkPoliciesQ.data ?? []} />
      </Section>

      <Section
        title="Resource quotas"
        icon={<SquareStack className="h-4 w-4" />}
        count={resourceQuotasQ.data?.length ?? 0}
      >
        <ResourceQuotasView rows={resourceQuotasQ.data ?? []} />
      </Section>

      <Section
        title="Limit ranges"
        icon={<Slash className="h-4 w-4" />}
        count={limitRangesQ.data?.length ?? 0}
      >
        <LimitRangesTable rows={limitRangesQ.data ?? []} />
      </Section>
    </div>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/resources/')({
  component: ClusterResourcesPage,
});
