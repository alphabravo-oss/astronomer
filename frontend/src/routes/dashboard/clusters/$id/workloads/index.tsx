import { createFileRoute } from "@tanstack/react-router";
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
 * Data path: TanStack DB collections over the k8s passthrough proxy —
 * one collection per kind, seeded by a REST list and kept live by the
 * proxy's `?watch=true` NDJSON stream (lib/db/collections.ts). We use
 * five collections not one because k8s doesn't expose a multi-kind
 * list endpoint. Search/namespace/kind filtering runs inside the live
 * query so frame folding stays incremental.
 *
 * Statuses are normalised so the table column is a single readable
 * label per row regardless of source kind: "5/5 ready" for healthy,
 * "0/3 ready" for unhealthy, "Suspended" for jobs/cronjobs, etc.
 */

import { useMemo, useState } from "react";
import { Link } from "@/lib/link";
import { useParams, useRouter } from "@/lib/navigation";
import { ilike, useLiveQuery } from "@tanstack/react-db";
import { useStore } from "@tanstack/react-store";
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
  Filter,
} from "lucide-react";

import { k8sCollection, type K8sWatchStatus } from "@/lib/db/collections";
import { formatRelativeTime } from "@/lib/utils";
import { DataTable, type Column } from "@/components/ui/data-table";

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

type Workload =
  | { kind: "Deployment"; item: DeploymentLike }
  | { kind: "StatefulSet"; item: DeploymentLike }
  | { kind: "DaemonSet"; item: DaemonSetLike }
  | { kind: "Job"; item: JobLike }
  | { kind: "CronJob"; item: CronJobLike };

interface KindFilters {
  search: string;
  kind: Workload["kind"] | "";
}

// Skip k8s controller-owned Jobs (they're CronJob children). The CronJob row
// already represents them; showing the Jobs here too doubles the row count
// without adding signal.
function unownedJob(i: JobLike): boolean {
  return !i.metadata.ownerReferences?.some((o) => o.controller);
}

/** Concrete row shape for the live queries — ref proxies need a non-generic type. */
interface WorkloadObj {
  metadata: K8sMeta;
}

/**
 * One workload kind as a live collection: `all` rows feed the namespace
 * dropdown and the total count; `filtered` applies the page filters inside
 * the collection query (kind gating disables the query outright).
 */
function useWorkloadKind<T extends WorkloadObj>(
  clusterId: string,
  path: string,
  kind: Workload["kind"],
  filters: KindFilters,
): { all: T[]; filtered: T[]; isLoading: boolean; status: K8sWatchStatus } {
  const handle = k8sCollection<WorkloadObj>({
    clusterId,
    source: { kind: "proxy", path },
  });
  const status = useStore(handle.status);
  const needle = filters.search.trim();

  const all = useLiveQuery(
    (q) => (clusterId ? q.from({ w: handle.collection }) : null),
    [handle.collection, clusterId],
  );
  const filtered = useLiveQuery(
    (q) => {
      if (!clusterId || (filters.kind && filters.kind !== kind)) return null;
      let qb = q.from({ w: handle.collection });
      if (needle)
        qb = qb.where(({ w }) => ilike(w.metadata.name, `%${needle}%`));
      return qb;
    },
    [
      handle.collection,
      clusterId,
      kind,
      filters.kind,
      needle,
    ],
  );

  return {
    all: (all.data ?? []) as unknown as T[],
    filtered: (filtered.data ?? []) as unknown as T[],
    isLoading: !!clusterId && !all.isReady,
    status,
  };
}

// ---------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------

function WorkloadsPage() {
  const params = useParams();
  const router = useRouter();
  const clusterId = params.id as string;

  const [search, setSearch] = useState("");
  const [selectedNamespaces, setSelectedNamespaces] = useState<string[]>([]);
  const [namespaceMenuOpen, setNamespaceMenuOpen] = useState(false);
  const [kindFilter, setKindFilter] = useState<Workload["kind"] | "">("");

  // One live collection per kind (seed list + `?watch=true` stream folded by
  // lib/db/collections.ts). Each kind has its own collection and status; a
  // missing apiVersion (e.g. batch/v1 disabled) doesn't take the others down
  // with it, and a stream that can't open self-heals with backoff re-seeds.
  const filters: KindFilters = { search, kind: kindFilter };
  const deployments = useWorkloadKind<DeploymentLike>(
    clusterId,
    "apis/apps/v1/deployments",
    "Deployment",
    filters,
  );
  const statefulsets = useWorkloadKind<DeploymentLike>(
    clusterId,
    "apis/apps/v1/statefulsets",
    "StatefulSet",
    filters,
  );
  const daemonsets = useWorkloadKind<DaemonSetLike>(
    clusterId,
    "apis/apps/v1/daemonsets",
    "DaemonSet",
    filters,
  );
  const jobs = useWorkloadKind<JobLike>(
    clusterId,
    "apis/batch/v1/jobs",
    "Job",
    filters,
  );
  const cronjobs = useWorkloadKind<CronJobLike>(
    clusterId,
    "apis/batch/v1/cronjobs",
    "CronJob",
    filters,
  );

  const anyLive = [deployments, statefulsets, daemonsets, jobs, cronjobs].some(
    (k) => k.status === "live",
  );

  // Unfiltered merge — drives the namespace dropdown and the total count.
  const rows = useMemo<Workload[]>(() => {
    const out: Workload[] = [];
    deployments.all.forEach((i) => out.push({ kind: "Deployment", item: i }));
    statefulsets.all.forEach((i) => out.push({ kind: "StatefulSet", item: i }));
    daemonsets.all.forEach((i) => out.push({ kind: "DaemonSet", item: i }));
    jobs.all
      .filter(unownedJob)
      .forEach((i) => out.push({ kind: "Job", item: i }));
    cronjobs.all.forEach((i) => out.push({ kind: "CronJob", item: i }));
    return out;
  }, [
    deployments.all,
    statefulsets.all,
    daemonsets.all,
    jobs.all,
    cronjobs.all,
  ]);

  const namespaces = useMemo(() => {
    const set = new Set<string>();
    rows.forEach((r) => set.add(r.item.metadata.namespace));
    return Array.from(set).sort();
  }, [rows]);

  // Filtered merge — the search/namespace/kind filters already ran inside
  // each collection's live query; only the Job-ownership rule stays here
  // (ownerReferences shape isn't expressible as a query predicate).
  const filtered = useMemo<Workload[]>(() => {
    const out: Workload[] = [];
    deployments.filtered.forEach((i) =>
      out.push({ kind: "Deployment", item: i }),
    );
    statefulsets.filtered.forEach((i) =>
      out.push({ kind: "StatefulSet", item: i }),
    );
    daemonsets.filtered.forEach((i) =>
      out.push({ kind: "DaemonSet", item: i }),
    );
    jobs.filtered
      .filter(unownedJob)
      .forEach((i) => out.push({ kind: "Job", item: i }));
    cronjobs.filtered.forEach((i) => out.push({ kind: "CronJob", item: i }));
    return selectedNamespaces.length === 0
      ? out
      : out.filter((row) =>
          selectedNamespaces.includes(row.item.metadata.namespace),
        );
  }, [
    deployments.filtered,
    statefulsets.filtered,
    daemonsets.filtered,
    jobs.filtered,
    cronjobs.filtered,
    selectedNamespaces,
  ]);

  const isLoading =
    deployments.isLoading ||
    statefulsets.isLoading ||
    daemonsets.isLoading ||
    jobs.isLoading ||
    cronjobs.isLoading;

  const failed = [
    deployments.status === "error" && "Deployments",
    statefulsets.status === "error" && "StatefulSets",
    daemonsets.status === "error" && "DaemonSets",
    jobs.status === "error" && "Jobs",
    cronjobs.status === "error" && "CronJobs",
  ].filter(Boolean) as string[];

  const columns = useMemo<Column<Workload>[]>(
    () => [
      {
        key: "kind",
        header: "Kind",
        accessor: (row) => {
          const meta = KIND_META[row.kind];
          return <span className="inline-flex items-center gap-1.5"><meta.icon className="h-3.5 w-3.5 text-muted-foreground" />{row.kind}</span>;
        },
        sortAccessor: (row) => row.kind,
      },
      {
        key: "name",
        header: "Name",
        accessor: (row) => {
          const meta = KIND_META[row.kind];
          const href = `/dashboard/clusters/${clusterId}/workloads/${meta.urlSegment}/${row.item.metadata.namespace}/${row.item.metadata.name}`;
          return <Link href={href} onClick={(event) => event.stopPropagation()} className="font-mono text-xs font-medium hover:underline">{row.item.metadata.name}</Link>;
        },
        sortAccessor: (row) => row.item.metadata.name,
      },
      {
        key: "namespace",
        header: "Namespace",
        accessor: (row) => <span className="font-mono text-xs text-muted-foreground">{row.item.metadata.namespace}</span>,
        sortAccessor: (row) => row.item.metadata.namespace,
      },
      {
        key: "status",
        header: "Status",
        accessor: (row) => {
          const status = computeStatus(row);
          return <span className={`inline-flex items-center gap-1 ${status.tone}`}>{status.icon}{status.label}</span>;
        },
        sortAccessor: (row) => computeStatus(row).label,
      },
      {
        key: "age",
        header: "Age",
        accessor: (row) => row.item.metadata.creationTimestamp ? formatRelativeTime(row.item.metadata.creationTimestamp) : "—",
        sortAccessor: (row) => Date.parse(row.item.metadata.creationTimestamp ?? "") || 0,
      },
    ],
    [clusterId],
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-semibold">Workloads</h1>
            {anyLive && (
              <span className="inline-flex items-center gap-1 rounded-full bg-status-success/10 px-2 py-0.5 text-xs text-status-success">
                <span className="h-1.5 w-1.5 rounded-full bg-status-success animate-pulse" />
                Live
              </span>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            All Deployments, StatefulSets, DaemonSets, Jobs, and CronJobs in
            this cluster.
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
          <div className="relative">
            <button
              type="button"
              aria-label="Filter workloads by namespace"
              aria-expanded={namespaceMenuOpen}
              onClick={() => setNamespaceMenuOpen((open) => !open)}
              className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-2 text-sm"
            >
              <Filter className="h-3.5 w-3.5 text-muted-foreground" />
              {selectedNamespaces.length === 0
                ? "All namespaces"
                : selectedNamespaces.length === 1
                  ? selectedNamespaces[0]
                  : `${selectedNamespaces.length} namespaces`}
            </button>
            {namespaceMenuOpen && (
              <div className="absolute right-0 top-full z-50 mt-1 max-h-72 w-56 overflow-auto rounded-md border border-border bg-popover p-1 shadow-lg">
                {namespaces.map((ns) => (
                  <label
                    key={ns}
                    className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-accent"
                  >
                    <input
                      type="checkbox"
                      checked={selectedNamespaces.includes(ns)}
                      onChange={() =>
                        setSelectedNamespaces((current) =>
                          current.includes(ns)
                            ? current.filter((value) => value !== ns)
                            : [...current, ns],
                        )
                      }
                      className="rounded border-border text-primary focus:ring-ring"
                    />
                    {ns}
                  </label>
                ))}
                {selectedNamespaces.length > 0 && (
                  <button
                    type="button"
                    onClick={() => setSelectedNamespaces([])}
                    className="mt-1 w-full rounded px-2 py-1.5 text-left text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
                  >
                    All namespaces
                  </button>
                )}
              </div>
            )}
          </div>
          <select
            aria-label="Filter workloads by kind"
            value={kindFilter}
            onChange={(e) =>
              setKindFilter(e.target.value as Workload["kind"] | "")
            }
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
          Couldn&apos;t list: {failed.join(", ")}. The other kinds are still
          displayed.
        </div>
      )}

      <DataTable
        data={filtered}
        columns={columns}
        keyExtractor={(row) => `${row.kind}/${row.item.metadata.namespace}/${row.item.metadata.name}`}
        searchable={false}
        loading={isLoading && rows.length === 0}
        emptyMessage={search || selectedNamespaces.length > 0 || kindFilter ? "No workloads match the filters." : "No workloads in this cluster yet."}
        persistKey="cluster-workloads"
        onRowClick={(row) => {
          const meta = KIND_META[row.kind];
          router.push(`/dashboard/clusters/${clusterId}/workloads/${meta.urlSegment}/${row.item.metadata.namespace}/${row.item.metadata.name}`);
        }}
      />

      <p className="text-xs text-muted-foreground">
        {filtered.length} of {rows.length} workloads
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------
// Kind metadata
// ---------------------------------------------------------------------

const KIND_META: Record<
  Workload["kind"],
  { icon: React.ComponentType<{ className?: string }>; urlSegment: string }
> = {
  Deployment: { icon: Boxes, urlSegment: "deployments" },
  StatefulSet: { icon: Database, urlSegment: "statefulsets" },
  DaemonSet: { icon: Disc, urlSegment: "daemonsets" },
  Job: { icon: Briefcase, urlSegment: "jobs" },
  CronJob: { icon: Clock3, urlSegment: "cronjobs" },
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
    tone: "text-status-success",
    icon: <CheckCircle2 className="h-3.5 w-3.5" />,
  });
  const bad = (label: string) => ({
    label,
    tone: "text-status-error",
    icon: <AlertCircle className="h-3.5 w-3.5" />,
  });
  const muted = (label: string) => ({
    label,
    tone: "text-muted-foreground",
    icon: <CircleHelp className="h-3.5 w-3.5" />,
  });

  if (w.kind === "Deployment" || w.kind === "StatefulSet") {
    const desired = w.item.spec?.replicas ?? 0;
    const ready = w.item.status?.readyReplicas ?? 0;
    const label = `${ready}/${desired} ready`;
    if (desired === 0) return muted("scaled to 0");
    return ready >= desired ? ok(label) : bad(label);
  }
  if (w.kind === "DaemonSet") {
    const desired = w.item.status?.desiredNumberScheduled ?? 0;
    const ready = w.item.status?.numberReady ?? 0;
    const label = `${ready}/${desired} ready`;
    if (desired === 0) return muted("no nodes match");
    return ready >= desired ? ok(label) : bad(label);
  }
  if (w.kind === "Job") {
    if (w.item.spec?.suspend) return muted("Suspended");
    const conds = w.item.status?.conditions ?? [];
    const complete = conds.find(
      (c) => c.type === "Complete" && c.status === "True",
    );
    if (complete) return ok("Complete");
    const failed = conds.find(
      (c) => c.type === "Failed" && c.status === "True",
    );
    if (failed) return bad("Failed");
    return muted(`active ${w.item.status?.active ?? 0}`);
  }
  if (w.kind === "CronJob") {
    if (w.item.spec?.suspend) return muted("Suspended");
    if ((w.item.status?.active ?? []).length > 0)
      return ok(`running (${w.item.status?.active!.length})`);
    return ok(w.item.spec?.schedule ?? "idle");
  }
  return muted("—");
}

export const Route = createFileRoute("/dashboard/clusters/$id/workloads/")({
  component: WorkloadsPage,
});
