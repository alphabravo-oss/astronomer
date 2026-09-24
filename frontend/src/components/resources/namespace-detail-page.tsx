import { useMemo, useState } from "react";
import {
  Activity,
  Boxes,
  Braces,
  Gauge,
  Network,
  Server,
  ShieldCheck,
  TriangleAlert,
} from "lucide-react";

import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { TabStrip } from "@/components/ui/tabs";
import { ResourceMasthead } from "@/components/ui/page";
import { MetricCard } from "@/components/ui/metric-card";
import {
  useNamespaceQueries,
  NamespacePageControls,
} from "./namespace-queries";
import { QueryStates } from "@/components/ui/query-states";
import { Link } from "@tanstack/react-router";
import { cn, formatBytes, formatCPU, formatRelativeTime } from "@/lib/utils";
import {
  useNamespaceResourceRows,
  type NamespaceResourceRow,
} from "./namespace-resource-rows";

type TabId =
  | "overview"
  | "workloads"
  | "pods"
  | "networking"
  | "storage"
  | "configuration"
  | "events"
  | "security"
  | "usage";

const tabs: Array<{ id: TabId; label: string }> = [
  { id: "overview", label: "Overview" },
  { id: "workloads", label: "Workloads" },
  { id: "pods", label: "Pods" },
  { id: "networking", label: "Networking" },
  { id: "storage", label: "Storage" },
  { id: "configuration", label: "Configuration" },
  { id: "events", label: "Events" },
  { id: "security", label: "Security" },
  { id: "usage", label: "Resource usage" },
];

const resourceColumns: Column<NamespaceResourceRow>[] = [
  { key: "kind", header: "Kind", accessor: (row) => row.kind },
  {
    key: "name",
    header: "Name",
    accessor: (row) =>
      row.href ? (
        <Link
          className="font-mono text-xs font-medium hover:underline"
          to={row.href}
        >
          {row.name}
        </Link>
      ) : (
        <span className="font-mono text-xs font-medium">{row.name}</span>
      ),
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  { key: "detail", header: "Details", accessor: (row) => row.detail },
  {
    key: "age",
    header: "Age",
    accessor: (row) => row.age,
    sortAccessor: (row) => Date.parse(row.createdAt) || 0,
  },
];

export function NamespaceDetailPage({
  clusterId,
  namespace,
}: {
  clusterId: string;
  namespace: string;
}) {
  const [tab, setTab] = useState<TabId>("overview");
  const queries = useNamespaceQueries(clusterId, namespace);
  const { namespaces, workloads, pods, services, policies, pvcs, events } =
    queries;

  const ns = (namespaces.isError ? undefined : namespaces.data)?.find(
    (item) => item.name === namespace,
  );
  const namespaceEvents = (events.isError ? [] : (events.data ?? [])).filter(
    (event) => event.involvedObject.namespace === namespace,
  );
  const warningEvents = namespaceEvents.filter(
    (event) => event.type === "Warning",
  );
  const namespacePods = useMemo(() => pods.data?.data ?? [], [pods.data]);
  const unhealthyPods = namespacePods.filter(
    (pod) => pod.phase !== "Running" && pod.phase !== "Succeeded",
  );
  const restarts = namespacePods.reduce((sum, pod) => sum + pod.restarts, 0);

  const rows = useNamespaceResourceRows(
    clusterId,
    namespace,
    queries,
    namespacePods,
    namespaceEvents,
  );

  const loading = workloads.isLoading || pods.isLoading || namespaces.isLoading;
  const cpuPct = ns?.cpuLimit ? (ns.cpuUsage / ns.cpuLimit) * 100 : 0;
  const memoryPct = ns?.memoryLimit
    ? (ns.memoryUsage / ns.memoryLimit) * 100
    : 0;

  if (namespaces.isError || namespaces.isLoading)
    return <QueryStates query={namespaces}>{null}</QueryStates>;
  if (!ns)
    return (
      <p role="status">
        Namespace {namespace} was not found in the accessible cluster inventory.
      </p>
    );
  const activeQueries =
    tab in queries.groups
      ? queries.groups[tab as keyof typeof queries.groups]
      : tab === "events"
        ? [events]
        : [];
  const activeError = activeQueries.find((query) => query.isError);

  return (
    <div className="space-y-6">
      <section className="overflow-hidden rounded-xl border border-border bg-card">
        <div className="border-b border-border bg-gradient-to-r from-primary/10 via-card to-card px-6 py-5">
          <ResourceMasthead
            eyebrow={
              <span className="inline-flex items-center gap-2">
                <Boxes className="h-4 w-4" /> Namespace
              </span>
            }
            title={namespace}
            mono
            status={
              <StatusBadge
                status={
                  ns?.status ?? (namespaces.isLoading ? "Loading" : "Unknown")
                }
              />
            }
            description={
              ns
                ? `Created ${formatRelativeTime(ns.createdAt)}`
                : "Namespace-scoped operations and resources"
            }
          />
        </div>
        <div className="grid grid-cols-2 divide-x divide-y divide-border sm:grid-cols-3 lg:grid-cols-6 lg:divide-y-0">
          <Stat
            label="Workloads"
            value={workloads.data ? workloads.data.data.length : "—"}
            icon={Boxes}
          />
          <Stat
            label="Pods"
            value={pods.data ? namespacePods.length : "—"}
            icon={Server}
          />
          <Stat
            label="Unhealthy"
            value={pods.data ? unhealthyPods.length : "—"}
            icon={TriangleAlert}
            danger={unhealthyPods.length > 0}
          />
          <Stat
            label="Restarts"
            value={pods.data ? restarts : "—"}
            icon={Activity}
            danger={restarts > 0}
          />
          <Stat
            label="Services"
            value={
              services.data
                ? services.data.data.filter(
                    (item) => item.namespace === namespace,
                  ).length
                : "—"
            }
            icon={Network}
          />
          <Stat
            label="Warning events"
            value={events.data && !events.isError ? warningEvents.length : "—"}
            icon={TriangleAlert}
            danger={warningEvents.length > 0}
          />
        </div>
      </section>

      <TabStrip
        tabs={tabs.map((item) => ({ key: item.id, label: item.label }))}
        value={tab}
        onChange={setTab}
        aria-label="Namespace details"
      />

      <NamespacePageControls queries={queries} tab={tab} />

      {tab === "overview" && (
        <div className="grid gap-4 lg:grid-cols-3">
          <UsageCard
            title="CPU"
            value={formatCPU(ns?.cpuUsage ?? 0)}
            limit={ns?.cpuLimit ? formatCPU(ns.cpuLimit) : "No limit"}
            percent={cpuPct}
          />
          <UsageCard
            title="Memory"
            value={formatBytes(ns?.memoryUsage ?? 0)}
            limit={ns?.memoryLimit ? formatBytes(ns.memoryLimit) : "No limit"}
            percent={memoryPct}
          />
          <section className="rounded-lg border border-border bg-card p-4">
            <div className="mb-3 flex items-center gap-2 font-medium">
              <Gauge className="h-4 w-4" /> Operational signals
            </div>
            <Signal
              label="Pods healthy"
              available={!!pods.data}
              value={`${namespacePods.length - unhealthyPods.length}/${namespacePods.length}`}
              good={unhealthyPods.length === 0}
            />
            <Signal
              label="Network policies"
              available={!!policies.data}
              value={String(
                (policies.data?.data ?? []).filter(
                  (item) => item.namespace === namespace,
                ).length,
              )}
              good={(policies.data?.data ?? []).some(
                (item) => item.namespace === namespace,
              )}
            />
            <Signal
              label="PVCs bound"
              available={!!pvcs.data}
              value={`${(pvcs.data?.data ?? []).filter((item) => item.namespace === namespace && item.status === "Bound").length}/${(pvcs.data?.data ?? []).filter((item) => item.namespace === namespace).length}`}
              good={
                !(pvcs.data?.data ?? []).some(
                  (item) =>
                    item.namespace === namespace && item.status !== "Bound",
                )
              }
            />
          </section>
          <ResourceGroup
            title="Workloads"
            icon={Boxes}
            rows={rows.workloads}
            onOpen={() => setTab("workloads")}
          />
          <ResourceGroup
            title="Networking"
            icon={Network}
            rows={rows.networking}
            onOpen={() => setTab("networking")}
          />
          <ResourceGroup
            title="Configuration & policy"
            icon={ShieldCheck}
            rows={[...rows.configuration, ...rows.security]}
            onOpen={() => setTab("configuration")}
          />
          <section className="rounded-lg border border-border bg-card p-4 lg:col-span-3">
            <div className="mb-3 flex items-center gap-2 font-medium">
              <Braces className="h-4 w-4" /> Metadata
            </div>
            <div className="grid gap-4 md:grid-cols-2">
              <MetadataList label="Labels" values={ns?.labels} />
              <MetadataList label="Annotations" values={ns?.annotations} />
            </div>
          </section>
        </div>
      )}

      {tab === "usage" ? (
        <div className="grid gap-4 md:grid-cols-2">
          <UsageCard
            title="CPU usage"
            value={formatCPU(ns?.cpuUsage ?? 0)}
            limit={
              ns?.cpuLimit ? formatCPU(ns.cpuLimit) : "No configured limit"
            }
            percent={cpuPct}
          />
          <UsageCard
            title="Memory usage"
            value={formatBytes(ns?.memoryUsage ?? 0)}
            limit={
              ns?.memoryLimit
                ? formatBytes(ns.memoryLimit)
                : "No configured limit"
            }
            percent={memoryPct}
          />
        </div>
      ) : tab !== "overview" ? (
        <DataTable
          data={rows[tab]}
          columns={resourceColumns}
          keyExtractor={(row) => row.id}
          loading={loading || activeQueries.some((query) => query.isLoading)}
          isError={!!activeError}
          error={activeError?.error}
          onRetry={() => activeQueries.forEach((query) => void query.refetch())}
          searchable
          searchPlaceholder={`Filter loaded ${tab} in ${namespace}…`}
          emptyState={{
            title: `No ${tab} resources on the loaded pages in ${namespace}`,
            description:
              "Try another resource page. Event history is limited to the recent cluster window.",
          }}
          persistKey={`namespace:${tab}`}
        />
      ) : null}
    </div>
  );
}

function Stat({
  label,
  value,
  icon: Icon,
  danger = false,
}: {
  label: string;
  value: number | string;
  icon: typeof Boxes;
  danger?: boolean;
}) {
  return (
    <div className="p-4">
      <MetricCard
        label={label}
        value={value}
        icon={<Icon className="h-3.5 w-3.5" />}
        tone={danger ? "warning" : undefined}
        className="border-0 bg-transparent p-0 hover:bg-transparent"
      />
    </div>
  );
}

function UsageCard({
  title,
  value,
  limit,
  percent,
}: {
  title: string;
  value: string;
  limit: string;
  percent: number;
}) {
  const bounded = Math.max(0, Math.min(percent, 100));
  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <div className="mb-3 flex items-center gap-2 font-medium">
        <Gauge className="h-4 w-4" />
        {title}
      </div>
      <div className="flex items-end justify-between">
        <span className="text-2xl font-semibold tabular-nums">{value}</span>
        <span className="text-xs text-muted-foreground">of {limit}</span>
      </div>
      <div className="mt-3 h-2 overflow-hidden rounded-full bg-muted">
        <div
          className={cn(
            "h-full rounded-full",
            bounded >= 90
              ? "bg-status-error"
              : bounded >= 75
                ? "bg-status-warning"
                : "bg-primary",
          )}
          style={{ width: `${bounded}%` }}
        />
      </div>
    </section>
  );
}

function Signal({
  label,
  value,
  good,
  available = true,
}: {
  label: string;
  value: string;
  good: boolean;
  available?: boolean;
}) {
  return (
    <div className="flex items-center justify-between border-t border-border py-2 text-sm first:border-t-0">
      <span className="text-muted-foreground">{label}</span>
      <span
        className={cn(
          "font-medium tabular-nums",
          !available
            ? "text-muted-foreground"
            : good
              ? "text-status-success"
              : "text-status-warning",
        )}
      >
        {available ? value : "Unavailable"}
      </span>
    </div>
  );
}

function ResourceGroup({
  title,
  icon: Icon,
  rows,
  onOpen,
}: {
  title: string;
  icon: typeof Braces;
  rows: NamespaceResourceRow[];
  onOpen: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onOpen}
      className="rounded-lg border border-border bg-card p-4 text-left transition-colors hover:bg-accent/40"
    >
      <div className="flex items-center justify-between">
        <span className="flex items-center gap-2 font-medium">
          <Icon className="h-4 w-4" />
          {title}
        </span>
        <span className="text-2xl font-semibold tabular-nums">
          {rows.length}
        </span>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        Open the namespace-scoped {title.toLowerCase()} inventory
      </p>
    </button>
  );
}

function MetadataList({
  label,
  values,
}: {
  label: string;
  values?: Record<string, string>;
}) {
  const entries = Object.entries(values ?? {});
  return (
    <div>
      <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      {entries.length === 0 ? (
        <p className="text-sm text-muted-foreground">None</p>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {entries.map(([key, value]) => (
            <span
              key={key}
              className="rounded border border-border bg-muted/40 px-2 py-1 font-mono text-2xs"
            >
              {key}={value}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
