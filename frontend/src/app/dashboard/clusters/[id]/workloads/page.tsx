'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Unified Workloads tab — aggregates every workload kind into one
 * filterable table.
 *
 * Why one tab instead of five (deployments / statefulsets / daemonsets
 * / jobs / cronjobs as separate pages): operators rarely think in k8s
 * kind boundaries when answering operational questions like "what's
 * unhealthy in the monitoring namespace?" or "show me everything
 * scaled below desired." Rancher's Cluster Explorer collapses all
 * workload kinds into one searchable list for the same reason.
 *
 * Data path: k8s passthrough proxy. Five parallel GETs (one per kind),
 * each fetching across all namespaces; the management plane proxies
 * via the agent's tunnel. We do five GETs not one because k8s doesn't
 * expose a multi-kind list endpoint — but the proxy handles all of
 * them concurrently so the wall-time is bounded by the slowest one.
 *
 * Statuses are normalised so the table column is a single readable
 * label per row regardless of source kind: "5/5 ready" for healthy,
 * "0/3 ready" for unhealthy, "Suspended" for jobs/cronjobs, etc.
 */

import { useMemo, useState } from 'react';
import { Link } from '@/lib/link';
import { useParams, useRouter } from '@/lib/navigation';
import { useQuery } from '@tanstack/react-query';
import {
  Boxes,
  Database,
  Disc,
  Briefcase,
  Clock3,
  CheckCircle2,
  AlertCircle,
  CircleHelp,
  Search,
} from 'lucide-react';

import { k8sGet } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import { formatRelativeTime } from '@/lib/utils';

// ---------------------------------------------------------------------
// Types — narrow shapes over k8s objects, only fields we render.
// ---------------------------------------------------------------------

interface K8sMeta {
  name: string;
  namespace: string;
  creationTimestamp?: string;
  ownerReferences?: Array<{ controller?: boolean }>;
}

interface DeploymentLike {
  apiVersion: string;
  kind: string;
  metadata: K8sMeta;
  spec?: { replicas?: number };
  status?: {
    replicas?: number;
    readyReplicas?: number;
    availableReplicas?: number;
    updatedReplicas?: number;
  };
}

interface DaemonSetLike {
  apiVersion: string;
  kind: string;
  metadata: K8sMeta;
  status?: {
    desiredNumberScheduled?: number;
    numberReady?: number;
    numberAvailable?: number;
  };
}

interface JobLike {
  apiVersion: string;
  kind: string;
  metadata: K8sMeta;
  spec?: { suspend?: boolean; completions?: number; parallelism?: number };
  status?: {
    active?: number;
    succeeded?: number;
    failed?: number;
    conditions?: Array<{ type: string; status: string }>;
  };
}

interface CronJobLike {
  apiVersion: string;
  kind: string;
  metadata: K8sMeta;
  spec?: { suspend?: boolean; schedule?: string };
  status?: {
    lastScheduleTime?: string;
    active?: Array<unknown>;
  };
}

interface K8sList<T> {
  items: T[];
}

type Workload =
  | { kind: 'Deployment'; item: DeploymentLike }
  | { kind: 'StatefulSet'; item: DeploymentLike }
  | { kind: 'DaemonSet'; item: DaemonSetLike }
  | { kind: 'Job'; item: JobLike }
  | { kind: 'CronJob'; item: CronJobLike };

// ---------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------

export default function WorkloadsPage() {
  const params = useParams();
  const clusterId = params.id as string;

  const [search, setSearch] = useState('');
  const [namespace, setNamespace] = useState<string>('');
  const [kindFilter, setKindFilter] = useState<Workload['kind'] | ''>('');

  // Five parallel queries. Each kind has its own retry/cache budget;
  // a missing apiVersion (e.g. batch/v1 disabled) doesn't take the
  // others down with it. enabled: !!clusterId guards against null
  // before the page params resolve.
  const deployments = useQuery({
    queryKey: queryKeys.clusterPages.workloadKind(clusterId, 'deployments'),
    queryFn: () => k8sGet(clusterId, 'apis/apps/v1/deployments') as Promise<K8sList<DeploymentLike>>,
    enabled: !!clusterId,
    refetchInterval: 30 * 1000,
  });
  const statefulsets = useQuery({
    queryKey: queryKeys.clusterPages.workloadKind(clusterId, 'statefulsets'),
    queryFn: () => k8sGet(clusterId, 'apis/apps/v1/statefulsets') as Promise<K8sList<DeploymentLike>>,
    enabled: !!clusterId,
    refetchInterval: 30 * 1000,
  });
  const daemonsets = useQuery({
    queryKey: queryKeys.clusterPages.workloadKind(clusterId, 'daemonsets'),
    queryFn: () => k8sGet(clusterId, 'apis/apps/v1/daemonsets') as Promise<K8sList<DaemonSetLike>>,
    enabled: !!clusterId,
    refetchInterval: 30 * 1000,
  });
  const jobs = useQuery({
    queryKey: queryKeys.clusterPages.workloadKind(clusterId, 'jobs'),
    queryFn: () => k8sGet(clusterId, 'apis/batch/v1/jobs') as Promise<K8sList<JobLike>>,
    enabled: !!clusterId,
    refetchInterval: 30 * 1000,
  });
  const cronjobs = useQuery({
    queryKey: queryKeys.clusterPages.workloadKind(clusterId, 'cronjobs'),
    queryFn: () => k8sGet(clusterId, 'apis/batch/v1/cronjobs') as Promise<K8sList<CronJobLike>>,
    enabled: !!clusterId,
    refetchInterval: 30 * 1000,
  });

  // Merge + filter. Memoise so re-renders triggered by other state
  // changes (search box typing) don't re-walk every row.
  const rows = useMemo<Workload[]>(() => {
    const out: Workload[] = [];
    deployments.data?.items?.forEach((i) => out.push({ kind: 'Deployment', item: i }));
    statefulsets.data?.items?.forEach((i) => out.push({ kind: 'StatefulSet', item: i }));
    daemonsets.data?.items?.forEach((i) => out.push({ kind: 'DaemonSet', item: i }));
    jobs.data?.items?.forEach((i) => {
      // Skip k8s controller-owned Jobs (they're CronJob children). The
      // CronJob row already represents them; showing the Jobs here too
      // doubles the row count without adding signal.
      const owned = i.metadata.ownerReferences?.some((o) => o.controller);
      if (!owned) out.push({ kind: 'Job', item: i });
    });
    cronjobs.data?.items?.forEach((i) => out.push({ kind: 'CronJob', item: i }));
    return out;
  }, [deployments.data, statefulsets.data, daemonsets.data, jobs.data, cronjobs.data]);

  const namespaces = useMemo(() => {
    const set = new Set<string>();
    rows.forEach((r) => set.add(r.item.metadata.namespace));
    return Array.from(set).sort();
  }, [rows]);

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return rows.filter((r) => {
      if (namespace && r.item.metadata.namespace !== namespace) return false;
      if (kindFilter && r.kind !== kindFilter) return false;
      if (needle && !r.item.metadata.name.toLowerCase().includes(needle)) return false;
      return true;
    });
  }, [rows, namespace, kindFilter, search]);

  const isLoading =
    deployments.isLoading ||
    statefulsets.isLoading ||
    daemonsets.isLoading ||
    jobs.isLoading ||
    cronjobs.isLoading;

  const failed = [
    deployments.error && 'Deployments',
    statefulsets.error && 'StatefulSets',
    daemonsets.error && 'DaemonSets',
    jobs.error && 'Jobs',
    cronjobs.error && 'CronJobs',
  ].filter(Boolean) as string[];

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Workloads</h1>
          <p className="text-sm text-muted-foreground">
            All Deployments, StatefulSets, DaemonSets, Jobs, and CronJobs in this cluster.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative">
            <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
            <input
              type="search"
              placeholder="search name…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="h-8 rounded-md border border-border bg-background pl-7 pr-2 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
            />
          </div>
          <select
            value={namespace}
            onChange={(e) => setNamespace(e.target.value)}
            className="h-8 rounded-md border border-border bg-background px-2 text-sm"
          >
            <option value="">all namespaces</option>
            {namespaces.map((ns) => (
              <option key={ns} value={ns}>
                {ns}
              </option>
            ))}
          </select>
          <select
            value={kindFilter}
            onChange={(e) => setKindFilter(e.target.value as Workload['kind'] | '')}
            className="h-8 rounded-md border border-border bg-background px-2 text-sm"
          >
            <option value="">all kinds</option>
            <option value="Deployment">Deployments</option>
            <option value="StatefulSet">StatefulSets</option>
            <option value="DaemonSet">DaemonSets</option>
            <option value="Job">Jobs</option>
            <option value="CronJob">CronJobs</option>
          </select>
        </div>
      </div>

      {failed.length > 0 && (
        <div className="rounded-md border border-status-warning/30 bg-status-warning/5 px-3 py-2 text-xs text-status-warning">
          Couldn&apos;t list: {failed.join(', ')}. The other kinds are still displayed.
        </div>
      )}

      <div className="rounded-lg border border-border overflow-hidden">
        <Table className="w-full text-sm">
          <TableHeader className="bg-muted/30 text-xs uppercase tracking-wide text-muted-foreground">
            <TableRow>
              <TableHead className="px-3 py-2 text-left font-medium w-32">Kind</TableHead>
              <TableHead className="px-3 py-2 text-left font-medium">Name</TableHead>
              <TableHead className="px-3 py-2 text-left font-medium w-48">Namespace</TableHead>
              <TableHead className="px-3 py-2 text-left font-medium w-32">Status</TableHead>
              <TableHead className="px-3 py-2 text-left font-medium w-28">Age</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className="divide-y divide-border">
            {isLoading && rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                  Loading…
                </TableCell>
              </TableRow>
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                  {search || namespace || kindFilter ? (
                    <>No workloads match the filters.</>
                  ) : (
                    <div className="space-y-3">
                      <p>No workloads in this cluster yet.</p>
                      <p className="text-xs">
                        Install a chart from the{' '}
                        <a href={`/dashboard/catalog?cluster_id=${clusterId}`} className="underline">
                          catalog
                        </a>{' '}
                        or open the{' '}
                        <a href={`/dashboard/clusters/${clusterId}/shell`} className="underline">
                          kubectl shell
                        </a>{' '}
                        to apply a manifest directly.
                      </p>
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((r) => <WorkloadRow key={`${r.kind}/${r.item.metadata.namespace}/${r.item.metadata.name}`} clusterId={clusterId} workload={r} />)
            )}
          </TableBody>
        </Table>
      </div>

      <p className="text-xs text-muted-foreground">
        {filtered.length} of {rows.length} workloads
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------
// Row
// ---------------------------------------------------------------------

function WorkloadRow({ clusterId, workload }: { clusterId: string; workload: Workload }) {
  const router = useRouter();
  const status = computeStatus(workload);
  const kindMeta = KIND_META[workload.kind];
  const href = `/dashboard/clusters/${clusterId}/workloads/${kindMeta.urlSegment}/${workload.item.metadata.namespace}/${workload.item.metadata.name}`;
  // ponytail: this list page is un-gated today (no permission hooks; the detail
  // route does its own read-gating). Make the whole row drill in for parity
  // with the resource tables; the name stays a real <Link> for open-in-new-tab.
  return (
    <TableRow className="hover:bg-muted/30 cursor-pointer" onClick={() => router.push(href)}>
      <TableCell className="px-3 py-2">
        <span className="inline-flex items-center gap-1.5 text-foreground">
          <kindMeta.icon className="h-3.5 w-3.5 text-muted-foreground" />
          {workload.kind}
        </span>
      </TableCell>
      <TableCell className="px-3 py-2">
        <Link
          href={href}
          onClick={(e) => e.stopPropagation()}
          className="text-foreground hover:underline"
        >
          {workload.item.metadata.name}
        </Link>
      </TableCell>
      <TableCell className="px-3 py-2 text-muted-foreground">{workload.item.metadata.namespace}</TableCell>
      <TableCell className="px-3 py-2">
        <span className={`inline-flex items-center gap-1 ${status.tone}`}>
          {status.icon}
          {status.label}
        </span>
      </TableCell>
      <TableCell className="px-3 py-2 text-muted-foreground">
        {workload.item.metadata.creationTimestamp
          ? formatRelativeTime(workload.item.metadata.creationTimestamp)
          : '—'}
      </TableCell>
    </TableRow>
  );
}

// ---------------------------------------------------------------------
// Kind metadata
// ---------------------------------------------------------------------

const KIND_META: Record<
  Workload['kind'],
  { icon: React.ComponentType<{ className?: string }>; urlSegment: string }
> = {
  Deployment: { icon: Boxes, urlSegment: 'deployments' },
  StatefulSet: { icon: Database, urlSegment: 'statefulsets' },
  DaemonSet: { icon: Disc, urlSegment: 'daemonsets' },
  Job: { icon: Briefcase, urlSegment: 'jobs' },
  CronJob: { icon: Clock3, urlSegment: 'cronjobs' },
};

// ---------------------------------------------------------------------
// Status normaliser
// ---------------------------------------------------------------------

function computeStatus(w: Workload): {
  label: string;
  tone: string;
  icon: React.ReactNode;
} {
  const ok = (label: string) => ({
    label,
    tone: 'text-status-success',
    icon: <CheckCircle2 className="h-3.5 w-3.5" />,
  });
  const bad = (label: string) => ({
    label,
    tone: 'text-status-critical',
    icon: <AlertCircle className="h-3.5 w-3.5" />,
  });
  const muted = (label: string) => ({
    label,
    tone: 'text-muted-foreground',
    icon: <CircleHelp className="h-3.5 w-3.5" />,
  });

  if (w.kind === 'Deployment' || w.kind === 'StatefulSet') {
    const desired = w.item.spec?.replicas ?? 0;
    const ready = w.item.status?.readyReplicas ?? 0;
    const label = `${ready}/${desired} ready`;
    if (desired === 0) return muted('scaled to 0');
    return ready >= desired ? ok(label) : bad(label);
  }
  if (w.kind === 'DaemonSet') {
    const desired = w.item.status?.desiredNumberScheduled ?? 0;
    const ready = w.item.status?.numberReady ?? 0;
    const label = `${ready}/${desired} ready`;
    if (desired === 0) return muted('no nodes match');
    return ready >= desired ? ok(label) : bad(label);
  }
  if (w.kind === 'Job') {
    if (w.item.spec?.suspend) return muted('Suspended');
    const conds = w.item.status?.conditions ?? [];
    const complete = conds.find((c) => c.type === 'Complete' && c.status === 'True');
    if (complete) return ok('Complete');
    const failed = conds.find((c) => c.type === 'Failed' && c.status === 'True');
    if (failed) return bad('Failed');
    return muted(`active ${w.item.status?.active ?? 0}`);
  }
  if (w.kind === 'CronJob') {
    if (w.item.spec?.suspend) return muted('Suspended');
    if ((w.item.status?.active ?? []).length > 0) return ok(`running (${w.item.status?.active!.length})`);
    return ok(w.item.spec?.schedule ?? 'idle');
  }
  return muted('—');
}
